package mysql

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

var standaloneBackupSteps = []string{
	"load-instance", "acquire-instance-lock", "resolve-credential", "inspect-mysql",
	"check-backup-space", "prepare-workdir", "dry-run-dump", "dump-instance",
	"build-manifest", "package-backup", "transfer-backup", "verify-checksum",
	"record-backup", "apply-retention", "cleanup-workdir",
}

func TestBackupStandalonePlanUsesExactDesignSequenceAndClampsParameters(t *testing.T) {
	// Production break caught: skipping/reordering a safety phase or forwarding unbounded request numbers would make the worker plan lie about execution.
	module, _, _ := newStandaloneBackupModule(t)
	request := standaloneBackupRequest(t)
	request.Parameters = map[string]any{"threads": 999, "maxRateMBps": 99999, "name": "nightly"}
	plan, err := module.PlanBackup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(plan))
	for index, step := range plan {
		got[index] = step.Name
		if step.Target != request.Instance.ServerID || step.Order != index+1 {
			t.Fatalf("step %d has target/order %+v", index, step)
		}
	}
	if !reflect.DeepEqual(got, standaloneBackupSteps) {
		t.Fatalf("backup plan = %v, want %v", got, standaloneBackupSteps)
	}
	if request.Parameters["threads"] != 999 {
		t.Fatal("PlanBackup must not mutate the caller request")
	}
}

func TestBackupStandaloneCompletesOneDumpTransferCommitRetentionAndCleanup(t *testing.T) {
	// Production break caught: omitting any lifecycle side effect can report a successful backup without a committed, attributable archive.
	module, data, remote := newStandaloneBackupModule(t)
	recorder := &backupRecorder{}
	request := standaloneBackupRequest(t)
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorder.startedSteps, standaloneBackupSteps) {
		t.Fatalf("executed steps = %v, want %v", recorder.startedSteps, standaloneBackupSteps)
	}
	if remote.dryRunRuns != 1 || remote.dumpRuns != 1 || remote.downloads != 1 || remote.packageRuns != 1 || remote.cleanupRuns != 1 {
		t.Fatalf("remote lifecycle counts dry-run=%d dump=%d package=%d download=%d cleanup=%d", remote.dryRunRuns, remote.dumpRuns, remote.packageRuns, remote.downloads, remote.cleanupRuns)
	}
	if data.credentialLoads != 1 {
		t.Fatalf("bound admin credential loads = %d, want 1", data.credentialLoads)
	}
	if len(data.backups) != 1 {
		t.Fatalf("backup records = %+v", data.backups)
	}
	backup := data.backups[0]
	if backup.Status != "success" || backup.TaskID != "tsk_1234567890abcdef12345678" || backup.Checksum != remote.archiveSHA || backup.Size != int64(len(remote.archive)) {
		t.Fatalf("completed backup = %+v", backup)
	}
	for _, name := range []string{"dump.tar", "backup-manifest.json", "checksums.txt"} {
		if info, err := os.Stat(filepath.Join(filepath.Dir(backup.Path), name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("committed %s missing or unsafe: info=%v err=%v", name, info, err)
		}
	}
	if strings.Contains(strings.Join(recorder.messages, "\n"), "top-secret") || strings.Contains(strings.Join(remote.commands, "\n"), "top-secret") {
		t.Fatal("decrypted MySQL password leaked to a command or task log")
	}
	if !remote.secretUploaded || remote.scriptUploads != 1 {
		t.Fatalf("secret/script upload evidence missing: secret=%v scripts=%d", remote.secretUploaded, remote.scriptUploads)
	}
	if data.listCalls == 0 {
		t.Fatal("retention did not inspect successful backups")
	}
}

