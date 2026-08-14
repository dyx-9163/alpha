package httpapi

import (
	"net/http"
	"strconv"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskmeta"

	"github.com/go-chi/chi/v5"
)

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListTasks()
	respond(w, decorateTasks(out), err)
}

func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	task, logs, err := a.store.GetTask(chi.URLParam(r, "id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	targets, targetsErr := a.store.ListTaskTargets(task.ID)
	if targetsErr != nil {
		respond(w, nil, targetsErr)
		return
	}
	steps, stepsErr := a.store.ListTaskSteps(task.ID)
	if stepsErr != nil {
		respond(w, nil, stepsErr)
		return
	}
	respond(w, map[string]any{"task": decorateTask(task), "logs": logs, "targets": targets, "steps": steps}, nil)
}

func decorateTasks(tasks []store.Task) []store.Task {
	out := make([]store.Task, 0, len(tasks))
	for _, task := range tasks {
		decorated := decorateTask(task)
		if !decorated.Trackable {
			continue
		}
		out = append(out, decorated)
	}
	return out
}

func decorateTask(task store.Task) store.Task {
	desc := taskmeta.Describe(task.Type)
	task.Category = desc.Category
	task.Trackable = desc.Trackable
	return task
}

func (a *API) taskEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if logs, err := a.store.TaskLogs(id); err == nil {
		for _, log := range logs {
			writeSSE(w, "task-event", log)
		}
	}
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	ch, unsubscribe := a.tasks.Subscribe(id)
	defer unsubscribe()
	for {
		select {
		case <-r.Context().Done():
			return
		case log := <-ch:
			writeSSE(w, "task-event", log)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (a *API) cancelTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cancelled := a.tasks.Cancel(id)
	if cancelled {
		a.audit(r, "tasks.cancel", id, "success", i18n.Text(languageFromRequest(r), "api.cancelRequested"))
	}
	writeJSON(w, http.StatusOK, map[string]any{"taskId": id, "cancelled": cancelled})
}

func (a *API) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !a.canDeleteTasks(w, r, []string{id}) {
		return
	}
	err := a.store.DeleteTask(id)
	if err == nil {
		a.audit(r, "tasks.delete", id, "success", i18n.Text(languageFromRequest(r), "api.tasksDeleted", 1))
	}
	respond(w, map[string]any{"deleted": id}, err)
}

func (a *API) deleteTasks(w http.ResponseWriter, r *http.Request) {
	var req deleteTasksRequest
	if !decode(w, r, &req) {
		return
	}
	req.IDs = cleanStringIDs(req.IDs)
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "IDS_REQUIRED", i18n.Text(languageFromRequest(r), "api.idsRequired"), nil)
		return
	}
	if !a.canDeleteTasks(w, r, req.IDs) {
		return
	}
	deleted, err := a.store.DeleteTasks(req.IDs)
	if err == nil {
		a.audit(r, "tasks.delete.batch", strconv.Itoa(deleted), "success", i18n.Text(languageFromRequest(r), "api.tasksDeleted", deleted))
	}
	respond(w, map[string]any{"deleted": deleted}, err)
}

func (a *API) canDeleteTasks(w http.ResponseWriter, r *http.Request, ids []string) bool {
	for _, id := range ids {
		task, _, err := a.store.GetTask(id)
		if err != nil {
			if store.IsNotFound(err) {
				continue
			}
			respond(w, nil, err)
			return false
		}
		if task.Status == "pending" || task.Status == "running" {
			writeError(w, http.StatusConflict, "TASK_RUNNING", i18n.Text(languageFromRequest(r), "api.runningTaskCannotDelete"), map[string]any{"taskId": id, "status": task.Status})
			return false
		}
	}
	return true
}

func (a *API) clearTaskLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, _, err := a.store.GetTask(id); err != nil {
		respond(w, nil, err)
		return
	}
	err := a.store.ClearTaskLogs(id)
	if err == nil {
		a.audit(r, "tasks.logs.clear", id, "success", i18n.Text(languageFromRequest(r), "api.taskLogsCleared", 1))
	}
	respond(w, map[string]any{"taskId": id, "cleared": err == nil}, err)
}

func (a *API) clearTaskLogsBatch(w http.ResponseWriter, r *http.Request) {
	var req deleteTasksRequest
	if !decode(w, r, &req) {
		return
	}
	ids := cleanStringIDs(req.IDs)
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "IDS_REQUIRED", i18n.Text(languageFromRequest(r), "api.taskLogIDsRequired"), nil)
		return
	}
	for _, id := range ids {
		if _, _, err := a.store.GetTask(id); err != nil {
			respond(w, nil, err)
			return
		}
	}
	deleted, err := a.store.ClearTaskLogsForTasks(ids)
	if err == nil {
		a.audit(r, "tasks.logs.clear.batch", strconv.Itoa(len(ids)), "success", i18n.Text(languageFromRequest(r), "api.taskLogsCleared", len(ids)))
	}
	respond(w, map[string]any{"ids": ids, "cleared": len(ids), "logsDeleted": deleted}, err)
}
