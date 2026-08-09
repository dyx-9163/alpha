package store

import (
	"errors"
	"reflect"
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

func TestSaveAIFARInitialDesiredWithLockRechecksExpiryAtTransactionWriteBoundary(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().UTC()
	owner := testAIFAROrchestrationLock("instance-1", "", "install")
	owner.ID = "near-expiry-owner"
	owner.StartedAt = now.Add(-time.Hour)
	owner.ExpiresAt = now.Add(2 * time.Second)
	if _, err := db.AcquireAIFAROrchestrationLock(owner); err != nil {
		t.Fatal(err)
	}

	// Hold the Store's only SQLite connection. Once WaitCount advances, the
	// save has finished validation and is blocked in Begin after capturing the
	// old implementation's pre-transaction timestamp.
	blocker, err := db.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	baselineWaitCount := db.db.Stats().WaitCount
	errCh := make(chan error, 1)
	go func() {
		errCh <- db.SaveAIFARInitialDesiredWithLock(owner.ID, []AIFARDeployment{{
			InstanceID: "instance-1", ServiceName: "gateway", DesiredReplicas: 1,
			CurrentRevision: "rev-1", SpecJSON: `{"service":"gateway","revision":"rev-1"}`,
			Generation: 1, Status: "pending_acceptance",
		}}, []AIFARReplicaSet{{
			InstanceID: "instance-1", ServiceName: "gateway", Revision: "rev-1",
			DesiredPods: 1, Status: "pending",
		}})
	}()
	deadline := owner.ExpiresAt.Add(-500 * time.Millisecond)
	for db.db.Stats().WaitCount <= baselineWaitCount && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if db.db.Stats().WaitCount <= baselineWaitCount {
		_ = blocker.Rollback()
		<-errCh
		t.Fatal("save did not reach the blocked transaction boundary")
	}
	// WaitCount can advance only after the goroutine has called Begin. The old
	// implementation captured its authorization time before that call, so this
	// assertion proves that stale timestamp was captured while the lease was
	// still valid. Releasing the connection only after expiry then exercises the
	// transaction write-boundary recheck deterministically.
	waitObservedAt := time.Now().UTC()
	if !waitObservedAt.Before(owner.ExpiresAt) {
		_ = blocker.Rollback()
		<-errCh
		t.Fatalf("save reached the transaction wait too late: observed=%s expires=%s", waitObservedAt, owner.ExpiresAt)
	}
	if wait := time.Until(owner.ExpiresAt.Add(25 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}

	if err := <-errCh; !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("save authorized an owner that expired before its write transaction: %v", err)
	}
	if got, err := db.ListAIFARDeployments("instance-1"); err != nil || len(got) != 0 {
		t.Fatalf("expired owner wrote deployments at transaction boundary: got=%+v err=%v", got, err)
	}
	if got, err := db.ListAIFARReplicaSets("instance-1"); err != nil || len(got) != 0 {
		t.Fatalf("expired owner wrote replicaSets at transaction boundary: got=%+v err=%v", got, err)
	}
}

func TestSaveAIFARInitialDesiredWithLockRequiresExactExistingServiceSet(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, db *Store, deployments []AIFARDeployment, replicaSets []AIFARReplicaSet)
	}{
		{
			name: "extra deployment",
			seed: func(t *testing.T, db *Store, _ []AIFARDeployment, _ []AIFARReplicaSet) {
				t.Helper()
				if _, err := db.SaveAIFARDeployment(AIFARDeployment{
					InstanceID: "instance-1", ServiceName: "extra", DesiredReplicas: 1,
					CurrentRevision: "extra-rev", SpecJSON: `{"service":"extra"}`,
					Generation: 1, Status: "pending_acceptance",
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra replicaSet",
			seed: func(t *testing.T, db *Store, _ []AIFARDeployment, _ []AIFARReplicaSet) {
				t.Helper()
				if _, err := db.SaveAIFARReplicaSet(AIFARReplicaSet{
					InstanceID: "instance-1", ServiceName: "extra", Revision: "extra-rev",
					Image: "aifar-extra:extra-rev", DesiredPods: 1, Status: "pending",
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "partial matching set",
			seed: func(t *testing.T, db *Store, deployments []AIFARDeployment, replicaSets []AIFARReplicaSet) {
				t.Helper()
				if _, err := db.SaveAIFARDeployment(deployments[0]); err != nil {
					t.Fatal(err)
				}
				if _, err := db.SaveAIFARReplicaSet(replicaSets[0]); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "mismatched existing desired",
			seed: func(t *testing.T, db *Store, deployments []AIFARDeployment, replicaSets []AIFARReplicaSet) {
				t.Helper()
				mismatched := deployments[0]
				mismatched.SpecJSON = `{"service":"gateway","revision":"other"}`
				if _, err := db.SaveAIFARDeployment(mismatched); err != nil {
					t.Fatal(err)
				}
				if _, err := db.SaveAIFARReplicaSet(replicaSets[0]); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestStore(t)
			owner := testAIFAROrchestrationLock("instance-1", "", "install")
			owner.ID = "owner"
			if _, err := db.AcquireAIFAROrchestrationLock(owner); err != nil {
				t.Fatal(err)
			}
			deployments, replicaSets := testInitialDesiredSet("instance-1", "rev-1", []string{"gateway", "system"})
			tc.seed(t, db, deployments, replicaSets)
			beforeDeployments, err := db.ListAIFARDeployments("instance-1")
			if err != nil {
				t.Fatal(err)
			}
			beforeReplicaSets, err := db.ListAIFARReplicaSets("instance-1")
			if err != nil {
				t.Fatal(err)
			}

			err = db.SaveAIFARInitialDesiredWithLock(owner.ID, deployments, replicaSets)
			if !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
				t.Errorf("non-exact existing set error=%v, want generation conflict", err)
			}
			afterDeployments, listErr := db.ListAIFARDeployments("instance-1")
			if listErr != nil {
				t.Fatal(listErr)
			}
			afterReplicaSets, listErr := db.ListAIFARReplicaSets("instance-1")
			if listErr != nil {
				t.Fatal(listErr)
			}
			if !reflect.DeepEqual(afterDeployments, beforeDeployments) || !reflect.DeepEqual(afterReplicaSets, beforeReplicaSets) {
				t.Fatalf("non-exact set was changed instead of rolling back: deployments before=%+v after=%+v replicaSets before=%+v after=%+v", beforeDeployments, afterDeployments, beforeReplicaSets, afterReplicaSets)
			}
		})
	}
}

func TestSaveAIFARInitialDesiredWithLockPreservesExactObservedSet(t *testing.T) {
	db := openTestStore(t)
	owner := testAIFAROrchestrationLock("instance-1", "", "install")
	owner.ID = "owner"
	if _, err := db.AcquireAIFAROrchestrationLock(owner); err != nil {
		t.Fatal(err)
	}
	deployments, replicaSets := testInitialDesiredSet("instance-1", "rev-1", []string{"gateway", "system"})
	if err := db.SaveAIFARInitialDesiredWithLock(owner.ID, deployments, replicaSets); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ObserveAIFARDeployment("instance-1", "gateway", 1, "Available", `[{"reason":"Ready"}]`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	observedReplicaSet := replicaSets[0]
	observedReplicaSet.ReadyPods = 1
	observedReplicaSet.Status = "active"
	if _, err := db.SaveAIFARReplicaSet(observedReplicaSet); err != nil {
		t.Fatal(err)
	}

	if err := db.SaveAIFARInitialDesiredWithLock(owner.ID, deployments, replicaSets); err != nil {
		t.Fatalf("exact observed set must remain idempotent: %v", err)
	}
	observed := deploymentByName(t, db, "instance-1", "gateway")
	if observed.ObservedGeneration != 1 || observed.Status != "Available" {
		t.Fatalf("exact retry regressed deployment observation: %+v", observed)
	}
	gotReplicaSets, err := db.ListAIFARReplicaSets("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	foundReady := false
	for _, replicaSet := range gotReplicaSets {
		if replicaSet.ServiceName == "gateway" && replicaSet.Revision == "rev-1" {
			foundReady = replicaSet.ReadyPods == 1 && replicaSet.Status == "active"
		}
	}
	if !foundReady {
		t.Fatalf("exact retry regressed replicaSet observation: %+v", gotReplicaSets)
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

func testInitialDesiredSet(instanceID, revision string, services []string) ([]AIFARDeployment, []AIFARReplicaSet) {
	deployments := make([]AIFARDeployment, 0, len(services))
	replicaSets := make([]AIFARReplicaSet, 0, len(services))
	for _, serviceName := range services {
		deployments = append(deployments, AIFARDeployment{
			InstanceID: instanceID, ServiceName: serviceName, DesiredReplicas: 1,
			CurrentRevision: revision, StrategyJSON: `{"type":"RollingUpdate"}`,
			SpecJSON:   `{"service":"` + serviceName + `","revision":"` + revision + `"}`,
			Generation: 1, Status: "pending_acceptance",
		})
		replicaSets = append(replicaSets, AIFARReplicaSet{
			InstanceID: instanceID, ServiceName: serviceName, Revision: revision,
			Image: "aifar-" + serviceName + ":" + revision, ArtifactHash: "artifact-hash",
			DesiredPods: 1, Status: "pending",
		})
	}
	return deployments, replicaSets
}
