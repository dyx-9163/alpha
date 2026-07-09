package installflow

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type fakeLogger struct {
	infos  []string
	errors []string
}

func (l *fakeLogger) Info(format string, args ...any) {
	l.infos = append(l.infos, fmt.Sprintf(format, args...))
}

func (l *fakeLogger) Error(format string, args ...any) {
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

type fakeRecorder struct {
	events []string
}

func (r *fakeRecorder) StartTarget(target string) {
	r.events = append(r.events, "target:start:"+target)
}

func (r *fakeRecorder) FinishTarget(target, status, errText string) {
	r.events = append(r.events, "target:finish:"+target+":"+status+":"+errText)
}

func (r *fakeRecorder) StartStep(target, name, title string, order int) {
	r.events = append(r.events, fmt.Sprintf("step:start:%s:%s:%d:%s", target, name, order, title))
}

func (r *fakeRecorder) FinishStep(target, name, status, errText string) {
	r.events = append(r.events, "step:finish:"+target+":"+name+":"+status+":"+errText)
}

func TestRunnerRecordsSuccessfulStep(t *testing.T) {
	log := &fakeLogger{}
	recorder := &fakeRecorder{}
	runner := Runner{
		Log:      log,
		Recorder: recorder,
		Target:   "srv-1",
		Steps:    []Step{{Name: "load-server", Title: "load server"}},
		Messages: Messages{
			StepStart:  "step %d/%d started: %s",
			StepDone:   "step %d/%d completed: %s",
			StepFailed: "step %d/%d failed: %s: %v",
		},
	}
	if err := runner.Run(1, "load-server", "load server", func() error { return nil }); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !reflect.DeepEqual(log.infos, []string{"step 1/1 started: load server", "step 1/1 completed: load server"}) {
		t.Fatalf("unexpected info logs: %#v", log.infos)
	}
	wantEvents := []string{
		"step:start:srv-1:load-server:1:load server",
		"step:finish:srv-1:load-server:success:",
	}
	if !reflect.DeepEqual(recorder.events, wantEvents) {
		t.Fatalf("unexpected events: %#v", recorder.events)
	}
}

func TestRunnerRecordsFailedStep(t *testing.T) {
	log := &fakeLogger{}
	recorder := &fakeRecorder{}
	runner := Runner{
		Log:      log,
		Recorder: recorder,
		Target:   "srv-1",
		Steps:    []Step{{Name: "install", Title: "install"}},
		Messages: Messages{
			StepStart:  "step %d/%d started: %s",
			StepDone:   "step %d/%d completed: %s",
			StepFailed: "step %d/%d failed: %s: %v",
		},
	}
	errBoom := errors.New("boom")
	if err := runner.Run(1, "install", "install", func() error { return errBoom }); !errors.Is(err, errBoom) {
		t.Fatalf("expected boom, got %v", err)
	}
	if !reflect.DeepEqual(log.errors, []string{"step 1/1 failed: install: boom"}) {
		t.Fatalf("unexpected error logs: %#v", log.errors)
	}
	wantEvents := []string{
		"step:start:srv-1:install:1:install",
		"step:finish:srv-1:install:failed:boom",
	}
	if !reflect.DeepEqual(recorder.events, wantEvents) {
		t.Fatalf("unexpected events: %#v", recorder.events)
	}
}

func TestBatchFormatterAddsTargetIndexes(t *testing.T) {
	log := &fakeLogger{}
	runner := Runner{
		Log:       log,
		Target:    "srv-1",
		Steps:     []Step{{Name: "install", Title: "install"}},
		Messages:  Messages{StepStart: "[%d/%d] step %d/%d started: %s", StepDone: "[%d/%d] step %d/%d completed: %s"},
		Formatter: BatchFormatter(2, 3),
	}
	if err := runner.Run(1, "install", "install", func() error { return nil }); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	want := []string{"[2/3] step 1/1 started: install", "[2/3] step 1/1 completed: install"}
	if !reflect.DeepEqual(log.infos, want) {
		t.Fatalf("unexpected logs: %#v", log.infos)
	}
}

func TestRegistryPlan(t *testing.T) {
	plan := RegistryPlan([]string{"srv-1", "srv-2"}, []Step{{Name: "a", Title: "A"}, {Name: "b", Title: "B"}})
	if len(plan) != 4 {
		t.Fatalf("expected 4 plan items, got %d", len(plan))
	}
	if plan[2].Target != "srv-2" || plan[2].Name != "a" || plan[3].Order != 2 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}
