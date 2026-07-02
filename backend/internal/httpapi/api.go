package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "aifar-deployment/backend/internal/apps/autoload"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/auditkit"
	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/security"
	serverdomain "aifar-deployment/backend/internal/servers"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type API struct {
	cfg     config.Config
	store   *store.Store
	tasks   *worker.Manager
	apps    *registry.Registry
	servers serverdomain.Service
	auth    *security.LoginGuard
	router  chi.Router
}

func New(cfg config.Config, s *store.Store, tasks *worker.Manager) *API {
	api := &API{
		cfg:     cfg,
		store:   s,
		tasks:   tasks,
		apps:    registry.NewFromRegistered(registry.Dependencies{Store: s, DefaultPassword: cfg.DefaultPassword}),
		servers: serverdomain.NewService(s, serverdomain.SSHProber{}, cfg.DefaultDeployDir),
		auth:    security.NewLoginGuard(cfg.AuthMaxFailures, time.Duration(cfg.AuthLockoutSeconds)*time.Second),
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer, api.securityHeaders, api.limitRequestBody)
	r.Route("/api/v2", func(r chi.Router) {
		r.Get("/health/live", api.healthLive)
		r.Get("/health/ready", api.healthReady)
		r.Post("/auth/login", api.login)
		r.Group(func(r chi.Router) {
			r.Use(api.requireAuth)
			r.Get("/health", api.healthDetail)
			r.Get("/settings", api.getSettings)
			r.Put("/settings", api.requirePermission(rbac.SettingsManage, api.putSettings))
			r.Get("/users", api.requirePermission(rbac.UsersManage, api.listUsers))
			r.Post("/users", api.requirePermission(rbac.UsersManage, api.createUser))
			r.Put("/users/{username}/role", api.requirePermission(rbac.UsersManage, api.updateUserRole))
			r.Put("/users/{username}/password", api.requirePermission(rbac.UsersManage, api.resetUserPassword))
			r.Get("/maintenance/database-backups", api.requirePermission(rbac.SettingsManage, api.listDatabaseBackups))
			r.Get("/maintenance/database-backups/{name}/download", api.requirePermission(rbac.SettingsManage, api.downloadDatabaseBackup))
			r.Post("/maintenance/database-backups/{name}/verify", api.requirePermission(rbac.SettingsManage, api.verifyDatabaseBackup))
			r.Delete("/maintenance/database-backups", api.requirePermission(rbac.SettingsManage, api.deleteDatabaseBackups))
			r.Post("/maintenance/database-backup/run", api.requirePermission(rbac.SettingsManage, api.runDatabaseBackup))
			r.Post("/maintenance/retention/run", api.requirePermission(rbac.SettingsManage, api.runRetentionCleanup))
			r.Get("/resources", api.listResources)
			r.Post("/resources/rescan", api.requirePermission(rbac.ResourcesScan, api.rescanResources))
			r.Get("/servers", api.listServers)
			r.Post("/servers", api.requirePermission(rbac.ServersManage, api.saveServer))
			r.Put("/servers/order", api.requirePermission(rbac.ServersManage, api.reorderServers))
			r.Put("/servers/{id}", api.requirePermission(rbac.ServersManage, api.saveServer))
			r.Delete("/servers/{id}", api.requirePermission(rbac.ServersManage, api.deleteServer))
			r.Post("/servers/{id}/probe", api.requirePermission(rbac.ServersManage, api.probeServer))
			r.Get("/servers/{id}/disks", api.requirePermission(rbac.AppsManage, api.serverDisks))
			r.Get("/servers/{id}/telemetry", api.serverTelemetry)
			r.Get("/servers/{id}/terminal/ws", api.requirePermission(rbac.TerminalConnect, api.serverTerminal))
			r.Get("/tasks", api.listTasks)
			r.Get("/tasks/{id}", api.getTask)
			r.Get("/tasks/{id}/events", api.taskEvents)
			r.Post("/tasks/{id}/cancel", api.requirePermission(rbac.TasksManage, api.cancelTask))
			r.Delete("/tasks/logs", api.requirePermission(rbac.TasksManage, api.clearTaskLogsBatch))
			r.Delete("/tasks", api.requirePermission(rbac.TasksManage, api.deleteTasks))
			r.Delete("/tasks/{id}", api.requirePermission(rbac.TasksManage, api.deleteTask))
			r.Delete("/tasks/{id}/logs", api.requirePermission(rbac.TasksManage, api.clearTaskLogs))
			r.Get("/audit", api.listAudit)
			r.Delete("/audit", api.requirePermission(rbac.AuditManage, api.deleteAudit))
			r.Get("/apps/catalog", api.appsCatalog)
			r.Get("/apps/instances", api.appInstances)
			r.Post("/apps/{app}/install", api.requirePermission(rbac.AppsManage, api.installApp))
			r.Post("/apps/instances/batch-delete", api.requirePermission(rbac.AppsManage, api.deleteAppInstances))
			r.Post("/apps/instances/{id}/check", api.requirePermission(rbac.AppsManage, api.checkAppInstance))
			r.Post("/apps/instances/{id}/delete", api.requirePermission(rbac.AppsManage, api.deleteAppInstance))
			r.Post("/apps/instances/{id}/uninstall", api.requirePermission(rbac.AppsManage, api.deleteAppInstance))
			r.Get("/credentials", api.requirePermission(rbac.CredentialsUse, api.listCredentials))
			r.Post("/credentials", api.requirePermission(rbac.CredentialsManage, api.saveCredential))
			r.Get("/credentials/{id}", api.requirePermission(rbac.CredentialsUse, api.getCredential))
			r.Put("/credentials/{id}", api.requirePermission(rbac.CredentialsManage, api.saveCredential))
			r.Delete("/credentials/{id}", api.requirePermission(rbac.CredentialsManage, api.deleteCredential))
			r.Get("/containers/summary", api.containerSummary)
			r.Get("/containers", api.containers)
			r.Post("/containers/actions", api.requirePermission(rbac.ContainersManage, api.containerBatchAction))
			r.Post("/containers/{id}/start", api.requirePermission(rbac.ContainersManage, api.containerAction("start")))
			r.Post("/containers/{id}/stop", api.requirePermission(rbac.ContainersManage, api.containerAction("stop")))
			r.Post("/containers/{id}/restart", api.requirePermission(rbac.ContainersManage, api.containerAction("restart")))
			r.Post("/containers/{id}/remove", api.requirePermission(rbac.ContainersManage, api.containerAction("remove")))
			r.Get("/containers/{id}/logs", api.containerLogs)
			r.Get("/database/instances", api.databaseInstances)
			r.Post("/database/mysql/install", api.requirePermission(rbac.DatabaseManage, api.installNamedApp("mysql")))
			r.Post("/database/mysql/clusters/start", api.requirePermission(rbac.DatabaseManage, api.startMySQLCluster))
			r.Post("/database/redis/install", api.requirePermission(rbac.DatabaseManage, api.installNamedApp("redis")))
			r.Get("/nacos/instances", api.nacosInstances)
			r.Get("/storage/instances", api.storageInstances)
			r.Post("/storage/instances", api.requirePermission(rbac.StorageManage, api.createStorageInstance))
			r.Get("/storage/{id}/buckets", api.storageCollection("buckets"))
			r.Post("/storage/{id}/buckets", api.requirePermission(rbac.StorageManage, api.createStorageItem("bucket")))
			r.Delete("/storage/{id}/buckets/{itemId}", api.requirePermission(rbac.StorageManage, api.deleteStorageItem("bucket")))
			r.Get("/storage/{id}/objects", api.storageCollection("objects"))
			r.Post("/storage/{id}/objects", api.requirePermission(rbac.StorageManage, api.createStorageItem("object")))
			r.Delete("/storage/{id}/objects/{itemId}", api.requirePermission(rbac.StorageManage, api.deleteStorageItem("object")))
			r.Get("/storage/{id}/users", api.storageCollection("users"))
			r.Post("/storage/{id}/users", api.requirePermission(rbac.StorageManage, api.createStorageItem("user")))
			r.Delete("/storage/{id}/users/{itemId}", api.requirePermission(rbac.StorageManage, api.deleteStorageItem("user")))
			r.Get("/storage/{id}/access-keys", api.storageCollection("accessKeys"))
			r.Post("/storage/{id}/access-keys", api.requirePermission(rbac.StorageManage, api.createStorageItem("accessKey")))
			r.Delete("/storage/{id}/access-keys/{itemId}", api.requirePermission(rbac.StorageManage, api.deleteStorageItem("accessKey")))
			r.Get("/storage/{id}/replicas", api.storageCollection("replicas"))
			r.Post("/storage/{id}/replicas", api.requirePermission(rbac.StorageManage, api.createStorageItem("replica")))
			r.Delete("/storage/{id}/replicas/{itemId}", api.requirePermission(rbac.StorageManage, api.deleteStorageItem("replica")))
		})
	})
	r.NotFound(api.staticFallback)
	api.router = r
	return api
}

