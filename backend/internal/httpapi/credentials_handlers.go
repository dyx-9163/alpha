package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/appmeta"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

func (a *API) listCredentials(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListCredentials(store.CredentialQuery{
		Kind:   r.URL.Query().Get("kind"),
		Status: r.URL.Query().Get("status"),
		Q:      r.URL.Query().Get("q"),
	})
	respond(w, items, err)
}

func (a *API) getCredential(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetCredential(chi.URLParam(r, "id"), false)
	respond(w, item, err)
}

func (a *API) credentialReferences(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	refs, err := a.store.ListCredentialReferences(id, "", "")
	if err != nil {
		respond(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": refs})
}

func (a *API) saveCredential(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req credentialSaveRequest
	if !decode(w, r, &req) {
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		id = strings.TrimSpace(req.ID)
	}
	secret := credentialSecretFromRequest(req)
	item := store.Credential{
		ID:            id,
		Name:          req.Name,
		Kind:          req.Kind,
		Username:      req.Username,
		Endpoint:      req.Endpoint,
		Scope:         req.Scope,
		Status:        req.Status,
		App:           req.App,
		ServerID:      req.ServerID,
		AppInstanceID: req.AppInstanceID,
		Purpose:       req.Purpose,
		Tags:          req.Tags,
		Secret:        secret,
		CreatedBy:     currentUser(r).Username,
	}
	if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Kind) == "" {
		writeError(w, http.StatusBadRequest, "CREDENTIAL_INVALID", i18n.Text(lang, "api.credentialInvalid"), nil)
		return
	}
	saved, err := a.store.SaveCredential(item)
	if err == nil {
		action := "credentials.create"
		if id != "" {
			action = "credentials.update"
		}
		a.audit(r, action, saved.ID, "success", saved.Kind)
	}
	respond(w, saved, err)
}

func (a *API) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := a.store.DeleteCredential(id)
	if err == nil {
		a.audit(r, "credentials.delete", id, "success", "")
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		return
	}
	if store.IsNotFound(err) {
		respond(w, nil, err)
		return
	}
	refs, _ := a.store.ListCredentialReferences(id, "", "")
	writeError(w, http.StatusConflict, "CREDENTIAL_BOUND", i18n.Text(languageFromRequest(r), "api.credentialBound"), map[string]any{"error": err.Error(), "references": refs})
}

func (a *API) resolveCredentialParameters(r *http.Request, parameters map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range parameters {
		out[key] = value
	}
	if !installUsesCredentials(out) {
		return out, nil
	}
	if !rbac.Allows(currentUser(r).Role, rbac.CredentialsUse) {
		return nil, errors.New(i18n.Text(languageFromRequest(r), "api.permissionDenied"))
	}
	mappings := []credentialParameterMapping{
		{IDKey: "rootCredentialId", UserKeys: []string{"rootUser"}, PasswordKeys: []string{"rootPassword", "password"}},
		{IDKey: "dbCredentialId", UserKeys: []string{"dbUser"}, PasswordKeys: []string{"dbPassword"}},
		{IDKey: "mysqlCredentialId", UserKeys: []string{"dbUser"}, PasswordKeys: []string{"dbPassword"}},
		{IDKey: "redisCredentialId", UserKeys: []string{"redisUser"}, PasswordKeys: []string{"redisPassword", "password"}},
		{IDKey: "nacosCredentialId", UserKeys: []string{"nacosUser"}, PasswordKeys: []string{"nacosPassword"}},
		{IDKey: "minioCredentialId", UserKeys: []string{"minioAccessKey"}, PasswordKeys: []string{"minioSecretKey"}, SecretKeyPreferred: true},
		{IDKey: "credentialId", UserKeys: []string{"username"}, PasswordKeys: []string{"password"}},
	}
	for _, mapping := range mappings {
		id := strings.TrimSpace(fmt.Sprint(out[mapping.IDKey]))
		if id == "" || id == "<nil>" {
			continue
		}
		credential, err := a.store.GetCredential(id, true)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(credential.Status, "active") {
			return nil, errors.New(i18n.Text(languageFromRequest(r), "api.credentialInactive"))
		}
		applyCredentialMapping(out, credential, mapping)
	}
	return out, nil
}

func (a *API) bindInstallCredentialReferences(app string, req registry.InstallRequest, log registry.Logger) {
	a.bindSelectedInstallCredentialReferences(app, req)
	a.registerGeneratedInstallCredentials(app, req, log)
	a.recordInstallClusterMembership(app, req, log)
}

