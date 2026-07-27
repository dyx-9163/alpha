package mysql

import (
	"archive/tar"
	"bytes"
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

type restoreProgressRecorder struct {
	backupRecorder
	targetStarts   int
	targetFinishes []string
	stepStatus     map[string]string
}

func (r *restoreProgressRecorder) StartTarget(string) { r.targetStarts++ }
func (r *restoreProgressRecorder) FinishTarget(_ string, status, _ string) {
	r.targetFinishes = append(r.targetFinishes, status)
}
func (r *restoreProgressRecorder) FinishStep(_ string, name, status, _ string) {
	if r.stepStatus == nil {
		r.stepStatus = map[string]string{}
	}
	r.stepStatus[name] = status
}

func TestRestoreStandaloneTerminallyRecordsExactPlanAndSingleTargetOnEveryOutcome(t *testing.T) {
	for _, outcome := range []string{"success", "load-failure", "cancelled", "load-backup-failure"} {
		t.Run(outcome, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
			repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
			data.backups = []store.AppBackup{backup}
			remote := &restoreFakeRemote{inspect: standaloneInspection("aifar_business")}
			if outcome == "load-failure" {
				remote.loadErr = errors.New("load failed")
			}
			if outcome == "cancelled" {
				remote.loadErr = context.Canceled
			}
			if outcome == "load-backup-failure" {
				data.backups = nil
			}
			service := NewService(data, remote)
			service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error { return nil }
			service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
				return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
			}
			recorder := &restoreProgressRecorder{}
			err := service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_restore_progress", Log: recorder})
			if (outcome == "success") != (err == nil) {
				t.Fatalf("outcome=%s err=%v", outcome, err)
			}
			if recorder.targetStarts != 1 || len(recorder.targetFinishes) != 1 {
				t.Fatalf("target lifecycle starts=%d finishes=%v", recorder.targetStarts, recorder.targetFinishes)
			}
			if len(recorder.stepStatus) != len(standaloneRestoreStepNames) {
				t.Fatalf("terminal steps=%d statuses=%v", len(recorder.stepStatus), recorder.stepStatus)
			}
			for _, name := range standaloneRestoreStepNames {
				if status := recorder.stepStatus[name]; status != "success" && status != "failed" && status != "cancelled" {
					t.Fatalf("step %s status=%q", name, status)
				}
			}
		})
	}
}

type restoreFakeRemote struct {
	commands    []string
	uploads     map[string]string
	inspect     string
	inspectErr  error
	loadErr     error
	verifyErr   error
	onLoad      func()
	finalOutput string
	onFinal     func()
}

func (r *restoreFakeRemote) Run(_ context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.commands = append(r.commands, command)
	switch {
	case strings.Contains(command, "__AIFAR_INFO__"):
		return adapter.CommandResult{Stdout: r.inspect}, r.inspectErr
	case strings.Contains(command, "logical-restore.sh"):
		if r.onLoad != nil {
			r.onLoad()
		}
		return adapter.CommandResult{}, r.loadErr
	case strings.Contains(command, "__AIFAR_VERIFY_FINAL__"):
		if r.onFinal != nil {
			r.onFinal()
		}
		output := r.finalOutput
		if output == "" {
			output = finalRestoreVerificationLiteral()
		}
		return adapter.CommandResult{Stdout: output}, r.verifyErr
	case strings.Contains(command, "__AIFAR_VERIFY_SCHEMAS__"):
		return adapter.CommandResult{Stdout: "aifar_business\n"}, r.verifyErr
	case strings.Contains(command, "__AIFAR_VERIFY_DATA__"):
		return adapter.CommandResult{Stdout: "1\n"}, r.verifyErr
	default:
		return adapter.CommandResult{}, nil
	}
}

func finalRestoreVerificationLiteral() string {
	return strings.Join([]string{
		"__AIFAR_VERIFY_PING__\t1",
		"__AIFAR_VERIFY_SCHEMA__\taifar_business\t4",
		"__AIFAR_VERIFY_TABLE__\taifar_business\talpha",
		"__AIFAR_VERIFY_TABLE__\taifar_business\tbeta",
		"__AIFAR_VERIFY_TABLE__\taifar_business\tdelta",
		"__AIFAR_VERIFY_TABLE__\taifar_business\tgamma",
	}, "\n") + "\n"
}

