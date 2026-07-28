package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type credentialRequiredReconcileRemote struct {
	password   string
	cleanupErr error
	commands   []string
	uploads    int
}

type reconciliationValidationRemote struct {
	password        string
	unsafeKind      string
	failAt          int
	validationCalls int
	mysqlshRuns     int
}

func TestDefaultLocalInfileSessionReportsLocalAndRemoteCredentialCleanupFailures(t *testing.T) {
	remote := newBackupFakeRemote()
	remote.cleanupErr = errors.New("private remote cleanup failure")
	originalRemove := removeMySQLCredentialContextFile
	removeMySQLCredentialContextFile = func(name string) error {
		_ = os.Remove(name)
		return errors.New("private local cleanup failure")
	}
	t.Cleanup(func() { removeMySQLCredentialContextFile = originalRemove })
	instance := store.AppInstance{ID: "app_cleanup_reporter", App: "mysql", Version: "8.0.36", Metadata: `{"port":3306}`}
	server := store.Server{ID: "srv_cleanup_reporter"}
	credential := store.Credential{Kind: "mysql", Status: "active", Username: "root", Secret: map[string]string{"password": "cleanup-test-secret"}}

	session, cleanup, err := defaultLocalInfileSessionFactory(remote)(context.Background(), instance, server, credential)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	reporter, ok := session.(credentialCleanupReporter)
	if !ok || reporter.CredentialCleanupError() == nil {
		t.Fatal("credential cleanup failure was swallowed")
	}
	if remote.cleanupRuns != 1 || strings.Contains(reporter.CredentialCleanupError().Error(), "private") || strings.Contains(reporter.CredentialCleanupError().Error(), "cleanup-test-secret") {
		t.Fatalf("cleanup result was not attempted and sanitized: runs=%d err=%v", remote.cleanupRuns, reporter.CredentialCleanupError())
	}
}

func (r *reconciliationValidationRemote) Run(_ context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if server.Password != r.password && strings.TrimSpace(server.PrivateKey) == "" {
		return adapter.CommandResult{}, errors.New("saved SSH credential missing")
	}
	if !strings.Contains(command, "mysqlsh") {
		return adapter.CommandResult{}, nil
	}
	hasValidation := strings.Contains(command, "test -f") && strings.Contains(command, "test ! -L") &&
		strings.Contains(command, "stat -c '%u'") && strings.Contains(command, "id -u") && strings.Contains(command, "stat -c '%a'") && strings.Contains(command, "= 600")
	if !hasValidation {
		return adapter.CommandResult{}, errors.New("remote credential validation missing")
	}
	r.validationCalls++
	if r.validationCalls == r.failAt {
		return adapter.CommandResult{}, errors.New("private-remote-context rejected as " + r.unsafeKind)
	}
	r.mysqlshRuns++
	if strings.Contains(command, "SELECT @@GLOBAL.local_infile") {
		return adapter.CommandResult{Stdout: "OFF\n"}, nil
	}
	return adapter.CommandResult{}, nil
}

func (r *reconciliationValidationRemote) UploadFile(_ context.Context, server store.Server, _ string, _ string, _ os.FileMode) error {
	if server.Password != r.password && strings.TrimSpace(server.PrivateKey) == "" {
		return errors.New("saved SSH credential missing")
	}
	return nil
}

