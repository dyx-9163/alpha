package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

func (a *API) containerSummary(w http.ResponseWriter, r *http.Request) {
	host := dockerHostFromRequest(r)
	server, useServer, serverErr := a.dockerServerFromRequest(r)
	if serverErr != nil {
		respond(w, nil, serverErr)
		return
	}
	if !useServer && host == "" {
		writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(languageFromRequest(r), "api.dockerTargetRequired"), nil)
		return
	}
	includeDisk := queryBool(r, "includeDisk", false)
	var summary adapter.DockerSummary
	var err error
	var df any
	if useServer {
		summary, df, err = dockerSummaryResponseForServer(r.Context(), server, includeDisk)
	} else {
		summary, df, err = dockerSummaryResponseForHost(r.Context(), host, includeDisk)
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error(), "containers": 0, "images": 0, "networks": 0, "volumes": 0})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "summary": summary, "diskUsage": df})
}

func dockerSummaryResponseForServer(ctx context.Context, server store.Server, includeDisk bool) (adapter.DockerSummary, any, error) {
	if !includeDisk {
		summary, err := adapter.DockerSummaryForServer(ctx, server)
		return summary, nil, err
	}
	var (
		summary adapter.DockerSummary
		df      []adapter.DockerDiskUsage
		err     error
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		summary, err = adapter.DockerSummaryForServer(ctx, server)
	}()
	go func() {
		defer wg.Done()
		df, _ = adapter.DockerSystemDFForServer(ctx, server)
	}()
	wg.Wait()
	return summary, df, err
}

func dockerSummaryResponseForHost(ctx context.Context, host string, includeDisk bool) (adapter.DockerSummary, any, error) {
	if !includeDisk {
		summary, err := adapter.DockerSummaryForHost(ctx, host)
		return summary, nil, err
	}
	var (
		summary adapter.DockerSummary
		df      []adapter.DockerDiskUsage
		err     error
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		summary, err = adapter.DockerSummaryForHost(ctx, host)
	}()
	go func() {
		defer wg.Done()
		df, _ = adapter.DockerSystemDF(ctx, host)
	}()
	wg.Wait()
	return summary, df, err
}

func (a *API) containers(w http.ResponseWriter, r *http.Request) {
	host := dockerHostFromRequest(r)
	server, useServer, serverErr := a.dockerServerFromRequest(r)
	if serverErr != nil {
		respond(w, nil, serverErr)
		return
	}
	if !useServer && host == "" {
		writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(languageFromRequest(r), "api.dockerTargetRequired"), nil)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	if kind == "" {
		kind = "containers"
	}
	var (
		out any
		err error
	)
	switch kind {
	case "containers":
		if useServer {
			out, err = adapter.DockerContainersForServer(r.Context(), server)
		} else {
			out, err = adapter.DockerContainers(r.Context(), host)
		}
	case "images":
		if useServer {
			out, err = adapter.DockerImagesForServer(r.Context(), server)
		} else {
			out, err = adapter.DockerImages(r.Context(), host)
		}
	case "networks", "network":
		if useServer {
			out, err = adapter.DockerNetworksForServer(r.Context(), server)
		} else {
			out, err = adapter.DockerNetworks(r.Context(), host)
		}
	case "volumes", "volume":
		if useServer {
			out, err = adapter.DockerVolumesForServer(r.Context(), server)
		} else {
			out, err = adapter.DockerVolumes(r.Context(), host)
		}
	case "df", "disk":
		if useServer {
			out, err = adapter.DockerSystemDFForServer(r.Context(), server)
		} else {
			out, err = adapter.DockerSystemDF(r.Context(), host)
		}
	default:
		writeError(w, http.StatusBadRequest, "UNSUPPORTED_CONTAINER_KIND", "unsupported container collection", map[string]any{"kind": kind})
		return
	}
	respond(w, out, err)
}