func (a *API) bindSelectedInstallCredentialReferences(app string, req registry.InstallRequest) {
	references := map[string]string{
		"rootCredentialId":  "admin",
		"dbCredentialId":    "database",
		"mysqlCredentialId": "database",
		"redisCredentialId": "redis",
		"nacosCredentialId": "nacos",
		"minioCredentialId": "minio",
		"credentialId":      "runtime",
	}
	for key, purpose := range references {
		id := paramString(req.Parameters, key, "")
		if id == "" {
			continue
		}
		for _, serverID := range req.TargetServerIDs() {
			a.bindCredentialToLatestInstance(id, app, serverID, purpose, false)
		}
	}
}

type generatedInstallCredentialSpec struct {
	Kind           string
	Username       string
	SecretKey      string
	SecretValue    string
	Purpose        string
	EndpointScheme string
	DefaultPort    int
	NacosPath      bool
}

func (a *API) registerGeneratedInstallCredentials(app string, req registry.InstallRequest, log registry.Logger) {
	spec, ok := generatedInstallCredentialSpecFor(app, req.Parameters)
	if !ok {
		return
	}
	for _, serverID := range req.TargetServerIDs() {
		instance, ok := a.latestAppInstanceForServer(app, serverID)
		if !ok {
			continue
		}
		endpoint := a.credentialEndpointForInstance(instance, spec)
		name := generatedCredentialName(spec, endpoint, serverID)
		credential, err := a.store.SaveCredential(store.Credential{
			Name:          name,
			Kind:          spec.Kind,
			Username:      spec.Username,
			Endpoint:      endpoint,
			Scope:         "app-instance",
			Status:        "active",
			App:           spec.Kind,
			ServerID:      serverID,
			AppInstanceID: instance.ID,
			Purpose:       spec.Purpose,
			Tags:          "generated,install",
			Secret:        map[string]string{spec.SecretKey: spec.SecretValue},
			CreatedBy:     req.Actor,
		})
		if err != nil {
			if log != nil {
				log.Error(i18n.Text(req.Language, "api.credentialAutoRegisterFailed"), spec.Kind, instance.ID, err)
			}
			continue
		}
		if _, err := a.store.BindCredential(store.CredentialBinding{
			CredentialID:  credential.ID,
			AppInstanceID: instance.ID,
			Purpose:       spec.Purpose,
			ServiceName:   spec.Kind,
		}); err != nil {
			if log != nil {
				log.Error(i18n.Text(req.Language, "api.credentialAutoRegisterFailed"), spec.Kind, instance.ID, err)
			}
			continue
		}
		a.recordGeneratedCredentialReference(credential, instance, spec.Purpose)
		if log != nil {
			log.Info(i18n.Text(req.Language, "api.credentialAutoRegistered"), spec.Kind, credential.Name)
		}
	}
}

func generatedInstallCredentialSpecFor(app string, params map[string]any) (generatedInstallCredentialSpec, bool) {
	app = strings.ToLower(strings.TrimSpace(app))
	switch app {
	case "mysql":
		if firstCredentialParam(params, "rootCredentialId", "mysqlCredentialId") != "" {
			return generatedInstallCredentialSpec{}, false
		}
		password := firstCredentialParam(params, "rootPassword", "password", "mysqlPassword")
		if password == "" {
			return generatedInstallCredentialSpec{}, false
		}
		return generatedInstallCredentialSpec{
			Kind:        "mysql",
			Username:    paramString(params, "rootUser", "root"),
			SecretKey:   "password",
			SecretValue: password,
			Purpose:     "admin",
			DefaultPort: paramInt(params, "port", 3306),
		}, true
	case "redis":
		if firstCredentialParam(params, "redisCredentialId") != "" {
			return generatedInstallCredentialSpec{}, false
		}
		password := firstCredentialParam(params, "password", "redisPassword")
		if password == "" {
			return generatedInstallCredentialSpec{}, false
		}
		return generatedInstallCredentialSpec{
			Kind:           "redis",
			Username:       paramString(params, "redisUser", "default"),
			SecretKey:      "password",
			SecretValue:    password,
			Purpose:        "redis",
			EndpointScheme: "redis",
			DefaultPort:    paramInt(params, "port", 6379),
		}, true
	case "minio":
		if firstCredentialParam(params, "rootCredentialId", "minioCredentialId") != "" {
			return generatedInstallCredentialSpec{}, false
		}
		password := firstCredentialParam(params, "rootPassword", "password", "minioPassword")
		if password == "" {
			return generatedInstallCredentialSpec{}, false
		}
		return generatedInstallCredentialSpec{
			Kind:           "minio",
			Username:       paramString(params, "rootUser", "admin"),
			SecretKey:      "secretKey",
			SecretValue:    password,
			Purpose:        "minio",
			EndpointScheme: "http",
			DefaultPort:    paramInt(params, "apiPort", 9000),
		}, true
	case "nacos":
		if firstCredentialParam(params, "nacosCredentialId") != "" {
			return generatedInstallCredentialSpec{}, false
		}
		password := firstCredentialParam(params, "nacosPassword")
		if password == "" {
			return generatedInstallCredentialSpec{}, false
		}
		return generatedInstallCredentialSpec{
			Kind:           "nacos",
			Username:       paramString(params, "nacosUser", "nacos"),
			SecretKey:      "password",
			SecretValue:    password,
			Purpose:        "nacos",
			EndpointScheme: "http",
			DefaultPort:    paramInt(params, "port", 8848),
			NacosPath:      true,
		}, true
	default:
		return generatedInstallCredentialSpec{}, false
	}
}

