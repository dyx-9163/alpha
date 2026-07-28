package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/appmeta"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

const mysqlRootCredentialVersionParameter = "__aifar_mysql_root_credential_version"

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
	locks, ok := a.acquireMySQLAdminCredentialMutationLocks(w, r, item)
	if !ok {
		return
	}
	defer a.releaseOperationLocks(locks)
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

func (a *API) acquireMySQLAdminCredentialMutationLocks(w http.ResponseWriter, r *http.Request, item store.Credential) ([]store.OperationLock, bool) {
	lang := languageFromRequest(r)
	lockFailure := func(status int) ([]store.OperationLock, bool) {
		writeError(w, status, "CREDENTIAL_MUTATION_LOCK_FAILED", i18n.Text(lang, "api.credentialMutationLockFailed"), nil)
		return nil, false
	}
	instances := map[string]store.AppInstance{}
	isMySQL := strings.EqualFold(strings.TrimSpace(item.Kind), "mysql")
	if item.ID != "" {
		if existing, err := a.store.GetCredential(item.ID, false); err == nil {
			isMySQL = isMySQL || strings.EqualFold(strings.TrimSpace(existing.Kind), "mysql")
		}
		bindings, err := a.store.CredentialBindings(item.ID)
		if err != nil {
			return lockFailure(http.StatusInternalServerError)
		}
		for _, binding := range bindings {
			if !strings.EqualFold(strings.TrimSpace(binding.Purpose), "admin") {
				continue
			}
			instance, err := a.store.GetAppInstance(binding.AppInstanceID)
			if err != nil {
				return lockFailure(http.StatusConflict)
			}
			instances[instance.ID] = instance
		}
	}
	if isMySQL && strings.EqualFold(strings.TrimSpace(item.Purpose), "admin") && strings.TrimSpace(item.AppInstanceID) != "" {
		instance, err := a.store.GetAppInstance(item.AppInstanceID)
		if err != nil {
			return lockFailure(http.StatusConflict)
		}
		instances[instance.ID] = instance
	}
	if !isMySQL {
		return nil, true
	}
	specs := credentialOperationLockSpecs("mysql-admin-credential-update", item.ID)
	values := make([]store.AppInstance, 0, len(instances))
	for _, instance := range instances {
		values = append(values, instance)
	}
	if len(values) > 0 {
		instanceSpecs, err := validatedAppMutationOperationLockSpecs("mysql-admin-credential-update", values)
		if err != nil {
			return lockFailure(http.StatusConflict)
		}
		specs = append(specs, instanceSpecs...)
	}
	if len(specs) == 0 {
		return nil, true
	}
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].Scope+"\x00"+specs[i].ResourceID < specs[j].Scope+"\x00"+specs[j].ResourceID
	})
	locks := make([]store.OperationLock, 0, len(specs))
	for _, spec := range specs {
		lock, err := a.store.AcquireOperationLock(store.OperationLock{
			Scope: spec.Scope, ResourceID: spec.ResourceID, Operation: operationLockMutation,
			Owner: currentUser(r).Username, ExpiresAt: time.Now().UTC().Add(operationLockTTL),
			Metadata: operationLockMetadata(map[string]any{"action": "mysql-admin-credential-update", "credentialId": item.ID}),
		})
		if err != nil {
			a.releaseOperationLocks(locks)
			var conflict store.OperationLockConflict
			if errors.As(err, &conflict) {
				writeError(w, http.StatusConflict, "OPERATION_LOCKED", i18n.Text(lang, "api.operationLocked", conflict.Lock.ResourceID), map[string]any{
					"scope": conflict.Lock.Scope, "resourceId": conflict.Lock.ResourceID, "operation": conflict.Lock.Operation,
					"ownerTaskId": conflict.Lock.OwnerTaskID, "expiresAt": conflict.Lock.ExpiresAt,
				})
				return nil, false
			}
			return lockFailure(http.StatusInternalServerError)
		}
		locks = append(locks, lock)
	}
	return locks, true
}

func (a *API) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	item, err := a.store.GetCredential(id, false)
	if err != nil {
		respond(w, nil, err)
		return
	}
	locks, ok := a.acquireMySQLAdminCredentialMutationLocks(w, r, item)
	if !ok {
		return
	}
	defer a.releaseOperationLocks(locks)
	err = a.store.DeleteCredential(id)
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
		if mapping.IDKey == "rootCredentialId" && strings.EqualFold(credential.Kind, "mysql") {
			out[mysqlRootCredentialVersionParameter] = credential.CurrentVersion
		}
	}
	return out, nil
}