func (a *API) containerAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		action = normalizeContainerAction(action)
		lang := languageFromRequest(r)
		if action == "" {
			writeError(w, http.StatusBadRequest, "UNSUPPORTED_CONTAINER_ACTION", i18n.Text(lang, "api.unsupportedContainerAction"), nil)
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "id"))
		if id == "" {
			writeError(w, http.StatusBadRequest, "IDS_REQUIRED", i18n.Text(lang, "api.containerIDsRequired"), nil)
			return
		}
		host := dockerHostFromRequest(r)
		server, useServer, serverErr := a.dockerServerFromRequest(r)
		if serverErr != nil {
			respond(w, nil, serverErr)
			return
		}
		if !useServer && host == "" {
			writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(lang, "api.dockerTargetRequired"), nil)
			return
		}
		if action == "remove" && a.rejectRunningContainerRemove(w, r, server, useServer, host, []string{id}) {
			return
		}
		task, err := a.tasks.StartWithLanguage("containers.container."+action, id, currentUser(r).Username, lang, func(ctx context.Context, log worker.Logger) error {
			log.Info(i18n.Text(lang, "api.containerActionRequested"), action, id)
			if err := runDockerContainerAction(ctx, server, useServer, host, id, action); err != nil {
				return err
			}
			log.Info(i18n.Text(lang, "api.containerActionCompleted"), action, id)
			return nil
		})
		if err == nil {
			a.audit(r, "containers.container."+action, id, "running", task.ID)
		}
		respondTask(w, task, err)
	}
}

func (a *API) containerBatchAction(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req containerBatchActionRequest
	if !decode(w, r, &req) {
		return
	}
	action := normalizeContainerAction(req.Action)
	if action == "" {
		writeError(w, http.StatusBadRequest, "UNSUPPORTED_CONTAINER_ACTION", i18n.Text(lang, "api.unsupportedContainerAction"), map[string]any{"action": req.Action})
		return
	}
	ids := normalizeContainerIDs(req.IDs)
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "IDS_REQUIRED", i18n.Text(lang, "api.containerIDsRequired"), nil)
		return
	}
	host := dockerHostFromRequest(r)
	server, useServer, serverErr := a.dockerServerFromRequest(r)
	if serverErr != nil {
		respond(w, nil, serverErr)
		return
	}
	if !useServer && host == "" {
		writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(lang, "api.dockerTargetRequired"), nil)
		return
	}
	if action == "remove" && a.rejectRunningContainerRemove(w, r, server, useServer, host, ids) {
		return
	}
	target := strings.Join(ids, ",")
	task, err := a.tasks.StartWithLanguage("containers.container.batch."+action, target, currentUser(r).Username, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info(i18n.Text(lang, "api.containerBatchActionRequested"), action, len(ids))
		for _, id := range ids {
			log.Info(i18n.Text(lang, "api.containerActionRequested"), action, id)
			if err := runDockerContainerAction(ctx, server, useServer, host, id, action); err != nil {
				return fmt.Errorf("%s %s: %w", action, id, err)
			}
			log.Info(i18n.Text(lang, "api.containerActionCompleted"), action, id)
		}
		log.Info(i18n.Text(lang, "api.containerBatchActionCompleted"), action, len(ids))
		return nil
	})
	if err == nil {
		a.audit(r, "containers.container.batch."+action, target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) containerImageRemove(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req containerImageRemoveRequest
	if !decode(w, r, &req) {
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		writeError(w, http.StatusBadRequest, "IMAGE_ID_REQUIRED", i18n.Text(lang, "api.containerImageIDRequired"), nil)
		return
	}
	host := dockerHostFromRequest(r)
	server, useServer, serverErr := a.dockerServerFromRequest(r)
	if serverErr != nil {
		respond(w, nil, serverErr)
		return
	}
	if !useServer && host == "" {
		writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(lang, "api.dockerTargetRequired"), nil)
		return
	}
	task, err := a.tasks.StartWithLanguage("containers.image.remove", id, currentUser(r).Username, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info(i18n.Text(lang, "api.containerImageRemoveRequested"), id)
		if err := runDockerImageRemove(ctx, server, useServer, host, id); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.containerImageRemoveCompleted"), id)
		return nil
	})
	if err == nil {
		a.audit(r, "containers.image.remove", id, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) containerLogs(w http.ResponseWriter, r *http.Request) {
	host := dockerHostFromRequest(r)
	server, useServer, serverErr := a.dockerServerFromRequest(r)
	if serverErr != nil {
		respond(w, nil, serverErr)
		return
	}
	if !useServer && host == "" {
		writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(languageFromRequest(r), "api.dockerTargetRequired"), nil)
		return
	}
	tail := queryInt(r, "tail", 200)
	var (
		logs []string
		err  error
	)
	if useServer {
		logs, err = adapter.DockerContainerLogsForServer(r.Context(), server, chi.URLParam(r, "id"), tail)
	} else {
		logs, err = adapter.DockerContainerLogs(r.Context(), host, chi.URLParam(r, "id"), tail)
	}
	respond(w, map[string]any{"logs": logs}, err)
}

