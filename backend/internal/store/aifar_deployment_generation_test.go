package store

import (
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAIFARRuntimeObservationEpochMigrationUpgradesExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aifar.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "rev-1", Generation: 4, ObservedGeneration: 3, Status: "Progressing",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`delete from schema_migrations where version=2026081201`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`drop table aifar_runtime_observation_sequence`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`alter table aifar_deployments drop column observation_epoch`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	rows, err := upgraded.ListAIFARDeployments("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Generation != 4 || rows[0].ObservedGeneration != 3 || rows[0].ObservationEpoch != 0 {
		t.Fatalf("existing deployment was not preserved with epoch zero: %+v", rows)
	}
	epoch, err := upgraded.AllocateAIFARRuntimeObservationEpoch()
	if err != nil {
		t.Fatal(err)
	}
	if epoch != 1 {
		t.Fatalf("first upgraded epoch=%d, want 1", epoch)
	}
	var migrations int
	if err := upgraded.db.QueryRow(`select count(*) from schema_migrations where version=2026081201`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != 1 {
		t.Fatalf("observation epoch migration records=%d, want 1", migrations)
	}
}

func TestAllocateAIFARRuntimeObservationEpochRejectsOverflowWithoutChangingSequence(t *testing.T) {
	db := openTestStore(t)
	if _, err := db.db.Exec(`update aifar_runtime_observation_sequence set next_epoch=? where singleton=1`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AllocateAIFARRuntimeObservationEpoch(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "exhausted") {
		t.Fatalf("expected explicit exhausted error, got %v", err)
	}
	var persisted int64
	if err := db.db.QueryRow(`select next_epoch from aifar_runtime_observation_sequence where singleton=1`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != math.MaxInt64 {
		t.Fatalf("overflow changed sequence to %d, want %d", persisted, int64(math.MaxInt64))
	}
}

func TestAllocateAIFARRuntimeObservationEpochIsPersistedAndStrictlyMonotonic(t *testing.T) {
	db := openTestStore(t)
	first, err := db.AllocateAIFARRuntimeObservationEpoch()
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.AllocateAIFARRuntimeObservationEpoch()
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("epochs=(%d,%d), want (1,2)", first, second)
	}
	var persisted int64
	if err := db.db.QueryRow(`select next_epoch from aifar_runtime_observation_sequence where singleton=1`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 2 {
		t.Fatalf("persisted epoch=%d, want 2", persisted)
	}
	var defaultEpoch int64
	if _, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "rev-1", Status: "Accepted",
	}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`select observation_epoch from aifar_deployments where instance_id=? and service_name=?`, "instance-1", "permission").Scan(&defaultEpoch); err != nil {
		t.Fatal(err)
	}
	if defaultEpoch != 0 {
		t.Fatalf("default observation epoch=%d, want 0", defaultEpoch)
	}
}

func TestObserveAIFARRuntimeServiceIgnoresOlderEpochWithoutReplacingProjection(t *testing.T) {
	db := openTestStore(t)
	deployment, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "rev-fresh", Status: "Accepted",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldEpoch, err := db.AllocateAIFARRuntimeObservationEpoch()
	if err != nil {
		t.Fatal(err)
	}
	freshEpoch, err := db.AllocateAIFARRuntimeObservationEpoch()
	if err != nil {
		t.Fatal(err)
	}
	freshAt := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	fresh, applied, err := db.ObserveAIFARRuntimeService(AIFARRuntimeServiceObservation{
		InstanceID: "instance-1", ServiceName: "permission", Generation: deployment.Generation,
		ObservationEpoch: freshEpoch, Status: "Available",
		ConditionsJSON: `[{"type":"Available","status":true,"reason":"Ready","generation":1}]`, ObservedAt: freshAt,
		ReplicaSet: &AIFARReplicaSet{InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-fresh", Image: "fresh:image", DesiredPods: 1, ReadyPods: 1, Status: "ready"},
		Pods:       []AIFARPod{{InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-fresh", PodID: "fresh-1", ContainerName: "fresh-container", Status: "running", Ready: true}},
		Endpoints:  []AIFARServiceEndpoint{{InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-fresh", PodID: "fresh-1", ContainerName: "fresh-container", State: "active", Ready: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !applied || fresh.ObservationEpoch != freshEpoch {
		t.Fatalf("fresh observation applied=%v row=%+v", applied, fresh)
	}
	stale, applied, err := db.ObserveAIFARRuntimeService(AIFARRuntimeServiceObservation{
		InstanceID: "instance-1", ServiceName: "permission", Generation: deployment.Generation,
		ObservationEpoch: oldEpoch, Status: "Degraded",
		ConditionsJSON: `[{"type":"Degraded","status":true,"reason":"OldSnapshot","generation":1}]`, ObservedAt: freshAt.Add(time.Minute),
		ReplicaSet: &AIFARReplicaSet{InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-old", Image: "old:image", DesiredPods: 1, ReadyPods: 0, Status: "degraded"},
		Pods:       []AIFARPod{{InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-old", PodID: "old-1", ContainerName: "old-container", Status: "exited"}},
		Endpoints:  []AIFARServiceEndpoint{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatalf("older observation unexpectedly applied: %+v", stale)
	}
	if stale.Status != "Available" || stale.ObservationEpoch != freshEpoch || stale.ConditionsJSON != fresh.ConditionsJSON || !stale.LastTransitionAt.Equal(freshAt) {
		t.Fatalf("older observation changed deployment: fresh=%+v stale=%+v", fresh, stale)
	}
	replicaSets, err := db.ListAIFARReplicaSets("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(replicaSets) != 1 || replicaSets[0].Revision != "rev-fresh" || replicaSets[0].Image != "fresh:image" || replicaSets[0].ReadyPods != 1 {
		t.Fatalf("older observation changed replica sets: %+v", replicaSets)
	}
	pods, err := db.ListAIFARPods("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].ContainerName != "fresh-container" || !pods[0].Ready {
		t.Fatalf("older observation changed pods: %+v", pods)
	}
	endpoints, err := db.ListAIFARServiceEndpoints("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].ContainerName != "fresh-container" || !endpoints[0].Ready {
		t.Fatalf("older observation changed endpoints: %+v", endpoints)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return db
}

func TestAcceptAIFARDeploymentKeepsObservedGenerationZero(t *testing.T) {
	db := openTestStore(t)
	desired, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "r1", SpecJSON: `{"metadata":{"generation":1}}`, Status: "pending_acceptance",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.August, 9, 10, 0, 0, 0, time.UTC)
	accepted, err := db.AcceptAIFARDeployment("instance-1", "permission", desired.Generation, "Accepted", `[{"type":"Accepted","status":true,"generation":1}]`, at)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Generation != 1 || accepted.ObservedGeneration != 0 || accepted.Status != "Accepted" || !accepted.LastTransitionAt.Equal(at) {
		t.Fatalf("accepted=%+v", accepted)
	}
}

func TestSaveAIFARDeploymentGenerationResetsObservedGenerationForNewDesiredState(t *testing.T) {
	db := openTestStore(t)
	gen1, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ObserveAIFARDeployment("instance-1", "permission", gen1.Generation, "Available", `[{"type":"Available","generation":1}]`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	gen2, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance", ConditionsJSON: `[{"generation":2}]`}, gen1.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if gen2.Generation != 2 || gen2.ObservedGeneration != 0 || gen2.Status != "pending_acceptance" {
		t.Fatalf("new desired state retained old observation: %+v", gen2)
	}
}

func TestObserveAIFARRuntimeServiceRejectsStaleGenerationWithoutChangingProjection(t *testing.T) {
	db := openTestStore(t)
	gen1, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "rev-old", Status: "pending_acceptance",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	gen2, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "rev-new", Status: "pending_acceptance",
	}, gen1.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARReplicaSet(AIFARReplicaSet{
		InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-new",
		Image: "aifar-permission:rev-new", DesiredPods: 1, ReadyPods: 1, Status: "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARPod(AIFARPod{
		InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-new", PodID: "permission-0",
		ContainerName: "aifar-permission-new", Port: 18080, Status: "running", Ready: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceAIFARServiceEndpoints("instance-1", "permission", []AIFARServiceEndpoint{{
		InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-new", PodID: "permission-0",
		ContainerName: "aifar-permission-new", Port: 18080, State: "ready", Ready: true,
	}}); err != nil {
		t.Fatal(err)
	}

	_, _, err = db.ObserveAIFARRuntimeService(AIFARRuntimeServiceObservation{
		InstanceID: "instance-1", ServiceName: "permission", Generation: gen1.Generation,
		ObservationEpoch: 1,
		Status:           "ready", ConditionsJSON: `[{"type":"Available","generation":1}]`, ObservedAt: time.Now().UTC(),
		ReplicaSet: &AIFARReplicaSet{
			InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-old",
			Image: "aifar-permission:rev-old", DesiredPods: 1, ReadyPods: 1, Status: "ready",
		},
		Pods: []AIFARPod{{
			InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-old", PodID: "permission-0",
			ContainerName: "aifar-permission-old", Port: 8080, Status: "running", Ready: true,
		}},
		Endpoints: []AIFARServiceEndpoint{{
			InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-old", PodID: "permission-0",
			ContainerName: "aifar-permission-old", Port: 8080, State: "ready", Ready: true,
		}},
	})
	if !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("stale observation err=%v, want generation conflict", err)
	}
	deployment := deploymentByName(t, db, "instance-1", "permission")
	if deployment.Generation != gen2.Generation || deployment.CurrentRevision != "rev-new" || deployment.ObservedGeneration != 0 {
		t.Fatalf("stale observation changed successor deployment: %+v", deployment)
	}
	replicaSets, err := db.ListAIFARReplicaSets("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(replicaSets) != 1 || replicaSets[0].Revision != "rev-new" || replicaSets[0].Image != "aifar-permission:rev-new" {
		t.Fatalf("stale observation changed successor replica set: %+v", replicaSets)
	}
	pods, err := db.ListAIFARPods("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].Revision != "rev-new" || pods[0].ContainerName != "aifar-permission-new" {
		t.Fatalf("stale observation changed successor pod: %+v", pods)
	}
	endpoints, err := db.ListAIFARServiceEndpoints("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].Revision != "rev-new" || endpoints[0].ContainerName != "aifar-permission-new" {
		t.Fatalf("stale observation changed successor endpoints: %+v", endpoints)
	}
}

func TestObserveAIFARRuntimeServiceAtomicallyReplacesOnlyTargetProjection(t *testing.T) {
	db := openTestStore(t)
	permission, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "rev-new", Status: "Accepted",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "file", DesiredReplicas: 1,
		CurrentRevision: "file-rev", Status: "Available",
	}, 0); err != nil {
		t.Fatal(err)
	}
	for _, pod := range []AIFARPod{
		{InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-old", PodID: "permission-old", ContainerName: "permission-old", Port: 8080},
		{InstanceID: "instance-1", ServiceName: "file", Revision: "file-rev", PodID: "file-0", ContainerName: "file-0", Port: 8081, Ready: true},
	} {
		if _, err := db.SaveAIFARPod(pod); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ReplaceAIFARServiceEndpoints("instance-1", "file", []AIFARServiceEndpoint{{
		InstanceID: "instance-1", ServiceName: "file", Revision: "file-rev", PodID: "file-0", ContainerName: "file-0", Port: 8081, Ready: true,
	}}); err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	observed, applied, err := db.ObserveAIFARRuntimeService(AIFARRuntimeServiceObservation{
		InstanceID: "instance-1", ServiceName: "permission", Generation: permission.Generation,
		ObservationEpoch: 1,
		Status:           "Available", ConditionsJSON: `[{"type":"Available","generation":1}]`, ObservedAt: observedAt,
		ReplicaSet: &AIFARReplicaSet{
			InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-new",
			Image: "aifar-permission:rev-new", DesiredPods: 1, ReadyPods: 1, Status: "ready",
		},
		Pods: []AIFARPod{{
			InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-new", PodID: "permission-0",
			ContainerName: "permission-new", Port: 18080, Status: "running", Ready: true,
		}},
		Endpoints: []AIFARServiceEndpoint{{
			InstanceID: "instance-1", ServiceName: "permission", Revision: "rev-new", PodID: "permission-0",
			ContainerName: "permission-new", Port: 18080, State: "ready", Ready: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("expected service observation to apply")
	}
	if observed.ObservedGeneration != permission.Generation || observed.Status != "Available" || !observed.LastTransitionAt.Equal(observedAt) {
		t.Fatalf("unexpected deployment observation: %+v", observed)
	}
	replicaSets, err := db.ListAIFARReplicaSets("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(replicaSets) != 1 || replicaSets[0].Revision != "rev-new" || replicaSets[0].ReadyPods != 1 {
		t.Fatalf("target replica set was not projected: %+v", replicaSets)
	}
	pods, err := db.ListAIFARPods("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 2 {
		t.Fatalf("expected target replacement plus peer preservation, got %+v", pods)
	}
	byService := map[string]AIFARPod{}
	for _, pod := range pods {
		byService[pod.ServiceName] = pod
	}
	if byService["permission"].ContainerName != "permission-new" || byService["file"].ContainerName != "file-0" {
		t.Fatalf("unexpected service-local pod projection: %+v", pods)
	}
	endpoints, err := db.ListAIFARServiceEndpoints("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected target and peer endpoints, got %+v", endpoints)
	}
	endpointByService := map[string]AIFARServiceEndpoint{}
	for _, endpoint := range endpoints {
		endpointByService[endpoint.ServiceName] = endpoint
	}
	if endpointByService["permission"].ContainerName != "permission-new" || endpointByService["file"].ContainerName != "file-0" {
		t.Fatalf("unexpected service-local endpoint projection: %+v", endpoints)
	}
}

func TestAcceptAIFARDeploymentPreservesAlreadyObservedRuntimeState(t *testing.T) {
	for _, status := range []string{"Available", "Degraded", "Offline", "Progressing"} {
		t.Run(status, func(t *testing.T) {
			db := openTestStore(t)
			desired, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance", ConditionsJSON: `[{"reason":"PendingAgentAcceptance"}]`}, 0)
			if err != nil {
				t.Fatal(err)
			}
			conditions := `[{"type":"` + status + `","reason":"RuntimeObserved","generation":1}]`
			observed, err := db.ObserveAIFARDeployment("instance-1", "permission", desired.Generation, status, conditions, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := db.AcceptAIFARDeployment("instance-1", "permission", desired.Generation, "Accepted", `[{"type":"Accepted","generation":1}]`, time.Now().UTC().Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if accepted.Status != observed.Status || accepted.ConditionsJSON != observed.ConditionsJSON || accepted.ObservedGeneration != observed.ObservedGeneration || !accepted.LastTransitionAt.Equal(observed.LastTransitionAt) {
				t.Fatalf("late acceptance regressed runtime state: before=%+v after=%+v", observed, accepted)
			}
		})
	}
}

func TestAcceptAIFARDeploymentIsIdempotentAfterAccepted(t *testing.T) {
	db := openTestStore(t)
	desired, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Now().UTC()
	first, err := db.AcceptAIFARDeployment("instance-1", "permission", desired.Generation, "Accepted", `[{"reason":"ManifestAccepted"}]`, firstAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.AcceptAIFARDeployment("instance-1", "permission", desired.Generation, "Accepted", `[{"reason":"different-retry"}]`, firstAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != first.Status || second.ConditionsJSON != first.ConditionsJSON || !second.LastTransitionAt.Equal(first.LastTransitionAt) {
		t.Fatalf("idempotent acceptance changed row: first=%+v second=%+v", first, second)
	}
}

func TestAcceptAIFARDeploymentRejectsUnexpectedUnobservedState(t *testing.T) {
	db := openTestStore(t)
	desired, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "repair_required", ConditionsJSON: `[{"reason":"ManualRepair"}]`}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.AcceptAIFARDeployment("instance-1", "permission", desired.Generation, "Accepted", `[{"reason":"ManifestAccepted"}]`, time.Now().UTC())
	if !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("err=%v, want generation conflict", err)
	}
	got := deploymentByName(t, db, "instance-1", "permission")
	if got.Status != "repair_required" || got.ConditionsJSON != `[{"reason":"ManualRepair"}]` {
		t.Fatalf("unexpected state was overwritten: %+v", got)
	}
}

func TestAcceptAndObserveAIFARDeploymentNeverRegressesRuntimeState(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		db := openTestStore(t)
		desired, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		done := make(chan error, 2)
		go func() {
			<-start
			_, acceptErr := db.AcceptAIFARDeployment("instance-1", "permission", desired.Generation, "Accepted", `[{"type":"Accepted","generation":1}]`, time.Now().UTC())
			done <- acceptErr
		}()
		go func() {
			<-start
			_, observeErr := db.ObserveAIFARDeployment("instance-1", "permission", desired.Generation, "Available", `[{"type":"Available","generation":1}]`, time.Now().UTC())
			done <- observeErr
		}()
		close(start)
		for call := 0; call < 2; call++ {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		got := deploymentByName(t, db, "instance-1", "permission")
		if got.Status != "Available" || got.ObservedGeneration != 1 || !strings.Contains(got.ConditionsJSON, `"Available"`) {
			t.Fatalf("accept/observe race regressed runtime state: %+v", got)
		}
	}
}

func TestStaleObservationBeforeAcceptanceCannotEraseNewDesiredAcceptance(t *testing.T) {
	db := openTestStore(t)
	gen1, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	gen2, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance", ConditionsJSON: `[{"type":"Accepted","status":false,"generation":2}]`}, gen1.Generation)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := db.ObserveAIFARDeployment("instance-1", "permission", gen1.Generation, "Available", `[{"type":"Available","generation":1}]`, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if stale.Generation != 2 || stale.ObservedGeneration != 0 || stale.Status != "pending_acceptance" || !strings.Contains(stale.ConditionsJSON, `"generation":2`) {
		t.Fatalf("stale observation changed generation 2 desired state: %+v", stale)
	}
	acceptedConditions := `[{"type":"Accepted","status":true,"generation":2}]`
	accepted, err := db.AcceptAIFARDeployment("instance-1", "permission", gen2.Generation, "Accepted", acceptedConditions, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	assertGenerationTwoAccepted(t, accepted, acceptedConditions)
	assertGenerationTwoCanBeObserved(t, db)
}

func TestStaleObservationAfterAcceptanceCannotEraseNewDesiredAcceptance(t *testing.T) {
	db := openTestStore(t)
	gen1, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	gen2, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance"}, gen1.Generation)
	if err != nil {
		t.Fatal(err)
	}
	acceptedConditions := `[{"type":"Accepted","status":true,"generation":2}]`
	accepted, err := db.AcceptAIFARDeployment("instance-1", "permission", gen2.Generation, "Accepted", acceptedConditions, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	assertGenerationTwoAccepted(t, accepted, acceptedConditions)
	stale, err := db.ObserveAIFARDeployment("instance-1", "permission", gen1.Generation, "Available", `[{"type":"Available","generation":1}]`, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	assertGenerationTwoAccepted(t, stale, acceptedConditions)
	assertGenerationTwoCanBeObserved(t, db)
}

func assertGenerationTwoAccepted(t *testing.T, deployment AIFARDeployment, conditions string) {
	t.Helper()
	if deployment.Generation != 2 || deployment.ObservedGeneration != 0 || deployment.Status != "Accepted" || deployment.ConditionsJSON != conditions {
		t.Fatalf("generation 2 acceptance was not preserved: %+v", deployment)
	}
}

func assertGenerationTwoCanBeObserved(t *testing.T, db *Store) {
	t.Helper()
	conditions := `[{"type":"Available","generation":2}]`
	observed, err := db.ObserveAIFARDeployment("instance-1", "permission", 2, "Available", conditions, time.Now().UTC().Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if observed.Generation != 2 || observed.ObservedGeneration != 2 || observed.Status != "Available" || observed.ConditionsJSON != conditions {
		t.Fatalf("generation 2 observation did not advance runtime state: %+v", observed)
	}
}

func TestAcceptAIFARDeploymentRejectsLateGenerationWithoutChangingNewerState(t *testing.T) {
	db := openTestStore(t)
	gen1, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	gen2, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance", ConditionsJSON: `[{"generation":2}]`}, gen1.Generation)
	if err != nil {
		t.Fatal(err)
	}
	gen3, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance", ConditionsJSON: `[{"generation":3}]`}, gen2.Generation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.AcceptAIFARDeployment("instance-1", "permission", gen2.Generation, "Accepted", `[{"generation":2}]`, time.Now().UTC())
	if !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("err=%v", err)
	}
	got := deploymentByName(t, db, "instance-1", "permission")
	if got.Generation != gen3.Generation || got.Status != "pending_acceptance" || got.ConditionsJSON != `[{"generation":3}]` || got.ObservedGeneration != 0 {
		t.Fatalf("late acceptance changed newer desired state: %+v", got)
	}
}

func TestAcceptAIFARDeploymentMissingRow(t *testing.T) {
	db := openTestStore(t)
	_, err := db.AcceptAIFARDeployment("missing", "permission", 1, "Accepted", `[]`, time.Now().UTC())
	if !errors.Is(err, ErrAIFARDeploymentNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestAcceptAIFARDeploymentConcurrentWithNextGenerationDoesNotOverwriteNextGeneration(t *testing.T) {
	db := openTestStore(t)
	gen1, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	gen2, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance", ConditionsJSON: `[{"generation":2}]`}, gen1.Generation)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, acceptErr := db.AcceptAIFARDeployment("instance-1", "permission", gen2.Generation, "Accepted", `[{"generation":2,"accepted":true}]`, time.Now().UTC())
		done <- acceptErr
	}()
	go func() {
		<-start
		_, saveErr := db.SaveAIFARDeploymentGeneration(AIFARDeployment{InstanceID: "instance-1", ServiceName: "permission", Status: "pending_acceptance", ConditionsJSON: `[{"generation":3}]`}, gen2.Generation)
		done <- saveErr
	}()
	close(start)
	for i := 0; i < 2; i++ {
		err := <-done
		if err != nil && !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
			t.Fatal(err)
		}
	}
	got := deploymentByName(t, db, "instance-1", "permission")
	if got.Generation == 3 && (got.Status != "pending_acceptance" || got.ConditionsJSON != `[{"generation":3}]`) {
		t.Fatalf("acceptance overwrote concurrent next generation: %+v", got)
	}
	if got.ObservedGeneration != 0 {
		t.Fatalf("observed=%d", got.ObservedGeneration)
	}
}

func deploymentByName(t *testing.T, db *Store, instanceID, serviceName string) AIFARDeployment {
	t.Helper()
	values, err := db.ListAIFARDeployments(instanceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.ServiceName == serviceName {
			return value
		}
	}
	t.Fatalf("deployment %s not found", serviceName)
	return AIFARDeployment{}
}

func TestSaveAIFARDeploymentGenerationRejectsStaleWriter(t *testing.T) {
	db := openTestStore(t)
	first, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "r1", SpecJSON: `{"metadata":{"generation":1}}`,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 {
		t.Fatalf("generation=%d, want 1", first.Generation)
	}

	_, err = db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 0,
		CurrentRevision: "r1", SpecJSON: `{"metadata":{"generation":2}}`,
	}, 0)
	if !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("error=%v, want generation conflict", err)
	}
}

func TestSaveAIFARDeploymentGenerationDoesNotCreateWithNonZeroExpectedGeneration(t *testing.T) {
	db := openTestStore(t)
	_, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-missing", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "r1", SpecJSON: `{"metadata":{"generation":2}}`,
	}, 1)
	if !errors.Is(err, ErrAIFARDeploymentNotFound) {
		t.Fatalf("error=%v, want deployment not found", err)
	}
	deployments, err := db.ListAIFARDeployments("instance-missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 0 {
		t.Fatalf("missing deployment was created: %+v", deployments)
	}
}

func TestObserveAIFARDeploymentCannotAdvancePastDesiredGeneration(t *testing.T) {
	db := openTestStore(t)
	first, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
		CurrentRevision: "r1", SpecJSON: `{"metadata":{"generation":1}}`,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := db.SaveAIFARDeploymentGeneration(AIFARDeployment{
		InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 0,
		CurrentRevision: "r1", SpecJSON: `{"metadata":{"generation":2}}`,
	}, first.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if desired.Generation != 2 {
		t.Fatalf("generation=%d, want 2", desired.Generation)
	}

	at := time.Date(2026, time.August, 7, 9, 30, 0, 0, time.UTC)
	observed, err := db.ObserveAIFARDeployment("instance-1", "permission", 2, "ready", `[{"type":"Available","status":"True"}]`, at)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ObservedGeneration != 2 || observed.Status != "ready" || observed.ConditionsJSON != `[{"type":"Available","status":"True"}]` || !observed.LastTransitionAt.Equal(at) {
		t.Fatalf("unexpected observed deployment: %+v", observed)
	}
	older, err := db.ObserveAIFARDeployment("instance-1", "permission", 1, "reconciling", `[{"type":"Progressing","status":"True"}]`, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if older.ObservedGeneration != 2 || older.Status != "ready" || older.ConditionsJSON != `[{"type":"Available","status":"True"}]` || !older.LastTransitionAt.Equal(at) {
		t.Fatalf("older observation overwrote newer deployment state: %+v", older)
	}

	_, err = db.ObserveAIFARDeployment("instance-1", "permission", 3, "offline", `[]`, at.Add(time.Minute))
	if !errors.Is(err, ErrAIFARDeploymentGenerationConflict) {
		t.Fatalf("error=%v, want generation conflict", err)
	}
	deployments, err := db.ListAIFARDeployments("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 1 || deployments[0].ObservedGeneration != 2 {
		t.Fatalf("observed_generation changed after stale observation: %+v", deployments)
	}
}
