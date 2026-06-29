package deleteflow

import (
	"errors"
	"testing"
)

type fakeLogger struct {
	info  []string
	error []string
}

func (f *fakeLogger) Info(format string, args ...any) {
	f.info = append(f.info, format)
}

func (f *fakeLogger) Error(format string, args ...any) {
	f.error = append(f.error, format)
}

type fakeRecorder struct {
	targetStatus string
	stepStatus   []string
}

func (f *fakeRecorder) StartTarget(target string) {}

func (f *fakeRecorder) FinishTarget(target, status, errText string) {
	f.targetStatus = status
}

func (f *fakeRecorder) StartStep(target, name, title string, order int) {}

func (f *fakeRecorder) FinishStep(target, name, status, errText string) {
	f.stepStatus = append(f.stepStatus, status)
}

func TestRunMarksSuccessAfterAllSteps(t *testing.T) {
	logger := &fakeLogger{}
	recorder := &fakeRecorder{}
	err := Run(Request{
		Target:     "srv-1",
		ServerName: "server",
		InstanceID: "app-1",
		Log:        logger,
		Recorder:   recorder,
		Steps: []Step{
			{Name: "remove-remote", Title: "Remove remote", Run: func() error { return nil }},
			{Name: "delete-instance", Title: "Delete instance", Run: func() error { return nil }},
		},
		Messages: Messages{
			StepStart:    "start",
			StepDone:     "done",
			StepFailed:   "failed",
			DeleteFailed: "delete failed",
			Deleted:      "deleted",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if recorder.targetStatus != "success" {
		t.Fatalf("expected success target, got %s", recorder.targetStatus)
	}
	if len(recorder.stepStatus) != 2 || recorder.stepStatus[0] != "success" || recorder.stepStatus[1] != "success" {
		t.Fatalf("expected successful steps, got %#v", recorder.stepStatus)
	}
	if len(logger.error) != 0 {
		t.Fatalf("expected no error logs, got %#v", logger.error)
	}
}

func TestRunMarksFailureAndStops(t *testing.T) {
	logger := &fakeLogger{}
	recorder := &fakeRecorder{}
	calledSecond := false
	err := Run(Request{
		Target:     "srv-1",
		ServerName: "server",
		InstanceID: "app-1",
		Log:        logger,
		Recorder:   recorder,
		Steps: []Step{
			{Name: "remove-remote", Title: "Remove remote", Run: func() error { return errors.New("boom") }},
			{Name: "delete-instance", Title: "Delete instance", Run: func() error {
				calledSecond = true
				return nil
			}},
		},
		Messages: Messages{
			StepStart:    "start",
			StepDone:     "done",
			StepFailed:   "failed",
			DeleteFailed: "delete failed",
			Deleted:      "deleted",
		},
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if calledSecond {
		t.Fatal("second step should not run after failure")
	}
	if recorder.targetStatus != "failed" {
		t.Fatalf("expected failed target, got %s", recorder.targetStatus)
	}
	if len(logger.error) == 0 {
		t.Fatal("expected delete failure to be logged")
	}
}
