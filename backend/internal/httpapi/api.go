package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/appcatalog"
	_ "aifar-deployment/backend/internal/apps/autoload"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/auth"
	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/resource"
	serverdomain "aifar-deployment/backend/internal/servers"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

type API struct {
	cfg     config.Config
	store   *store.Store
	tasks   *worker.Manager
	apps    *registry.Registry
	servers serverdomain.Service
	router  chi.Router
}

func New(cfg config.Config, s *store.Store, tasks *worker.Manager) *API {
	api := &API{
		cfg:     cfg,
		store:   s,
		tasks:   tasks,
		apps:    registry.NewFromRegistered(registry.Dependencies{Store: s}),
		servers: serverdomain.NewService(s, serverdomain.SSHProber{}, cfg.DefaultDeployDir),
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Route("/api/v2", func(r chi.Router) {
		r.Post("/auth/login", api.login)
		r.Group(func(r chi.Router) {
			r.Use(api.requireAuth)
			r.Get("/settings", api.getSettings)
			r.Put("/settings", api.putSettings)
			r.Get("/resources", api.listResources)
			r.Post("/resources/rescan", api.rescanResources)
			r.Get("/servers", api.listServers)
			r.Post("/servers", api.saveServer)
			r.Put("/servers/{id}", api.saveServer)
			r.Delete("/servers/{id}", api.deleteServer)
			r.Post("/servers/{id}/probe", api.probeServer)
			r.Get("/servers/{id}/telemetry", api.serverTelemetry)
			r.Get("/servers/{id}/terminal/ws", api.serverTerminal)
			r.Get("/tasks", api.listTasks)
			r.Get("/tasks/{id}", api.getTask)
			r.Get("/tasks/{id}/events", api.taskEvents)
			r.Post("/tasks/{id}/cancel", api.cancelTask)
			r.Delete("/tasks", api.deleteTasks)
			r.Delete("/tasks/{id}", api.deleteTask)
			r.Delete("/tasks/{id}/logs", api.clearTaskLogs)
			r.Get("/audit", api.listAudit)
			r.Delete("/audit", api.deleteAudit)
			r.Get("/apps/catalog", api.appsCatalog)
			r.Get("/apps/instances", api.appInstances)
			r.Post("/apps/{app}/install", api.installApp)
			r.Post("/apps/instances/{id}/upgrade", api.instanceAction("upgrade"))
			r.Post("/apps/instances/{id}/check", api.checkAppInstance)
			r.Post("/apps/instances/{id}/delete", api.deleteAppInstance)
			r.Post("/apps/instances/{id}/uninstall", api.deleteAppInstance)
			r.Get("/containers/summary", api.containerSummary)
			r.Get("/containers", api.containers)
			r.Post("/containers/{id}/start", api.containerAction("start"))
			r.Post("/containers/{id}/stop", api.containerAction("stop"))
			r.Post("/containers/{id}/restart", api.containerAction("restart"))
			r.Get("/containers/{id}/logs", api.containerLogs)
			r.Get("/containers/{id}/terminal/ws", api.containerTerminal)
			r.Get("/database/instances", api.databaseInstances)
			r.Post("/database/instances/{id}/backup", api.databaseBackup)
			r.Post("/database/mysql/install", api.installNamedApp("mysql"))
			r.Post("/database/redis/install", api.installNamedApp("redis"))
			r.Get("/storage/instances", api.storageInstances)
			r.Post("/storage/instances", api.createStorageInstance)
			r.Get("/storage/{id}/buckets", api.storageCollection("buckets"))
			r.Post("/storage/{id}/buckets", api.createStorageItem("bucket"))
			r.Delete("/storage/{id}/buckets/{itemId}", api.deleteStorageItem("bucket"))
			r.Get("/storage/{id}/objects", api.storageCollection("objects"))
			r.Post("/storage/{id}/objects", api.createStorageItem("object"))
			r.Delete("/storage/{id}/objects/{itemId}", api.deleteStorageItem("object"))
			r.Get("/storage/{id}/users", api.storageCollection("users"))
			r.Post("/storage/{id}/users", api.createStorageItem("user"))
			r.Delete("/storage/{id}/users/{itemId}", api.deleteStorageItem("user"))
			r.Get("/storage/{id}/access-keys", api.storageCollection("accessKeys"))
			r.Post("/storage/{id}/access-keys", api.createStorageItem("accessKey"))
			r.Delete("/storage/{id}/access-keys/{itemId}", api.deleteStorageItem("accessKey"))
			r.Get("/storage/{id}/replicas", api.storageCollection("replicas"))
			r.Post("/storage/{id}/replicas", api.createStorageItem("replica"))
			r.Delete("/storage/{id}/replicas/{itemId}", api.deleteStorageItem("replica"))
		})
	})
	r.NotFound(api.staticFallback)
	api.router = r
	return api
}

func (a *API) Router() http.Handler { return a.router }

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, err := a.store.UserByUsername(req.Username)
	if err != nil || auth.CheckPassword(u.PasswordHash, req.Password) != nil {
		writeError(w, http.StatusUnauthorized, "AUTH_FAILED", i18n.Text(lang, "api.authFailed"), nil)
		return
	}
	token, err := auth.IssueToken(a.cfg.JWTSecret, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TOKEN_ERROR", err.Error(), nil)
		return
	}
	_ = a.store.AddAudit(u.Username, "auth.login", u.ID, "success", "login")
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": map[string]any{"username": u.Username, "role": u.Role}})
}

