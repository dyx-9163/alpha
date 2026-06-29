package taskrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestRunTargetsUsesConcurrencyLimit(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	started := make(chan string, 3)
	done := make(chan []TargetFailure, 1)
	go func() {
		done <- RunTargets(context.Background(), []string{"srv-1", "srv-2", "srv-3"}, 2, func(target string) error {
			started <- target
			<-release
			return nil
		})
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case target := <-started:
			seen[target] = true
		case <-time.After(time.Second):
			t.Fatalf("expected first two targets to start, got %v", seen)
		}
	}
	select {
	case target := <-started:
		t.Fatalf("third target %s started before a concurrency slot was released", target)
	case <-time.After(50 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case failures := <-done:
		if len(failures) != 0 {
			t.Fatalf("unexpected failures: %+v", failures)
		}
	case <-time.After(time.Second):
		t.Fatal("targets did not finish after release")
	}
}
