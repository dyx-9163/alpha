package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

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