func (a *API) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		claims, err := auth.ParseToken(a.cfg.JWTSecret, token)
		if token == "" || err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", i18n.Text(languageFromRequest(r), "api.authRequired"), nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxClaims{}, claims)))
	})
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"language":              a.store.GetSetting("language", "zh"),
		"deploymentConcurrency": a.store.GetSetting("deploymentConcurrency", fmt.Sprintf("%d", a.cfg.DeploymentConcurrency)),
		"providerStatus":        "real",
		"providerMode":          a.cfg.ProviderMode,
		"databasePath":          a.cfg.DatabasePath,
		"resourcePath":          a.cfg.ResourceDir,
		"staticPath":            a.cfg.StaticDir,
		"defaultDeployDir":      a.cfg.DefaultDeployDir,
		"defaultPassword":       a.cfg.DefaultPassword,
		"moduleStatus": map[string]string{
			"servers": "available", "apps": "available", "containers": "available",
			"database": "available", "storage": "available", "terminal": "available",
			"audit": "available", "settings": "available",
		},
	})
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req map[string]any
	if !decode(w, r, &req) {
		return
	}
	for _, key := range []string{"language", "deploymentConcurrency"} {
		if value, ok := req[key]; ok {
			_ = a.store.SetSetting(key, fmt.Sprint(value))
		}
	}
	a.audit(r, "settings.update", "panel", "success", i18n.Text(lang, "api.settingsUpdated"))
	a.getSettings(w, r)
}

func (a *API) listResources(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListResources()
	respond(w, out, err)
}

func (a *API) rescanResources(w http.ResponseWriter, r *http.Request) {
	if err := resource.ScanAndSave(a.store, a.cfg.ResourceDir); err != nil {
		writeError(w, http.StatusInternalServerError, "RESOURCE_SCAN_FAILED", err.Error(), nil)
		return
	}
	a.audit(r, "resources.scan", a.cfg.ResourceDir, "success", i18n.Text(languageFromRequest(r), "api.resourceScanCompleted"))
	a.listResources(w, r)
}

func (a *API) listServers(w http.ResponseWriter, r *http.Request) {
	out, err := a.servers.List()
	respond(w, out, err)
}

type serverSaveRequest struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Host       string  `json:"host"`
	Port       int     `json:"port"`
	Username   string  `json:"username"`
	AuthType   string  `json:"authType"`
	Password   string  `json:"password"`
	PrivateKey string  `json:"privateKey"`
	Tags       string  `json:"tags"`
	Note       string  `json:"note"`
	DeployDir  string  `json:"deployDir"`
	DockerHost *string `json:"dockerHost"`
	Status     string  `json:"status"`
	LastError  string  `json:"lastError"`
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

