package taskrun

import (
	"errors"
	"fmt"
	"strings"
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
	r.events = append(r.events, "start-target:"+target)
}

func (r *fakeRecorder) FinishTarget(target, status, errText string) {
	r.events = append(r.events, "finish-target:"+target+":"+status+":"+errText)
}

func (r *fakeRecorder) StartStep(target, name, title string, order int) {
	r.events = append(r.events, fmt.Sprintf("start-step:%s:%s:%d:%s", target, name, order, title))
}

func (r *fakeRecorder) FinishStep(target, name, status, errText string) {
	r.events = append(r.events, "finish-step:"+target+":"+name+":"+status+":"+errText)
}

func TestRunnerRecordsSuccessfulStep(t *testing.T) {
	log := &fakeLogger{}
	recorder := &fakeRecorder{}
	runner := Runner{
		Log:      log,
		Recorder: recorder,
		Target:   "srv-1",
		Steps:    []Step{{Name: "load", Title: "Load server"}, {Name: "install", Title: "Install"}},
		Messages: Messages{StepStart: "start %d/%d %s", StepDone: "done %d/%d %s", StepFailed: "failed %d/%d %s: %v"},
	}
	if err := runner.Run(2, "install", "Install", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	events := strings.Join(recorder.events, "\n")
	if !strings.Contains(events, "start-step:srv-1:install:2:Install") || !strings.Contains(events, "finish-step:srv-1:install:success:") {
		t.Fatalf("unexpected events:\n%s", events)
	}
	if strings.Join(log.infos, "\n") != "start 2/2 Install\ndone 2/2 Install" {
		t.Fatalf("unexpected info logs: %+v", log.infos)
	}
}

func TestRunnerRecordsFailedStep(t *testing.T) {
	log := &fakeLogger{}
	recorder := &fakeRecorder{}
	runner := Runner{
		Log:      log,
		Recorder: recorder,
		Target:   "srv-1",
		Steps:    []Step{{Name: "install", Title: "Install"}},
		Messages: Messages{StepStart: "start %d/%d %s", StepDone: "done %d/%d %s", StepFailed: "failed %d/%d %s: %v"},
	}
	err := runner.Run(1, "install", "Install", func() error { return errors.New("boom") })
	if err == nil {
		t.Fatal("expected error")
	}
	events := strings.Join(recorder.events, "\n")
	if !strings.Contains(events, "finish-step:srv-1:install:failed:boom") {
		t.Fatalf("unexpected events:\n%s", events)
	}
	if len(log.errors) != 1 || !strings.Contains(log.errors[0], "boom") {
		t.Fatalf("unexpected error logs: %+v", log.errors)
	}
}
