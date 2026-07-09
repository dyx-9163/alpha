package installflow

import (
	"errors"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/taskrun"
)

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
}

type Recorder = taskrun.Recorder

type Step struct {
	Name  string
	Title string
}

type Messages struct {
	StepStart  string
	StepDone   string
	StepFailed string
}

type ArgsFormatter struct {
	Started func(index, total int, title string) []any
	Done    func(index, total int, title string) []any
	Failed  func(index, total int, title string, err error) []any
}

type Runner struct {
	Log       Logger
	Recorder  Recorder
	Target    string
	Steps     []Step
	Messages  Messages
	Formatter ArgsFormatter
}

func (r Runner) Run(index int, name, title string, fn func() error) error {
	if fn == nil {
		return errors.New("installflow step function is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = StepName(r.Steps, index)
	}
	total := len(r.Steps)
	if total == 0 {
		total = index
	}
	if r.Recorder != nil {
		r.Recorder.StartStep(r.Target, name, title, index)
	}
	if r.Log != nil && r.Messages.StepStart != "" {
		r.Log.Info(r.Messages.StepStart, r.startArgs(index, total, title)...)
	}
	if err := fn(); err != nil {
		if r.Log != nil && r.Messages.StepFailed != "" {
			r.Log.Error(r.Messages.StepFailed, r.failedArgs(index, total, title, err)...)
		}
		if r.Recorder != nil {
			r.Recorder.FinishStep(r.Target, name, "failed", err.Error())
		}
		return err
	}
	if r.Log != nil && r.Messages.StepDone != "" {
		r.Log.Info(r.Messages.StepDone, r.doneArgs(index, total, title)...)
	}
	if r.Recorder != nil {
		r.Recorder.FinishStep(r.Target, name, "success", "")
	}
	return nil
}

func (r Runner) startArgs(index, total int, title string) []any {
	if r.Formatter.Started != nil {
		return r.Formatter.Started(index, total, title)
	}
	return []any{index, total, title}
}

func (r Runner) doneArgs(index, total int, title string) []any {
	if r.Formatter.Done != nil {
		return r.Formatter.Done(index, total, title)
	}
	return []any{index, total, title}
}

func (r Runner) failedArgs(index, total int, title string, err error) []any {
	if r.Formatter.Failed != nil {
		return r.Formatter.Failed(index, total, title, err)
	}
	return []any{index, total, title, err}
}

func BatchFormatter(targetIndex, targetTotal int) ArgsFormatter {
	return ArgsFormatter{
		Started: func(index, total int, title string) []any {
			return []any{targetIndex, targetTotal, index, total, title}
		},
		Done: func(index, total int, title string) []any {
			return []any{targetIndex, targetTotal, index, total, title}
		},
		Failed: func(index, total int, title string, err error) []any {
			return []any{targetIndex, targetTotal, index, total, title, err}
		},
	}
}

func StepName(steps []Step, index int) string {
	if index > 0 && index <= len(steps) {
		if name := strings.TrimSpace(steps[index-1].Name); name != "" {
			return name
		}
	}
	return "step-" + itoa(index)
}

func RegistryPlan(targets []string, steps []Step) []registry.InstallStepPlan {
	plan := make([]registry.InstallStepPlan, 0, len(targets)*len(steps))
	for _, target := range targets {
		for idx, step := range steps {
			plan = append(plan, registry.InstallStepPlan{
				Target: target,
				Name:   step.Name,
				Title:  step.Title,
				Order:  idx + 1,
			})
		}
	}
	return plan
}

func StartTarget(recorder Recorder, target string) {
	taskrun.StartTarget(recorder, target)
}

func FinishTarget(recorder Recorder, target, status, errText string) {
	taskrun.FinishTarget(recorder, target, status, errText)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
