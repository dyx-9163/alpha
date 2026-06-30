package httpapi

import (
	"context"
	"net/http"
	"strings"

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
	var (
		summary adapter.DockerSummary
		err     error
		df      any
	)
	if useServer {
		var serverSummary adapter.DockerSummary
		serverSummary, err = adapter.DockerSummaryForServer(r.Context(), server)
		summary = serverSummary
		df, _ = adapter.DockerSystemDFForServer(r.Context(), server)
	} else {
		var hostSummary adapter.DockerSummary
		hostSummary, err = adapter.DockerSummaryForHost(r.Context(), host)
		summary = hostSummary
		df, _ = adapter.DockerSystemDF(r.Context(), host)
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error(), "containers": 0, "images": 0, "networks": 0, "volumes": 0})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "summary": summary, "diskUsage": df})
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
		id := chi.URLParam(r, "id")
		lang := languageFromRequest(r)
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
		task, err := a.tasks.StartWithLanguage("containers.container."+action, id, currentUser(r).Username, lang, func(ctx context.Context, log worker.Logger) error {
			log.Info(i18n.Text(lang, "api.containerActionRequested"), action, id)
			if useServer {
				if err := adapter.DockerContainerActionForServer(ctx, server, id, action); err != nil {
					return err
				}
			} else {
				if err := adapter.DockerContainerAction(ctx, host, id, action); err != nil {
					return err
				}
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
