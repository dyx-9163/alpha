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

func TestSaveAIFARDeploymentGenerationWithLockRequiresExactActiveServiceOwner(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().UTC()
	base, err := db.SaveAIFARDeployment(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "rev-1", SpecJSON: `{"service":"permission","revision":"rev-1"}`,
		Generation: 1, Status: "Accepted",
	})
	if err != nil {
		t.Fatal(err)
	}
	next := base
	next.DesiredReplicas = 2
	next.SpecJSON = `{"service":"permission","revision":"rev-1","replicas":2}`
	next.Status = "pending_acceptance"

	for _, tc := range []struct {
		name string
		lock AIFAROrchestrationLock
	}{
		{name: "different service", lock: testAIFAROrchestrationLock("instance-1", "file", "scale")},
		{name: "different instance", lock: testAIFAROrchestrationLock("instance-2", "permission", "scale")},
		{name: "non install global maintenance", lock: testAIFAROrchestrationLock("instance-1", "", "migrate")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acquired, err := db.AcquireAIFAROrchestrationLock(tc.lock)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.SaveAIFARDeploymentGenerationWithLock(acquired.ID, next, base.Generation); !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
				t.Fatalf("error=%v, want exact lock ownership rejection", err)
			}
			if released, err := db.ReleaseAIFAROrchestrationLockByID(acquired.ID); err != nil || !released {
				t.Fatalf("release test lock: released=%v err=%v", released, err)
			}
		})
	}

	ownerA := testAIFAROrchestrationLock("instance-1", "permission", "scale")
	ownerA.ID = "runtime-owner-a"
	ownerA.StartedAt = now.Add(-2 * time.Hour)
	ownerA.ExpiresAt = now.Add(-time.Hour)
	if _, err := db.AcquireAIFAROrchestrationLock(ownerA); err != nil {
		t.Fatal(err)
	}
	ownerB := testAIFAROrchestrationLock("instance-1", "permission", "scale")
	ownerB.ID = "runtime-owner-b"
	if _, err := db.AcquireAIFAROrchestrationLock(ownerB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeploymentGenerationWithLock(ownerA.ID, next, base.Generation); !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("expired predecessor error=%v", err)
	}
	saved, err := db.SaveAIFARDeploymentGenerationWithLock(ownerB.ID, next, base.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Generation != base.Generation+1 || saved.DesiredReplicas != 2 {
		t.Fatalf("active successor did not write exact next generation: %+v", saved)
	}
}

