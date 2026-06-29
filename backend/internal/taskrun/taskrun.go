package taskrun

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
