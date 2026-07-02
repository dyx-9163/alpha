package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

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
	writeError(w, http.StatusConflict, "CREDENTIAL_BOUND", i18n.Text(languageFromRequest(r), "api.credentialBound"), map[string]any{"error": err.Error()})
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

func (a *API) registerInstallCredentials(_ context.Context, app string, req registry.InstallRequest, actor string, log worker.Logger) {
	a.bindInstallCredentialReferences(app, req)
	switch app {
	case "mysql":
		a.registerPasswordCredential(credentialRegisterRequest{
			App:                app,
			Kind:               "mysql",
			Purpose:            "admin",
			Language:           req.Language,
			Parameters:         req.Parameters,
			User:               paramString(req.Parameters, "rootUser", "root"),
			Password:           firstParamString(req.Parameters, []string{"rootPassword", "mysqlPassword", "password"}, a.cfg.DefaultPassword),
			ServerIDs:          req.TargetServerIDs(),
			Port:               paramInt(req.Parameters, "port", 3306),
			EndpointScheme:     "",
			NamePrefix:         "MySQL",
			SkipIfCredentialID: "rootCredentialId",
		}, actor, log)
	case "redis":
		a.registerPasswordCredential(credentialRegisterRequest{
			App:                app,
			Kind:               "redis",
			Purpose:            "runtime",
			Language:           req.Language,
			Parameters:         req.Parameters,
			User:               "default",
			Password:           firstParamString(req.Parameters, []string{"redisPassword", "password"}, a.cfg.DefaultPassword),
			ServerIDs:          req.TargetServerIDs(),
			Port:               paramInt(req.Parameters, "port", 6379),
			EndpointScheme:     "",
			NamePrefix:         "Redis",
			SkipIfCredentialID: "redisCredentialId",
		}, actor, log)
	case "minio":
		a.registerPasswordCredential(credentialRegisterRequest{
			App:                app,
			Kind:               "minio",
			Purpose:            "admin",
			Language:           req.Language,
			Parameters:         req.Parameters,
			User:               paramString(req.Parameters, "rootUser", "admin"),
			Password:           firstParamString(req.Parameters, []string{"rootPassword", "minioPassword", "password"}, a.cfg.DefaultPassword),
			ServerIDs:          req.TargetServerIDs(),
			Port:               paramInt(req.Parameters, "apiPort", 9000),
			EndpointScheme:     "http",
			NamePrefix:         "MinIO",
			SkipIfCredentialID: "rootCredentialId",
		}, actor, log)
	case "nacos":
		a.registerPasswordCredential(credentialRegisterRequest{
			App:                app,
			Kind:               "nacos",
			Purpose:            "admin",
			Language:           req.Language,
			Parameters:         req.Parameters,
			User:               paramString(req.Parameters, "nacosUser", "nacos"),
			Password:           firstParamString(req.Parameters, []string{"nacosPassword", "password"}, "nacos"),
			ServerIDs:          req.TargetServerIDs(),
			Port:               paramInt(req.Parameters, "port", 8848),
			EndpointScheme:     "http",
			EndpointPath:       "nacos",
			NamePrefix:         "Nacos",
			SkipIfCredentialID: "nacosCredentialId",
		}, actor, log)
	}
}

func (a *API) bindInstallCredentialReferences(app string, req registry.InstallRequest) {
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
			a.bindCredentialToLatestInstance(id, app, serverID, purpose)
		}
	}
}

type credentialParameterMapping struct {
	IDKey              string
	UserKeys           []string
	PasswordKeys       []string
	SecretKeyPreferred bool
}

type credentialRegisterRequest struct {
	App                string
	Kind               string
	Purpose            string
	Language           string
	Parameters         map[string]any
	User               string
	Password           string
	ServerIDs          []string
	Port               int
	EndpointScheme     string
	EndpointPath       string
	NamePrefix         string
	SkipIfCredentialID string
}

func (a *API) registerPasswordCredential(req credentialRegisterRequest, actor string, log worker.Logger) {
	if strings.TrimSpace(req.Password) == "" || hasCredentialReference(req.Parameters, req.SkipIfCredentialID) {
		return
	}
	for _, serverID := range req.ServerIDs {
		server, err := a.store.GetServer(serverID, false)
		if err != nil {
			log.Error("credential registration skipped for %s: %v", serverID, err)
			continue
		}
		endpoint := endpointWithPath(netEndpoint(req.EndpointScheme, server.Host, req.Port), req.EndpointPath)
		credential := store.Credential{
			Name:     credentialName(req.NamePrefix, server),
			Kind:     req.Kind,
			Username: req.User,
			Endpoint: endpoint,
			Scope:    "app-instance",
			Status:   "active",
			App:      req.App,
			ServerID: serverID,
			Purpose:  req.Purpose,
			Tags:     "auto",
			Secret: map[string]string{
				"password": req.Password,
			},
			CreatedBy: actor,
		}
		saved, err := a.store.SaveCredential(credential)
		if err != nil {
			log.Error("credential registration failed for %s: %v", serverID, err)
			continue
		}
		a.bindCredentialToLatestInstance(saved.ID, req.App, serverID, req.Purpose)
		log.Info(i18n.Text(req.Language, "api.credentialRegistered"), saved.Name)
	}
}

func hasCredentialReference(params map[string]any, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	value, ok := params[key]
	if !ok {
		return false
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	return text != "" && text != "<nil>"
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

func (a *API) bindCredentialToLatestInstance(credentialID, app, serverID, purpose string) {
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
	_, _ = a.store.BindCredential(store.CredentialBinding{
		CredentialID:  credentialID,
		AppInstanceID: selected.ID,
		Purpose:       purpose,
		ServiceName:   app,
	})
}

func credentialName(prefix string, server store.Server) string {
	label := strings.TrimSpace(server.Name)
	if label == "" {
		label = strings.TrimSpace(server.Host)
	}
	if label == "" {
		label = server.ID
	}
	return prefix + " / " + label
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

func endpointWithPath(endpoint, path string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	path = strings.Trim(strings.TrimSpace(path), "/")
	if endpoint == "" || path == "" {
		return endpoint
	}
	return endpoint + "/" + path
}

func paramString(params map[string]any, key, fallback string) string {
	value := strings.TrimSpace(fmt.Sprint(params[key]))
	if value == "" || value == "<nil>" {
		return fallback
	}
	return value
}

func firstParamString(params map[string]any, keys []string, fallback string) string {
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprint(params[key]))
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return fallback
}

func paramInt(params map[string]any, key string, fallback int) int {
	switch v := params[key].(type) {
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