func TestSaveAIFARDeploymentGenerationWithLockRechecksExpiryAtWriteTransaction(t *testing.T) {
	db := openTestStore(t)
	base, err := db.SaveAIFARDeployment(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "rev-1", SpecJSON: `{"service":"permission","replicas":1}`,
		Generation: 1, Status: "Accepted",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner := testAIFAROrchestrationLock("instance-1", "permission", "scale")
	owner.ID = "near-expiry-runtime-owner"
	owner.StartedAt = now.Add(-time.Hour)
	owner.ExpiresAt = now.Add(2 * time.Second)
	if _, err := db.AcquireAIFAROrchestrationLock(owner); err != nil {
		t.Fatal(err)
	}
	blocker, err := db.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	baselineWaitCount := db.db.Stats().WaitCount
	next := base
	next.DesiredReplicas = 2
	next.SpecJSON = `{"service":"permission","replicas":2}`
	next.Status = "pending_acceptance"
	errCh := make(chan error, 1)
	go func() {
		_, saveErr := db.SaveAIFARDeploymentGenerationWithLock(owner.ID, next, base.Generation)
		errCh <- saveErr
	}()
	deadline := owner.ExpiresAt.Add(-500 * time.Millisecond)
	for db.db.Stats().WaitCount <= baselineWaitCount && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if db.db.Stats().WaitCount <= baselineWaitCount {
		_ = blocker.Rollback()
		<-errCh
		t.Fatal("runtime desired save did not reach the blocked transaction boundary")
	}
	if observed := time.Now().UTC(); !observed.Before(owner.ExpiresAt) {
		_ = blocker.Rollback()
		<-errCh
		t.Fatalf("runtime desired save reached transaction wait after expiry: observed=%s expires=%s", observed, owner.ExpiresAt)
	}
	if wait := time.Until(owner.ExpiresAt.Add(25 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("runtime desired write used pre-transaction lease time: %v", err)
	}
	current := deploymentByName(t, db, "instance-1", "permission")
	if current.Generation != base.Generation || current.DesiredReplicas != base.DesiredReplicas || current.SpecJSON != base.SpecJSON {
		t.Fatalf("expired runtime owner changed desired state: before=%+v after=%+v", base, current)
	}
}

func TestAcceptAIFARDeploymentWithLockSupportsExactServiceOwnerAndRejectsPredecessor(t *testing.T) {
	db := openTestStore(t)
	now := time.Now().UTC()
	pending, err := db.SaveAIFARDeployment(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 2,
		CurrentRevision: "rev-2", SpecJSON: `{"service":"permission","revision":"rev-2"}`,
		Generation: 2, Status: "pending_acceptance",
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerA := testAIFAROrchestrationLock("instance-1", "permission", "scale")
	ownerA.ID = "accept-owner-a"
	ownerA.StartedAt = now.Add(-2 * time.Hour)
	ownerA.ExpiresAt = now.Add(-time.Hour)
	if _, err := db.AcquireAIFAROrchestrationLock(ownerA); err != nil {
		t.Fatal(err)
	}
	ownerB := testAIFAROrchestrationLock("instance-1", "permission", "scale")
	ownerB.ID = "accept-owner-b"
	if _, err := db.AcquireAIFAROrchestrationLock(ownerB); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcceptAIFARDeploymentWithLock(ownerA.ID, pending, "Accepted", `[{"reason":"A"}]`, now); !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("expired predecessor acceptance error=%v", err)
	}
	accepted, err := db.AcceptAIFARDeploymentWithLock(ownerB.ID, pending, "Accepted", `[{"reason":"B"}]`, now)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "Accepted" || accepted.Generation != pending.Generation || accepted.CurrentRevision != pending.CurrentRevision || accepted.SpecJSON != pending.SpecJSON {
		t.Fatalf("active exact service owner did not accept exact desired proof: %+v", accepted)
	}
}

func TestSaveAIFARAcceptedProjectionWithLockAtomicallyRequiresOwnerProofAndInstanceCAS(t *testing.T) {
	db := openTestStore(t)
	instance, err := db.SaveAppInstance(AppInstance{
		App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "install_failed", Metadata: `{"desiredReplicas":{"permission":1}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := db.SaveAIFARDeployment(AIFARDeployment{
		InstanceID: instance.ID, ServiceName: "permission", DesiredReplicas: 2,
		CurrentRevision: "rev-2", SpecJSON: `{"service":"permission","revision":"rev-2","replicas":2}`,
		Generation: 2, Status: "Accepted",
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock(instance.ID, "permission", "scale-service"))
	if err != nil {
		t.Fatal(err)
	}
	next := instance
	next.Metadata = `{"desiredReplicas":{"permission":2}}`
	saved, err := db.SaveAIFARAcceptedProjectionWithLock(owner.ID, accepted, next, instance.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "install_failed" || saved.Metadata != next.Metadata || !saved.UpdatedAt.After(instance.UpdatedAt) {
		t.Fatalf("saved projection=%+v", saved)
	}

	staleNext := saved
	staleNext.Metadata = `{"desiredReplicas":{"permission":3}}`
	wrongProof := accepted
	wrongProof.Generation++
	if _, err := db.SaveAIFARAcceptedProjectionWithLock(owner.ID, wrongProof, staleNext, saved.UpdatedAt); !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("wrong canonical proof error=%v, want generation conflict", err)
	}
	if _, err := db.SaveAIFARAcceptedProjectionWithLock(owner.ID, accepted, staleNext, instance.UpdatedAt); !errors.Is(err, ErrAppInstanceConflict) {
		t.Fatalf("stale app-instance token error=%v, want CAS conflict", err)
	}
	if released, err := db.ReleaseAIFAROrchestrationLockByID(owner.ID); err != nil || !released {
		t.Fatalf("release owner: released=%v err=%v", released, err)
	}
	globalInstall, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock(instance.ID, "", "install"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARAcceptedProjectionWithLock(globalInstall.ID, accepted, staleNext, saved.UpdatedAt); !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("global install projection error=%v, want exact service ownership conflict", err)
	}
	if released, err := db.ReleaseAIFAROrchestrationLockByID(globalInstall.ID); err != nil || !released {
		t.Fatalf("release global install lock: released=%v err=%v", released, err)
	}
	if _, err := db.SaveAIFARAcceptedProjectionWithLock(owner.ID, accepted, staleNext, saved.UpdatedAt); !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("released owner error=%v, want ownership conflict", err)
	}
	current, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Metadata != next.Metadata {
		t.Fatalf("rejected projection changed metadata: %s", current.Metadata)
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

func TestCommitAIFARRuntimeMigrationRejectsSuccessorGenerationWithoutOverwrite(t *testing.T) {
	db := openTestStore(t)
	instance, err := db.SaveAppInstance(AppInstance{ID: "instance-1", App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "installed", Metadata: `{"orchestrationModel":"agent-runtime-v2","peer":"keep"}`})
	if err != nil {
		t.Fatal(err)
	}
	base, err := db.SaveAIFARDeployment(AIFARDeployment{InstanceID: instance.ID, ServiceName: "permission", DesiredReplicas: 1, CurrentRevision: "rev-1", Generation: 1, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock(instance.ID, "", "migrate-runtime-model"))
	if err != nil {
		t.Fatal(err)
	}
	successor := base
	successor.Generation = 2
	successor.DesiredReplicas = 2
	successor.SpecJSON = `{"generation":2}`
	successor.Status = "pending_acceptance"
	if _, err := db.SaveAIFARDeployment(successor); err != nil {
		t.Fatal(err)
	}
	next := base
	next.SpecJSON = `{"generation":1}`
	next.Status = "Accepted"
	_, err = db.CommitAIFARRuntimeMigrationWithLock(AIFARRuntimeMigrationCommit{
		LockID: owner.ID, InstanceID: instance.ID, ExpectedInstanceUpdatedAt: instance.UpdatedAt,
		NextMetadata: `{"orchestrationModel":"agent-service-controller-v1"}`,
		Deployments:  []AIFARRuntimeMigrationDeploymentCommit{{Expected: base, Next: next}},
	})
	if !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("successor generation error=%v, want generation conflict", err)
	}
	current := deploymentByName(t, db, instance.ID, "permission")
	if current.Generation != 2 || current.DesiredReplicas != 2 || current.SpecJSON != successor.SpecJSON || current.Status != successor.Status {
		t.Fatalf("migration overwrote successor desired state: %+v", current)
	}
	fresh, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Metadata != instance.Metadata || fresh.Status != "installed" {
		t.Fatalf("rejected migration changed app instance: %+v", fresh)
	}
}

func TestCommitAIFARRuntimeMigrationRejectsReplacedGlobalOwner(t *testing.T) {
	db := openTestStore(t)
	instance, err := db.SaveAppInstance(AppInstance{ID: "instance-1", App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "install_failed", Metadata: `{"orchestrationModel":"agent-runtime-v2"}`})
	if err != nil {
		t.Fatal(err)
	}
	base, err := db.SaveAIFARDeployment(AIFARDeployment{InstanceID: instance.ID, ServiceName: "permission", DesiredReplicas: 1, CurrentRevision: "rev-1", Generation: 1, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ownerA := testAIFAROrchestrationLock(instance.ID, "", "migrate-runtime-model")
	ownerA.ID = "migration-owner-a"
	ownerA.StartedAt = now.Add(-2 * time.Hour)
	ownerA.ExpiresAt = now.Add(-time.Hour)
	if _, err := db.AcquireAIFAROrchestrationLock(ownerA); err != nil {
		t.Fatal(err)
	}
	ownerB := testAIFAROrchestrationLock(instance.ID, "", "migrate-runtime-model")
	ownerB.ID = "migration-owner-b"
	if _, err := db.AcquireAIFAROrchestrationLock(ownerB); err != nil {
		t.Fatal(err)
	}
	next := base
	next.SpecJSON = `{"generation":1}`
	next.Status = "Accepted"
	_, err = db.CommitAIFARRuntimeMigrationWithLock(AIFARRuntimeMigrationCommit{
		LockID: ownerA.ID, InstanceID: instance.ID, ExpectedInstanceUpdatedAt: instance.UpdatedAt,
		NextMetadata: `{"orchestrationModel":"agent-service-controller-v1"}`,
		Deployments:  []AIFARRuntimeMigrationDeploymentCommit{{Expected: base, Next: next}},
	})
	if !errors.Is(err, ErrAIFAROrchestrationLockOwnership) {
		t.Fatalf("replaced migration owner error=%v, want ownership conflict", err)
	}
	current := deploymentByName(t, db, instance.ID, "permission")
	if current.SpecJSON != "" || current.Status != "active" {
		t.Fatalf("replaced owner changed deployment: %+v", current)
	}
	fresh, _ := db.GetAppInstance(instance.ID)
	if fresh.Metadata != instance.Metadata || fresh.Status != "install_failed" {
		t.Fatalf("replaced owner changed app lifecycle or metadata: %+v", fresh)
	}
}

func TestCommitAIFARRuntimeMigrationRollsBackCompleteSetOnOneServiceConflict(t *testing.T) {
	db := openTestStore(t)
	instance, err := db.SaveAppInstance(AppInstance{ID: "instance-1", App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "installed", Metadata: `{"orchestrationModel":"agent-runtime-v2","peer":{"keep":true}}`})
	if err != nil {
		t.Fatal(err)
	}
	permission, err := db.SaveAIFARDeployment(AIFARDeployment{InstanceID: instance.ID, ServiceName: "permission", DesiredReplicas: 1, CurrentRevision: "rev-1", Generation: 1, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := db.SaveAIFARDeployment(AIFARDeployment{InstanceID: instance.ID, ServiceName: "file", DesiredReplicas: 0, CurrentRevision: "rev-1", Generation: 1, Status: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock(instance.ID, "", "migrate-runtime-model"))
	if err != nil {
		t.Fatal(err)
	}
	permissionNext := permission
	permissionNext.SpecJSON = `{"service":"permission"}`
	permissionNext.Status = "Accepted"
	fileExpected := file
	fileExpected.DesiredReplicas = 1 // stale/wrong proof forces the second item to fail.
	fileNext := file
	fileNext.SpecJSON = `{"service":"file"}`
	fileNext.Status = "Accepted"
	_, err = db.CommitAIFARRuntimeMigrationWithLock(AIFARRuntimeMigrationCommit{
		LockID: owner.ID, InstanceID: instance.ID, ExpectedInstanceUpdatedAt: instance.UpdatedAt,
		NextMetadata: `{"orchestrationModel":"agent-service-controller-v1"}`,
		Deployments: []AIFARRuntimeMigrationDeploymentCommit{
			{Expected: permission, Next: permissionNext},
			{Expected: fileExpected, Next: fileNext},
		},
	})
	if !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("partial conflict error=%v, want generation conflict", err)
	}
	permissionAfter := deploymentByName(t, db, instance.ID, "permission")
	fileAfter := deploymentByName(t, db, instance.ID, "file")
	if permissionAfter.SpecJSON != "" || permissionAfter.Status != "active" || fileAfter.SpecJSON != "" || fileAfter.Status != "offline" || fileAfter.DesiredReplicas != 0 {
		t.Fatalf("partial conflict did not roll back complete set: permission=%+v file=%+v", permissionAfter, fileAfter)
	}
	fresh, _ := db.GetAppInstance(instance.ID)
	if fresh.Metadata != instance.Metadata || fresh.Status != "installed" {
		t.Fatalf("partial rollback changed instance: %+v", fresh)
	}
}

func TestCommitAIFARRuntimeMigrationAtomicallyPreservesLifecycleAndPeerMetadata(t *testing.T) {
	db := openTestStore(t)
	instance, err := db.SaveAppInstance(AppInstance{ID: "instance-1", App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "install_failed", Topology: "standalone", Metadata: `{"orchestrationModel":"agent-runtime-v2","peer":{"keep":true}}`})
	if err != nil {
		t.Fatal(err)
	}
	base, err := db.SaveAIFARDeployment(AIFARDeployment{InstanceID: instance.ID, ServiceName: "file", DesiredReplicas: 0, CurrentRevision: "rev-1", Generation: 1, Status: "offline", MetadataJSON: `{"peer":"keep"}`})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock(instance.ID, "", "migrate-runtime-model"))
	if err != nil {
		t.Fatal(err)
	}
	next := base
	next.SpecJSON = `{"service":"file","replicas":0}`
	next.Status = "Accepted"
	next.MetadataJSON = `{"model":"agent-service-controller-v1","specHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	next.ConditionsJSON = `[{"type":"Accepted","status":true,"generation":1}]`
	next.LastTransitionAt = time.Now().UTC()
	saved, err := db.CommitAIFARRuntimeMigrationWithLock(AIFARRuntimeMigrationCommit{
		LockID: owner.ID, InstanceID: instance.ID, ExpectedInstanceUpdatedAt: instance.UpdatedAt,
		NextMetadata: `{"orchestrationModel":"agent-service-controller-v1","peer":{"keep":true}}`,
		Deployments:  []AIFARRuntimeMigrationDeploymentCommit{{Expected: base, Next: next}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "install_failed" || saved.Version != instance.Version || saved.ServerID != instance.ServerID || saved.Topology != instance.Topology || saved.Metadata != `{"orchestrationModel":"agent-service-controller-v1","peer":{"keep":true}}` {
		t.Fatalf("migration commit regressed lifecycle or peer metadata: %+v", saved)
	}
	committed := deploymentByName(t, db, instance.ID, "file")
	if committed.Generation != 1 || committed.DesiredReplicas != 0 || committed.CurrentRevision != "rev-1" || committed.SpecJSON != next.SpecJSON || committed.Status != "Accepted" {
		t.Fatalf("migration commit changed desired identity or lost offline=0: %+v", committed)
	}
}

func TestCommitAIFARRuntimeMigrationDoesNotRegressObservedGenerationOneProjection(t *testing.T) {
	db := openTestStore(t)
	instance, err := db.SaveAppInstance(AppInstance{ID: "instance-1", App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "installed", Metadata: `{"orchestrationModel":"agent-runtime-v2"}`})
	if err != nil {
		t.Fatal(err)
	}
	transition := time.Date(2026, 7, 1, 2, 3, 4, 0, time.UTC)
	base, err := db.SaveAIFARDeployment(AIFARDeployment{
		InstanceID: instance.ID, ServiceName: "permission", DesiredReplicas: 1, CurrentRevision: "rev-1",
		Generation: 1, ObservedGeneration: 1, Status: "Available", MetadataJSON: `{"runtime":"peer"}`,
		ConditionsJSON: `[{"type":"Available","status":true,"generation":1}]`, LastTransitionAt: transition,
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock(instance.ID, "", "migrate-runtime-model"))
	if err != nil {
		t.Fatal(err)
	}
	next := base
	next.SpecJSON = `{"service":"permission"}`
	next.Status = "Accepted"
	next.MetadataJSON = `{"model":"agent-service-controller-v1"}`
	next.ConditionsJSON = `[{"type":"Accepted","status":true,"generation":1}]`
	next.LastTransitionAt = transition.Add(time.Hour)
	if _, err := db.CommitAIFARRuntimeMigrationWithLock(AIFARRuntimeMigrationCommit{
		LockID: owner.ID, InstanceID: instance.ID, ExpectedInstanceUpdatedAt: instance.UpdatedAt,
		NextMetadata: `{"orchestrationModel":"agent-service-controller-v1"}`,
		Deployments:  []AIFARRuntimeMigrationDeploymentCommit{{Expected: base, Next: next}},
	}); err != nil {
		t.Fatal(err)
	}
	committed := deploymentByName(t, db, instance.ID, "permission")
	if committed.SpecJSON != next.SpecJSON || committed.Status != base.Status || committed.MetadataJSON != base.MetadataJSON || committed.ConditionsJSON != base.ConditionsJSON || !committed.LastTransitionAt.Equal(transition) {
		t.Fatalf("observed runtime projection regressed: %+v", committed)
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
