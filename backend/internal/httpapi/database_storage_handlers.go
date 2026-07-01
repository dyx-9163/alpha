package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

func (a *API) databaseInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_LIST_FAILED", err.Error(), nil)
		return
	}
	var out []store.AppInstance
	for _, instance := range instances {
		if instance.App == "mysql" || instance.App == "redis" || instance.App == "mysql-router" {
			out = append(out, instance)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) startMySQLCluster(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req startMySQLClusterRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Language) != "" {
		lang = req.Language
	}
	ids := cleanStringIDs(req.InstanceIDs)
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "IDS_REQUIRED", i18n.Text(lang, "api.idsRequired"), nil)
		return
	}
	module, ok := a.apps.Get("mysql")
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": "mysql"})
		return
	}
	clusterModule, ok := module.(registry.ClusterStartModule)
	if !ok {
		writeError(w, http.StatusConflict, "MYSQL_CLUSTER_START_UNSUPPORTED", i18n.Text(lang, "api.mysqlClusterStartUnsupported"), map[string]any{"app": "mysql"})
		return
	}

	instances := make([]store.AppInstance, 0, len(ids))
	servers := make([]store.Server, 0, len(ids))
	serverSeen := map[string]bool{}
	clusterKey := ""
	for _, id := range ids {
		instance, err := a.store.GetAppInstance(id)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if instance.App != "mysql" || appInstanceTopology(instance) != "innodb-cluster" {
			writeError(w, http.StatusBadRequest, "MYSQL_CLUSTER_REQUIRED", i18n.Text(lang, "api.mysqlClusterRequired"), map[string]any{"instanceId": id})
			return
		}
		key := mysqlClusterKey(instance)
		if clusterKey == "" {
			clusterKey = key
		}
		if key == "" || key != clusterKey {
			writeError(w, http.StatusBadRequest, "MYSQL_CLUSTER_MIXED", i18n.Text(lang, "api.mysqlClusterMixed"), nil)
			return
		}
		if strings.TrimSpace(instance.ServerID) == "" {
			writeError(w, http.StatusBadRequest, "INSTANCE_SERVER_REQUIRED", i18n.Text(lang, "api.instanceServerRequired"), map[string]any{"instanceId": id})
			return
		}
		if !serverSeen[instance.ServerID] {
			server, err := a.store.GetServer(instance.ServerID, true)
			if err != nil {
				respond(w, nil, err)
				return
			}
			servers = append(servers, server)
			serverSeen[instance.ServerID] = true
		}
		instances = append(instances, instance)
	}

	targets := make([]string, 0, len(servers))
	for _, server := range servers {
		targets = append(targets, server.ID)
	}
	target := strings.Join(targets, ",")
	actor := currentUser(r).Username
	clusterReq := registry.ClusterStartRequest{
		Instances:       instances,
		Servers:         servers,
		Language:        lang,
		Actor:           actor,
		DefaultPassword: a.cfg.DefaultPassword,
	}
	task, err := a.tasks.StartWithLanguage("database.mysql.cluster.start", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		plan, err := clusterModule.PlanClusterStart(ctx, clusterReq)
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
		log.Info(i18n.Text(lang, "api.mysqlClusterStartRequested"), target)
		if err := clusterModule.StartCluster(ctx, clusterReq, registry.RunContext{
			Log:         log,
			Concurrency: a.store.DeploymentConcurrency(a.cfg.DeploymentConcurrency),
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		}); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.mysqlClusterStartCompleted"), len(instances))
		return nil
	})
	if err == nil {
		a.audit(r, "database.mysql.cluster.start", target, "running", task.ID)
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

func (a *API) nacosInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "NACOS_LIST_FAILED", err.Error(), nil)
		return
	}
	var out []store.AppInstance
	for _, instance := range instances {
		if instance.App == "nacos" {
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

func appInstanceTopology(instance store.AppInstance) string {
	if value := strings.TrimSpace(instance.Topology); value != "" {
		return strings.ToLower(value)
	}
	return strings.ToLower(appInstanceMetadataValue(instance, "topology"))
}

func mysqlClusterKey(instance store.AppInstance) string {
	if value := appInstanceMetadataValue(instance, "clusterId"); value != "" {
		return "id:" + value
	}
	if value := appInstanceMetadataValue(instance, "clusterName"); value != "" {
		return "name:" + strings.ToLower(value)
	}
	return ""
}

func appInstanceMetadataValue(instance store.AppInstance, key string) string {
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		return ""
	}
	value := strings.TrimSpace(fmt.Sprint(metadata[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}