func TestExplicitReconciliationValidatesRemoteCredentialBeforeEveryMySQLShellRead(t *testing.T) {
	for index, unsafeKind := range []string{"symlink", "wrong type", "wrong owner", "wrong mode"} {
		t.Run(unsafeKind, func(t *testing.T) {
			db, instance := newStandaloneReconciliationStore(t, "ssh-test-only-secret")
			failAt := 1
			if index%2 == 1 {
				failAt = 2
			}
			remote := &reconciliationValidationRemote{password: "ssh-test-only-secret", unsafeKind: unsafeKind, failAt: failAt}
			module := NewModule(db, remote)
			err := module.Reconcile(context.Background(), mustReconciliationPlan(t, db, instance), "en", store.NewID("tsk"), fakeLogger{})
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || stable.StableCode() != MySQLReconciliationRequired || strings.Contains(errString(err), "private-remote-context") {
				t.Fatalf("unsafe context was not rejected generically: %T %v", err, err)
			}
			if remote.validationCalls != failAt || remote.mysqlshRuns != failAt-1 {
				t.Fatalf("validation boundary calls=%d mysqlshRuns=%d want validation=%d mysqlsh=%d", remote.validationCalls, remote.mysqlshRuns, failAt, failAt-1)
			}
			fresh, getErr := db.GetAppInstance(instance.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if _, _, present, parseErr := parseMySQLReconciliationMarker(fresh.Metadata); parseErr != nil || !present {
				t.Fatalf("unsafe remote context cleared marker: present=%v err=%v", present, parseErr)
			}
		})
	}
}

func (r *credentialRequiredReconcileRemote) Run(_ context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	r.commands = append(r.commands, command)
	if server.Password != r.password && strings.TrimSpace(server.PrivateKey) == "" {
		return adapter.CommandResult{}, errors.New("saved SSH credential missing")
	}
	if strings.Contains(command, r.password) {
		return adapter.CommandResult{}, errors.New("SSH credential leaked into command")
	}
	if strings.Contains(command, "rm -rf") && r.cleanupErr != nil {
		return adapter.CommandResult{}, r.cleanupErr
	}
	if strings.Contains(command, "SELECT @@GLOBAL.local_infile") {
		return adapter.CommandResult{Stdout: "OFF\n"}, nil
	}
	return adapter.CommandResult{}, nil
}

func (r *credentialRequiredReconcileRemote) UploadFile(_ context.Context, server store.Server, _ string, _ string, _ os.FileMode) error {
	if server.Password != r.password && strings.TrimSpace(server.PrivateKey) == "" {
		return errors.New("saved SSH credential missing")
	}
	r.uploads++
	return nil
}

func TestExplicitReconciliationUsesSavedSSHCredentialWithoutLeakingIt(t *testing.T) {
	db, instance := newStandaloneReconciliationStore(t, "ssh-test-only-secret")
	remote := &credentialRequiredReconcileRemote{password: "ssh-test-only-secret"}
	module := NewModule(db, remote)
	err := module.Reconcile(context.Background(), mustReconciliationPlan(t, db, instance), "en", store.NewID("tsk"), fakeLogger{})
	if err != nil {
		t.Fatalf("reconciliation could not use the saved SSH credential: %v", err)
	}
	fresh, getErr := db.GetAppInstance(instance.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if _, _, present, parseErr := parseMySQLReconciliationMarker(fresh.Metadata); parseErr != nil || present {
		t.Fatalf("successful reconciliation retained marker: present=%v err=%v", present, parseErr)
	}
	joined := strings.Join(remote.commands, "\n") + errString(err) + fresh.Metadata
	if strings.Contains(joined, remote.password) || remote.uploads != 1 {
		t.Fatalf("SSH credential leaked or was not used: uploads=%d evidence=%q", remote.uploads, joined)
	}
}

func TestExplicitReconciliationRetainsMarkerWhenRemoteSecretCleanupFails(t *testing.T) {
	db, instance := newStandaloneReconciliationStore(t, "ssh-test-only-secret")
	remote := &credentialRequiredReconcileRemote{password: "ssh-test-only-secret", cleanupErr: errors.New("private-path cleanup failed")}
	module := NewModule(db, remote)
	err := module.Reconcile(context.Background(), mustReconciliationPlan(t, db, instance), "en", store.NewID("tsk"), fakeLogger{})
	var stable interface{ StableCode() string }
	if !errors.As(err, &stable) || stable.StableCode() != MySQLReconciliationRequired || strings.Contains(errString(err), "private-path") {
		t.Fatalf("cleanup failure was not sanitized: %T %v", err, err)
	}
	fresh, getErr := db.GetAppInstance(instance.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if _, _, present, parseErr := parseMySQLReconciliationMarker(fresh.Metadata); parseErr != nil || !present {
		t.Fatalf("cleanup failure cleared marker: present=%v err=%v metadata=%s", present, parseErr, fresh.Metadata)
	}
}

func TestExplicitReconciliationCompletesCleanupBeforeMarkerCASAcrossFailureMatrix(t *testing.T) {
	for _, test := range []struct {
		name       string
		cancel     bool
		primaryErr error
		cleanupErr error
		wantOK     bool
	}{
		{name: "success", wantOK: true},
		{name: "primary failure", primaryErr: errors.New("primary private-value failure")},
		{name: "cancellation", cancel: true},
		{name: "local cleanup failure", cleanupErr: errors.New("local private-path cleanup failure")},
		{name: "remote cleanup failure", cleanupErr: errors.New("remote private-path cleanup failure")},
		{name: "primary and both cleanup failures", primaryErr: errors.New("primary private-value failure"), cleanupErr: errors.Join(errors.New("local private-path cleanup failure"), errors.New("remote private-path cleanup failure"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, instance := newStandaloneReconciliationStore(t, "ssh-test-only-secret")
			plan := mustReconciliationPlan(t, db, instance)
			module := NewModule(db, newBackupFakeRemote())
			cleanupCalls := 0
			markerPresentDuringCleanup := false
			module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
				session := &fakeLocalInfileSession{value: "ON", setErr: test.primaryErr}
				return session, func() error {
					cleanupCalls++
					fresh, err := db.GetAppInstance(instance.ID)
					if err == nil {
						_, _, markerPresentDuringCleanup, _ = parseMySQLReconciliationMarker(fresh.Metadata)
					}
					return test.cleanupErr
				}, nil
			}
			ctx := context.Background()
			if test.cancel {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
				module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
					return cancelledReconciliationSession{}, func() error {
						cleanupCalls++
						fresh, _ := db.GetAppInstance(instance.ID)
						_, _, markerPresentDuringCleanup, _ = parseMySQLReconciliationMarker(fresh.Metadata)
						return nil
					}, nil
				}
			}
			err := module.Reconcile(ctx, plan, "en", store.NewID("tsk"), fakeLogger{})
			if (err == nil) != test.wantOK || cleanupCalls != 1 || !markerPresentDuringCleanup {
				t.Fatalf("result err=%v cleanupCalls=%d markerDuringCleanup=%v", err, cleanupCalls, markerPresentDuringCleanup)
			}
			fresh, getErr := db.GetAppInstance(instance.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			_, _, present, parseErr := parseMySQLReconciliationMarker(fresh.Metadata)
			if parseErr != nil || present == test.wantOK {
				t.Fatalf("marker state present=%v want=%v err=%v", present, !test.wantOK, parseErr)
			}
			if err != nil && (strings.Contains(err.Error(), "private-value") || strings.Contains(err.Error(), "private-path")) {
				t.Fatalf("unsanitized failure: %v", err)
			}
		})
	}
}

type cancelledReconciliationSession struct{}

func (cancelledReconciliationSession) SetLocalInfile(ctx context.Context, _ string) error {
	return ctx.Err()
}
func (cancelledReconciliationSession) ReadLocalInfile(ctx context.Context) (string, error) {
	return "", ctx.Err()
}

func TestReconciliationSessionCleanupAttemptsLocalAndRemoteIndependentlyAndSanitizesEvidence(t *testing.T) {
	remote := &credentialRequiredReconcileRemote{password: "ssh-test-only-secret", cleanupErr: errors.New("remote private-path failure")}
	localCalls := 0
	factory := reconciliationSessionFactoryWithRemove(remote, func(string) error {
		localCalls++
		return errors.New("local private-path failure")
	})
	instance := store.AppInstance{ID: store.NewID("app"), App: "mysql", ServerID: store.NewID("srv"), Topology: "standalone", Metadata: `{"port":3306}`}
	server := store.Server{ID: instance.ServerID, Password: remote.password}
	credential := store.Credential{Kind: "mysql", Username: "root", Secret: map[string]string{"password": "mysql-private-value"}}
	_, cleanup, err := factory(context.Background(), instance, server, credential)
	if err != nil {
		t.Fatal(err)
	}
	err = cleanup()
	joined := errString(err) + strings.Join(remote.commands, "\n")
	if localCalls != 1 || err == nil || !strings.Contains(joined, "local reconciliation secret cleanup failed") || !strings.Contains(joined, "remote reconciliation secret cleanup failed") || strings.Contains(joined, "private-path") || strings.Contains(joined, "mysql-private-value") {
		t.Fatalf("cleanup was not independent and sanitized: local=%d evidence=%q", localCalls, joined)
	}
}

func newStandaloneReconciliationStore(t *testing.T, sshPassword string) (*store.Store, store.AppInstance) {
	t.Helper()
	db, err := store.OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "reconciliation-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root", Password: sshPassword})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "standalone", Metadata: `{"port":3306,"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := db.SaveCredential(store.Credential{Name: "mysql-admin", Kind: "mysql", Username: "root", Scope: "app-instance", Status: "active", Secret: map[string]string{"password": "mysql-test-only-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindCredential(store.CredentialBinding{CredentialID: credential.ID, AppInstanceID: instance.ID, Purpose: "admin"}); err != nil {
		t.Fatal(err)
	}
	return db, instance
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func mustReconciliationPlan(t *testing.T, data maintenanceReader, instance store.AppInstance) ReconciliationPlan {
	t.Helper()
	plan, err := BuildReconciliationPlan(data, instance)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type fakeLocalInfileSession struct {
	value            string
	setErr           error
	setErrors        map[string]error
	readErr          error
	setCalls         []string
	onSet            func(string)
	applyBeforeError map[string]bool
	ignoreSet        bool
}

func (s *fakeLocalInfileSession) ReadLocalInfile(context.Context) (string, error) {
	if s.readErr != nil {
		return "", s.readErr
	}
	return s.value, nil
}

func (s *fakeLocalInfileSession) SetLocalInfile(_ context.Context, value string) error {
	s.setCalls = append(s.setCalls, value)
	if s.onSet != nil {
		s.onSet(value)
	}
	if s.applyBeforeError[value] {
		s.value = value
	}
	if err := s.setErrors[value]; err != nil {
		return err
	}
	if s.setErr != nil {
		return s.setErr
	}
	if s.ignoreSet {
		return nil
	}
	s.value = value
	return nil
}

func TestLocalInfileGuardRestoresOriginalOFFAndONIdempotently(t *testing.T) {
	for _, original := range []string{"OFF", "ON"} {
		t.Run(original, func(t *testing.T) {
			session := &fakeLocalInfileSession{value: original}
			guard := newLocalInfileGuard(session)
			if err := guard.Capture(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := guard.Enable(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := guard.Restore(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := guard.Restore(context.Background()); err != nil {
				t.Fatalf("idempotent Restore: %v", err)
			}
			if session.value != original {
				t.Fatalf("local_infile = %s, want %s", session.value, original)
			}
			wantSetCalls := 2
			if original == "ON" {
				wantSetCalls = 1
			}
			if len(session.setCalls) != wantSetCalls {
				t.Fatalf("SetLocalInfile calls = %v", session.setCalls)
			}
		})
	}
}

func TestLocalInfileGuardFailsClosedWhenRestoreCannotBeVerified(t *testing.T) {
	session := &fakeLocalInfileSession{value: "OFF"}
	guard := newLocalInfileGuard(session)
	if err := guard.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := guard.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	session.readErr = errors.New("target unreachable")
	err := guard.Restore(context.Background())
	if err == nil {
		t.Fatal("Restore succeeded without verifying the original value")
	}
}

type restoreFakeStore struct {
	*backupFakeStore
	savedInstances         []store.AppInstance
	instanceSaveFailures   int
	instanceSaveCalls      int
	instanceSaveFailCalls  map[int]bool
	backupSaveFailures     map[string]int
	backupSaveCalls        int
	clearReconciliationErr error
}

func (s *restoreFakeStore) ClearMySQLReconciliation(instanceID, original, recordedAt, taskID string) error {
	if s.clearReconciliationErr != nil {
		return s.clearReconciliationErr
	}
	fresh, err := s.GetAppInstance(instanceID)
	if err != nil {
		return err
	}
	metadata, marker, present, err := parseMySQLReconciliationMarker(fresh.Metadata)
	if err != nil || !present || marker.OriginalValue != original || marker.RecordedAt != recordedAt || marker.TaskID != taskID {
		return errors.New("reconciliation compare-and-swap failed")
	}
	delete(metadata, "mysqlReconciliation")
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	fresh.Metadata = string(encoded)
	_, err = s.SaveAppInstance(fresh)
	return err
}

func (s *restoreFakeStore) GetAppCluster(string) (store.AppCluster, error) {
	return store.AppCluster{}, errors.New("cluster unavailable")
}

func (s *restoreFakeStore) ListAppClusterMembers(string) ([]store.AppClusterMember, error) {
	return nil, errors.New("cluster unavailable")
}

func (s *restoreFakeStore) SetMySQLMaintenance([]string, store.MySQLMaintenanceMarker) error {
	return nil
}
func (s *restoreFakeStore) AdvanceMySQLMaintenance([]string, store.MySQLMaintenanceMarker, string) error {
	return nil
}
func (s *restoreFakeStore) ClearMySQLMaintenance([]string, store.MySQLMaintenanceMarker) error {
	return nil
}

func TestExplicitReconciliationRestoresExactValueAndPreservesMaintenance(t *testing.T) {
	for _, original := range []string{"OFF", "ON"} {
		t.Run(original, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
			data.instance.Metadata = `{"port":3306,"keep":"value","mysqlMaintenance":{"version":1,"state":"required","reason":"restore_incomplete","scope":"standalone","backupId":"backup_1234567890abcdef12345678","taskId":"tsk_1234567890abcdef12345678","restorePhase":"load_complete","recordedAt":"2026-07-28T00:00:00Z"},"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"` + original + `","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`
			session := &fakeLocalInfileSession{value: map[string]string{"OFF": "ON", "ON": "OFF"}[original]}
			module := NewModule(data, newBackupFakeRemote())
			module.service.reconciliationSession = func(_ context.Context, instance store.AppInstance, _ store.Server, credential store.Credential) (localInfileSession, func() error, error) {
				if instance.ID != data.instance.ID || credential.Username != "root" || credential.Secret["password"] == "" {
					t.Fatalf("incomplete authoritative inputs: instance=%+v credential=%+v", instance, credential)
				}
				return session, func() error { return nil }, nil
			}
			if err := module.Reconcile(context.Background(), mustReconciliationPlan(t, data, data.instance), "en", "tsk_999999999999999999999999", fakeLogger{}); err != nil {
				t.Fatal(err)
			}
			if session.value != original || len(session.setCalls) != 1 || session.setCalls[0] != original {
				t.Fatalf("session=%+v", session)
			}
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal([]byte(data.instance.Metadata), &metadata); err != nil {
				t.Fatal(err)
			}
			if _, present := metadata["mysqlReconciliation"]; present || len(metadata["mysqlMaintenance"]) == 0 || string(metadata["keep"]) != `"value"` {
				t.Fatalf("metadata after explicit reconciliation: %s", data.instance.Metadata)
			}
		})
	}
}

func TestExplicitReconciliationFailsClosedForCredentialMarkerAndVerificationFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*restoreFakeStore, *fakeLocalInfileSession)
	}{
		{name: "missing username", mutate: func(data *restoreFakeStore, _ *fakeLocalInfileSession) { data.credential.Username = "" }},
		{name: "marker drift", mutate: func(data *restoreFakeStore, _ *fakeLocalInfileSession) {
			data.instance.Metadata = strings.Replace(data.instance.Metadata, "tsk_abcdef1234567890abcdef12", "tsk_000000000000000000000000", 1)
		}},
		{name: "set failure", mutate: func(_ *restoreFakeStore, session *fakeLocalInfileSession) { session.setErr = errors.New("set failed") }},
		{name: "readback mismatch", mutate: func(_ *restoreFakeStore, session *fakeLocalInfileSession) { session.ignoreSet = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
			data.instance.Metadata = `{"port":3306,"mysqlMaintenance":{"version":1,"state":"required","reason":"restore_incomplete","scope":"standalone","backupId":"backup_1234567890abcdef12345678","taskId":"tsk_1234567890abcdef12345678","restorePhase":"load_complete","recordedAt":"2026-07-28T00:00:00Z"},"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`
			expected := data.instance
			plan := mustReconciliationPlan(t, data, expected)
			session := &fakeLocalInfileSession{value: "ON"}
			test.mutate(data, session)
			module := NewModule(data, newBackupFakeRemote())
			module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
				return session, func() error { return nil }, nil
			}
			err := module.Reconcile(context.Background(), plan, "en", "tsk_999999999999999999999999", fakeLogger{})
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || stable.StableCode() != MySQLReconciliationRequired {
				t.Fatalf("error=%T %v", err, err)
			}
			if !strings.Contains(data.instance.Metadata, `"mysqlReconciliation"`) || !strings.Contains(data.instance.Metadata, `"mysqlMaintenance"`) {
				t.Fatalf("failure cleared a safety marker: %s", data.instance.Metadata)
			}
		})
	}
}

