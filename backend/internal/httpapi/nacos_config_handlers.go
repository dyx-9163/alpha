package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

type nacosConfigRequest struct {
	NacosInstanceID   string `json:"nacosInstanceId"`
	NacosCredentialID string `json:"nacosCredentialId"`
	Namespace         string `json:"namespace"`
	Group             string `json:"group"`
	DataID            string `json:"dataId"`
	AppName           string `json:"appName"`
	Profile           string `json:"profile"`

	IncludeDatasource bool   `json:"includeDatasource"`
	MySQLInstanceID   string `json:"mysqlInstanceId"`
	MySQLCredentialID string `json:"mysqlCredentialId"`
	DatabaseName      string `json:"databaseName"`

	IncludeRedis      bool   `json:"includeRedis"`
	RedisInstanceID   string `json:"redisInstanceId"`
	RedisCredentialID string `json:"redisCredentialId"`
	RedisDatabase     int    `json:"redisDatabase"`

	IncludeMinIO      bool   `json:"includeMinio"`
	MinIOInstanceID   string `json:"minioInstanceId"`
	MinIOCredentialID string `json:"minioCredentialId"`
	MinIOBucket       string `json:"minioBucket"`
	MinIOPlatform     string `json:"minioPlatform"`
}

type nacosConfigRollbackRequest struct {
	RevisionID        string `json:"revisionId"`
	NacosCredentialID string `json:"nacosCredentialId"`
}

type nacosConfigPreviewResponse struct {
	Generated string                      `json:"generated"`
	Current   string                      `json:"current"`
	Changed   bool                        `json:"changed"`
	Summary   []string                    `json:"summary"`
	Revisions []store.NacosConfigRevision `json:"revisions"`
}

type nacosConfigDocument struct {
	Datasource *nacosDatasourceConfig
	Redis      *nacosRedisConfig
	MinIO      *nacosMinIOConfig
	Summary    []string
}

type nacosDatasourceConfig struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
}

type nacosRedisConfig struct {
	Topology string
	Host     string
	Port     int
	Password string
	Database int
	Master   string
	Nodes    []string
}

type nacosMinIOConfig struct {
	Platform  string
	Endpoint  string
	Domain    string
	Bucket    string
	AccessKey string
	SecretKey string
}

type nacosEndpointConfig struct {
	BaseURL  string
	Username string
	Password string
}

func (a *API) nacosConfigRevisions(w http.ResponseWriter, r *http.Request) {
	revisions, err := a.store.ListNacosConfigRevisions(store.NacosConfigRevisionQuery{
		NacosInstanceID: r.URL.Query().Get("nacosInstanceId"),
		Namespace:       r.URL.Query().Get("namespace"),
		Group:           r.URL.Query().Get("group"),
		DataID:          r.URL.Query().Get("dataId"),
		Limit:           queryInt(r, "limit", 20),
	}, false)
	respond(w, revisions, err)
}

func (a *API) previewNacosConfig(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req nacosConfigRequest
	if !decode(w, r, &req) {
		return
	}
	req = normalizeNacosConfigRequest(req)
	if !a.ensureNacosConfigCredentialsAllowed(w, r, req, "") {
		return
	}
	doc, content, err := a.renderNacosConfig(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "NACOS_CONFIG_INVALID", err.Error(), nil)
		return
	}
	endpoint, err := a.resolveNacosEndpoint(req.NacosInstanceID, req.NacosCredentialID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "NACOS_ENDPOINT_INVALID", err.Error(), nil)
		return
	}
	client := newNacosConfigClient(endpoint)
	current, err := client.GetConfig(r.Context(), req.Namespace, req.Group, req.DataID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "NACOS_CONFIG_READ_FAILED", nacosConfigErrorText(lang, err), nil)
		return
	}
	revisions, err := a.store.ListNacosConfigRevisions(nacosRevisionQuery(req, 3), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "NACOS_CONFIG_REVISION_FAILED", err.Error(), nil)
		return
	}
	_ = lang
	writeJSON(w, http.StatusOK, nacosConfigPreviewResponse{
		Generated: redactNacosConfigSecrets(content),
		Current:   redactNacosConfigSecrets(current),
		Changed:   strings.TrimSpace(current) != strings.TrimSpace(content),
		Summary:   doc.Summary,
		Revisions: revisions,
	})
}

