package httpapi

import (
	"net/http"

	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskplan"
	"aifar-deployment/backend/internal/worker"
)

type simpleTaskStep struct {
	name  string
	title string
}

func simpleTaskPlan(target string, steps []simpleTaskStep) []taskplan.Step {
	if len(steps) == 0 && target != "" {
		return []taskplan.Step{{Target: target}}
	}
	out := make([]taskplan.Step, 0, len(steps))
	for index, step := range steps {
		out = append(out, taskplan.Step{
			Target: target,
			Name:   step.name,
			Title:  step.title,
			Order:  index + 1,
		})
	}
	return out
}

func (a *API) startSimplePlannedTask(w http.ResponseWriter, taskType, target, actor, lang, planTarget string, steps []simpleTaskStep, job worker.Job) (store.Task, error, bool) {
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return task, err, false
	}
	if err := a.storeTaskPlanOrDelete(task.ID, simpleTaskPlan(planTarget, steps)); err != nil {
		writeError(w, http.StatusInternalServerError, "TASK_PLAN_STORE_FAILED", err.Error(), map[string]any{"taskType": taskType, "target": target})
		return task, err, false
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, job)
	return task, err, true
}

func (a *API) startSimplePlannedTaskWithLocks(w http.ResponseWriter, taskType, target, actor, lang, planTarget string, steps []simpleTaskStep, specs []operationLockSpec, job worker.Job) (store.Task, error, bool) {
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return task, err, false
	}
	if err := a.storeTaskPlanOrDelete(task.ID, simpleTaskPlan(planTarget, steps)); err != nil {
		writeError(w, http.StatusInternalServerError, "TASK_PLAN_STORE_FAILED", err.Error(), map[string]any{"taskType": taskType, "target": target})
		return task, err, false
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, specs)
	if !ok {
		return task, nil, false
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, job)
	if err != nil {
		a.releaseOperationLocks(locks)
		_ = a.store.DeleteTask(task.ID)
	}
	return task, err, true
}