func TestExplicitReconciliationSanitizesConnectionAndCASFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		connectErr error
		clearErr   error
	}{
		{name: "connection", connectErr: errors.New("password=private-value connection failed")},
		{name: "compare and swap", clearErr: errors.New("metadata write private-value failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t), clearReconciliationErr: test.clearErr}
			data.instance.Metadata = `{"port":3306,"mysqlMaintenance":{"version":1,"state":"required","reason":"restore_incomplete","scope":"standalone","backupId":"backup_1234567890abcdef12345678","taskId":"tsk_1234567890abcdef12345678","restorePhase":"load_complete","recordedAt":"2026-07-28T00:00:00Z"},"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`
			module := NewModule(data, newBackupFakeRemote())
			module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
				if test.connectErr != nil {
					return nil, func() error { return nil }, test.connectErr
				}
				return &fakeLocalInfileSession{value: "ON"}, func() error { return nil }, nil
			}
			err := module.Reconcile(context.Background(), mustReconciliationPlan(t, data, data.instance), "en", store.NewID("tsk"), fakeLogger{})
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || stable.StableCode() != MySQLReconciliationRequired || strings.Contains(err.Error(), "private-value") {
				t.Fatalf("unsafe error=%T %v", err, err)
			}
			if !strings.Contains(data.instance.Metadata, `"mysqlReconciliation"`) || !strings.Contains(data.instance.Metadata, `"mysqlMaintenance"`) {
				t.Fatalf("failure cleared marker: %s", data.instance.Metadata)
			}
		})
	}
}