func (a *API) publishNacosConfig(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req nacosConfigRequest
	if !decode(w, r, &req) {
		return
	}
	req = normalizeNacosConfigRequest(req)
	if !a.ensureNacosConfigCredentialsAllowed(w, r, req, "") {
		return
	}
	actor := currentUser(r).Username
	target := nacosConfigTarget(req)
	taskType := "apps.nacos.config.publish"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeTaskPlanOrDelete(task.ID, simpleTaskPlan("", nacosConfigPublishSteps(lang))); err != nil {
		writeError(w, http.StatusInternalServerError, "NACOS_CONFIG_PLAN_STORE_FAILED", err.Error(), map[string]any{"target": target})
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		return a.runNacosConfigPublish(ctx, lang, actor, req, "", log)
	})
	if err == nil {
		a.audit(r, "nacos.config.publish", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) rollbackNacosConfig(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req nacosConfigRollbackRequest
	if !decode(w, r, &req) {
		return
	}
	req.RevisionID = strings.TrimSpace(req.RevisionID)
	req.NacosCredentialID = strings.TrimSpace(req.NacosCredentialID)
	if req.RevisionID == "" || req.NacosCredentialID == "" {
		writeError(w, http.StatusBadRequest, "NACOS_CONFIG_INVALID", i18n.Text(lang, "api.nacosConfigInvalid"), nil)
		return
	}
	if !a.ensureNacosConfigCredentialsAllowed(w, r, nacosConfigRequest{NacosCredentialID: req.NacosCredentialID}, req.NacosCredentialID) {
		return
	}
	actor := currentUser(r).Username
	target := req.RevisionID
	taskType := "apps.nacos.config.rollback"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeTaskPlanOrDelete(task.ID, simpleTaskPlan("", nacosConfigRollbackSteps(lang))); err != nil {
		writeError(w, http.StatusInternalServerError, "NACOS_CONFIG_PLAN_STORE_FAILED", err.Error(), map[string]any{"target": target})
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		return a.runNacosConfigRollback(ctx, lang, actor, req, log)
	})
	if err == nil {
		a.audit(r, "nacos.config.rollback", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func nacosConfigPublishSteps(lang string) []simpleTaskStep {
	return []simpleTaskStep{
		{"build-config", i18n.Text(lang, "nacos.config.stepBuild")},
		{"load-current", i18n.Text(lang, "nacos.config.stepLoadCurrent")},
		{"publish-nacos", i18n.Text(lang, "nacos.config.stepPublish")},
		{"record-revision", i18n.Text(lang, "nacos.config.stepRecord")},
	}
}

func nacosConfigRollbackSteps(lang string) []simpleTaskStep {
	return []simpleTaskStep{
		{"load-revision", i18n.Text(lang, "nacos.config.stepLoadRevision")},
		{"publish-nacos", i18n.Text(lang, "nacos.config.stepPublish")},
		{"record-revision", i18n.Text(lang, "nacos.config.stepRecord")},
	}
}

func (a *API) runNacosConfigPublish(ctx context.Context, lang, actor string, req nacosConfigRequest, rollbackFrom string, log worker.Logger) error {
	steps := nacosConfigPublishSteps(lang)
	log.StartStep("", steps[0].name, steps[0].title, 1)
	doc, content, err := a.renderNacosConfig(ctx, req)
	if err != nil {
		log.FinishStep("", steps[0].name, "failed", err.Error())
		return err
	}
	log.FinishStep("", steps[0].name, "success", "")

	log.StartStep("", steps[1].name, steps[1].title, 2)
	endpoint, err := a.resolveNacosEndpoint(req.NacosInstanceID, req.NacosCredentialID)
	if err != nil {
		log.FinishStep("", steps[1].name, "failed", err.Error())
		return err
	}
	client := newNacosConfigClient(endpoint)
	current, err := client.GetConfig(ctx, req.Namespace, req.Group, req.DataID)
	if err != nil {
		err = nacosConfigUserError(lang, err)
		log.FinishStep("", steps[1].name, "failed", err.Error())
		return err
	}
	log.FinishStep("", steps[1].name, "success", "")

	log.StartStep("", steps[2].name, steps[2].title, 3)
	if strings.TrimSpace(current) != strings.TrimSpace(content) {
		if err := client.PublishConfig(ctx, req.Namespace, req.Group, req.DataID, content); err != nil {
			err = nacosConfigUserError(lang, err)
			log.FinishStep("", steps[2].name, "failed", err.Error())
			return err
		}
	}
	log.FinishStep("", steps[2].name, "success", "")

	log.StartStep("", steps[3].name, steps[3].title, 4)
	metadata := map[string]any{
		"summary":      doc.Summary,
		"currentHash":  sha256Hex(current),
		"rollbackFrom": strings.TrimSpace(rollbackFrom),
	}
	raw, _ := json.Marshal(metadata)
	revision, err := a.store.SaveNacosConfigRevision(store.NacosConfigRevision{
		NacosInstanceID: req.NacosInstanceID,
		Namespace:       req.Namespace,
		Group:           req.Group,
		DataID:          req.DataID,
		Content:         content,
		Metadata:        string(raw),
		CreatedBy:       actor,
		PublishedAt:     time.Now(),
	})
	if err != nil {
		log.FinishStep("", steps[3].name, "failed", err.Error())
		return err
	}
	if err := a.store.DeleteOldNacosConfigRevisions(req.NacosInstanceID, req.Namespace, req.Group, req.DataID, 3); err != nil {
		log.FinishStep("", steps[3].name, "failed", err.Error())
		return err
	}
	log.Info(i18n.Text(lang, "nacos.config.published"), req.DataID, revision.ID)
	log.FinishStep("", steps[3].name, "success", "")
	return nil
}

func (a *API) runNacosConfigRollback(ctx context.Context, lang, actor string, req nacosConfigRollbackRequest, log worker.Logger) error {
	steps := nacosConfigRollbackSteps(lang)
	log.StartStep("", steps[0].name, steps[0].title, 1)
	revision, err := a.store.GetNacosConfigRevision(req.RevisionID, true)
	if err != nil {
		log.FinishStep("", steps[0].name, "failed", err.Error())
		return err
	}
	if strings.TrimSpace(revision.Content) == "" {
		err := errors.New(i18n.Text(lang, "api.nacosConfigRevisionEmpty"))
		log.FinishStep("", steps[0].name, "failed", err.Error())
		return err
	}
	log.FinishStep("", steps[0].name, "success", "")

	log.StartStep("", steps[1].name, steps[1].title, 2)
	endpoint, err := a.resolveNacosEndpoint(revision.NacosInstanceID, req.NacosCredentialID)
	if err != nil {
		log.FinishStep("", steps[1].name, "failed", err.Error())
		return err
	}
	client := newNacosConfigClient(endpoint)
	if err := client.PublishConfig(ctx, revision.Namespace, revision.Group, revision.DataID, revision.Content); err != nil {
		err = nacosConfigUserError(lang, err)
		log.FinishStep("", steps[1].name, "failed", err.Error())
		return err
	}
	log.FinishStep("", steps[1].name, "success", "")

	log.StartStep("", steps[2].name, steps[2].title, 3)
	raw, _ := json.Marshal(map[string]any{"rollbackFrom": revision.ID})
	saved, err := a.store.SaveNacosConfigRevision(store.NacosConfigRevision{
		NacosInstanceID: revision.NacosInstanceID,
		Namespace:       revision.Namespace,
		Group:           revision.Group,
		DataID:          revision.DataID,
		Content:         revision.Content,
		Metadata:        string(raw),
		CreatedBy:       actor,
		PublishedAt:     time.Now(),
	})
	if err != nil {
		log.FinishStep("", steps[2].name, "failed", err.Error())
		return err
	}
	if err := a.store.DeleteOldNacosConfigRevisions(revision.NacosInstanceID, revision.Namespace, revision.Group, revision.DataID, 3); err != nil {
		log.FinishStep("", steps[2].name, "failed", err.Error())
		return err
	}
	log.Info(i18n.Text(lang, "nacos.config.rolledBack"), revision.ID, saved.ID)
	log.FinishStep("", steps[2].name, "success", "")
	return nil
}

func (a *API) renderNacosConfig(ctx context.Context, req nacosConfigRequest) (nacosConfigDocument, string, error) {
	_ = ctx
	doc := nacosConfigDocument{}
	if req.IncludeDatasource {
		ds, err := a.resolveNacosDatasource(req)
		if err != nil {
			return doc, "", err
		}
		doc.Datasource = &ds
		doc.Summary = append(doc.Summary, fmt.Sprintf("MySQL %s:%d/%s", ds.Host, ds.Port, ds.Database))
	}
	if req.IncludeRedis {
		redisConfig, err := a.resolveNacosRedis(req)
		if err != nil {
			return doc, "", err
		}
		doc.Redis = &redisConfig
		if redisConfig.Topology == "sentinel" || redisConfig.Topology == "cluster" {
			doc.Summary = append(doc.Summary, fmt.Sprintf("Redis %s %s", redisConfig.Topology, strings.Join(redisConfig.Nodes, ",")))
		} else {
			doc.Summary = append(doc.Summary, fmt.Sprintf("Redis %s:%d/%d", redisConfig.Host, redisConfig.Port, redisConfig.Database))
		}
	}
	if req.IncludeMinIO {
		minioConfig, err := a.resolveNacosMinIO(req)
		if err != nil {
			return doc, "", err
		}
		doc.MinIO = &minioConfig
		doc.Summary = append(doc.Summary, fmt.Sprintf("MinIO %s bucket=%s", minioConfig.Endpoint, minioConfig.Bucket))
	}
	content := renderNacosRuntimeConfig(doc)
	if strings.TrimSpace(content) == "" {
		return doc, "", errors.New("at least one config section must be selected")
	}
	return doc, content, nil
}

func (a *API) resolveNacosEndpoint(instanceID, credentialID string) (nacosEndpointConfig, error) {
	instance, err := a.store.GetAppInstance(strings.TrimSpace(instanceID))
	if err != nil {
		return nacosEndpointConfig{}, err
	}
	if instance.App != "nacos" {
		return nacosEndpointConfig{}, errors.New("selected instance is not a Nacos instance")
	}
	endpoint := appInstanceMetadataValue(instance, "endpoint")
	if endpoint == "" {
		server, err := a.store.GetServer(instance.ServerID, false)
		if err != nil {
			return nacosEndpointConfig{}, err
		}
		endpoint = netEndpoint("http", server.Host, metadataInt(metadataMap(instance), "port", 8848))
	}
	credential, password, err := a.activeCredential(credentialID, "nacos", false)
	if err != nil {
		return nacosEndpointConfig{}, err
	}
	username := strings.TrimSpace(credential.Username)
	if username == "" {
		username = "nacos"
	}
	return nacosEndpointConfig{
		BaseURL:  normalizeNacosBaseURL(endpoint),
		Username: username,
		Password: password,
	}, nil
}

func (a *API) resolveNacosDatasource(req nacosConfigRequest) (nacosDatasourceConfig, error) {
	instance, err := a.store.GetAppInstance(strings.TrimSpace(req.MySQLInstanceID))
	if err != nil {
		return nacosDatasourceConfig{}, err
	}
	if instance.App != "mysql" && instance.App != "mysql-router" {
		return nacosDatasourceConfig{}, errors.New("selected instance is not a MySQL instance")
	}
	selected := instance
	if instance.App == "mysql" {
		if router := a.findMatchingMySQLRouter(instance); router.ID != "" {
			selected = router
		}
	}
	credential, password, err := a.activeCredential(req.MySQLCredentialID, "mysql", false)
	if err != nil {
		return nacosDatasourceConfig{}, err
	}
	username := strings.TrimSpace(credential.Username)
	if username == "" {
		username = "root"
	}
	host, port := a.instanceHostPort(selected, []string{"endpoint", "clusterEndpoint", "bootstrapEndpoint"}, 3306)
	if host == "" || port <= 0 {
		return nacosDatasourceConfig{}, errors.New("mysql endpoint is not available")
	}
	database := strings.TrimSpace(req.DatabaseName)
	if database == "" {
		database = "aifar_admin"
	}
	return nacosDatasourceConfig{
		Host:     host,
		Port:     port,
		Database: database,
		Username: username,
		Password: password,
	}, nil
}

func (a *API) resolveNacosRedis(req nacosConfigRequest) (nacosRedisConfig, error) {
	instance, err := a.store.GetAppInstance(strings.TrimSpace(req.RedisInstanceID))
	if err != nil {
		return nacosRedisConfig{}, err
	}
	if instance.App != "redis" {
		return nacosRedisConfig{}, errors.New("selected instance is not a Redis instance")
	}
	_, password, err := a.activeCredential(req.RedisCredentialID, "redis", false)
	if err != nil {
		return nacosRedisConfig{}, err
	}
	topology := appInstanceTopology(instance)
	if topology == "" {
		topology = "standalone"
	}
	database := req.RedisDatabase
	if database < 0 {
		database = 0
	}
	if database == 0 && topology != "cluster" {
		database = 1
	}
	switch topology {
	case "sentinel":
		nodes := a.redisSentinelNodes(instance)
		if len(nodes) == 0 {
			return nacosRedisConfig{}, errors.New("redis sentinel endpoints are not available")
		}
		master := appInstanceMetadataValue(instance, "masterName")
		if master == "" {
			master = "aifar-master"
		}
		return nacosRedisConfig{Topology: "sentinel", Password: password, Database: database, Master: master, Nodes: nodes}, nil
	case "cluster":
		nodes := a.redisClusterNodes(instance)
		if len(nodes) == 0 {
			return nacosRedisConfig{}, errors.New("redis cluster endpoints are not available")
		}
		return nacosRedisConfig{Topology: "cluster", Password: password, Nodes: nodes}, nil
	default:
		host, port := a.instanceHostPort(instance, []string{"endpoint"}, 6379)
		if host == "" || port <= 0 {
			return nacosRedisConfig{}, errors.New("redis endpoint is not available")
		}
		return nacosRedisConfig{Topology: "standalone", Host: host, Port: port, Password: password, Database: database}, nil
	}
}

func (a *API) resolveNacosMinIO(req nacosConfigRequest) (nacosMinIOConfig, error) {
	instance, err := a.store.GetAppInstance(strings.TrimSpace(req.MinIOInstanceID))
	if err != nil {
		return nacosMinIOConfig{}, err
	}
	if instance.App != "minio" {
		return nacosMinIOConfig{}, errors.New("selected instance is not a MinIO instance")
	}
	credential, secretKey, err := a.activeCredential(req.MinIOCredentialID, "minio", true)
	if err != nil {
		return nacosMinIOConfig{}, err
	}
	accessKey := strings.TrimSpace(credential.Username)
	if accessKey == "" {
		accessKey = "admin"
	}
	endpoint := appInstanceMetadataValue(instance, "endpoint")
	if endpoint == "" {
		host, port := a.instanceHostPort(instance, []string{"endpoint"}, 9000)
		if host != "" && port > 0 {
			endpoint = fmt.Sprintf("http://%s:%d", host, port)
		}
	}
	if endpoint == "" {
		return nacosMinIOConfig{}, errors.New("minio endpoint is not available")
	}
	bucket := strings.TrimSpace(req.MinIOBucket)
	if bucket == "" {
		bucket = "aifar"
	}
	platform := strings.TrimSpace(req.MinIOPlatform)
	if platform == "" {
		platform = "minio-1"
	}
	return nacosMinIOConfig{
		Platform:  platform,
		Endpoint:  ensureTrailingSlash(endpoint),
		Domain:    ensureTrailingSlash(endpoint),
		Bucket:    bucket,
		AccessKey: accessKey,
		SecretKey: secretKey,
	}, nil
}

func (a *API) activeCredential(id, kind string, secretKeyPreferred bool) (store.Credential, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.Credential{}, "", errors.New("credential is required")
	}
	credential, err := a.store.GetCredential(id, true)
	if err != nil {
		return store.Credential{}, "", err
	}
	if !strings.EqualFold(credential.Status, "active") {
		return store.Credential{}, "", errors.New("credential is not active")
	}
	if kind != "" && credential.Kind != kind && credential.Kind != "generic" {
		return store.Credential{}, "", fmt.Errorf("credential kind %s does not match %s", credential.Kind, kind)
	}
	secret := credentialSecretValue(credential.Secret, secretKeyPreferred)
	if secret == "" {
		return store.Credential{}, "", errors.New("credential secret is empty")
	}
	return credential, secret, nil
}

func (a *API) findMatchingMySQLRouter(mysql store.AppInstance) store.AppInstance {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return store.AppInstance{}
	}
	clusterKey := mysqlClusterKey(mysql)
	for _, item := range instances {
		if item.App != "mysql-router" || item.Status == "failed" {
			continue
		}
		if clusterKey != "" && mysqlClusterKey(item) == clusterKey {
			return item
		}
	}
	return store.AppInstance{}
}

func (a *API) instanceHostPort(instance store.AppInstance, endpointKeys []string, fallbackPort int) (string, int) {
	for _, key := range endpointKeys {
		if endpoint := appInstanceMetadataValue(instance, key); endpoint != "" {
			host, port := splitEndpointHostPort(endpoint, fallbackPort)
			if host != "" {
				return host, port
			}
		}
	}
	metadata := metadataMap(instance)
	host := stringFromMetadata(metadata, "host")
	if host == "" {
		if server, err := a.store.GetServer(instance.ServerID, false); err == nil {
			host = server.Host
		}
	}
	port := metadataInt(metadata, "port", fallbackPort)
	if instance.App == "mysql-router" {
		port = metadataInt(metadata, "readWritePort", metadataInt(metadata, "basePort", fallbackPort))
	}
	return host, port
}

func (a *API) redisSentinelNodes(instance store.AppInstance) []string {
	metadata := metadataMap(instance)
	nodes := stringSliceFromMetadata(metadata, "sentinelEndpoints")
	if single := stringFromMetadata(metadata, "sentinelEndpoint"); single != "" {
		nodes = append(nodes, single)
	}
	if len(nodes) == 0 {
		nodes = append(nodes, a.redisGroupEndpoints(instance, true)...)
	}
	nodes = uniqueSortedStrings(nodes)
	if len(nodes) > 0 {
		return nodes
	}
	host, _ := a.instanceHostPort(instance, []string{"endpoint"}, 6379)
	if host == "" {
		return nil
	}
	port := metadataInt(metadata, "sentinelPort", 26379)
	return []string{fmt.Sprintf("%s:%d", host, port)}
}

func (a *API) redisClusterNodes(instance store.AppInstance) []string {
	metadata := metadataMap(instance)
	nodes := stringSliceFromMetadata(metadata, "clusterEndpoints")
	if len(nodes) == 0 {
		nodes = append(nodes, a.redisGroupEndpoints(instance, false)...)
	}
	return uniqueSortedStrings(nodes)
}

func (a *API) redisGroupEndpoints(instance store.AppInstance, sentinel bool) []string {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return nil
	}
	key := redisGroupKey(instance)
	if key == "" {
		return nil
	}
	out := []string{}
	for _, item := range instances {
		if item.App != "redis" || redisGroupKey(item) != key {
			continue
		}
		if sentinel {
			metadata := metadataMap(item)
			if endpoint := stringFromMetadata(metadata, "sentinelEndpoint"); endpoint != "" {
				out = append(out, endpoint)
				continue
			}
			host, _ := a.instanceHostPort(item, []string{"endpoint"}, 6379)
			if host != "" {
				out = append(out, fmt.Sprintf("%s:%d", host, metadataInt(metadata, "sentinelPort", 26379)))
			}
			continue
		}
		host, port := a.instanceHostPort(item, []string{"endpoint"}, 6379)
		if host != "" && port > 0 {
			out = append(out, fmt.Sprintf("%s:%d", host, port))
		}
	}
	return out
}