func (a *API) dockerServerFromRequest(r *http.Request) (store.Server, bool, error) {
	serverID := strings.TrimSpace(r.URL.Query().Get("serverId"))
	if serverID == "" {
		return store.Server{}, false, nil
	}
	server, err := a.store.GetServer(serverID, true)
	if err != nil {
		return store.Server{}, false, err
	}
	return server, true, nil
}

func dockerHostFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("dockerHost"))
}

func runDockerContainerAction(ctx context.Context, server store.Server, useServer bool, host, id, action string) error {
	if useServer {
		return adapter.DockerContainerActionForServer(ctx, server, id, action)
	}
	return adapter.DockerContainerAction(ctx, host, id, action)
}

func runDockerImageRemove(ctx context.Context, server store.Server, useServer bool, host, id string) error {
	if useServer {
		return adapter.DockerImageRemoveForServer(ctx, server, id)
	}
	return adapter.DockerImageRemove(ctx, host, id)
}

func (a *API) rejectRunningContainerRemove(w http.ResponseWriter, r *http.Request, server store.Server, useServer bool, host string, ids []string) bool {
	running, err := a.selectedRunningContainers(r.Context(), server, useServer, host, ids)
	if err != nil {
		respond(w, nil, err)
		return true
	}
	if len(running) == 0 {
		return false
	}
	writeError(w, http.StatusConflict, "CONTAINER_RUNNING_CANNOT_REMOVE", i18n.Text(languageFromRequest(r), "api.containerRunningCannotRemove"), map[string]any{"containers": running})
	return true
}

func (a *API) selectedRunningContainers(ctx context.Context, server store.Server, useServer bool, host string, ids []string) ([]string, error) {
	var (
		rows []adapter.DockerContainer
		err  error
	)
	if useServer {
		rows, err = adapter.DockerContainersForServer(ctx, server)
	} else {
		rows, err = adapter.DockerContainers(ctx, host)
	}
	if err != nil {
		return nil, err
	}
	running := make([]string, 0)
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.State), "running") {
			continue
		}
		for _, id := range ids {
			if containerMatchesID(row, id) {
				running = append(running, containerLabel(row))
				break
			}
		}
	}
	return running, nil
}

func normalizeContainerAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "start", "stop", "restart":
		return strings.ToLower(strings.TrimSpace(action))
	case "remove", "rm", "delete", "uninstall":
		return "remove"
	default:
		return ""
	}
}

func normalizeContainerIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func containerMatchesID(row adapter.DockerContainer, id string) bool {
	needle := strings.ToLower(strings.TrimSpace(id))
	if needle == "" {
		return false
	}
	rowID := strings.ToLower(strings.TrimSpace(row.ID))
	if rowID != "" && (rowID == needle || strings.HasPrefix(rowID, needle) || strings.HasPrefix(needle, rowID)) {
		return true
	}
	return strings.ToLower(strings.TrimSpace(row.Name)) == needle
}

func containerLabel(row adapter.DockerContainer) string {
	if strings.TrimSpace(row.Name) != "" {
		return row.Name
	}
	return row.ID
}