func TestOrdinaryCheckDoesNotRepairOrClearReconciliationMarker(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	data.instance.Metadata = `{"port":3306,"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`
	session := &fakeLocalInfileSession{value: "ON"}
	service := NewService(data, newBackupFakeRemote())
	service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return session, func() {}, nil
	}
	_, err := service.Check(context.Background(), CheckRequest{Instance: data.instance, Server: data.server, Language: "en"}, fakeLogger{}, nil)
	var stable interface{ StableCode() string }
	if !errors.As(err, &stable) || stable.StableCode() != MySQLReconciliationRequired {
		t.Fatalf("error=%T %v", err, err)
	}
	if len(session.setCalls) != 0 || !strings.Contains(data.instance.Metadata, `"mysqlReconciliation"`) {
		t.Fatalf("ordinary check mutated reconciliation state: calls=%v metadata=%s", session.setCalls, data.instance.Metadata)
	}
}

func TestOrdinaryCheckRejectsSecondaryClusterReconciliationAndMembershipDriftBeforeRemoteCall(t *testing.T) {
	for _, drift := range []bool{false, true} {
		name := "secondary marker"
		if drift {
			name = "member server drift"
		}
		t.Run(name, func(t *testing.T) {
			db, instances, clusterID := newOrdinaryReconciliationCluster(t)
			if drift {
				members, err := db.ListAppClusterMembers(clusterID)
				if err != nil {
					t.Fatal(err)
				}
				members[1].ServerID = instances[0].ServerID
				if _, err := db.SaveAppClusterMember(members[1]); err != nil {
					t.Fatal(err)
				}
			}
			remote := newBackupFakeRemote()
			service := NewService(db, remote)
			_, err := service.Check(context.Background(), CheckRequest{Instance: instances[0], Language: "en"}, fakeLogger{}, nil)
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || (stable.StableCode() != MySQLReconciliationRequired && (!drift || stable.StableCode() != MySQLMaintenanceStateInvalid)) {
				t.Fatalf("error=%T %v", err, err)
			}
			if len(remote.commands) != 0 {
				t.Fatalf("ordinary check reached remote target: %v", remote.commands)
			}
		})
	}
}

