package deleteflow

import (
	"fmt"
	"strings"

	"aifar-deployment/backend/internal/taskrun"
)

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
}

type Step struct {
	Name  string
	Title string
	Run   func() error
}

type Messages struct {
	StepStart    string
	StepDone     string
	StepFailed   string
	DeleteFailed string
	Deleted      string
}

type Request struct {
	Target     string
	ServerName string
	InstanceID string
	Log        Logger
	Recorder   taskrun.Recorder
	Steps      []Step
	Messages   Messages
}

func Run(req Request) error {
	target := strings.TrimSpace(req.Target)
	taskrun.StartTarget(req.Recorder, target)
	runner := taskrun.Runner{
		Log:      req.Log,
		Recorder: req.Recorder,
		Target:   target,
		Steps:    taskSteps(req.Steps),
		Messages: taskrun.Messages{
			StepStart:  req.Messages.StepStart,
			StepDone:   req.Messages.StepDone,
			StepFailed: req.Messages.StepFailed,
		},
	}
	for idx, step := range req.Steps {
		if err := runner.Run(idx+1, step.Name, step.Title, step.Run); err != nil {
			msg := failureMessage(req.ServerName, err)
			if req.Log != nil && req.Messages.DeleteFailed != "" {
				req.Log.Error(req.Messages.DeleteFailed, msg)
			}
			taskrun.FinishTarget(req.Recorder, target, "failed", msg)
			return err
		}
	}
	if req.Log != nil && req.Messages.Deleted != "" {
		req.Log.Info(req.Messages.Deleted, req.InstanceID)
	}
	taskrun.FinishTarget(req.Recorder, target, "success", "")
	return nil
}

func taskSteps(steps []Step) []taskrun.Step {
	out := make([]taskrun.Step, 0, len(steps))
	for _, step := range steps {
		out = append(out, taskrun.Step{Name: step.Name, Title: step.Title})
	}
	return out
}

func failureMessage(serverName string, err error) string {
	if strings.TrimSpace(serverName) == "" {
		return err.Error()
	}
	return fmt.Sprintf("%s: %v", serverName, err)
}