func (a *API) bindInstallCredentialReferences(app string, req registry.InstallRequest, log registry.Logger, installStartedAt ...time.Time) error {
	if strings.EqualFold(strings.TrimSpace(app), "mysql") {
		startedAt := time.Time{}
		if len(installStartedAt) == 1 {
			startedAt = installStartedAt[0]
		}
		if err := a.bindMySQLInstallAdminCredentials(req, startedAt); err != nil {
			return errors.New(i18n.Text(req.Language, "api.mysqlInstallCredentialBindingFailed"))
		}
		a.recordInstallClusterMembership(app, req, log)
		return nil
	}
	a.bindSelectedInstallCredentialReferences(app, req)
	a.registerGeneratedInstallCredentials(app, req, log)
	a.recordInstallClusterMembership(app, req, log)
	return nil
}

func (a *API) bindMySQLInstallAdminCredentials(req registry.InstallRequest, startedAt time.Time) error {
	targets := mysqlInstallCredentialTargetServerIDs(req)
	if len(targets) == 0 {
		return store.ErrMySQLInstallAdminCredentialBinding
	}
	selectedID := strings.TrimSpace(paramString(req.Parameters, "rootCredentialId", ""))
	selectedVersion := paramInt(req.Parameters, mysqlRootCredentialVersionParameter, 0)
	spec, generated := generatedInstallCredentialSpecFor("mysql", req.Parameters)
	if selectedID == "" && !generated {
		return store.ErrMySQLInstallAdminCredentialBinding
	}
	items := make([]store.MySQLInstallAdminCredential, 0, len(targets))
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return store.ErrMySQLInstallAdminCredentialBinding
	}
	owned, err := ownedMySQLInstallInstances(req, startedAt, instances)
	if err != nil || len(owned) != len(targets) {
		return store.ErrMySQLInstallAdminCredentialBinding
	}
	for index, serverID := range targets {
		instance := owned[index]
		item := store.MySQLInstallAdminCredential{Instance: instance, Generated: generated}
		if generated {
			endpoint := a.credentialEndpointForInstance(instance, spec)
			item.Credential = store.Credential{
				Name: generatedCredentialName(spec, endpoint, serverID), Kind: "mysql", Username: spec.Username, Endpoint: endpoint,
				ServerID: serverID, Tags: "generated,install", Secret: map[string]string{"password": spec.SecretValue}, CreatedBy: req.Actor,
			}
		} else {
			if selectedVersion <= 0 {
				return store.ErrMySQLInstallAdminCredentialBinding
			}
			item.Credential = store.Credential{
				ID: selectedID, CurrentVersion: selectedVersion,
				Username: paramString(req.Parameters, "rootUser", ""), Secret: map[string]string{"password": paramString(req.Parameters, "rootPassword", "")},
			}
		}
		items = append(items, item)
	}
	if err := a.store.SaveMySQLInstallAdminCredentials(items); err != nil {
		return store.ErrMySQLInstallAdminCredentialBinding
	}
	for _, item := range items {
		credential, err := a.store.GetBoundCredential(item.Instance.ID, "admin", true)
		if err != nil || credential.Kind != "mysql" || credential.Status != "active" || strings.TrimSpace(credential.Username) == "" || strings.TrimSpace(credential.Secret["password"]) == "" {
			return store.ErrMySQLInstallAdminCredentialBinding
		}
	}
	return nil
}

func mysqlInstallCredentialTargetServerIDs(req registry.InstallRequest) []string {
	for _, key := range []string{"mysqlServerIds", "mysqlDataServerIds", "clusterServerIds"} {
		if targets := cleanStringIDs(stringSliceFromRaw(req.Parameters[key])); len(targets) > 0 {
			return targets
		}
	}
	return cleanStringIDs(req.TargetServerIDs())
}

func ownedMySQLInstallInstances(req registry.InstallRequest, startedAt time.Time, instances []store.AppInstance) ([]store.AppInstance, error) {
	targets := mysqlInstallCredentialTargetServerIDs(req)
	topology := normalizedInstallTopology(req.Topology)
	if req.App != "mysql" || startedAt.IsZero() || (topology == "standalone" && len(targets) != 1) || (topology == "innodb-cluster" && len(targets) < 3) {
		return nil, store.ErrMySQLInstallAdminCredentialBinding
	}
	if topology != "standalone" && topology != "innodb-cluster" {
		return nil, store.ErrMySQLInstallAdminCredentialBinding
	}
	owned := make([]store.AppInstance, 0, len(targets))
	seen := map[string]bool{}
	clusterID := ""
	for _, serverID := range targets {
		instance, ok := uniqueInstallInstanceCreatedAfter(instances, req.App, req.Version, serverID, topology, startedAt)
		if !ok || seen[instance.ID] {
			return nil, store.ErrMySQLInstallAdminCredentialBinding
		}
		seen[instance.ID] = true
		if topology == "innodb-cluster" {
			candidateClusterID := mysqlClusterID(instance)
			if candidateClusterID == "" || (clusterID != "" && candidateClusterID != clusterID) {
				return nil, store.ErrMySQLInstallAdminCredentialBinding
			}
			clusterID = candidateClusterID
		}
		owned = append(owned, instance)
	}
	return owned, nil
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
