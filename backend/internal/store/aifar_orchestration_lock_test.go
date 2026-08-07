package store

import (
	"errors"
	"testing"
	"time"
)

func TestAIFAROrchestrationLocksAllowDifferentServices(t *testing.T) {
	db := openTestStore(t)
	if _, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock("i1", "permission", "scale")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock("i1", "file", "offline")); err != nil {
		t.Fatal(err)
	}
}

func TestAIFAROrchestrationLocksRejectMaintenanceWhenAServiceIsActive(t *testing.T) {
	db := openTestStore(t)
	if _, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock("i1", "permission", "scale")); err != nil {
		t.Fatal(err)
	}
	_, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock("i1", "", "migrate"))
	var conflict AIFAROrchestrationLockConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error=%v, want lock conflict", err)
	}
}

func TestAIFAROrchestrationLocksRejectServiceWhenMaintenanceIsActive(t *testing.T) {
	db := openTestStore(t)
	if _, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock("i1", "", "migrate")); err != nil {
		t.Fatal(err)
	}
	_, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock("i1", "permission", "scale"))
	var conflict AIFAROrchestrationLockConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error=%v, want lock conflict", err)
	}
}

func TestAIFAROrchestrationLocksRenewOnlyActiveUnexpiredLocks(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().UTC()
	active := testAIFAROrchestrationLock("i1", "permission", "scale")
	active.ID = "active"
	active.ExpiresAt = now.Add(time.Hour)
	if _, err := db.AcquireAIFAROrchestrationLock(active); err != nil {
		t.Fatal(err)
	}
	wantExpiry := now.Add(2 * time.Hour)
	renewed, err := db.RenewAIFAROrchestrationLock(active.ID, wantExpiry)
	if err != nil || !renewed {
		t.Fatalf("renew active lock: renewed=%v err=%v", renewed, err)
	}

	if released, err := db.ReleaseAIFAROrchestrationLock("i1", "scale", "permission"); err != nil || !released {
		t.Fatalf("release active lock: released=%v err=%v", released, err)
	}
	if renewed, err := db.RenewAIFAROrchestrationLock(active.ID, now.Add(3*time.Hour)); err != nil || renewed {
		t.Fatalf("renew released lock: renewed=%v err=%v", renewed, err)
	}

	expired := testAIFAROrchestrationLock("i1", "file", "offline")
	expired.ID = "expired"
	expired.StartedAt = now.Add(-2 * time.Hour)
	expired.ExpiresAt = now.Add(-time.Hour)
	if _, err := db.AcquireAIFAROrchestrationLock(expired); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock("i1", "other", "scale")); err != nil {
		t.Fatal(err)
	}
	if renewed, err := db.RenewAIFAROrchestrationLock(expired.ID, now.Add(3*time.Hour)); err != nil || renewed {
		t.Fatalf("renew expired lock: renewed=%v err=%v", renewed, err)
	}
}

func testAIFAROrchestrationLock(instanceID, serviceName, operation string) AIFAROrchestrationLock {
	now := time.Now().UTC()
	return AIFAROrchestrationLock{
		InstanceID:  instanceID,
		ServiceName: serviceName,
		Operation:   operation,
		StartedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}
}