func (a *API) serverTelemetry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	writeJSON(w, http.StatusOK, map[string]any{
		"serverId": id, "cpu": 0, "memory": 0, "disk": 0, "load": []float64{0, 0, 0},
		"sampledAt": time.Now(),
	})
}

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListTasks()
	respond(w, out, err)
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
	respond(w, map[string]any{"task": task, "logs": logs, "targets": targets, "steps": steps}, nil)
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

type deleteTasksRequest struct {
	IDs []string `json:"ids"`
}

func (a *API) deleteTasks(w http.ResponseWriter, r *http.Request) {
	var req deleteTasksRequest
	if !decode(w, r, &req) {
		return
	}
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
		a.audit(r, "tasks.logs.clear", id, "success", "task logs cleared")
	}
	respond(w, map[string]any{"taskId": id, "cleared": err == nil}, err)
}

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListAuditPage(store.AuditQuery{
		Page:     queryInt(r, "page", 1),
		PageSize: queryInt(r, "pageSize", 20),
		Module:   strings.TrimSpace(r.URL.Query().Get("module")),
		Status:   strings.TrimSpace(r.URL.Query().Get("status")),
	})
	respond(w, out, err)
}

type deleteAuditRequest struct {
	IDs []int64 `json:"ids"`
}

func (a *API) deleteAudit(w http.ResponseWriter, r *http.Request) {
	var req deleteAuditRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "IDS_REQUIRED", i18n.Text(languageFromRequest(r), "api.idsRequired"), nil)
		return
	}
	deleted, err := a.store.DeleteAuditLogs(req.IDs)
	if err == nil {
		a.audit(r, "audit.delete.batch", strconv.Itoa(deleted), "success", i18n.Text(languageFromRequest(r), "api.auditDeleted", deleted))
	}
	respond(w, map[string]any{"deleted": deleted}, err)
}

func (a *API) appsCatalog(w http.ResponseWriter, r *http.Request) {
	resources, err := a.store.ListResources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RESOURCE_LIST_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, appcatalog.BuildWithModules(resources, a.apps.Modules(), languageFromRequest(r)))
}

func (a *API) appInstances(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListAppInstances()
	respond(w, out, err)
}

type deleteAppInstanceRequest struct {
	ServerPassword string `json:"serverPassword"`
	Password       string `json:"password"`
	Language       string `json:"language"`
}