func TestBackupStandaloneRetainsFailedRecordWithoutFinalArchive(t *testing.T) {
	// Production break caught: a failed safety phase must not publish an archive or erase the forensic backup record.
	tests := []struct {
		name   string
		mutate func(*backupFakeStore, *backupFakeRemote)
		code   string
	}{
		{"missing credential", func(s *backupFakeStore, _ *backupFakeRemote) { s.credentialErr = store.ErrBoundCredentialNotFound }, MySQLCredentialUnavailable},
		{"inactive credential", func(s *backupFakeStore, _ *backupFakeRemote) { s.credentialErr = store.ErrBoundCredentialNotFound }, MySQLCredentialUnavailable},
		{"ambiguous credential", func(s *backupFakeStore, _ *backupFakeRemote) { s.credentialErr = store.ErrBoundCredentialAmbiguous }, MySQLCredentialUnavailable},
		{"mysqlsh failure", func(_ *backupFakeStore, r *backupFakeRemote) { r.inspectErr = errors.New("mysqlsh failed") }, ""},
		{"system schema discovery", func(_ *backupFakeStore, r *backupFakeRemote) { r.inspectOutput += "__AIFAR_SCHEMA__\tmysql\n" }, MySQLRestoreManifestInvalid},
		{"insufficient source space", func(_ *backupFakeStore, r *backupFakeRemote) { r.sourceAvailable = 1 }, MySQLBackupSpaceInsufficient},
		{"transfer cancellation", func(_ *backupFakeStore, r *backupFakeRemote) { r.downloadErr = context.Canceled }, MySQLBackupTransferFailed},
		{"checksum mismatch", func(_ *backupFakeStore, r *backupFakeRemote) { r.downloadSHA = strings.Repeat("0", 64) }, MySQLBackupChecksumMismatch},
		{"cleanup failure", func(_ *backupFakeStore, r *backupFakeRemote) { r.cleanupErr = errors.New("cleanup failed") }, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module, data, remote := newStandaloneBackupModule(t)
			test.mutate(data, remote)
			err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}})
			if err == nil {
				t.Fatal("expected backup failure")
			}
			if test.code != "" {
				var operationErr *MySQLOperationError
				if !errors.As(err, &operationErr) || operationErr.Code != test.code {
					t.Fatalf("error = %v, want code %s", err, test.code)
				}
			}
			if len(data.backups) != 1 || data.backups[0].Status != "failed" {
				t.Fatalf("failed backup record not retained: %+v", data.backups)
			}
			if _, statErr := os.Stat(data.backups[0].Path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("final archive exists after failure: %v", statErr)
			}
		})
	}
}

func TestBackupStandaloneRejectsRemoteWithoutFileDownloader(t *testing.T) {
	// Production break caught: a remote that cannot stream to the controlled panel partial must fail before publishing anything.
	data := newBackupFakeStore(t)
	module := NewModule(data, backupRemoteWithoutDownloader{})
	err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}})
	var operationErr *MySQLOperationError
	if !errors.As(err, &operationErr) || operationErr.Code != MySQLBackupTransferFailed {
		t.Fatalf("error = %v, want %s", err, MySQLBackupTransferFailed)
	}
	if len(data.backups) != 1 || data.backups[0].Status != "failed" {
		t.Fatalf("missing failed record: %+v", data.backups)
	}
}

func TestBackupStandaloneFailsWhenPanelRepositorySpaceIsInsufficient(t *testing.T) {
	// Production break caught: source capacity alone cannot protect the panel repository from an oversized transfer.
	module, data, _ := newStandaloneBackupModule(t)
	original := panelBackupAvailableBytes
	panelBackupAvailableBytes = func(string) (int64, error) { return 1, nil }
	t.Cleanup(func() { panelBackupAvailableBytes = original })
	err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}})
	var operationErr *MySQLOperationError
	if !errors.As(err, &operationErr) || operationErr.Code != MySQLBackupSpaceInsufficient {
		t.Fatalf("error = %v, want %s", err, MySQLBackupSpaceInsufficient)
	}
	if len(data.backups) != 1 || data.backups[0].Status != "failed" {
		t.Fatalf("missing failed record: %+v", data.backups)
	}
}

type backupFakeStore struct {
	server          store.Server
	credential      store.Credential
	credentialErr   error
	credentialLoads int
	backups         []store.AppBackup
	listCalls       int
}

func newBackupFakeStore(t *testing.T) *backupFakeStore {
	t.Helper()
	return &backupFakeStore{
		server:     store.Server{ID: "srv_1234567890abcdef12345678", Name: "mysql", Host: "10.0.0.8", Username: "root"},
		credential: store.Credential{ID: "cred_1234567890abcdef12345678", Kind: "mysql", Username: "root", Status: "active", Purpose: "admin", Secret: map[string]string{"password": "top-secret"}},
	}
}

func (s *backupFakeStore) GetServer(id string, includeSecret bool) (store.Server, error) {
	return s.server, nil
}
func (s *backupFakeStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	return v, nil
}
func (s *backupFakeStore) ListAppInstances() ([]store.AppInstance, error) { return nil, nil }
func (s *backupFakeStore) DeleteAppInstance(string) error                 { return nil }
func (s *backupFakeStore) GetBoundCredential(instanceID, purpose string, includeSecret bool) (store.Credential, error) {
	s.credentialLoads++
	if s.credentialErr != nil {
		return store.Credential{}, s.credentialErr
	}
	return s.credential, nil
}
func (s *backupFakeStore) SaveAppBackup(value store.AppBackup) (store.AppBackup, error) {
	for index := range s.backups {
		if s.backups[index].ID == value.ID {
			s.backups[index] = value
			return value, nil
		}
	}
	s.backups = append(s.backups, value)
	return value, nil
}
func (s *backupFakeStore) ListAppBackupsForInstances(ids []string, includeDeleted bool) ([]store.AppBackup, error) {
	s.listCalls++
	return append([]store.AppBackup(nil), s.backups...), nil
}

