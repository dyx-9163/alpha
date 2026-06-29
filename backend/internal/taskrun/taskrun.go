package taskrun

import (
	"context"
	"sync"
)

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
}

type Recorder interface {
	StartTarget(target string)
	FinishTarget(target, status, errText string)
	StartStep(target, name, title string, order int)
	FinishStep(target, name, status, errText string)
}

type Step struct {
	Name  string
	Title string
}

type TargetFailure struct {
	Target string
	Err    error
}

type Messages struct {
	StepStart  string
	StepDone   string
	StepFailed string
}

type Runner struct {
	Log      Logger
	Recorder Recorder
	Target   string
	Steps    []Step
	Messages Messages
}

func (r Runner) Run(index int, name, title string, fn func() error) error {
	if r.Recorder != nil {
		r.Recorder.StartStep(r.Target, name, title, index)
	}
	total := len(r.Steps)
	if total == 0 {
		total = index
	}
	if r.Log != nil && r.Messages.StepStart != "" {
		r.Log.Info(r.Messages.StepStart, index, total, title)
	}
	if err := fn(); err != nil {
		if r.Log != nil && r.Messages.StepFailed != "" {
			r.Log.Error(r.Messages.StepFailed, index, total, title, err)
		}
		if r.Recorder != nil {
			r.Recorder.FinishStep(r.Target, name, "failed", err.Error())
		}
		return err
	}
	if r.Log != nil && r.Messages.StepDone != "" {
		r.Log.Info(r.Messages.StepDone, index, total, title)
	}
	if r.Recorder != nil {
		r.Recorder.FinishStep(r.Target, name, "success", "")
	}
	return nil
}

func StartTarget(recorder Recorder, target string) {
	if recorder != nil {
		recorder.StartTarget(target)
	}
}

func FinishTarget(recorder Recorder, target, status, errText string) {
	if recorder != nil {
		recorder.FinishTarget(target, status, errText)
	}
}

func RunTargets(ctx context.Context, targets []string, concurrency int, run func(target string) error) []TargetFailure {
	if len(targets) == 0 || run == nil {
		return nil
	}
	limit := NormalizeConcurrency(concurrency, len(targets))
	sem := make(chan struct{}, limit)
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for idx, target := range targets {
		idx, target := idx, target
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				errs[idx] = ctx.Err()
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			if err := run(target); err != nil {
				errs[idx] = err
			}
		}()
	}
	wg.Wait()
	failures := make([]TargetFailure, 0)
	for idx, err := range errs {
		if err != nil {
			failures = append(failures, TargetFailure{Target: targets[idx], Err: err})
		}
	}
	return failures
}

func NormalizeConcurrency(value, total int) int {
	if total < 1 {
		return 1
	}
	if value < 1 {
		return total
	}
	if value > total {
		return total
	}
	return value
}

func FailureMessages(failures []TargetFailure) []string {
	messages := make([]string, 0, len(failures))
	for _, failure := range failures {
		if failure.Err != nil {
			messages = append(messages, failure.Err.Error())
		}
	}
	return messages
}

func FailureTargets(failures []TargetFailure) map[string]bool {
	out := make(map[string]bool, len(failures))
	for _, failure := range failures {
		if failure.Target != "" {
			out[failure.Target] = true
		}
	}
	return out
}