func renderNacosRuntimeConfig(doc nacosConfigDocument) string {
	var b strings.Builder
	b.WriteString("spring:\n")
	if doc.Redis != nil {
		b.WriteString("  data:\n")
		b.WriteString("    redis:\n")
		if doc.Redis.Topology != "cluster" {
			fmt.Fprintf(&b, "      database: %d\n", doc.Redis.Database)
		}
		if doc.Redis.Topology == "standalone" {
			fmt.Fprintf(&b, "      host: %s\n", yamlScalar(doc.Redis.Host))
			fmt.Fprintf(&b, "      port: %d\n", doc.Redis.Port)
		}
		if doc.Redis.Password != "" {
			fmt.Fprintf(&b, "      password: %s\n", yamlScalar(doc.Redis.Password))
		}
		b.WriteString("      timeout: 3s\n")
		switch doc.Redis.Topology {
		case "sentinel":
			b.WriteString("      sentinel:\n")
			fmt.Fprintf(&b, "        master: %s\n", yamlScalar(doc.Redis.Master))
			b.WriteString("        nodes:\n")
			for _, node := range doc.Redis.Nodes {
				fmt.Fprintf(&b, "          - %s\n", yamlScalar(node))
			}
			if doc.Redis.Password != "" {
				fmt.Fprintf(&b, "        password: %s\n", yamlScalar(doc.Redis.Password))
			}
		case "cluster":
			b.WriteString("      cluster:\n")
			b.WriteString("        nodes:\n")
			for _, node := range doc.Redis.Nodes {
				fmt.Fprintf(&b, "          - %s\n", yamlScalar(node))
			}
		}
		b.WriteString("      lettuce:\n")
		b.WriteString("        pool:\n")
		b.WriteString("          max-active: 128\n")
		b.WriteString("          max-wait: -1\n")
		b.WriteString("          min-idle: 16\n")
		b.WriteString("          max-idle: 64\n")
	}
	if doc.Datasource != nil {
		b.WriteString("  datasource:\n")
		b.WriteString("    db-type: MySQL\n")
		fmt.Fprintf(&b, "    db-name: %s\n", yamlScalar(doc.Datasource.Database))
		fmt.Fprintf(&b, "    host: %s\n", yamlScalar(doc.Datasource.Host))
		fmt.Fprintf(&b, "    port: %d\n", doc.Datasource.Port)
		fmt.Fprintf(&b, "    username: %s\n", yamlScalar(doc.Datasource.Username))
		fmt.Fprintf(&b, "    password: %s\n", yamlScalar(doc.Datasource.Password))
		b.WriteString("    db-schema:\n")
		b.WriteString("    prepare-url:\n")
		b.WriteString("    tablespace:\n")
		b.WriteString("    dynamic:\n")
		b.WriteString("      seata: false\n")
		b.WriteString("      primary: master\n")
		b.WriteString("      strict: true\n")
		b.WriteString("      druid:\n")
		b.WriteString("        test-while-idle: true\n")
		b.WriteString("        time-between-eviction-runs-millis: 60000\n")
		b.WriteString("        max-wait: 10000\n")
		b.WriteString("        initial-size: 4\n")
		b.WriteString("        max-active: 50\n")
		b.WriteString("        min-idle: 4\n")
		b.WriteString("        keep-alive: true\n")
		b.WriteString("        connect-timeout: 10000\n")
		b.WriteString("        socket-timeout: 10000\n")
		b.WriteString("        query-timeout: 90000\n")
		b.WriteString("        transaction-query-timeout: 90000\n")
	}
	if doc.MinIO != nil {
		b.WriteString("\nminio:\n")
		fmt.Fprintf(&b, "  - platform: %s\n", yamlScalar(doc.MinIO.Platform))
		b.WriteString("    enable-storage: true\n")
		fmt.Fprintf(&b, "    access-key: %s\n", yamlScalar(doc.MinIO.AccessKey))
		fmt.Fprintf(&b, "    secret-key: %s\n", yamlScalar(doc.MinIO.SecretKey))
		fmt.Fprintf(&b, "    end-point: %s\n", yamlScalar(doc.MinIO.Endpoint))
		fmt.Fprintf(&b, "    bucket-name: %s\n", yamlScalar(doc.MinIO.Bucket))
		fmt.Fprintf(&b, "    domain: %s\n", yamlScalar(doc.MinIO.Domain))
		b.WriteString("    base-path:\n")
		b.WriteString("    cleanup:\n")
		b.WriteString("      cron: 0 0 2 * * ?\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func (a *API) ensureNacosConfigCredentialsAllowed(w http.ResponseWriter, r *http.Request, req nacosConfigRequest, extraID string) bool {
	ids := []string{req.NacosCredentialID, req.MySQLCredentialID, req.RedisCredentialID, req.MinIOCredentialID, extraID}
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			if !rbac.Allows(currentUser(r).Role, rbac.CredentialsUse) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", i18n.Text(languageFromRequest(r), "api.permissionDenied"), map[string]any{"permission": string(rbac.CredentialsUse)})
				return false
			}
			return true
		}
	}
	return true
}