func (a *API) deleteAppInstance(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req deleteAppInstanceRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Language) != "" {
		lang = req.Language
	}
	password := strings.TrimSpace(req.ServerPassword)
	if password == "" {
		password = strings.TrimSpace(req.Password)
	}
	id := chi.URLParam(r, "id")
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "INSTANCE_SERVER_REQUIRED", i18n.Text(lang, "api.instanceServerRequired"), map[string]any{"instanceId": id})
		return
	}
	server, err := a.store.GetServer(instance.ServerID, true)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if strings.TrimSpace(server.Password) == "" {
		writeError(w, http.StatusConflict, "SERVER_PASSWORD_NOT_CONFIGURED", i18n.Text(lang, "api.serverPasswordNotConfigured"), map[string]any{"serverId": server.ID})
		return
	}
	if password == "" {
		writeError(w, http.StatusBadRequest, "SERVER_PASSWORD_REQUIRED", i18n.Text(lang, "api.serverPasswordRequired"), nil)
		return
	}
	if password != strings.TrimSpace(server.Password) {
		writeError(w, http.StatusForbidden, "SERVER_PASSWORD_INVALID", i18n.Text(lang, "api.serverPasswordInvalid"), nil)
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	deleteModule, ok := module.(registry.DeleteModule)
	if !ok {
		writeError(w, http.StatusConflict, "APP_DELETE_UNSUPPORTED", i18n.Text(lang, "api.appDeleteUnsupported"), map[string]any{"app": instance.App})
		return
	}

	actor := currentUser(r).Username
	target := instance.ServerID
	task, err := a.tasks.StartWithLanguage("apps."+instance.App+".delete", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		deleteReq := registry.DeleteRequest{
			Instance: instance,
			Server:   server,
			Language: lang,
			Actor:    actor,
			Parameters: map[string]any{
				registry.DeleteParamConfirmedWithServerPassword: true,
			},
		}
		plan, err := deleteModule.PlanDelete(ctx, deleteReq)
		if err != nil {
			return err
		}
		plannedTargets := map[string]bool{}
		for _, step := range plan {
			if step.Target != "" && !plannedTargets[step.Target] {
				log.PlanTarget(step.Target)
				plannedTargets[step.Target] = true
			}
			log.PlanStep(step.Target, step.Name, step.Title, step.Order)
		}
		log.Info(i18n.Text(lang, "api.deleteInstanceRequested"), instance.App, instance.ID)
		if err := deleteModule.Delete(ctx, deleteReq, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		}); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.deleteInstanceCompleted"), instance.App, instance.ID)
		return nil
	})
	if err == nil {
		a.audit(r, "apps."+instance.App+".delete", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) checkAppInstance(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	id := chi.URLParam(r, "id")
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "INSTANCE_SERVER_REQUIRED", i18n.Text(lang, "api.instanceServerRequired"), map[string]any{"instanceId": id})
		return
	}
	server, err := a.store.GetServer(instance.ServerID, true)
	if err != nil {
		respond(w, nil, err)
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	checkModule, ok := module.(registry.CheckModule)
	if !ok {
		writeError(w, http.StatusConflict, "APP_CHECK_UNSUPPORTED", i18n.Text(lang, "api.appCheckUnsupported"), map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	target := instance.ServerID
	task, err := a.tasks.StartWithLanguage("apps."+instance.App+".check", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		checkReq := registry.CheckRequest{
			Instance: instance,
			Server:   server,
			Language: lang,
			Actor:    actor,
		}
		plan, err := checkModule.PlanCheck(ctx, checkReq)
		if err != nil {
			return err
		}
		plannedTargets := map[string]bool{}
		for _, step := range plan {
			if step.Target != "" && !plannedTargets[step.Target] {
				log.PlanTarget(step.Target)
				plannedTargets[step.Target] = true
			}
			log.PlanStep(step.Target, step.Name, step.Title, step.Order)
		}
		log.Info(i18n.Text(lang, "api.checkInstanceRequested"), instance.App, instance.ID)
		status, err := checkModule.Check(ctx, checkReq, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
		if err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.checkInstanceCompleted"), instance.App, instance.ID, status.Status)
		return nil
	})
	if err == nil {
		a.audit(r, "apps."+instance.App+".check", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) installApp(w http.ResponseWriter, r *http.Request) {
	a.installAppName(w, r, chi.URLParam(r, "app"))
}

func (a *API) installNamedApp(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.installAppName(w, r, name)
	}
}

type installAppRequest struct {
	Version    string         `json:"version"`
	ServerID   string         `json:"serverId"`
	ServerIDs  []string       `json:"serverIds"`
	Topology   string         `json:"topology"`
	Language   string         `json:"language"`
	Parameters map[string]any `json:"-"`
}

func (a *API) installAppName(w http.ResponseWriter, r *http.Request, app string) {
	req := decodeInstallAppRequest(r)
	if req.Version == "" {
		req.Version = "latest"
	}
	lang := req.Language
	if lang == "" {
		lang = languageFromRequest(r)
	}
	module, ok := a.apps.Get(app)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": app})
		return
	}
	manifest := module.Manifest(lang)
	def := appcatalog.DefinitionFromManifest(manifest)
	serverIDs := installTargetServerIDs(req)
	if def.RequiresServer && len(serverIDs) == 0 {
		writeError(w, http.StatusBadRequest, "TARGET_SERVER_REQUIRED", i18n.Text(lang, "api.targetServerRequired"), map[string]any{"app": def.Name})
		return
	}
	if !manifest.AllowsMultiTargetFor(req.Topology) && len(serverIDs) > 1 {
		writeError(w, http.StatusBadRequest, "MULTI_TARGET_UNSUPPORTED", i18n.Text(lang, "api.multiTargetUnsupported"), map[string]any{"app": def.Name})
		return
	}
	target := strings.Join(serverIDs, ",")
	actor := currentUser(r).Username
	task, err := a.tasks.StartWithLanguage("apps."+def.Name+".install", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info(i18n.Text(lang, "api.installAccepted"), def.Name, req.Version)
		log.Info(i18n.Text(lang, "api.installTargets"), target)
		log.Info(i18n.Text(lang, "api.resourceRoot"), a.cfg.ResourceDir)
		resources, _ := a.store.ListResources()
		missing := appcatalog.MissingForInstall(def, resources, req.Version)
		if len(missing) > 0 {
			return fmt.Errorf(i18n.Text(lang, "api.appNotDeployable"), def.Name, strings.Join(missing, ", "))
		}
		_, matched := appcatalog.ResolveResources(def, resources, req.Version)
		for _, res := range matched {
			log.Info(i18n.Text(lang, "api.resourceFound"), res.Part, res.Path)
		}
		moduleReq := registry.InstallRequest{
			App:             def.Name,
			Version:         req.Version,
			Topology:        req.Topology,
			Language:        lang,
			ServerID:        req.ServerID,
			ServerIDs:       req.ServerIDs,
			Actor:           actor,
			DefaultPassword: a.cfg.DefaultPassword,
			Parameters:      req.Parameters,
		}
		preflight, err := module.PreflightInstall(ctx, moduleReq, resources)
		if err != nil {
			return err
		}
		for _, warning := range preflight.Warnings {
			log.Info(i18n.Text(lang, "api.preflightWarning"), warning)
		}
		plan, err := module.PlanInstall(ctx, moduleReq, resources)
		if err != nil {
			return err
		}
		plannedTargets := map[string]bool{}
		for _, step := range plan {
			if step.Target != "" && !plannedTargets[step.Target] {
				log.PlanTarget(step.Target)
				plannedTargets[step.Target] = true
			}
			log.PlanStep(step.Target, step.Name, step.Title, step.Order)
		}
		if len(plan) > 0 {
			log.Info(i18n.Text(lang, "api.installPlanPrepared"), len(plan))
		}
		if err := module.ValidateInstall(ctx, moduleReq, resources); err != nil {
			return err
		}
		return module.Install(ctx, moduleReq, registry.RunContext{
			Resources: resources,
			Log:       log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "apps."+def.Name+".install", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func installTargetServerIDs(req installAppRequest) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(req.ServerID)
	for _, id := range req.ServerIDs {
		add(id)
	}
	return out
}

func decodeInstallAppRequest(r *http.Request) installAppRequest {
	defer r.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return installAppRequest{Parameters: map[string]any{}}
	}
	req := installAppRequest{
		Version:    stringFromRaw(raw["version"]),
		ServerID:   stringFromRaw(raw["serverId"]),
		ServerIDs:  stringSliceFromRaw(raw["serverIds"]),
		Topology:   stringFromRaw(raw["topology"]),
		Language:   stringFromRaw(raw["language"]),
		Parameters: map[string]any{},
	}
	for _, key := range []string{"version", "serverId", "serverIds", "topology", "language"} {
		delete(raw, key)
	}
	for key, value := range raw {
		req.Parameters[key] = value
	}
	return req
}

func stringFromRaw(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		if value == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func stringSliceFromRaw(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if text := stringFromRaw(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func (a *API) instanceAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		lang := languageFromRequest(r)
		actor := currentUser(r).Username
		task, err := a.tasks.StartWithLanguage("apps.instance."+action, id, actor, lang, func(ctx context.Context, log worker.Logger) error {
			log.Info(i18n.Text(lang, "api.instanceActionRequested"), action, id)
			log.Info("%s", i18n.Text(lang, "api.instanceAdapterReady"))
			return nil
		})
		if err == nil {
			a.audit(r, "apps.instance."+action, id, "running", task.ID)
		}
		respondTask(w, task, err)
	}
}

func (a *API) containerSummary(w http.ResponseWriter, r *http.Request) {
	host := dockerHostFromRequest(r)
	summary, err := adapter.DockerSummaryForHost(r.Context(), host)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error(), "containers": 0, "images": 0, "networks": 0, "volumes": 0})
		return
	}
	df, _ := adapter.DockerSystemDF(r.Context(), host)
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "summary": summary, "diskUsage": df})
}

