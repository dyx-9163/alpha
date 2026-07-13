package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	plan, err := clusterModule.PlanClusterStart(r.Context(), clusterReq)
	if err != nil {
		writeError(w, http.StatusBadRequest, "MYSQL_CLUSTER_START_PLAN_FAILED", err.Error(), map[string]any{"instances": ids})
		return
	}
	taskType := "database.mysql.cluster.start"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "MYSQL_CLUSTER_START_PLAN_STORE_FAILED", err.Error(), map[string]any{"instances": ids})
		return
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, appInstanceOperationLockSpecs("mysql-cluster-start", instances))
	if !ok {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
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
	if err != nil {
		a.releaseOperationLocks(locks)
	}
	if err == nil {
		a.audit(r, taskType, target, "running", task.ID)
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

func (a *API) storageCleanupEstimate(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	id := chi.URLParam(r, "id")
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if instance.App != "minio" {
		writeError(w, http.StatusBadRequest, "STORAGE_INSTANCE_REQUIRED", i18n.Text(lang, "api.storageInstanceRequired"), map[string]any{"instanceId": id})
		return
	}
	if strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "INSTANCE_SERVER_REQUIRED", i18n.Text(lang, "api.instanceServerRequired"), map[string]any{"instanceId": id})
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	estimateModule, ok := module.(registry.StorageCleanupEstimateModule)
	if !ok {
		writeError(w, http.StatusConflict, "STORAGE_CLEANUP_ESTIMATE_UNSUPPORTED", i18n.Text(lang, "api.storageCleanupEstimateUnsupported"), map[string]any{"app": instance.App})
		return
	}
	server, err := a.store.GetServer(instance.ServerID, true)
	if err != nil {
		respond(w, nil, err)
		return
	}
	result, err := estimateModule.EstimateStorageCleanup(r.Context(), registry.StorageCleanupEstimateRequest{
		Instance:      instance,
		Server:        server,
		Language:      lang,
		Actor:         currentUser(r).Username,
		RetentionDays: queryInt(r, "retentionDays", 30),
	}, registry.RunContext{Log: silentHTTPLogger{}})
	respond(w, result, err)
}

type storageCleanupPolicyRequest struct {
	Enabled       *bool  `json:"enabled"`
	Bucket        string `json:"bucket"`
	Prefix        string `json:"prefix"`
	RetentionDays int    `json:"retentionDays"`
}

func (a *API) storageCleanupPolicy(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	id := chi.URLParam(r, "id")
	var req storageCleanupPolicyRequest
	if !decode(w, r, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	rawBucket := strings.TrimSpace(req.Bucket)
	bucket := normalizeStorageCleanupPolicyBucket(req.Bucket)
	if rawBucket != "" && bucket == "" {
		writeError(w, http.StatusBadRequest, "INVALID_STORAGE_CLEANUP_BUCKET", i18n.Text(lang, "api.invalidStorageCleanupBucket"), map[string]any{"bucket": req.Bucket})
		return
	}
	if bucket == "" {
		bucket = "aifar"
	}
	if invalidStorageCleanupPolicyPrefix(req.Prefix) {
		writeError(w, http.StatusBadRequest, "INVALID_STORAGE_CLEANUP_PREFIX", i18n.Text(lang, "api.invalidStorageCleanupPrefix"), map[string]any{"prefix": req.Prefix})
		return
	}
	prefix := normalizeStorageCleanupPolicyPrefix(req.Prefix)
	retentionDays := normalizeStorageCleanupPolicyRetentionDays(req.RetentionDays)
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if instance.App != "minio" {
		writeError(w, http.StatusBadRequest, "STORAGE_INSTANCE_REQUIRED", i18n.Text(lang, "api.storageInstanceRequired"), map[string]any{"instanceId": id})
		return
	}
	if strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "INSTANCE_SERVER_REQUIRED", i18n.Text(lang, "api.instanceServerRequired"), map[string]any{"instanceId": id})
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	policyModule, ok := module.(registry.StorageCleanupPolicyModule)
	if !ok {
		writeError(w, http.StatusConflict, "STORAGE_CLEANUP_POLICY_UNSUPPORTED", i18n.Text(lang, "api.storageCleanupPolicyUnsupported"), map[string]any{"app": instance.App})
		return
	}
	server, err := a.store.GetServer(instance.ServerID, true)
	if err != nil {
		respond(w, nil, err)
		return
	}
	actor := currentUser(r).Username
	policyName := storageCleanupPolicyName(bucket, prefix)
	existingRuleID := a.existingStorageCleanupPolicyRuleID(instance, bucket, prefix)
	taskType := "storage.minio.cleanup-policy.apply"
	if !enabled {
		taskType = "storage.minio.cleanup-policy.disable"
	}
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: instance.ID, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeTaskPlanOrDelete(task.ID, simpleTaskPlan(server.ID, storageCleanupPolicySteps(lang))); err != nil {
		writeError(w, http.StatusInternalServerError, "STORAGE_CLEANUP_POLICY_PLAN_STORE_FAILED", err.Error(), map[string]any{"instanceId": instance.ID})
		return
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, appInstanceOperationLockSpecs("storage-cleanup-policy", []store.AppInstance{instance}))
	if !ok {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		currentServer, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.storageCleanupPolicyRequested"), policyName, retentionDays)
		result, err := policyModule.ApplyStorageCleanupPolicy(ctx, registry.StorageCleanupPolicyRequest{
			Instance:       current,
			Server:         currentServer,
			Language:       lang,
			Actor:          actor,
			Enabled:        enabled,
			Bucket:         bucket,
			Prefix:         prefix,
			RetentionDays:  retentionDays,
			ExistingRuleID: existingRuleID,
		}, registry.RunContext{
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
		if err != nil {
			return err
		}
		if err := a.recordStorageCleanupPolicy(current, result, task.ID); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.storageCleanupPolicyCompleted"), result.Bucket, result.RetentionDays, result.Status)
		return nil
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	}
	if err == nil {
		a.audit(r, taskType, instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
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
	case "cleanupPolicies":
		return "cleanupPolicy"
	default:
		return name
	}
}

func storageCleanupPolicySteps(lang string) []simpleTaskStep {
	return []simpleTaskStep{
		{name: "apply-cleanup-policy", title: i18n.Text(lang, "storage.cleanupPolicy.stepApply")},
		{name: "record-cleanup-policy", title: i18n.Text(lang, "storage.cleanupPolicy.stepRecord")},
	}
}

func normalizeStorageCleanupPolicyRetentionDays(days int) int {
	if days <= 0 {
		return 30
	}
	if days > 3650 {
		return 3650
	}
	return days
}

func normalizeStorageCleanupPolicyBucket(bucket string) string {
	bucket = strings.ToLower(strings.TrimSpace(bucket))
	if bucket == "" {
		return ""
	}
	if strings.ContainsAny(bucket, `/\`) || strings.IndexFunc(bucket, func(r rune) bool { return r <= ' ' }) >= 0 {
		return ""
	}
	return bucket
}

func normalizeStorageCleanupPolicyPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.TrimLeft(prefix, "/")
	if strings.IndexFunc(prefix, func(r rune) bool { return r == '\n' || r == '\r' || r == 0 }) >= 0 {
		return ""
	}
	return prefix
}

func invalidStorageCleanupPolicyPrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	return strings.IndexFunc(prefix, func(r rune) bool { return r == '\n' || r == '\r' || r == 0 }) >= 0
}

func storageCleanupPolicyName(bucket, prefix string) string {
	if strings.TrimSpace(prefix) == "" {
		return bucket
	}
	return bucket + ":" + prefix
}

func (a *API) existingStorageCleanupPolicyRuleID(instance store.AppInstance, bucket, prefix string) string {
	name := storageCleanupPolicyName(bucket, prefix)
	items, err := a.store.ListStorageItems(instance.ID, "cleanupPolicy")
	if err == nil {
		for _, item := range items {
			if item.Name == name {
				if ruleID := storageCleanupPolicyRuleIDFromMetadata(item.Metadata); ruleID != "" {
					return ruleID
				}
			}
		}
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err == nil {
		return storageCleanupPolicyRuleIDFromRecord(objectMap(metadata["cleanupPolicy"]), bucket, prefix)
	}
	return ""
}

func storageCleanupPolicyRuleIDFromMetadata(raw string) string {
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(metadata["ruleId"]))
}

func storageCleanupPolicyRuleIDFromRecord(record map[string]any, bucket, prefix string) string {
	if record == nil {
		return ""
	}
	if strings.TrimSpace(fmt.Sprint(record["bucket"])) != bucket || strings.TrimSpace(fmt.Sprint(record["prefix"])) != prefix {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(record["ruleId"]))
}

func (a *API) recordStorageCleanupPolicy(instance store.AppInstance, result registry.StorageCleanupPolicyResult, taskID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	status := strings.TrimSpace(result.Status)
	if status == "" {
		if result.Enabled {
			status = "enabled"
		} else {
			status = "disabled"
		}
	}
	policy := map[string]any{
		"enabled":       result.Enabled,
		"status":        status,
		"bucket":        result.Bucket,
		"prefix":        result.Prefix,
		"retentionDays": result.RetentionDays,
		"ruleId":        result.RuleID,
		"source":        result.Source,
		"taskId":        taskID,
		"updatedAt":     now,
	}
	if result.Details != nil {
		policy["details"] = result.Details
	}
	metadataBytes, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if _, err := a.store.SaveStorageItem(store.StorageItem{
		InstanceID: instance.ID,
		Kind:       "cleanupPolicy",
		Name:       storageCleanupPolicyName(result.Bucket, result.Prefix),
		Policy:     status,
		Metadata:   string(metadataBytes),
	}); err != nil {
		return err
	}
	instanceMetadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &instanceMetadata)
	instanceMetadata["cleanupPolicy"] = policy
	nextMetadata, err := json.Marshal(instanceMetadata)
	if err != nil {
		return err
	}
	instance.Metadata = string(nextMetadata)
	_, err = a.store.SaveAppInstance(instance)
	return err
}

func objectMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return nil
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

type silentHTTPLogger struct{}

func (silentHTTPLogger) Info(string, ...any)  {}
func (silentHTTPLogger) Error(string, ...any) {}
