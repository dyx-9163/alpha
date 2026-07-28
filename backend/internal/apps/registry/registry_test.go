package registry

import (
	"context"
	"reflect"
	"testing"

	"aifar-deployment/backend/internal/store"
)

type backupLifecycleRequiredModuleFake struct{}

func (backupLifecycleRequiredModuleFake) Name() string { return "required" }

func (backupLifecycleRequiredModuleFake) Manifest(string) Manifest { return Manifest{Name: "required"} }

func (backupLifecycleRequiredModuleFake) PreflightInstall(context.Context, InstallRequest, []store.Resource) (PreflightResult, error) {
	return PreflightResult{}, nil
}

func (backupLifecycleRequiredModuleFake) PlanInstall(context.Context, InstallRequest, []store.Resource) ([]InstallStepPlan, error) {
	return nil, nil
}

func (backupLifecycleRequiredModuleFake) ValidateInstall(context.Context, InstallRequest, []store.Resource) error {
	return nil
}

func (backupLifecycleRequiredModuleFake) Install(context.Context, InstallRequest, RunContext) error {
	return nil
}

type backupLifecycleModuleFake struct {
	backupLifecycleRequiredModuleFake
	backupRequest  BackupRequest
	restoreRequest RestoreRequest
}

func (f *backupLifecycleModuleFake) PlanBackup(_ context.Context, req BackupRequest) ([]InstallStepPlan, error) {
	f.backupRequest = req
	return []InstallStepPlan{
		{Target: "server-2", Name: "prepare", Title: "Prepare", Order: 2},
		{Target: "server-1", Name: "dump", Title: "Dump", Order: 3},
	}, nil
}

func (f *backupLifecycleModuleFake) Backup(context.Context, BackupRequest, RunContext) error {
	return nil
}

func (f *backupLifecycleModuleFake) PlanRestore(_ context.Context, req RestoreRequest) ([]InstallStepPlan, error) {
	f.restoreRequest = req
	return []InstallStepPlan{{Target: "server-1", Name: "restore", Title: "Restore", Order: 7}}, nil
}

func (f *backupLifecycleModuleFake) Restore(context.Context, RestoreRequest, RunContext) error {
	return nil
}

func TestBackupAndRestoreLifecyclesRemainOptional(t *testing.T) {
	var _ Module = backupLifecycleRequiredModuleFake{}
	var _ BackupModule = (*backupLifecycleModuleFake)(nil)
	var _ RestoreModule = (*backupLifecycleModuleFake)(nil)

	required := backupLifecycleRequiredModuleFake{}
	if _, ok := any(required).(BackupModule); ok {
		t.Fatal("required Module must not be forced to implement BackupModule")
	}
	if _, ok := any(required).(RestoreModule); ok {
		t.Fatal("required Module must not be forced to implement RestoreModule")
	}
}

func TestBackupAndRestoreRequestsCloneMutableFields(t *testing.T) {
	backup := BackupRequest{
		Instances: []store.AppInstance{{ID: "instance-1"}},
		Servers:   []store.Server{{ID: "server-1"}},
		Parameters: map[string]any{
			"nested": map[string]any{"value": "original"},
			"items":  []any{"original"},
		},
	}
	restore := RestoreRequest{
		Instances: []store.AppInstance{{ID: "instance-2"}},
		Servers:   []store.Server{{ID: "server-2"}},
		Parameters: map[string]any{
			"nested": map[string]any{"value": "original"},
			"items":  []any{"original"},
		},
	}

	backupCopy := backup.Clone()
	restoreCopy := restore.Clone()
	backupCopy.Instances[0].ID = "changed"
	backupCopy.Servers[0].ID = "changed"
	backupCopy.Parameters["nested"].(map[string]any)["value"] = "changed"
	backupCopy.Parameters["items"].([]any)[0] = "changed"
	restoreCopy.Instances[0].ID = "changed"
	restoreCopy.Servers[0].ID = "changed"
	restoreCopy.Parameters["nested"].(map[string]any)["value"] = "changed"
	restoreCopy.Parameters["items"].([]any)[0] = "changed"

	if backup.Instances[0].ID != "instance-1" || backup.Servers[0].ID != "server-1" || backup.Parameters["nested"].(map[string]any)["value"] != "original" || backup.Parameters["items"].([]any)[0] != "original" {
		t.Fatalf("BackupRequest.Clone mutated source request: %#v", backup)
	}
	if restore.Instances[0].ID != "instance-2" || restore.Servers[0].ID != "server-2" || restore.Parameters["nested"].(map[string]any)["value"] != "original" || restore.Parameters["items"].([]any)[0] != "original" {
		t.Fatalf("RestoreRequest.Clone mutated source request: %#v", restore)
	}
}

func TestBackupAndRestorePlansPreserveTargetNameAndOrder(t *testing.T) {
	module := &backupLifecycleModuleFake{}
	backupRequest := BackupRequest{Instance: store.AppInstance{ID: "instance-1"}, Parameters: map[string]any{"threads": 4}}
	restoreRequest := RestoreRequest{Instance: store.AppInstance{ID: "instance-1"}, Backup: store.AppBackup{ID: "backup-1"}, Parameters: map[string]any{"threads": 4}}

	backupPlan, err := module.PlanBackup(context.Background(), backupRequest.Clone())
	if err != nil {
		t.Fatal(err)
	}
	restorePlan, err := module.PlanRestore(context.Background(), restoreRequest.Clone())
	if err != nil {
		t.Fatal(err)
	}

	wantBackupPlan := []InstallStepPlan{
		{Target: "server-2", Name: "prepare", Title: "Prepare", Order: 2},
		{Target: "server-1", Name: "dump", Title: "Dump", Order: 3},
	}
	wantRestorePlan := []InstallStepPlan{{Target: "server-1", Name: "restore", Title: "Restore", Order: 7}}
	if !reflect.DeepEqual(backupPlan, wantBackupPlan) {
		t.Fatalf("backup plan = %#v, want %#v", backupPlan, wantBackupPlan)
	}
	if !reflect.DeepEqual(restorePlan, wantRestorePlan) {
		t.Fatalf("restore plan = %#v, want %#v", restorePlan, wantRestorePlan)
	}
	if !reflect.DeepEqual(module.backupRequest, backupRequest) {
		t.Fatalf("backup request = %#v, want %#v", module.backupRequest, backupRequest)
	}
	if !reflect.DeepEqual(module.restoreRequest, restoreRequest) {
		t.Fatalf("restore request = %#v, want %#v", module.restoreRequest, restoreRequest)
	}
}
