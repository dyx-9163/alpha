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

func TestAIFAROrchestrationLocksDoNotReleaseASuccessorByStaleID(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().UTC()
	ownerA := testAIFAROrchestrationLock("i1", "file", "scale")
	ownerA.ID = "owner-a"
	ownerA.StartedAt = now.Add(-2 * time.Hour)
	ownerA.ExpiresAt = now.Add(-time.Hour)
	if _, err := db.AcquireAIFAROrchestrationLock(ownerA); err != nil {
		t.Fatal(err)
	}
	ownerB := testAIFAROrchestrationLock("i1", "file", "scale")
	ownerB.ID = "owner-b"
	if _, err := db.AcquireAIFAROrchestrationLock(ownerB); err != nil {
		t.Fatal(err)
	}
	if released, err := db.ReleaseAIFAROrchestrationLockByID(ownerA.ID); err != nil || released {
		t.Fatalf("release stale owner: released=%v err=%v", released, err)
	}
	_, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock("i1", "file", "scale"))
	var conflict AIFAROrchestrationLockConflict
	if !errors.As(err, &conflict) || conflict.Lock.ID != ownerB.ID {
		t.Fatalf("same-service successor must stay locked: err=%v", err)
	}
}

func TestSaveAIFARInitialDesiredWithLockRejectsExpiredOwnerAtomically(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().UTC()
	ownerA := testAIFAROrchestrationLock("instance-1", "", "install")
	ownerA.ID = "owner-a"
	ownerA.StartedAt = now.Add(-2 * time.Hour)
	ownerA.ExpiresAt = now.Add(-time.Hour)
	if _, err := db.AcquireAIFAROrchestrationLock(ownerA); err != nil {
		t.Fatal(err)
	}
	ownerB := testAIFAROrchestrationLock("instance-1", "", "install")
	ownerB.ID = "owner-b"
	if _, err := db.AcquireAIFAROrchestrationLock(ownerB); err != nil {
		t.Fatal(err)
	}
	deployments := []AIFARDeployment{
		{InstanceID: "instance-1", ServiceName: "gateway", DesiredReplicas: 1, CurrentRevision: "rev-b", SpecJSON: `{"service":"gateway","revision":"rev-b"}`, Generation: 1, Status: "pending_acceptance"},
		{InstanceID: "instance-1", ServiceName: "system", DesiredReplicas: 1, CurrentRevision: "rev-b", SpecJSON: `{"service":"system","revision":"rev-b"}`, Generation: 1, Status: "pending_acceptance"},
	}
	replicaSets := []AIFARReplicaSet{
		{InstanceID: "instance-1", ServiceName: "gateway", Revision: "rev-b", DesiredPods: 1, ReadyPods: 0, Status: "pending"},
		{InstanceID: "instance-1", ServiceName: "system", Revision: "rev-b", DesiredPods: 1, ReadyPods: 0, Status: "pending"},
	}
	if err := db.SaveAIFARInitialDesiredWithLock(ownerA.ID, deployments, replicaSets); !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("expired owner error=%v", err)
	}
	if got, err := db.ListAIFARDeployments("instance-1"); err != nil || len(got) != 0 {
		t.Fatalf("expired owner partially wrote deployments: got=%+v err=%v", got, err)
	}
	if got, err := db.ListAIFARReplicaSets("instance-1"); err != nil || len(got) != 0 {
		t.Fatalf("expired owner partially wrote replicaSets: got=%+v err=%v", got, err)
	}
	if err := db.SaveAIFARInitialDesiredWithLock(ownerB.ID, deployments, replicaSets); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ListAIFARDeployments("instance-1"); len(got) != 2 {
		t.Fatalf("active owner deployments=%+v", got)
	}
	if got, _ := db.ListAIFARReplicaSets("instance-1"); len(got) != 2 {
		t.Fatalf("active owner replicaSets=%+v", got)
	}
}

func TestAcceptAIFARDeploymentWithLockRejectsExpiredOwnerAndWrongDesiredProof(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().UTC()
	ownerA := testAIFAROrchestrationLock("instance-1", "", "install")
	ownerA.ID = "owner-a"
	ownerA.StartedAt = now.Add(-2 * time.Hour)
	ownerA.ExpiresAt = now.Add(-time.Hour)
	if _, err := db.AcquireAIFAROrchestrationLock(ownerA); err != nil {
		t.Fatal(err)
	}
	ownerB := testAIFAROrchestrationLock("instance-1", "", "install")
	ownerB.ID = "owner-b"
	if _, err := db.AcquireAIFAROrchestrationLock(ownerB); err != nil {
		t.Fatal(err)
	}
	desiredB := AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "gateway", DesiredReplicas: 1, CurrentRevision: "rev-b",
		SpecJSON: `{"service":"gateway","revision":"rev-b"}`, Generation: 1, Status: "pending_acceptance",
	}
	if err := db.SaveAIFARInitialDesiredWithLock(ownerB.ID, []AIFARDeployment{desiredB}, []AIFARReplicaSet{{
		InstanceID: "instance-1", ServiceName: "gateway", Revision: "rev-b", DesiredPods: 1, Status: "pending",
	}}); err != nil {
		t.Fatal(err)
	}
	desiredA := desiredB
	desiredA.CurrentRevision = "rev-a"
	desiredA.SpecJSON = `{"service":"gateway","revision":"rev-a"}`
	if _, err := db.AcceptAIFARDeploymentWithLock(ownerA.ID, desiredA, "Accepted", `[{"reason":"A"}]`, now); !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("expired acceptance error=%v", err)
	}
	if _, err := db.AcceptAIFARDeploymentWithLock(ownerB.ID, desiredA, "Accepted", `[{"reason":"A"}]`, now); !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("wrong desired proof error=%v", err)
	}
	pending := deploymentByName(t, db, "instance-1", "gateway")
	if pending.Status != "pending_acceptance" || pending.CurrentRevision != "rev-b" || pending.SpecJSON != desiredB.SpecJSON {
		t.Fatalf("stale proof changed successor desired state: %+v", pending)
	}
	accepted, err := db.AcceptAIFARDeploymentWithLock(ownerB.ID, desiredB, "Accepted", `[{"reason":"B"}]`, now)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "Accepted" || accepted.CurrentRevision != "rev-b" || accepted.SpecJSON != desiredB.SpecJSON {
		t.Fatalf("exact proof was not accepted: %+v", accepted)
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
