package mysql

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	"aifar-deployment/backend/internal/backuprepo"
	"aifar-deployment/backend/internal/store"
)

func TestRestoreStandalonePlanMatchesApprovedSectionNine(t *testing.T) {
	module := NewModule(newBackupFakeStore(t), newBackupFakeRemote())
	restore, ok := any(module).(registry.RestoreModule)
	if !ok {
		t.Fatal("MySQL module does not implement registry.RestoreModule")
	}
	instance := store.AppInstance{
		ID: "app_1234567890abcdef12345678", App: "mysql", ServerID: "srv_1234567890abcdef12345678",
		Version: "8.0.36", Topology: "standalone",
	}
	plan, err := restore.PlanRestore(context.Background(), registry.RestoreRequest{
		Instance: instance,
		Backup: store.AppBackup{
			ID: "backup_1234567890abcdef1234", App: "mysql", InstanceID: instance.ID,
			ServerID: instance.ServerID, BackupType: "logical-full", Status: "success",
		},
		RepositoryDir: t.TempDir(),
		Parameters: map[string]any{
			"mode": "standalone", "maintenanceConfirmed": true,
			"createPreRestoreBackup": true, "disasterConfirmed": false, "threads": 4,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"load-backup", "acquire-instance-lock", "verify-maintenance-confirmation", "verify-manifest",
		"verify-checksum", "verify-version", "create-pre-restore-backup", "upload-backup", "extract-backup",
		"dry-run-load", "capture-local-infile", "enable-local-infile", "drop-target-schemas", "load-dump",
		"restore-local-infile", "verify-schemas", "verify-data", "record-restore", "cleanup-workdir", "release-lock",
	}
	got := make([]string, len(plan))
	for i := range plan {
		got[i] = plan[i].Name
		if plan[i].Target != instance.ServerID || plan[i].Order != i+1 {
			t.Fatalf("plan[%d] = %+v", i, plan[i])
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restore plan = %v, want %v", got, want)
	}
}

type restoreFakeRemote struct {
	commands  []string
	uploads   map[string]string
	inspect   string
	loadErr   error
	verifyErr error
	onLoad    func()
}

func (r *restoreFakeRemote) Run(_ context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.commands = append(r.commands, command)
	switch {
	case strings.Contains(command, "__AIFAR_INFO__"):
		return adapter.CommandResult{Stdout: r.inspect}, nil
	case strings.Contains(command, "logical-restore.sh"):
		if r.onLoad != nil {
			r.onLoad()
		}
		return adapter.CommandResult{}, r.loadErr
	case strings.Contains(command, "__AIFAR_VERIFY_SCHEMAS__"):
		return adapter.CommandResult{Stdout: "aifar_business\n"}, r.verifyErr
	case strings.Contains(command, "__AIFAR_VERIFY_DATA__"):
		return adapter.CommandResult{Stdout: "1\n"}, r.verifyErr
	default:
		return adapter.CommandResult{}, nil
	}
}

func TestRestoreStandaloneRestoresLocalInfileAcrossAllReachableOutcomes(t *testing.T) {
	for _, original := range []string{"OFF", "ON"} {
		for _, outcome := range []string{"success", "load-failure", "cancelled", "verify-failure"} {
			t.Run(original+"/"+outcome, func(t *testing.T) {
				data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
				repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
				data.backups = []store.AppBackup{backup}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				remote := &restoreFakeRemote{inspect: "__AIFAR_INFO__\t8.0.36\t123e4567-e89b-12d3-a456-426614174000\tuuid:1-9\t8.0.36\t1048576\n__AIFAR_SCHEMA__\taifar_business\n"}
				switch outcome {
				case "load-failure":
					remote.loadErr = errors.New("load failed")
				case "cancelled":
					remote.loadErr = context.Canceled
					remote.onLoad = cancel
				case "verify-failure":
					remote.verifyErr = errors.New("verify failed")
				}
				service := NewService(data, remote)
				service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error { return nil }
				local := &fakeLocalInfileSession{value: original}
				service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
					return local, func() {}, nil
				}
				err := service.restoreStandalone(ctx, standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_restore_outcome", Log: fakeLogger{}})
				if outcome == "success" && err != nil {
					t.Fatal(err)
				}
				if outcome != "success" && err == nil {
					t.Fatalf("%s unexpectedly succeeded", outcome)
				}
				if local.value != original {
					t.Fatalf("local_infile = %s, want original %s (err=%v)", local.value, original, err)
				}
			})
		}
	}
}

func TestRestoreStandaloneUnreachableFinallyWritesExactMarkerAndReturnsNewCode(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
	data.backups = []store.AppBackup{backup}
	remote := &restoreFakeRemote{
		inspect: "__AIFAR_INFO__\t8.0.36\t123e4567-e89b-12d3-a456-426614174000\tuuid:1-9\t8.0.36\t1048576\n__AIFAR_SCHEMA__\taifar_business\n",
		loadErr: errors.New("target disconnected during load"),
	}
	service := NewService(data, remote)
	service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error { return nil }
	local := &fakeLocalInfileSession{value: "OFF", setErrors: map[string]error{"OFF": errors.New("target unreachable")}}
	service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return local, func() {}, nil
	}
	err := service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_restore_marker", Log: fakeLogger{}})
	var stable interface{ StableCode() string }
	if !errors.As(err, &stable) || stable.StableCode() != MySQLLocalInfileRestoreFailed {
		t.Fatalf("error = %T %v", err, err)
	}
	if strings.Contains(err.Error(), MySQLRestoreLocalInfileRestoreFailed) {
		t.Fatalf("new restore emitted legacy error alias: %v", err)
	}
	_, marker, present, parseErr := parseMySQLReconciliationMarker(data.instance.Metadata)
	if parseErr != nil || !present || marker.Version != 1 || marker.Kind != "local_infile" || marker.OriginalValue != "OFF" || marker.TaskID != "task_restore_marker" {
		t.Fatalf("marker = %+v present=%v err=%v metadata=%s", marker, present, parseErr, data.instance.Metadata)
	}
	if _, parseErr := time.Parse(time.RFC3339, marker.RecordedAt); parseErr != nil {
		t.Fatalf("recordedAt = %q: %v", marker.RecordedAt, parseErr)
	}
	if got := restorePhase(data.backups[0].Metadata); got != "restore_incomplete" {
		t.Fatalf("restore phase = %q", got)
	}
}

func (r *restoreFakeRemote) UploadFile(_ context.Context, _ store.Server, localPath, remotePath string, _ os.FileMode) error {
	if r.uploads == nil {
		r.uploads = map[string]string{}
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	r.uploads[remotePath] = string(data)
	return nil
}

func TestRestoreStandaloneVerifiesRepositoryBeforeRemoteMutationAndRunsControlledFlow(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
	data.backups = []store.AppBackup{backup}
	remote := &restoreFakeRemote{inspect: "__AIFAR_INFO__\t8.0.36\t123e4567-e89b-12d3-a456-426614174000\tuuid:1-9\t8.0.36\t1048576\n__AIFAR_SCHEMA__\taifar_business\n"}
	service := NewService(data, remote)
	preRestoreCalls := 0
	service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error {
		preRestoreCalls++
		return nil
	}
	local := &fakeLocalInfileSession{value: "OFF"}
	service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return local, func() {}, nil
	}
	err := service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_restore_1", Log: fakeLogger{}})
	if err != nil {
		t.Fatal(err)
	}
	if preRestoreCalls != 1 {
		t.Fatalf("pre-restore backup calls = %d", preRestoreCalls)
	}
	commands := strings.Join(remote.commands, "\n")
	if !strings.Contains(commands, "DROP DATABASE IF EXISTS `aifar_business`") || strings.Contains(strings.ToLower(commands), "drop database `mysql`") {
		t.Fatalf("schema drop was not manifest-exact and safe:\n%s", commands)
	}
	var restoreScript string
	for name, contents := range remote.uploads {
		if strings.HasSuffix(name, "logical-restore.sh") {
			restoreScript = contents
		}
	}
	if !strings.Contains(restoreScript, "ignoreExistingObjects: false") {
		t.Fatalf("controlled load script missing strict merge protection:\n%s", restoreScript)
	}
	if local.value != "OFF" {
		t.Fatalf("local_infile = %s, want restored OFF", local.value)
	}
	if got := restorePhase(data.backups[0].Metadata); got != "verified" {
		t.Fatalf("restore phase = %q metadata=%s", got, data.backups[0].Metadata)
	}
}

func TestRestoreStandaloneRejectsFailedRepositoryVerificationBeforeRemoteMutation(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
	data.backups = []store.AppBackup{backup}
	if err := os.WriteFile(backup.Path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := &restoreFakeRemote{}
	service := NewService(data, remote)
	err := service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_restore_2", Log: fakeLogger{}})
	if err == nil {
		t.Fatal("tampered repository backup was accepted")
	}
	if len(remote.commands) != 0 || len(remote.uploads) != 0 {
		t.Fatalf("remote mutated before repository verification: commands=%v uploads=%v", remote.commands, remote.uploads)
	}
}

func TestRestoreStandalonePreRestoreBackupCoreUsesDedicatedTypeWithoutPlanOrRetention(t *testing.T) {
	data := newBackupFakeStore(t)
	remote := newBackupFakeRemote()
	service := NewService(data, remote)
	request := standaloneBackupRequest(t)
	recorder := &backupRecorder{}
	err := service.backupStandaloneCore(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder}, standaloneBackupExecution{
		backupType: "pre-restore", recordPlan: false, retention: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(data.backups) != 1 || data.backups[0].BackupType != "pre-restore" || data.backups[0].Status != "success" {
		t.Fatalf("pre-restore records = %+v", data.backups)
	}
	if len(recorder.startedSteps) != 0 {
		t.Fatalf("pre-restore core injected ordinary backup steps: %v", recorder.startedSteps)
	}
	if data.listCalls != 0 {
		t.Fatalf("pre-restore core ran ordinary retention: listCalls=%d", data.listCalls)
	}
}

func createStandaloneRestoreBackup(t *testing.T, instance store.AppInstance) (string, store.AppBackup) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	repository, err := backuprepo.New(root)
	if err != nil {
		t.Fatal(err)
	}
	backupID := "backup_1234567890abcdef12345678"
	paths, err := repository.Prepare(backupID)
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("controlled logical archive")
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	manifest, err := CanonicalBackupManifestJSON(BackupManifest{
		BackupID: backupID, App: "mysql", Topology: "standalone", InstanceID: instance.ID,
		SourceServerID: instance.ServerID, SourceEndpoint: "10.0.0.8:3306",
		SourceServerUUID: "123e4567-e89b-12d3-a456-426614174000", MySQLVersion: "8.0.36", MySQLShellVersion: "8.0.36",
		Schemas: []string{"aifar_business"}, ExcludedSchemas: append([]string(nil), fixedSystemSchemas...),
		Consistent: true, GTIDExecuted: "uuid:1-9", CreatedAt: time.Now().UTC(), TaskID: "tsk_1234567890abcdef12345678",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(paths, manifest, digest, int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	return root, store.AppBackup{
		ID: backupID, App: "mysql", InstanceID: instance.ID, ServerID: instance.ServerID,
		BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest,
		Size: int64(len(archive)), Metadata: `{}`, CreatedAt: time.Now().UTC(),
	}
}

func standaloneRestoreRequest(instance store.AppInstance, backup store.AppBackup, repositoryDir string) registry.RestoreRequest {
	return registry.RestoreRequest{
		Instance: instance, Backup: backup, RepositoryDir: repositoryDir, Language: "en", Actor: "owner",
		Parameters: map[string]any{"mode": "standalone", "maintenanceConfirmed": true, "createPreRestoreBackup": true, "disasterConfirmed": false, "threads": 4},
	}
}

func restorePhase(raw string) string {
	metadata, err := strictBackupMetadata(raw)
	if err != nil {
		return ""
	}
	var phase string
	_ = json.Unmarshal(metadata["restorePhase"], &phase)
	return phase
}