func (a *API) containers(w http.ResponseWriter, r *http.Request) {
	host := dockerHostFromRequest(r)
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
		out, err = adapter.DockerContainers(r.Context(), host)
	case "images":
		out, err = adapter.DockerImages(r.Context(), host)
	case "networks", "network":
		out, err = adapter.DockerNetworks(r.Context(), host)
	case "volumes", "volume":
		out, err = adapter.DockerVolumes(r.Context(), host)
	case "df", "disk":
		out, err = adapter.DockerSystemDF(r.Context(), host)
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
		task, err := a.tasks.StartWithLanguage("containers.container."+action, id, currentUser(r).Username, lang, func(ctx context.Context, log worker.Logger) error {
			log.Info(i18n.Text(lang, "api.containerActionRequested"), action, id)
			if err := adapter.DockerContainerAction(ctx, host, id, action); err != nil {
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

func (a *API) containerLogs(w http.ResponseWriter, r *http.Request) {
	host := dockerHostFromRequest(r)
	tail := queryInt(r, "tail", 200)
	logs, err := adapter.DockerContainerLogs(r.Context(), host, chi.URLParam(r, "id"), tail)
	respond(w, map[string]any{"logs": logs}, err)
}

func (a *API) databaseInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_LIST_FAILED", err.Error(), nil)
		return
	}
	var out []store.AppInstance
	for _, instance := range instances {
		if instance.App == "mysql" || instance.App == "redis" {
			out = append(out, instance)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) databaseBackup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	lang := languageFromRequest(r)
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if instance.App != "mysql" && instance.App != "redis" {
		writeError(w, http.StatusBadRequest, "DATABASE_INSTANCE_REQUIRED", i18n.Text(lang, "api.databaseInstanceRequired"), map[string]any{"instanceId": id})
		return
	}
	task, err := a.tasks.StartWithLanguage("database.backup", id, currentUser(r).Username, lang, func(ctx context.Context, log worker.Logger) error {
		log.PlanTarget(id)
		log.PlanStep(id, "prepare", i18n.Text(lang, "api.databaseBackupPrepare"), 1)
		log.PlanStep(id, "record", i18n.Text(lang, "api.databaseBackupRecord"), 2)
		log.StartTarget(id)
		log.StartStep(id, "prepare", i18n.Text(lang, "api.databaseBackupPrepare"), 1)
		log.Info("%s", i18n.Text(lang, "api.databaseBackupRequested", instance.App, instance.ID))
		log.FinishStep(id, "prepare", "success", "")
		log.StartStep(id, "record", i18n.Text(lang, "api.databaseBackupRecord"), 2)
		log.Info("%s", i18n.Text(lang, "api.databaseBackupReady"))
		log.FinishStep(id, "record", "success", "")
		log.FinishTarget(id, "success", "")
		return nil
	})
	if err == nil {
		a.audit(r, "database.backup", id, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) storageInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORAGE_LIST_FAILED", err.Error(), nil)
		return
	}
	var out []store.AppInstance
	for _, instance := range instances {
		if instance.App == "minio" {
			out = append(out, instance)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createStorageInstance(w http.ResponseWriter, r *http.Request) {
	a.installAppName(w, r, "minio")
}

func (a *API) storageCollection(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		kind := storageKind(name)
		if !a.ensureStorageInstance(w, r, id) {
			return
		}
		items, err := a.store.ListStorageItems(id, kind)
		respond(w, map[string]any{"instanceId": id, "kind": kind, "items": items, name: items}, err)
	}
}

func (a *API) createStorageItem(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if !a.ensureStorageInstance(w, r, id) {
			return
		}
		var req struct {
			Name      string         `json:"name"`
			Policy    string         `json:"policy"`
			AccessKey string         `json:"accessKey"`
			SecretKey string         `json:"secretKey"`
			Metadata  map[string]any `json:"metadata"`
		}
		if !decode(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "NAME_REQUIRED", i18n.Text(languageFromRequest(r), "api.nameRequired"), nil)
			return
		}
		metadata := ""
		if len(req.Metadata) > 0 {
			if raw, err := json.Marshal(req.Metadata); err == nil {
				metadata = string(raw)
			}
		}
		item, err := a.store.SaveStorageItem(store.StorageItem{
			InstanceID: id,
			Kind:       kind,
			Name:       strings.TrimSpace(req.Name),
			Policy:     strings.TrimSpace(req.Policy),
			AccessKey:  strings.TrimSpace(req.AccessKey),
			SecretKey:  strings.TrimSpace(req.SecretKey),
			Metadata:   metadata,
		})
		if err == nil {
			a.audit(r, "storage."+kind+".save", id, "success", item.Name)
		}
		respond(w, item, err)
	}
}

func (a *API) deleteStorageItem(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		itemID := chi.URLParam(r, "itemId")
		if !a.ensureStorageInstance(w, r, id) {
			return
		}
		err := a.store.DeleteStorageItem(id, kind, itemID)
		if err == nil {
			a.audit(r, "storage."+kind+".delete", id, "success", itemID)
		}
		respond(w, map[string]any{"deleted": itemID}, err)
	}
}

func (a *API) ensureStorageInstance(w http.ResponseWriter, r *http.Request, id string) bool {
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return false
	}
	if instance.App != "minio" {
		writeError(w, http.StatusBadRequest, "STORAGE_INSTANCE_REQUIRED", i18n.Text(languageFromRequest(r), "api.storageInstanceRequired"), map[string]any{"instanceId": id})
		return false
	}
	return true
}

func storageKind(name string) string {
	switch name {
	case "buckets":
		return "bucket"
	case "users":
		return "user"
	case "accessKeys":
		return "accessKey"
	case "objects":
		return "object"
	case "replicas":
		return "replica"
	default:
		return name
	}
}

func dockerHostFromRequest(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("dockerHost"))
}

