package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteLogRetentionBatchMethodsDeleteOnlyOneBatch(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	old := now.Add(-72 * time.Hour)
	recent := now.Add(-2 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)

	for i := 0; i < 3; i++ {
		if err := db.AddAudit("owner", fmt.Sprintf("old.audit.%d", i), "target", "success", "old"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`update audit_logs set created_at=? where action like 'old.audit.%'`, old); err != nil {
		t.Fatal(err)
	}
	if err := db.AddAudit("owner", "recent.audit", "target", "success", "recent"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`update audit_logs set created_at=? where action='recent.audit'`, recent); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		task, err := db.CreateTask(Task{Type: fmt.Sprintf("old.task.%d", i), Target: "target", Status: "success", CreatedBy: "owner", CreatedAt: old, FinishedAt: old})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.AddTaskLog(task.ID, "info", "old log"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateTask(Task{Type: "recent.task", Target: "target", Status: "success", CreatedBy: "owner", CreatedAt: recent, FinishedAt: recent}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		if _, _, err := db.UpsertStatusSnapshot(StatusSnapshot{Scope: "docker.summary", ResourceID: "srv-1", ServerID: "srv-1", Status: fmt.Sprintf("failed-%d", i), Payload: fmt.Sprintf(`{"i":%d}`, i), CollectedAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`update status_snapshot_history set created_at=? where scope='docker.summary'`, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertStatusSnapshot(StatusSnapshot{Scope: "docker.summary", ResourceID: "srv-1", ServerID: "srv-1", Status: "recent", Payload: `{"recent":true}`, CollectedAt: recent}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`update status_snapshot_history set created_at=? where status='recent'`, recent); err != nil {
		t.Fatal(err)
	}

	alert, _, err := db.UpsertAlert(Alert{Fingerprint: "fp-batch", Severity: "critical", Scope: "mysql", ResourceID: "mysql-1", Status: "open", Title: "mysql down"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.AddAlertEvent(AlertEvent{AlertID: alert.ID, Fingerprint: alert.Fingerprint, Event: fmt.Sprintf("updated-%d", i), Actor: "system", CreatedAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.AddAlertEvent(AlertEvent{AlertID: alert.ID, Fingerprint: alert.Fingerprint, Event: "recent", Actor: "system", CreatedAt: recent}); err != nil {
		t.Fatal(err)
	}

	assertBatch := func(name string, got int, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s batch delete failed: %v", name, err)
		}
		if got != 2 {
			t.Fatalf("%s batch deleted %d rows, want exactly one limited batch of 2", name, got)
		}
	}
	deleted, err := db.DeleteAuditLogsBeforeBatch(cutoff, 2)
	assertBatch("audit", deleted, err)
	deleted, err = db.DeleteFinishedTasksBeforeBatch(cutoff, 2)
	assertBatch("tasks", deleted, err)
	deleted, err = db.DeleteStatusSnapshotHistoryBeforeBatch(cutoff, 2)
	assertBatch("status history", deleted, err)
	deleted, err = db.DeleteAlertEventsBeforeBatch(cutoff, 2)
	assertBatch("alert events", deleted, err)

	for _, tc := range []struct {
		name  string
		query string
		want  int
	}{
		{"old audit", `select count(*) from audit_logs where created_at < ?`, 1},
		{"old tasks", `select count(*) from tasks where finished_at < ?`, 1},
		{"old status history", `select count(*) from status_snapshot_history where created_at < ?`, 2},
		{"old alert events", `select count(*) from alert_events where created_at < ?`, 1},
	} {
		var count int
		if err := db.db.QueryRow(tc.query, cutoff).Scan(&count); err != nil {
			t.Fatalf("%s count failed: %v", tc.name, err)
		}
		if count != tc.want {
			t.Fatalf("%s remaining count = %d, want %d", tc.name, count, tc.want)
		}
	}
}