func (a *API) latestAppInstanceForServer(app, serverID string) (store.AppInstance, bool) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return store.AppInstance{}, false
	}
	var selected store.AppInstance
	for _, item := range instances {
		if item.App != app || item.ServerID != serverID {
			continue
		}
		if selected.ID == "" || item.UpdatedAt.After(selected.UpdatedAt) {
			selected = item
		}
	}
	return selected, selected.ID != ""
}

func (a *API) credentialEndpointForInstance(instance store.AppInstance, spec generatedInstallCredentialSpec) string {
	metadata := appInstanceMetadata(instance)
	if endpoint := metadataString(metadata, "endpoint"); endpoint != "" {
		return endpoint
	}
	port := metadataInt(metadata, "port", spec.DefaultPort)
	if spec.Kind == "minio" {
		port = metadataInt(metadata, "apiPort", port)
	}
	server, err := a.store.GetServer(instance.ServerID, false)
	host := instance.ServerID
	if err == nil && strings.TrimSpace(server.Host) != "" {
		host = server.Host
	}
	endpoint := netEndpoint(spec.EndpointScheme, host, port)
	if spec.NacosPath && endpoint != "" && !strings.HasSuffix(endpoint, "/nacos") {
		endpoint += "/nacos"
	}
	return endpoint
}

func generatedCredentialName(spec generatedInstallCredentialSpec, endpoint, fallback string) string {
	label := strings.ToUpper(spec.Kind[:1]) + spec.Kind[1:]
	target := strings.TrimSpace(endpoint)
	if target == "" {
		target = strings.TrimSpace(fallback)
	}
	if target == "" {
		return fmt.Sprintf("%s %s", label, spec.Purpose)
	}
	return fmt.Sprintf("%s %s @ %s", label, spec.Username, target)
}

type credentialParameterMapping struct {
	IDKey              string
	UserKeys           []string
	PasswordKeys       []string
	SecretKeyPreferred bool
}

func credentialSecretFromRequest(req credentialSaveRequest) map[string]string {
	secret := map[string]string{}
	for key, value := range req.Secret {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			secret[key] = value
		}
	}
	if strings.TrimSpace(req.Password) != "" {
		secret["password"] = req.Password
	}
	if strings.TrimSpace(req.SecretKey) != "" {
		secret["secretKey"] = req.SecretKey
	}
	if strings.TrimSpace(req.Token) != "" {
		secret["token"] = req.Token
	}
	if strings.TrimSpace(req.PrivateKey) != "" {
		secret["privateKey"] = req.PrivateKey
	}
	if len(secret) == 0 {
		return nil
	}
	return secret
}

func installUsesCredentials(parameters map[string]any) bool {
	for key, value := range parameters {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(key)), "credentialid") && strings.TrimSpace(fmt.Sprint(value)) != "" {
			return true
		}
	}
	return false
}

func applyCredentialMapping(values map[string]any, credential store.Credential, mapping credentialParameterMapping) {
	for _, key := range mapping.UserKeys {
		if strings.TrimSpace(credential.Username) != "" {
			values[key] = credential.Username
		}
	}
	password := credentialSecretValue(credential.Secret, mapping.SecretKeyPreferred)
	for _, key := range mapping.PasswordKeys {
		if strings.TrimSpace(password) != "" {
			values[key] = password
		}
	}
}

func credentialSecretValue(secret map[string]string, secretKeyPreferred bool) string {
	keys := []string{"password", "secret", "secretKey", "token"}
	if secretKeyPreferred {
		keys = []string{"secretKey", "password", "secret", "token"}
	}
	for _, key := range keys {
		if value := strings.TrimSpace(secret[key]); value != "" {
			return value
		}
	}
	return ""
}