func (a *API) serverTerminal(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	if _, err := auth.ParseToken(a.cfg.JWTSecret, tokenFromWS(r)); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", i18n.Text(lang, "api.wsAuthRequired"), nil)
		return
	}
	serverID := chi.URLParam(r, "id")
	server, err := a.store.GetServer(serverID, true)
	if err != nil {
		code := http.StatusInternalServerError
		if store.IsNotFound(err) {
			code = http.StatusNotFound
		}
		writeError(w, code, "SERVER_NOT_FOUND", err.Error(), nil)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }, Subprotocols: []string{"aifar.terminal"}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	target := server.Name
	if target == "" {
		target = server.Host
	}
	if err := a.sshTerminalWS(r.Context(), conn, server, target, lang); err != nil {
		var writeMu sync.Mutex
		writeTerminalLine(conn, &writeMu, "[error] "+err.Error())
	}
}

func (a *API) containerTerminal(w http.ResponseWriter, r *http.Request) {
	a.terminalWS(w, r, "container "+chi.URLParam(r, "id"))
}

func (a *API) terminalWS(w http.ResponseWriter, r *http.Request, target string) {
	if _, err := auth.ParseToken(a.cfg.JWTSecret, tokenFromWS(r)); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", i18n.Text(languageFromRequest(r), "api.wsAuthRequired"), nil)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }, Subprotocols: []string{"aifar.terminal"}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(i18n.Text(languageFromRequest(r), "api.connectedToTarget", target)+"\r\n"))
	for {
		typ, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if typ == websocket.TextMessage || typ == websocket.BinaryMessage {
			_ = conn.WriteMessage(websocket.TextMessage, append([]byte("$ "), msg...))
		}
	}
}