type backupFakeRemote struct {
	archive         []byte
	archiveSHA      string
	downloadSHA     string
	inspectOutput   string
	sourceAvailable int64
	inspectErr      error
	downloadErr     error
	cleanupErr      error
	commands        []string
	dumpRuns        int
	dryRunRuns      int
	packageRuns     int
	downloads       int
	cleanupRuns     int
	secretUploaded  bool
	scriptUploads   int
}

func newBackupFakeRemote() *backupFakeRemote {
	archive := []byte("portable mysql dump archive")
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	return &backupFakeRemote{
		archive: archive, archiveSHA: sum, downloadSHA: sum, sourceAvailable: 1 << 40,
		inspectOutput: "__AIFAR_INFO__\t8.0.36\t123e4567-e89b-12d3-a456-426614174000\tuuid:1-9\t8.0.36\t1048576\n__AIFAR_SCHEMA__\taifar_business\n",
	}
}

func (r *backupFakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	r.commands = append(r.commands, command)
	switch {
	case strings.Contains(command, "__AIFAR_INFO__"):
		return adapter.CommandResult{Stdout: r.inspectOutput}, r.inspectErr
	case strings.Contains(command, "df -Pk"):
		return adapter.CommandResult{Stdout: fmt.Sprintf("%d\n", r.sourceAvailable)}, nil
	case strings.Contains(command, "dryRun: true"):
		r.dryRunRuns++
		return adapter.CommandResult{}, nil
	case strings.Contains(command, "logical-backup.sh"):
		r.dumpRuns++
		return adapter.CommandResult{}, nil
	case strings.Contains(command, "sha256sum"):
		r.packageRuns++
		return adapter.CommandResult{Stdout: fmt.Sprintf("__AIFAR_ARCHIVE__\t%d\t%s\n", len(r.archive), r.archiveSHA)}, nil
	case strings.Contains(command, "rm -rf"):
		r.cleanupRuns++
		return adapter.CommandResult{}, r.cleanupErr
	default:
		return adapter.CommandResult{}, nil
	}
}

func (r *backupFakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	if strings.HasSuffix(remotePath, "secret-context.cnf") {
		r.secretUploaded = mode.Perm() == 0o600 && strings.Contains(string(data), "top-secret")
	}
	if strings.HasSuffix(remotePath, "logical-backup.sh") {
		r.scriptUploads++
	}
	return nil
}

func (r *backupFakeRemote) DownloadFile(ctx context.Context, server store.Server, remotePath, localPath string, mode os.FileMode) (adapter.DownloadResult, error) {
	r.downloads++
	if r.downloadErr != nil {
		return adapter.DownloadResult{}, r.downloadErr
	}
	if err := os.WriteFile(localPath, r.archive, mode); err != nil {
		return adapter.DownloadResult{}, err
	}
	return adapter.DownloadResult{Size: int64(len(r.archive)), SHA256: r.downloadSHA}, nil
}

type backupRemoteWithoutDownloader struct{}

func (backupRemoteWithoutDownloader) Run(context.Context, store.Server, string) (adapter.CommandResult, error) {
	return adapter.CommandResult{}, nil
}
func (backupRemoteWithoutDownloader) UploadFile(context.Context, store.Server, string, string, os.FileMode) error {
	return nil
}

type backupRecorder struct{ startedSteps, messages []string }

func (r *backupRecorder) Info(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}
func (r *backupRecorder) Error(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}
func (r *backupRecorder) StartTarget(string)                  {}
func (r *backupRecorder) FinishTarget(string, string, string) {}
func (r *backupRecorder) StartStep(_ string, name, _ string, _ int) {
	r.startedSteps = append(r.startedSteps, name)
}
func (r *backupRecorder) FinishStep(string, string, string, string) {}

func newStandaloneBackupModule(t *testing.T) (Module, *backupFakeStore, *backupFakeRemote) {
	t.Helper()
	data := newBackupFakeStore(t)
	remote := newBackupFakeRemote()
	return NewModule(data, remote), data, remote
}

func standaloneBackupRequest(t *testing.T) registry.BackupRequest {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "mysql-backups")
	return registry.BackupRequest{
		Instance: store.AppInstance{ID: "app_1234567890abcdef12345678", App: "mysql", ServerID: "srv_1234567890abcdef12345678", Version: "8.0.36", Status: "installed", Topology: "standalone", Metadata: `{"port":3306,"rootUser":"root","endpoint":"10.0.0.8:3306"}`},
		Servers:  []store.Server{{ID: "srv_1234567890abcdef12345678", Host: "10.0.0.8"}},
		Language: "en", Actor: "operator", RepositoryDir: repository, KeepLast: 5,
		Parameters: map[string]any{"name": "nightly", "threads": 4, "maxRateMBps": 64},
	}
}

var _ registry.BackupModule = Module{}

// Keep time imported in this test fixture so timestamp assertions can be added without production-derived expectations.
var _ = time.Time{}