func (a *API) bindCredentialToLatestInstance(credentialID, app, serverID, purpose string, updateCredentialRecord bool) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return
	}
	var selected store.AppInstance
	for _, item := range instances {
		if item.App != app || item.ServerID != serverID {
			continue
		}
		if selected.ID == "" || item.UpdatedAt.After(selected.UpdatedAt) {
			selected = item
		}
	}
	if selected.ID == "" {
		return
	}
	if updateCredentialRecord {
		if credential, err := a.store.GetCredential(credentialID, false); err == nil && strings.TrimSpace(credential.AppInstanceID) == "" {
			credential.AppInstanceID = selected.ID
			credential.Secret = nil
			_, _ = a.store.SaveCredential(credential)
		}
	}
	_, _ = a.store.BindCredential(store.CredentialBinding{
		CredentialID:  credentialID,
		AppInstanceID: selected.ID,
		Purpose:       purpose,
		ServiceName:   app,
	})
	_, _ = a.store.SaveCredentialReference(store.CredentialReference{
		CredentialID:    credentialID,
		ResourceType:    "app-instance",
		ResourceID:      selected.ID,
		Purpose:         purpose,
		Generated:       false,
		LifecyclePolicy: "retain",
		Metadata:        credentialReferenceMetadata(app, selected.ServerID),
	})
}

func (a *API) recordGeneratedCredentialReference(credential store.Credential, instance store.AppInstance, purpose string) {
	_, _ = a.store.SaveCredentialReference(store.CredentialReference{
		CredentialID:    credential.ID,
		ResourceType:    "app-instance",
		ResourceID:      instance.ID,
		Purpose:         purpose,
		Generated:       true,
		LifecyclePolicy: "delete-with-resource",
		Metadata:        credentialReferenceMetadata(credential.Kind, instance.ServerID),
	})
}

func credentialReferenceMetadata(app, serverID string) string {
	raw, _ := json.Marshal(map[string]any{
		"app":      strings.TrimSpace(app),
		"serverId": strings.TrimSpace(serverID),
	})
	return string(raw)
}

func (a *API) recordInstallClusterMembership(app string, req registry.InstallRequest, log registry.Logger) {
	for _, serverID := range req.TargetServerIDs() {
		instance, ok := a.latestAppInstanceForServer(app, serverID)
		if !ok {
			continue
		}
		if err := a.recordAppClusterMember(instance, req.Topology); err != nil && log != nil {
			log.Error("record app cluster membership failed: %v", err)
		}
	}
}

func (a *API) recordAppClusterMember(instance store.AppInstance, requestedTopology string) error {
	metadata := appmeta.Parse(instance.Metadata)
	name := appmeta.ClusterID(metadata)
	if name == "" {
		name = appmeta.String(metadata, "clusterName", "")
	}
	if name == "" {
		name = instance.ID
	}
	topology := strings.TrimSpace(instance.Topology)
	if topology == "" {
		topology = strings.TrimSpace(requestedTopology)
	}
	if topology == "" {
		topology = "standalone"
	}
	status := strings.TrimSpace(instance.Status)
	if status == "" {
		status = "installed"
	}
	cluster, err := a.store.SaveAppCluster(store.AppCluster{
		App:      lifecycleAppFamily(instance.App),
		Name:     name,
		Topology: topology,
		Status:   status,
		Metadata: appmeta.Marshal(map[string]any{
			"source":     "install",
			"instanceId": instance.ID,
			"app":        instance.App,
		}),
	})
	if err != nil {
		return err
	}
	role := appmeta.String(metadata, "role", "")
	if role == "" {
		role = appmeta.String(metadata, "nodeRole", "")
	}
	_, err = a.store.SaveAppClusterMember(store.AppClusterMember{
		ClusterID:  cluster.ID,
		InstanceID: instance.ID,
		ServerID:   instance.ServerID,
		Role:       role,
		Status:     status,
		Metadata: appmeta.Marshal(map[string]any{
			"endpoint": appmeta.Endpoint(metadata),
			"app":      instance.App,
		}),
	})
	return err
}

func netEndpoint(scheme, host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return host
	}
	endpoint := fmt.Sprintf("%s:%d", host, port)
	if strings.TrimSpace(scheme) != "" {
		return strings.TrimSpace(scheme) + "://" + endpoint
	}
	return endpoint
}

func paramString(params map[string]any, key, fallback string) string {
	value := strings.TrimSpace(fmt.Sprint(params[key]))
	if value == "" || value == "<nil>" {
		return fallback
	}
	return value
}

func firstCredentialParam(params map[string]any, keys ...string) string {
	for _, key := range keys {
		value := paramString(params, key, "")
		if value != "" {
			return value
		}
	}
	return ""
}

func paramInt(params map[string]any, key string, fallback int) int {
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return normalizeCredentialPort(typed, fallback)
	case int64:
		return normalizeCredentialPort(int(typed), fallback)
	case float64:
		return normalizeCredentialPort(int(typed), fallback)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
		return normalizeCredentialPort(n, fallback)
	default:
		return fallback
	}
}

func normalizeCredentialPort(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}