func (a *API) sshTerminalWS(ctx context.Context, conn *websocket.Conn, server store.Server, target, lang string) error {
	writeMu := &sync.Mutex{}
	client, err := adapter.DialSSH(ctx, server)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", 40, 120, modes); err != nil {
		return err
	}
	if err := session.Shell(); err != nil {
		return err
	}
	writeTerminalLine(conn, writeMu, i18n.Text(lang, "api.connectedToTarget", target))

	streamDone := make(chan error, 3)
	go streamTerminalOutput(conn, writeMu, stdout, streamDone)
	go streamTerminalOutput(conn, writeMu, stderr, streamDone)
	go func() {
		streamDone <- session.Wait()
	}()

	readDone := make(chan error, 1)
	go func() {
		for {
			typ, msg, err := conn.ReadMessage()
			if err != nil {
				readDone <- err
				return
			}
			if typ == websocket.TextMessage || typ == websocket.BinaryMessage {
				if _, err := stdin.Write(msg); err != nil {
					readDone <- err
					return
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
		_ = stdin.Close()
		_ = session.Close()
		return nil
	case err := <-readDone:
		_ = stdin.Close()
		_ = session.Close()
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
			return nil
		}
		return err
	case err := <-streamDone:
		_ = stdin.Close()
		_ = conn.Close()
		if err == io.EOF {
			return nil
		}
		return err
	}
}

func streamTerminalOutput(conn *websocket.Conn, writeMu *sync.Mutex, r io.Reader, done chan<- error) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			writeMu.Lock()
			writeErr := conn.WriteMessage(websocket.BinaryMessage, chunk)
			writeMu.Unlock()
			if writeErr != nil {
				done <- writeErr
				return
			}
		}
		if err != nil {
			if err == io.EOF {
				done <- nil
				return
			}
			done <- err
			return
		}
	}
}