func TestOrdinaryClusterMutationServicesRejectSecondaryReconciliationBeforeRemoteCall(t *testing.T) {
	for _, operation := range []string{"backup", "restore", "start", "delete"} {
		t.Run(operation, func(t *testing.T) {
			db, instances, _ := newOrdinaryReconciliationCluster(t)
			remote := newBackupFakeRemote()
			module := NewModule(db, remote)
			var err error
			if operation == "backup" {
				err = module.service.backupInnoDBCluster(context.Background(), registry.BackupRequest{Instance: instances[0], Language: "en"}, registry.RunContext{})
			} else if operation == "restore" {
				err = module.restoreHealthyInnoDBCluster(context.Background(), registry.RestoreRequest{Instance: instances[0], Language: "en"}, registry.RunContext{})
			} else if operation == "start" {
				err = module.service.StartInnoDBCluster(context.Background(), StartClusterRequest{Instances: instances, Language: "en"}, fakeLogger{}, nil)
			} else {
				err = module.service.Delete(context.Background(), DeleteRequest{Instance: instances[0], Language: "en"}, fakeLogger{}, nil)
			}
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || stable.StableCode() != MySQLReconciliationRequired {
				t.Fatalf("error=%T %v", err, err)
			}
			if len(remote.commands) != 0 {
				t.Fatalf("%s reached remote target: %v", operation, remote.commands)
			}
		})
	}
}