func normalizeNacosConfigRequest(req nacosConfigRequest) nacosConfigRequest {
	req.NacosInstanceID = strings.TrimSpace(req.NacosInstanceID)
	req.NacosCredentialID = strings.TrimSpace(req.NacosCredentialID)
	req.Namespace = defaultString(req.Namespace, "prod")
	req.Group = defaultString(req.Group, "DEFAULT_GROUP")
	req.DataID = defaultString(req.DataID, "application-prod.yml")
	req.AppName = defaultString(req.AppName, "aifar")
	req.Profile = defaultString(req.Profile, "prod")
	req.MySQLInstanceID = strings.TrimSpace(req.MySQLInstanceID)
	req.MySQLCredentialID = strings.TrimSpace(req.MySQLCredentialID)
	req.DatabaseName = defaultString(req.DatabaseName, "aifar_admin")
	req.RedisInstanceID = strings.TrimSpace(req.RedisInstanceID)
	req.RedisCredentialID = strings.TrimSpace(req.RedisCredentialID)
	if req.RedisDatabase < 0 {
		req.RedisDatabase = 0
	}
	req.MinIOInstanceID = strings.TrimSpace(req.MinIOInstanceID)
	req.MinIOCredentialID = strings.TrimSpace(req.MinIOCredentialID)
	req.MinIOBucket = defaultString(req.MinIOBucket, "aifar")
	req.MinIOPlatform = defaultString(req.MinIOPlatform, "minio-1")
	return req
}