func TestFinalRestoreVerificationDoesNotIssueSampleRowCounts(t *testing.T) {
	command := finalRestoreVerificationCommand("/aifar/apps/mysql/restore/task_restore", 3306)
	for _, forbidden := range []string{"__AIFAR_VERIFY_SAMPLE__", "COUNT(*) FROM `"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("final verification contains removed row-sampling query %q: %s", forbidden, command)
		}
	}
}

func TestRestoreStandaloneFinalGateRequiresExactManifestV2Expectations(t *testing.T) {
	tests := []struct {
		name   string
		output string
		mutate func(*testing.T, *restoreFakeStore, store.AppBackup)
	}{
		{name: "ping missing", output: strings.Replace(finalRestoreVerificationLiteral(), "__AIFAR_VERIFY_PING__\t1\n", "", 1)},
		{name: "extra table", output: finalRestoreVerificationLiteral() + "__AIFAR_VERIFY_TABLE__\taifar_business\textra\n"},
		{name: "missing table", output: strings.Replace(finalRestoreVerificationLiteral(), "__AIFAR_VERIFY_TABLE__\taifar_business\tgamma\n", "", 1)},
		{name: "schema table count mismatch", output: strings.Replace(finalRestoreVerificationLiteral(), "\taifar_business\t4\n", "\taifar_business\t5\n", 1)},
		{name: "task id mismatch", mutate: func(_ *testing.T, data *restoreFakeStore, _ store.AppBackup) {
			metadata, _ := strictBackupMetadata(data.backups[0].Metadata)
			metadata["restoreTaskId"], _ = json.Marshal("task_other_restore")
			encoded, _ := json.Marshal(metadata)
			data.backups[0].Metadata = string(encoded)
		}},
		{name: "digest mismatch", mutate: func(_ *testing.T, data *restoreFakeStore, _ store.AppBackup) {
			metadata, _ := strictBackupMetadata(data.backups[0].Metadata)
			metadata["restoreExpectedManifestSha256"], _ = json.Marshal(strings.Repeat("0", 64))
			encoded, _ := json.Marshal(metadata)
			data.backups[0].Metadata = string(encoded)
		}},
		{name: "repository manifest replaced", mutate: func(t *testing.T, _ *restoreFakeStore, backup store.AppBackup) {
			manifestPath := filepath.Join(filepath.Dir(backup.Path), "backup-manifest.json")
			raw, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := decodeRestoreManifest(raw)
			if err != nil {
				t.Fatal(err)
			}
			manifest.CreatedAt = manifest.CreatedAt.Add(time.Second)
			replaced, err := CanonicalBackupManifestJSON(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestPath, replaced, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
			repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
			data.backups = []store.AppBackup{backup}
			remote := &restoreFakeRemote{inspect: standaloneInspection("aifar_business"), finalOutput: test.output}
			remote.onFinal = func() {
				if test.mutate != nil {
					test.mutate(t, data, backup)
				}
			}
			service := NewService(data, remote)
			service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error { return nil }
			service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
				return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
			}
			err := service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_final_gate", Log: fakeLogger{}})
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || stable.StableCode() != MySQLRestoreIncomplete || restorePhase(data.backups[0].Metadata) != "restore_incomplete" {
				t.Fatalf("final gate accepted mismatch: code=%v phase=%s err=%v", stable, restorePhase(data.backups[0].Metadata), err)
			}
		})
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

func TestRestoreStandaloneRejectsLegacyV1BeforeAnyRemoteMutation(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
	repository, err := backuprepo.New(repositoryDir)
	if err != nil {
		t.Fatal(err)
	}
	// Replace the v2 fixture with a separately committed legacy backup.
	legacyID := "backup_abcdefabcdefabcdefabcdef"
	paths, err := repository.Prepare(legacyID)
	if err != nil {
		t.Fatal(err)
	}
	archive := standaloneRestoreTar(t, []byte("{}"))
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	legacy := validBackupManifest()
	legacy.BackupID, legacy.InstanceID, legacy.SourceServerID = legacyID, data.instance.ID, data.instance.ServerID
	legacy.SourceEndpoint = "10.0.0.8:3306"
	legacy.Schemas = []string{"aifar_business"}
	legacy.ManifestVersion, legacy.Verification = 1, nil
	manifestBytes, err := CanonicalBackupManifestJSON(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(paths, manifestBytes, digest, int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	backup = store.AppBackup{ID: legacyID, App: "mysql", InstanceID: data.instance.ID, ServerID: data.instance.ServerID, BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), Metadata: `{}`}
	data.backups = []store.AppBackup{backup}
	remote := &restoreFakeRemote{}
	service := NewService(data, remote)
	err = service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_restore_v1", Log: fakeLogger{}})
	var stable interface{ StableCode() string }
	if !errors.As(err, &stable) || stable.StableCode() != MySQLRestoreManifestInvalid {
		t.Fatalf("legacy restore error = %T %v", err, err)
	}
	if len(remote.commands) != 0 || len(remote.uploads) != 0 {
		t.Fatalf("legacy v1 caused remote mutation: commands=%v uploads=%v", remote.commands, remote.uploads)
	}
}

func TestRestoreStandalonePersistsMarkerBeforeEnableAndRefusesEnableWhenMarkerCannotPersist(t *testing.T) {
	for _, test := range []struct {
		name      string
		failSaves int
		wantSet   bool
	}{
		{name: "marker visible before enable", wantSet: true},
		{name: "marker save permanently fails", failSaves: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t), instanceSaveFailures: test.failSaves}
			repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
			data.backups = []store.AppBackup{backup}
			remote := &restoreFakeRemote{inspect: standaloneInspection("aifar_business")}
			service := NewService(data, remote)
			service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error { return nil }
			markerVisible := false
			local := &fakeLocalInfileSession{value: "OFF", onSet: func(value string) {
				if value == "ON" {
					_, marker, present, err := parseMySQLReconciliationMarker(data.instance.Metadata)
					markerVisible = err == nil && present && marker.OriginalValue == "OFF" && marker.TaskID == "task_marker_before_enable"
				}
			}}
			service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
				return local, func() {}, nil
			}
			err := service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_marker_before_enable", Log: fakeLogger{}})
			if test.wantSet {
				if err != nil || !markerVisible {
					t.Fatalf("marker was not durable before enable: visible=%v err=%v metadata=%s", markerVisible, err, data.instance.Metadata)
				}
			} else if err == nil || len(local.setCalls) != 0 {
				t.Fatalf("marker persistence failure did not block enable: calls=%v err=%v", local.setCalls, err)
			}
		})
	}
}