func (a *API) Router() http.Handler { return a.router }

type deleteDatabaseBackupsRequest struct {
	Names []string `json:"names"`
}

type reorderServersRequest struct {
	IDs []string `json:"ids"`
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
	SortOrder  int     `json:"sortOrder"`
}

type deleteTasksRequest struct {
	IDs []string `json:"ids"`
}

type deleteAuditRequest struct {
	IDs []int64 `json:"ids"`
}

type deleteAppInstanceRequest struct {
	ServerPassword     string         `json:"serverPassword"`
	Password           string         `json:"password"`
	Language           string         `json:"language"`
	Parameters         map[string]any `json:"parameters"`
	RemoveMountedDisks *bool          `json:"removeMountedDisks"`
}

type deleteAppInstancesRequest struct {
	InstanceIDs        []string          `json:"instanceIds"`
	ServerPasswords    map[string]string `json:"serverPasswords"`
	Passwords          map[string]string `json:"passwords"`
	Language           string            `json:"language"`
	Parameters         map[string]any    `json:"parameters"`
	RemoveMountedDisks *bool             `json:"removeMountedDisks"`
}

type containerBatchActionRequest struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids"`
}

type credentialSaveRequest struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Username      string            `json:"username"`
	Endpoint      string            `json:"endpoint"`
	Scope         string            `json:"scope"`
	Status        string            `json:"status"`
	App           string            `json:"app"`
	ServerID      string            `json:"serverId"`
	AppInstanceID string            `json:"appInstanceId"`
	Purpose       string            `json:"purpose"`
	Tags          string            `json:"tags"`
	Secret        map[string]string `json:"secret"`
	Password      string            `json:"password"`
	SecretKey     string            `json:"secretKey"`
	Token         string            `json:"token"`
	PrivateKey    string            `json:"privateKey"`
}

type startMySQLClusterRequest struct {
	InstanceIDs []string `json:"instanceIds"`
	Language    string   `json:"language"`
}

type installAppRequest struct {
	Version    string         `json:"version"`
	ServerID   string         `json:"serverId"`
	ServerIDs  []string       `json:"serverIds"`
	Topology   string         `json:"topology"`
	Language   string         `json:"language"`
	Parameters map[string]any `json:"-"`
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
	_ = auditkit.Record(a.store, auditkit.Event{
		Actor:   currentUser(r).Username,
		Action:  action,
		Target:  target,
		Status:  status,
		Message: message,
	})
}
