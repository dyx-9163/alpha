package taskplan

import "testing"

type fakeStore struct {
	targets []string
	steps   []Step
}

func (f *fakeStore) UpsertTaskTarget(taskID, target, status, errText string) error {
	f.targets = append(f.targets, target)
	return nil
}

func (f *fakeStore) UpsertTaskStep(taskID, target, name, title string, order int, status, errText string) error {
	f.steps = append(f.steps, Step{Target: target, Name: name, Title: title, Order: order})
	return nil
}

func TestStorePlanStoresTargetsOnceAndSkipsUnnamedSteps(t *testing.T) {
	store := &fakeStore{}
	err := StorePlan(store, "task-1", []Step{
		{Target: "srv-1", Name: "load-server", Title: "Load server", Order: 1},
		{Target: "srv-1", Name: "install", Title: "Install", Order: 2},
		{Target: "srv-2", Name: "", Title: "Target only", Order: 0},
		{Target: "", Name: "control", Title: "Control", Order: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.targets) != 2 || store.targets[0] != "srv-1" || store.targets[1] != "srv-2" {
		t.Fatalf("unexpected targets: %#v", store.targets)
	}
	if len(store.steps) != 3 {
		t.Fatalf("expected three named steps, got %#v", store.steps)
	}
	if store.steps[2].Target != "" || store.steps[2].Name != "control" {
		t.Fatalf("expected control step to be stored without target, got %#v", store.steps[2])
	}
}
