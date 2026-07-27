package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type fakeLocalInfileSession struct {
	value            string
	setErr           error
	setErrors        map[string]error
	readErr          error
	setCalls         []string
	onSet            func(string)
	applyBeforeError map[string]bool
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
	savedInstances        []store.AppInstance
	instanceSaveFailures  int
	instanceSaveCalls     int
	instanceSaveFailCalls map[int]bool
	backupSaveFailures    map[string]int
	backupSaveCalls       int
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
	data.instance.Metadata = `{"port":3306,"keep":"value","mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"task_restore_1"}}`
	session := &fakeLocalInfileSession{value: "ON"}
	service := NewService(data, newBackupFakeRemote())
	service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return session, func() {}, nil
	}
	if err := service.reconcileMySQL(context.Background(), data.instance, "en"); err != nil {
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
	err := service.reconcileMySQL(context.Background(), data.instance, "en")
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