func TestRestoreStandaloneEnableUncertainDurablyRecordsIncompleteAndRestoresOriginal(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
	data.backups = []store.AppBackup{backup}
	service := NewService(data, &restoreFakeRemote{inspect: standaloneInspection("aifar_business")})
	service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error { return nil }
	local := &fakeLocalInfileSession{value: "OFF", setErrors: map[string]error{"ON": errors.New("transport failed after SET")}, applyBeforeError: map[string]bool{"ON": true}}
	service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return local, func() {}, nil
	}
	err := service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_enable_uncertain", Log: fakeLogger{}})
	if err == nil || local.value != "OFF" || restorePhase(data.backups[0].Metadata) != "restore_incomplete" {
		t.Fatalf("enable uncertain result: value=%s phase=%s err=%v", local.value, restorePhase(data.backups[0].Metadata), err)
	}
	if _, _, present, parseErr := parseMySQLReconciliationMarker(data.instance.Metadata); parseErr != nil || present {
		t.Fatalf("verified restoration did not clear marker: present=%v err=%v metadata=%s", present, parseErr, data.instance.Metadata)
	}
}

func TestRestorePersistenceRetriesAreBoundedAndPropagated(t *testing.T) {
	const taskID = "task_restore_persistence"
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, test := range []struct {
		name     string
		failures int
		wantErr  bool
	}{
		{name: "phase transient", failures: 2},
		{name: "phase permanent", failures: 3, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t), backupSaveFailures: map[string]int{"preflight": test.failures}}
			backup := store.AppBackup{ID: "backup_phase_retry", Metadata: `{}`}
			data.backups = []store.AppBackup{backup}
			err := updateRestorePhase(data, &backup, "preflight", taskID, digest)
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v", err)
			}
			if data.backupSaveCalls != 3 {
				t.Fatalf("save attempts=%d", data.backupSaveCalls)
			}
		})
	}
	for _, test := range []struct {
		name      string
		failCalls map[int]bool
		wantErr   bool
	}{
		{name: "clear transient", failCalls: map[int]bool{1: true, 2: true}},
		{name: "clear permanent", failCalls: map[int]bool{1: true, 2: true, 3: true}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t), instanceSaveFailCalls: test.failCalls}
			data.instance.Metadata = `{"port":3306,"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"` + taskID + `"}}`
			err := clearMySQLReconciliationMarker(data, data.instance.ID, "OFF", taskID)
			if (err != nil) != test.wantErr || data.instanceSaveCalls != 3 {
				t.Fatalf("attempts=%d err=%v", data.instanceSaveCalls, err)
			}
			_, _, present, parseErr := parseMySQLReconciliationMarker(data.instance.Metadata)
			if parseErr != nil || present != test.wantErr { // success clears; permanent failure retains
				t.Fatalf("present=%v parseErr=%v wantErr=%v", present, parseErr, test.wantErr)
			}
		})
	}
}