func writeTerminalLine(conn *websocket.Conn, writeMu *sync.Mutex, line string) {
	writeMu.Lock()
	defer writeMu.Unlock()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(line+"\r\n"))
}

func (a *API) staticFallback(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", i18n.Text(languageFromRequest(r), "api.routeNotFound"), nil)
		return
	}
	path := filepath.Join(a.cfg.StaticDir, filepath.Clean(r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(a.cfg.StaticDir, "index.html"))
}

func (a *API) audit(r *http.Request, action, target, status, message string) {
	_ = a.store.AddAudit(currentUser(r).Username, action, target, status, message)
}

type ctxClaims struct{}

func currentUser(r *http.Request) auth.Claims {
	claims, _ := r.Context().Value(ctxClaims{}).(auth.Claims)
	return claims
}

func languageFromRequest(r *http.Request) string {
	if lang := strings.TrimSpace(r.URL.Query().Get("lang")); lang != "" {
		return lang
	}
	if lang := strings.TrimSpace(r.Header.Get("X-AIFAR-Language")); lang != "" {
		return lang
	}
	return r.Header.Get("Accept-Language")
}

func queryInt(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	return ""
}

func tokenFromWS(r *http.Request) string {
	if token := bearerToken(r); token != "" {
		return token
	}
	for _, proto := range websocket.Subprotocols(r) {
		if strings.HasPrefix(proto, "aifar.auth.") {
			raw := strings.TrimPrefix(proto, "aifar.auth.")
			data, err := base64.RawURLEncoding.DecodeString(raw)
			if err == nil {
				return string(data)
			}
		}
	}
	return r.URL.Query().Get("token")
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", i18n.Text(languageFromRequest(r), "api.invalidJSON"), map[string]any{"error": err.Error()})
		return false
	}
	return true
}

func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		if store.IsNotFound(err) {
			code = http.StatusNotFound
		}
		writeError(w, code, "REQUEST_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func respondTask(w http.ResponseWriter, task store.Task, err error) {
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TASK_START_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": task.ID, "status": task.Status})
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, code int, errCode, message string, details any) {
	writeJSON(w, code, map[string]any{"code": errCode, "message": message, "details": details})
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