func nacosRevisionQuery(req nacosConfigRequest, limit int) store.NacosConfigRevisionQuery {
	return store.NacosConfigRevisionQuery{
		NacosInstanceID: req.NacosInstanceID,
		Namespace:       req.Namespace,
		Group:           req.Group,
		DataID:          req.DataID,
		Limit:           limit,
	}
}

func nacosConfigTarget(req nacosConfigRequest) string {
	return strings.Join([]string{req.NacosInstanceID, req.Namespace, req.Group, req.DataID}, "/")
}

func normalizeNacosBaseURL(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, "/nacos") {
		endpoint = strings.TrimSuffix(endpoint, "/nacos")
	}
	return endpoint
}

type nacosLoginError struct {
	Username string
	Status   string
	Body     string
}

func (e nacosLoginError) Error() string {
	return fmt.Sprintf("selected Nacos credential %q was rejected by Nacos (%s): %s", e.Username, e.Status, e.Body)
}

func nacosConfigUserError(lang string, err error) error {
	return errors.New(nacosConfigErrorText(lang, err))
}

func nacosConfigErrorText(lang string, err error) string {
	var loginErr nacosLoginError
	if errors.As(err, &loginErr) {
		body := strings.TrimSpace(loginErr.Body)
		if body == "" {
			body = "-"
		}
		return i18n.Text(lang, "api.nacosCredentialRejected", loginErr.Username, loginErr.Status, body)
	}
	return err.Error()
}