func newOrdinaryReconciliationCluster(t *testing.T) (*store.Store, []store.AppInstance, string) {
	t.Helper()
	db, err := store.OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "reconciliation-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clusterID := "cluster_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "ordinary-gate", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	instances := make([]store.AppInstance, 0, 3)
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0." + string(rune('1'+index)), Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		metadata := `{"clusterId":"` + clusterID + `"}`
		if index == 1 {
			metadata = `{"clusterId":"` + clusterID + `","mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`
		}
		instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "innodb-cluster", Metadata: metadata})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	return db, instances, clusterID
}

func TestExplicitClusterReconciliationUsesAuthoritativeTopologyAndFailsOnDrift(t *testing.T) {
	for _, drift := range []bool{false, true} {
		name := "success"
		if drift {
			name = "topology drift"
		}
		t.Run(name, func(t *testing.T) {
			db, affected, clusterID := newReconciliationCluster(t)
			module := NewModule(db, newBackupFakeRemote())
			session := &fakeLocalInfileSession{value: "ON"}
			if drift {
				session.onSet = func(string) {
					if err := db.DeleteAppCluster(clusterID); err != nil {
						t.Fatal(err)
					}
				}
			}
			module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
				return session, func() error { return nil }, nil
			}
			err := module.Reconcile(context.Background(), mustReconciliationPlan(t, db, affected), "en", store.NewID("tsk"), fakeLogger{})
			if drift {
				var stable interface{ StableCode() string }
				if !errors.As(err, &stable) || stable.StableCode() != MySQLReconciliationRequired {
					t.Fatalf("drift error=%T %v", err, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			fresh, getErr := db.GetAppInstance(affected.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			_, _, present, parseErr := parseMySQLReconciliationMarker(fresh.Metadata)
			if parseErr != nil || present != drift {
				t.Fatalf("present=%v parseErr=%v metadata=%s", present, parseErr, fresh.Metadata)
			}
			if _, maintenancePresent, maintenanceErr := store.ParseMySQLMaintenanceMarker(fresh.Metadata); maintenanceErr != nil || !maintenancePresent {
				t.Fatalf("maintenance changed: present=%v err=%v metadata=%s", maintenancePresent, maintenanceErr, fresh.Metadata)
			}
		})
	}
}

func TestExplicitClusterReconciliationRejectsPlanToWorkerIdentityDriftBeforeRemoteCall(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *store.Store, store.AppInstance, string)
	}{
		{name: "raw cluster id", mutate: func(t *testing.T, db *store.Store, affected store.AppInstance, clusterID string) {
			affected.Metadata = strings.Replace(affected.Metadata, clusterID, "cluster_000000000000000000000000", 1)
			if _, err := db.SaveAppInstance(affected); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "cluster row", mutate: func(t *testing.T, db *store.Store, _ store.AppInstance, clusterID string) {
			cluster, err := db.GetAppCluster(clusterID)
			if err != nil {
				t.Fatal(err)
			}
			cluster.Status = "maintenance"
			if _, err := db.SaveAppCluster(cluster); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "member instance", mutate: func(t *testing.T, db *store.Store, _ store.AppInstance, clusterID string) {
			server, err := db.SaveServer(store.Server{Name: "new-member", Host: "10.0.0.99", Username: "root"})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "innodb-cluster", Metadata: `{"clusterId":"` + clusterID + `"}`})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "ONLINE"}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "member server", mutate: func(t *testing.T, db *store.Store, affected store.AppInstance, clusterID string) {
			members, err := db.ListAppClusterMembers(clusterID)
			if err != nil {
				t.Fatal(err)
			}
			for _, member := range members {
				if member.InstanceID != affected.ID {
					member.ServerID = affected.ServerID
					if _, err := db.SaveAppClusterMember(member); err != nil {
						t.Fatal(err)
					}
					return
				}
			}
			t.Fatal("secondary cluster member not found")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, affected, clusterID := newReconciliationCluster(t)
			plan := mustReconciliationPlan(t, db, affected)
			test.mutate(t, db, affected, clusterID)
			remoteCalls := 0
			module := NewModule(db, newBackupFakeRemote())
			module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
				remoteCalls++
				return &fakeLocalInfileSession{value: "ON"}, func() error { return nil }, nil
			}
			err := module.Reconcile(context.Background(), plan, "en", store.NewID("tsk"), fakeLogger{})
			var stable interface{ StableCode() string }
			if !errors.As(err, &stable) || stable.StableCode() != MySQLReconciliationRequired || remoteCalls != 0 {
				t.Fatalf("drift reached remote path: calls=%d error=%T %v", remoteCalls, err, err)
			}
		})
	}
}