func TestRestoreStandaloneRejectsTamperedBackupOrFreshOwnershipChangeBeforeReconciliationMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *restoreFakeStore, store.AppBackup)
	}{
		{name: "tampered archive", mutate: func(t *testing.T, _ *restoreFakeStore, backup store.AppBackup) {
			if err := os.WriteFile(backup.Path, []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "fresh topology changed", mutate: func(_ *testing.T, data *restoreFakeStore, _ store.AppBackup) {
			data.instance.Topology = "innodb-cluster"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
			repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
			data.backups = []store.AppBackup{backup}
			data.instance.Metadata = `{"port":3306,"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"task_previous"}}`
			test.mutate(t, data, backup)
			service := NewService(data, &restoreFakeRemote{})
			connected := false
			service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
				connected = true
				return &fakeLocalInfileSession{value: "ON"}, func() {}, nil
			}
			_ = service.restoreStandalone(context.Background(), standaloneRestoreRequest(store.AppInstance{ID: data.instance.ID, App: "mysql", ServerID: data.instance.ServerID, Topology: "standalone"}, backup, repositoryDir), registry.RunContext{TaskID: "task_preflight_first", Log: fakeLogger{}})
			if connected {
				t.Fatal("reconciliation mutated before fresh ownership/repository preflight")
			}
		})
	}
}

