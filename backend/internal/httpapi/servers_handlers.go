package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"aifar-deployment/backend/internal/i18n"
	serverdomain "aifar-deployment/backend/internal/servers"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

func (a *API) listServers(w http.ResponseWriter, r *http.Request) {
	out, err := a.servers.List()
	respond(w, out, err)
}

func (a *API) reorderServers(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req reorderServersRequest
	if !decode(w, r, &req) {
		return
	}
	ids := make([]string, 0, len(req.IDs))
	seen := map[string]bool{}
	for _, id := range req.IDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "IDS_REQUIRED", i18n.Text(lang, "servers.orderRequired"), nil)
		return
	}
	err := a.servers.Reorder(ids)
	if err == nil {
		a.audit(r, "servers.reorder", strings.Join(ids, ","), "success", i18n.Text(lang, "servers.reordered"))
	}
	respond(w, map[string]any{"ids": ids}, err)
}

func (req serverSaveRequest) toStoreServer() store.Server {
	dockerHost := ""
	if req.DockerHost != nil {
		dockerHost = *req.DockerHost
	}
	return store.Server{
		ID:         req.ID,
		Name:       req.Name,
		Host:       req.Host,
		Port:       req.Port,
		Username:   req.Username,
		AuthType:   req.AuthType,
		Password:   req.Password,
		PrivateKey: req.PrivateKey,
		Tags:       req.Tags,
		Note:       req.Note,
		DeployDir:  req.DeployDir,
		DockerHost: dockerHost,
		Status:     req.Status,
		LastError:  req.LastError,
		SortOrder:  req.SortOrder,
	}
}

func (a *API) saveServer(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req serverSaveRequest
	if !decode(w, r, &req) {
		return
	}
	if id := chi.URLParam(r, "id"); id != "" {
		req.ID = id
	}
	input := req.toStoreServer()
	if req.DockerHost == nil && input.ID != "" {
		if current, currentErr := a.store.GetServer(input.ID, false); currentErr == nil {
			input.DockerHost = current.DockerHost
		}
	}
	out, err := a.servers.Save(input, lang)
	if serverdomain.IsValidationError(err) {
		writeError(w, http.StatusBadRequest, "INVALID_SERVER", err.Error(), nil)
		return
	}
	if err == nil {
		a.audit(r, "servers.save", out.ID, "success", i18n.Text(lang, "servers.saved"))
	}
	respond(w, out, err)
}

func (a *API) deleteServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := a.servers.Delete(id)
	if err == nil {
		a.audit(r, "servers.delete", id, "success", i18n.Text(languageFromRequest(r), "servers.deleted"))
	}
	respond(w, map[string]any{"deleted": id}, err)
}

func (a *API) probeServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	lang := languageFromRequest(r)
	actor := currentUser(r).Username
	task, err := a.tasks.StartWithLanguage("servers.probe", id, actor, lang, func(ctx context.Context, log worker.Logger) error {
		return a.servers.Probe(ctx, id, lang, log)
	})
	if err == nil {
		a.audit(r, "servers.probe", id, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) serverDisks(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	id := chi.URLParam(r, "id")
	inventory, err := a.servers.ListDiskDevices(r.Context(), id)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"serverId":  inventory.ServerID,
			"devices":   inventory.Devices,
			"sampledAt": time.Now(),
		})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "SERVER_NOT_FOUND", i18n.Text(lang, "api.serverNotFound"), map[string]any{"serverId": id})
		return
	}
	writeError(w, http.StatusBadGateway, "SERVER_DISK_DETECT_FAILED", i18n.Text(lang, "api.serverDiskDetectFailed"), map[string]any{"serverId": id, "error": err.Error()})
}

func (a *API) serverTelemetry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	writeJSON(w, http.StatusOK, map[string]any{
		"serverId": id, "cpu": 0, "memory": 0, "disk": 0, "load": []float64{0, 0, 0},
		"sampledAt": time.Now(),
	})
}