func TestOptionalMaintenanceStateRejectsMarkerScopeThatDoesNotMatchTopology(t *testing.T) {
	instance := store.AppInstance{ID: "app_1234567890abcdef12345678", App: "mysql", Topology: "standalone", Metadata: `{"mysqlMaintenance":{"version":1,"state":"required","reason":"restore_incomplete","scope":"cluster","clusterId":"cluster_1234567890abcdef12345678","backupId":"backup_1234567890abcdef12345678","taskId":"tsk_1234567890abcdef12345678","restorePhase":"load_complete","recordedAt":"2026-07-28T00:00:00Z"}}`}
	if validOptionalMaintenanceState([]store.AppInstance{instance}) {
		t.Fatal("cluster-scoped maintenance marker was accepted for a standalone reconciliation target")
	}
}

func newReconciliationCluster(t *testing.T) (*store.Store, store.AppInstance, string) {
	t.Helper()
	db, err := store.OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "reconciliation-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clusterID := "cluster_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "reconcile", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	instances := make([]store.AppInstance, 0, 3)
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0." + string(rune('1'+index)), Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		metadata := `{"clusterId":"` + clusterID + `","port":3306}`
		if index == 0 {
			metadata = `{"clusterId":"` + clusterID + `","port":3306,"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`
		}
		instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "innodb-cluster", Metadata: metadata})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: map[bool]string{true: "PRIMARY", false: "SECONDARY"}[index == 0], Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	marker := store.MySQLMaintenanceMarker{Version: 1, State: "required", Reason: "restore_incomplete", Scope: "cluster", ClusterID: clusterID, BackupID: "backup_1234567890abcdef12345678", TaskID: "tsk_1234567890abcdef12345678", RestorePhase: "load_complete", RecordedAt: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)}
	ids := []string{instances[0].ID, instances[1].ID, instances[2].ID}
	if err := db.SetMySQLMaintenance(ids, marker); err != nil {
		t.Fatal(err)
	}
	affected, err := db.GetAppInstance(instances[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := db.SaveCredential(store.Credential{Name: "mysql-admin", Kind: "mysql", Username: "root", Scope: "app-instance", Status: "active", Secret: map[string]string{"password": "test-only-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindCredential(store.CredentialBinding{CredentialID: credential.ID, AppInstanceID: affected.ID, Purpose: "admin"}); err != nil {
		t.Fatal(err)
	}
	return db, affected, clusterID
}

func (s *restoreFakeStore) SaveAppInstance(value store.AppInstance) (store.AppInstance, error) {
	s.instanceSaveCalls++
	if s.instanceSaveFailCalls[s.instanceSaveCalls] {
		return store.AppInstance{}, errors.New("injected instance save failure")
	}
	if s.instanceSaveFailures > 0 {
		s.instanceSaveFailures--
		return store.AppInstance{}, errors.New("injected instance save failure")
	}
	s.instance = value
	s.savedInstances = append(s.savedInstances, value)
	return value, nil
}

func (s *restoreFakeStore) SaveAppBackup(value store.AppBackup) (store.AppBackup, error) {
	s.backupSaveCalls++
	phase := restorePhase(value.Metadata)
	if s.backupSaveFailures[phase] > 0 {
		s.backupSaveFailures[phase]--
		return store.AppBackup{}, errors.New("injected backup phase save failure")
	}
	return s.backupFakeStore.SaveAppBackup(value)
}

func TestReconcileRestoresRecordedLocalInfileAndOnlyThenClearsMarker(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	data.instance.Metadata = `{"port":3306,"keep":"value","mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`
	session := &fakeLocalInfileSession{value: "ON"}
	module := NewModule(data, newBackupFakeRemote())
	module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
		return session, func() error { return nil }, nil
	}
	if err := module.Reconcile(context.Background(), mustReconciliationPlan(t, data, data.instance), "en", "tsk_999999999999999999999999", fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if session.value != "OFF" || len(session.setCalls) != 1 {
		t.Fatalf("session after reconciliation = value %q calls %v", session.value, session.setCalls)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data.instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if _, exists := metadata["mysqlReconciliation"]; exists {
		t.Fatalf("marker was not cleared: %s", data.instance.Metadata)
	}
	if string(metadata["keep"]) != `"value"` {
		t.Fatalf("unrelated metadata was changed: %s", data.instance.Metadata)
	}
}

func TestReconcileMalformedMarkerFailsClosedBeforeConnecting(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	data.instance.Metadata = `{"port":3306,"mysqlReconciliation":{"version":2,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"task_restore_1"}}`
	service := NewService(data, newBackupFakeRemote())
	connected := false
	service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		connected = true
		return nil, func() {}, errors.New("must not connect")
	}
	module := Module{service: service}
	err := module.Reconcile(context.Background(), mustReconciliationPlan(t, data, data.instance), "en", "tsk_999999999999999999999999", fakeLogger{})
	var stable interface{ StableCode() string }
	if !errors.As(err, &stable) || stable.StableCode() != "MYSQL_RECONCILIATION_REQUIRED" {
		t.Fatalf("error = %T %v", err, err)
	}
	if connected || len(data.savedInstances) != 0 {
		t.Fatalf("malformed marker caused mutation: connected=%v saves=%d", connected, len(data.savedInstances))
	}
}

func TestReconcileNoMarkerLeavesExistingCheckBehaviorUnchanged(t *testing.T) {
	data := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	remote := newBackupFakeRemote()
	remote.inspectOutput = ""
	service := NewService(data, remote)
	service.remote = runtimeProbeRemote{Remote: remote}
	result, err := service.Check(context.Background(), CheckRequest{
		Instance: data.instance, Server: data.server, Language: "en", DefaultPassword: "unused",
	}, fakeLogger{}, nil)
	if err != nil || result.Status != "running" {
		t.Fatalf("Check without marker = %+v, %v", result, err)
	}
}

type runtimeProbeRemote struct{ Remote }

func (r runtimeProbeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if strings.Contains(command, "AIFAR_MYSQL_RUNTIME_PROBE=1") {
		return adapter.CommandResult{Stdout: "runtimeStatus=running\nmysqlPingStatus=running\nmysqlServiceStatus=running\nmysqlPortStatus=running\nmysqlRuntimeSource=mysqladmin\n"}, nil
	}
	return r.Remote.Run(ctx, server, command)
}