func TestRestoreStandaloneDisasterSkipRequiresExplicitConnectionUnreachable(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
	data.backups = []store.AppBackup{backup}
	for _, test := range []struct {
		name       string
		inspect    string
		inspectErr error
		wantCode   string
	}{
		{name: "malformed success", inspect: "__AIFAR_INFO__\ttruncated\n", wantCode: MySQLRestoreManifestInvalid},
		{name: "non-connectivity command failure", inspectErr: errors.New("mysqlsh output parser failed"), wantCode: MySQLRestoreTargetNotClean},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote := &restoreFakeRemote{inspect: test.inspect, inspectErr: test.inspectErr}
			service := NewService(data, remote)
			connected := false
			service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
				connected = true
				return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
			}
			request := standaloneRestoreRequest(data.instance, backup, repositoryDir)
			request.Parameters["disasterConfirmed"] = true
			request.Parameters["createPreRestoreBackup"] = false
			err := service.restoreStandalone(context.Background(), request, registry.RunContext{TaskID: "task_disaster_classify", Log: fakeLogger{}})
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || stable.StableCode() != test.wantCode || connected || strings.Contains(strings.Join(remote.commands, "\n"), "DROP DATABASE") {
				t.Fatalf("unsafe disaster skip: err=%v commands=%v", err, remote.commands)
			}
		})
	}
	t.Run("explicit connection unreachable", func(t *testing.T) {
		data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
		repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
		data.backups = []store.AppBackup{backup}
		remote := &restoreFakeRemote{inspectErr: errors.New("dial tcp 10.0.0.8:3306: connection refused")}
		service := NewService(data, remote)
		preRestoreCalls := 0
		service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error {
			preRestoreCalls++
			return nil
		}
		service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
			return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
		}
		request := standaloneRestoreRequest(data.instance, backup, repositoryDir)
		request.Parameters["disasterConfirmed"] = true
		request.Parameters["createPreRestoreBackup"] = false
		if err := service.restoreStandalone(context.Background(), request, registry.RunContext{TaskID: "task_disaster_unreachable", Log: fakeLogger{}}); err != nil {
			t.Fatal(err)
		}
		if preRestoreCalls != 0 || !strings.Contains(strings.Join(remote.commands, "\n"), "DROP DATABASE") {
			t.Fatalf("disaster flow preRestore=%d commands=%v", preRestoreCalls, remote.commands)
		}
	})
}

func TestRestoreStandaloneReadableTargetRequiresExactManifestSchemaSetBeforeEnable(t *testing.T) {
	for _, schemas := range [][]string{{"aifar_business", "extra"}, {"other"}} {
		t.Run(strings.Join(schemas, "+"), func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
			repositoryDir, backup := createStandaloneRestoreBackup(t, data.instance)
			data.backups = []store.AppBackup{backup}
			remote := &restoreFakeRemote{inspect: standaloneInspection(schemas...)}
			service := NewService(data, remote)
			local := &fakeLocalInfileSession{value: "OFF"}
			service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
				return local, func() {}, nil
			}
			err := service.restoreStandalone(context.Background(), standaloneRestoreRequest(data.instance, backup, repositoryDir), registry.RunContext{TaskID: "task_schema_gate", Log: fakeLogger{}})
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || stable.StableCode() != MySQLRestoreTargetNotClean || len(local.setCalls) != 0 || strings.Contains(strings.Join(remote.commands, "\n"), "DROP DATABASE") {
				t.Fatalf("schema gate failed: schemas=%v calls=%v err=%v commands=%v", schemas, local.setCalls, err, remote.commands)
			}
		})
	}
}

func standaloneInspection(schemas ...string) string {
	var output strings.Builder
	output.WriteString("__AIFAR_INFO__\t8.0.36\t123e4567-e89b-12d3-a456-426614174000\tuuid:1-9\t8.0.36\t1048576\n")
	for _, schema := range schemas {
		output.WriteString("__AIFAR_SCHEMA__\t" + schema + "\n")
	}
	return output.String()
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
	archive := standaloneRestoreTar(t, []byte("{}"))
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	manifest, err := CanonicalBackupManifestJSON(BackupManifest{
		ManifestVersion: 2,
		BackupID:        backupID, App: "mysql", Topology: "standalone", InstanceID: instance.ID,
		SourceServerID: instance.ServerID, SourceEndpoint: "10.0.0.8:3306",
		SourceServerUUID: "123e4567-e89b-12d3-a456-426614174000", MySQLVersion: "8.0.36", MySQLShellVersion: "8.0.36",
		Schemas: []string{"aifar_business"}, ExcludedSchemas: append([]string(nil), fixedSystemSchemas...),
		Consistent: true, GTIDExecuted: "uuid:1-9", CreatedAt: time.Now().UTC(), TaskID: "tsk_1234567890abcdef12345678",
		Verification: validManifestV2Literal().Verification,
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

func standaloneRestoreTar(t *testing.T, done []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	if err := writer.WriteHeader(&tar.Header{Name: "dump/@.done.json", Mode: 0o600, Size: int64(len(done)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(done); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
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