type nacosConfigClient struct {
	endpoint nacosEndpointConfig
	client   *http.Client
}

func newNacosConfigClient(endpoint nacosEndpointConfig) nacosConfigClient {
	return nacosConfigClient{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (c nacosConfigClient) GetConfig(ctx context.Context, namespace, group, dataID string) (string, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Set("dataId", dataID)
	values.Set("group", group)
	values.Set("tenant", namespace)
	if token != "" {
		values.Set("accessToken", token)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint.BaseURL+"/nacos/v1/cs/configs?"+values.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("nacos config read failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func (c nacosConfigClient) PublishConfig(ctx context.Context, namespace, group, dataID, content string) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	values := url.Values{}
	values.Set("dataId", dataID)
	values.Set("group", group)
	values.Set("tenant", namespace)
	values.Set("content", content)
	values.Set("type", "yaml")
	if token != "" {
		values.Set("accessToken", token)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.BaseURL+"/nacos/v1/cs/configs", strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("nacos config publish failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if text := strings.TrimSpace(string(body)); text != "" && !strings.EqualFold(text, "true") {
		mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if !strings.Contains(mediaType, "json") {
			return fmt.Errorf("nacos config publish returned %q", text)
		}
	}
	return nil
}

func (c nacosConfigClient) accessToken(ctx context.Context) (string, error) {
	if strings.TrimSpace(c.endpoint.Username) == "" || strings.TrimSpace(c.endpoint.Password) == "" {
		return "", nil
	}
	values := url.Values{}
	values.Set("username", c.endpoint.Username)
	values.Set("password", c.endpoint.Password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.BaseURL+"/nacos/v1/auth/users/login", strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nacosLoginError{
			Username: c.endpoint.Username,
			Status:   resp.Status,
			Body:     limitNacosResponseText(body),
		}
	}
	var parsed map[string]any
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&parsed); err != nil {
		return "", err
	}
	token := strings.TrimSpace(fmt.Sprint(parsed["accessToken"]))
	if token == "" || token == "<nil>" {
		return "", errors.New("nacos login response has no accessToken")
	}
	return token, nil
}

func limitNacosResponseText(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) <= 300 {
		return text
	}
	return text[:300]
}

func metadataMap(instance store.AppInstance) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &out)
	return out
}

func metadataInt(metadata map[string]any, key string, fallback int) int {
	switch value := metadata[key].(type) {
	case float64:
		if value > 0 {
			return int(value)
		}
	case int:
		if value > 0 {
			return value
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func stringFromMetadata(metadata map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(metadata[key]))
	if value == "" || value == "<nil>" {
		return ""
	}
	return value
}

func stringSliceFromMetadata(metadata map[string]any, key string) []string {
	value := metadata[key]
	switch typed := value.(type) {
	case []any:
		out := []string{}
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		out := []string{}
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		return splitEndpointList(typed)
	default:
		return nil
	}
}

func splitEndpointList(value string) []string {
	value = strings.TrimSpace(strings.Trim(value, "[]"))
	if value == "" {
		return nil
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})
	if len(fields) <= 1 {
		fields = strings.Fields(value)
	}
	out := []string{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func redisGroupKey(instance store.AppInstance) string {
	if value := appInstanceMetadataValue(instance, "clusterId"); value != "" {
		return "cluster:" + value
	}
	if value := appInstanceMetadataValue(instance, "sentinelName"); value != "" {
		return "sentinel:" + value
	}
	if value := appInstanceMetadataValue(instance, "masterName"); value != "" && appInstanceTopology(instance) == "sentinel" {
		return "sentinel-master:" + value
	}
	return ""
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func splitEndpointHostPort(endpoint string, fallbackPort int) (string, int) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", 0
	}
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err == nil {
			host := parsed.Hostname()
			port := fallbackPort
			if rawPort := parsed.Port(); rawPort != "" {
				if parsedPort, err := strconv.Atoi(rawPort); err == nil {
					port = parsedPort
				}
			}
			return host, port
		}
	}
	if host, portText, err := net.SplitHostPort(endpoint); err == nil {
		port, _ := strconv.Atoi(portText)
		return host, port
	}
	if strings.Contains(endpoint, ":") {
		parts := strings.Split(endpoint, ":")
		last := parts[len(parts)-1]
		if port, err := strconv.Atoi(last); err == nil {
			return strings.Join(parts[:len(parts)-1], ":"), port
		}
	}
	return endpoint, fallbackPort
}

func ensureTrailingSlash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasSuffix(value, "/") {
		return value
	}
	return value + "/"
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func yamlScalar(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '@' || r == '*' || r == '?' || r == '=' || r == '+' || r == ',' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) == -1 && !strings.HasPrefix(value, "*") && !strings.HasPrefix(value, "?") && !strings.HasPrefix(value, "-") {
		return value
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func redactNacosConfigSecrets(content string) string {
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "password:") || strings.Contains(lower, " password:") ||
			strings.HasPrefix(lower, "secret-key:") || strings.Contains(lower, " secret-key:") ||
			strings.HasPrefix(lower, "access-key:") || strings.Contains(lower, " access-key:") {
			prefix := line[:strings.Index(line, ":")+1]
			lines[index] = prefix + " ******"
		}
	}
	return strings.Join(lines, "\n")
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
