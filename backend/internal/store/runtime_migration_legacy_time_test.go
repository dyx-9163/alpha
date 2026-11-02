package store

import (
	"testing"
	"time"
)

func TestRuntimeMigrationAcceptsLegacyMonotonicTimestamp(t *testing.T) {
	db := openTestStore(t)
	instance, err := db.SaveAppInstance(AppInstance{ID: "legacy-time", App: "aifar", ServerID: "srv", Metadata: `{"orchestrationModel":"agent-runtime-v2"}`})
	if err != nil {
		t.Fatal(err)
	}
	legacy := time.Now().String()
	if _, err = db.db.Exec(`update app_instances set updated_at=? where id=?`, legacy, instance.ID); err != nil {
		t.Fatal(err)
	}
	instance, err = db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := db.SaveAIFARDeployment(AIFARDeployment{InstanceID: instance.ID, ServiceName: "web-vue3", Generation: 1, DesiredReplicas: 1, CurrentRevision: "rev"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.AcquireAIFAROrchestrationLock(testAIFAROrchestrationLock(instance.ID, "", "migrate-runtime-model"))
	if err != nil {
		t.Fatal(err)
	}
	next := deployment
	next.SpecJSON = `{"generation":1}`
	_, err = db.CommitAIFARRuntimeMigrationWithLock(AIFARRuntimeMigrationCommit{LockID: owner.ID, InstanceID: instance.ID, ExpectedInstanceUpdatedAt: instance.UpdatedAt, NextMetadata: `{"orchestrationModel":"agent-service-controller-v1"}`, Deployments: []AIFARRuntimeMigrationDeploymentCommit{{Expected: deployment, Next: next}}})
	if err != nil {
		t.Fatalf("legacy timestamp prevents commit: %v", err)
	}
}
