package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"aifar-deployment/backend/internal/appcatalog"
	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskplan"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

func (a *API) requireOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rbac.NormalizeRole(currentUser(r).Role) != "owner" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", i18n.Text(languageFromRequest(r), "api.permissionDenied"), map[string]any{"role": rbac.NormalizeRole(currentUser(r).Role)})
			return
		}
		next(w, r)
	}
}

func (a *API) appsCatalog(w http.ResponseWriter, r *http.Request) {
	resources, err := a.store.ListResources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RESOURCE_LIST_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, appcatalog.BuildWithModules(resources, a.apps.Modules(), languageFromRequest(r)))
}

func (a *API) appInstallModules(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "app")))
	module, ok := a.apps.Get(name)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(languageFromRequest(r), "api.appBackendMissing"), map[string]any{"app": name})
		return
	}
	provider, ok := module.(registry.InstallModuleProvider)
	if !ok {
		writeJSON(w, http.StatusOK, []registry.InstallModuleDefinition{})
		return
	}
	resources, err := a.store.ListResources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RESOURCE_LIST_FAILED", err.Error(), nil)
		return
	}
	modules, err := provider.InstallModules(resources, r.URL.Query().Get("version"), languageFromRequest(r))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "INSTALL_MODULE_DISCOVERY_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, modules)
}

func (a *API) appInstances(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListAppInstances()
	respond(w, out, err)
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
	if code := a.mysqlMaintenanceGate(instance); code != "" {
		writeError(w, http.StatusConflict, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instance.ID})
		return
	}
	lockSpecs, lockSpecErr := validatedAppMutationOperationLockSpecs("delete", []store.AppInstance{instance})
	if lockSpecErr != nil {
		writeError(w, http.StatusConflict, mysqlapp.MySQLBackupClusterUnhealthy, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupClusterUnhealthy), map[string]any{"instanceId": instance.ID})
		return
	}
	if !a.ensureCompleteDeleteSelection(w, lang, []store.AppInstance{instance}) {
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
	parameters := map[string]any{}
	for key, value := range req.Parameters {
		parameters[key] = value
	}
	if req.RemoveMountedDisks != nil {
		parameters["removeMountedDisks"] = *req.RemoveMountedDisks
	}
	parameters[registry.DeleteParamConfirmedWithServerPassword] = true
	deleteReq := registry.DeleteRequest{
		Instance:   instance,
		Server:     server,
		Language:   lang,
		Actor:      actor,
		Parameters: parameters,
	}
	plan, err := deleteModule.PlanDelete(r.Context(), deleteReq)
	if err != nil {
		writeError(w, http.StatusBadRequest, "DELETE_PLAN_FAILED", err.Error(), map[string]any{"app": instance.App, "instanceId": instance.ID})
		return
	}
	taskType := "apps." + instance.App + ".delete"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": instance.App, "instanceId": instance.ID})
		return
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, lockSpecs)
	if !ok {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info(i18n.Text(lang, "api.deleteInstanceRequested"), instance.App, instance.ID)
		if err := deleteModule.Delete(ctx, deleteReq, registry.RunContext{
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		}); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.deleteInstanceCompleted"), instance.App, instance.ID)
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

type preparedDeleteInstance struct {
	instance     store.AppInstance
	server       store.Server
	deleteModule registry.DeleteModule
	plan         []registry.InstallStepPlan
}

func (a *API) deleteAppInstances(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req deleteAppInstancesRequest
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
	passwords := req.ServerPasswords
	if len(passwords) == 0 {
		passwords = req.Passwords
	}
	items := make([]preparedDeleteInstance, 0, len(ids))
	targets := make([]string, 0, len(ids))
	apps := map[string]bool{}
	seenTargets := map[string]bool{}
	for _, id := range ids {
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
		password := strings.TrimSpace(passwords[server.ID])
		if password == "" {
			writeError(w, http.StatusBadRequest, "SERVER_PASSWORD_REQUIRED", i18n.Text(lang, "api.serverPasswordRequired"), map[string]any{"serverId": server.ID})
			return
		}
		if password != strings.TrimSpace(server.Password) {
			writeError(w, http.StatusForbidden, "SERVER_PASSWORD_INVALID", i18n.Text(lang, "api.serverPasswordInvalid"), map[string]any{"serverId": server.ID})
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
		items = append(items, preparedDeleteInstance{instance: instance, server: server, deleteModule: deleteModule})
		apps[instance.App] = true
		if !seenTargets[server.ID] {
			seenTargets[server.ID] = true
			targets = append(targets, server.ID)
		}
	}
	selectedInstances := make([]store.AppInstance, 0, len(items))
	for _, item := range items {
		selectedInstances = append(selectedInstances, item.instance)
	}
	if !a.ensureCompleteDeleteSelection(w, lang, selectedInstances) {
		return
	}

	actor := currentUser(r).Username
	target := strings.Join(targets, ",")
	taskType := "apps.delete.batch"
	if len(apps) == 1 {
		taskType = "apps." + items[0].instance.App + ".delete"
	}
	parameters := map[string]any{}
	for key, value := range req.Parameters {
		parameters[key] = value
	}
	if req.RemoveMountedDisks != nil {
		parameters["removeMountedDisks"] = *req.RemoveMountedDisks
	}
	parameters[registry.DeleteParamConfirmedWithServerPassword] = true
	var combinedPlan []registry.InstallStepPlan
	for index := range items {
		deleteReq := registry.DeleteRequest{
			Instance:   items[index].instance,
			Server:     items[index].server,
			Language:   lang,
			Actor:      actor,
			Parameters: parameters,
		}
		plan, err := items[index].deleteModule.PlanDelete(r.Context(), deleteReq)
		if err != nil {
			writeError(w, http.StatusBadRequest, "DELETE_PLAN_FAILED", err.Error(), map[string]any{"app": items[index].instance.App, "instanceId": items[index].instance.ID})
			return
		}
		items[index].plan = plan
		combinedPlan = append(combinedPlan, plan...)
	}
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, combinedPlan); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_PLAN_STORE_FAILED", err.Error(), map[string]any{"target": target})
		return
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, appMutationOperationLockSpecs("delete", selectedInstances))
	if !ok {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info(i18n.Text(lang, "api.deleteInstancesRequested"), len(items), target)
		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			deleteReq := registry.DeleteRequest{
				Instance:   item.instance,
				Server:     item.server,
				Language:   lang,
				Actor:      actor,
				Parameters: parameters,
			}
			log.Info(i18n.Text(lang, "api.deleteInstanceRequested"), item.instance.App, item.instance.ID)
			if err := item.deleteModule.Delete(ctx, deleteReq, registry.RunContext{
				TaskID: log.TaskID(),
				Log:    log,
				TargetLog: func(target string) registry.Logger {
					return log.Target(target)
				},
			}); err != nil {
				return err
			}
			log.Info(i18n.Text(lang, "api.deleteInstanceCompleted"), item.instance.App, item.instance.ID)
		}
		log.Info(i18n.Text(lang, "api.deleteInstancesCompleted"), len(items))
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

func (a *API) checkAppInstance(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	id := chi.URLParam(r, "id")
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if code := a.mysqlMaintenanceGate(instance); code != "" {
		writeError(w, http.StatusConflict, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instance.ID})
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
	checkReq := registry.CheckRequest{
		Instance: instance,
		Server:   server,
		Language: lang,
		Actor:    actor,
	}
	plan, err := checkModule.PlanCheck(r.Context(), checkReq)
	if err != nil {
		writeError(w, http.StatusBadRequest, "CHECK_PLAN_FAILED", err.Error(), map[string]any{"app": instance.App, "instanceId": instance.ID})
		return
	}
	taskType := "apps." + instance.App + ".check"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "CHECK_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": instance.App, "instanceId": instance.ID})
		return
	}
	var locks []store.OperationLock
	if strings.EqualFold(strings.TrimSpace(instance.App), "mysql") {
		var acquired bool
		locks, acquired = a.acquireTaskOperationLocks(w, lang, task, mysqlClusterOperationLockSpecs("mysql-check", instance))
		if !acquired {
			return
		}
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info(i18n.Text(lang, "api.checkInstanceRequested"), instance.App, instance.ID)
		status, err := checkModule.Check(ctx, checkReq, registry.RunContext{
			TaskID: task.ID,
			Log:    log,
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
	if err != nil {
		a.releaseOperationLocks(locks)
	}
	if err == nil {
		a.audit(r, taskType, target, "running", task.ID)
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

func (a *API) installAppName(w http.ResponseWriter, r *http.Request, app string) {
	req := decodeInstallAppRequest(r)
	resolvedParameters, err := a.resolveCredentialParameters(r, req.Parameters)
	if err != nil {
		writeError(w, http.StatusBadRequest, "CREDENTIAL_RESOLVE_FAILED", err.Error(), nil)
		return
	}
	req.Parameters = resolvedParameters
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
	if conflicts, err := a.installConflicts(def.Name, serverIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "APP_INSTANCE_LIST_FAILED", err.Error(), map[string]any{"app": def.Name})
		return
	} else if len(conflicts) > 0 {
		writeError(w, http.StatusConflict, "APP_ALREADY_INSTALLED", i18n.Text(lang, "api.appAlreadyInstalled", def.Name, strings.Join(conflicts, ", ")), map[string]any{"app": def.Name, "servers": conflicts})
		return
	}
	if err := requireExplicitInstallPasswords(def.Name, lang, req.Parameters); err != nil {
		writeError(w, http.StatusBadRequest, "INSTALL_PASSWORD_REQUIRED", err.Error(), map[string]any{"app": def.Name})
		return
	}
	target := strings.Join(serverIDs, ",")
	actor := currentUser(r).Username
	resources, err := a.store.ListResources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RESOURCE_LIST_FAILED", err.Error(), nil)
		return
	}
	missing := appcatalog.MissingForInstall(def, resources, req.Version)
	if len(missing) > 0 {
		writeError(w, http.StatusConflict, "APP_NOT_DEPLOYABLE", fmt.Sprintf(i18n.Text(lang, "api.appNotDeployable"), def.Name, strings.Join(missing, ", ")), map[string]any{"app": def.Name, "missing": missing})
		return
	}
	_, matched := appcatalog.ResolveResources(def, resources, req.Version)
	moduleReq := registry.InstallRequest{
		App:             def.Name,
		Version:         req.Version,
		Topology:        req.Topology,
		Language:        lang,
		ServerIDs:       serverIDs,
		Actor:           actor,
		DefaultPassword: a.cfg.DefaultPassword,
		Parameters:      req.Parameters,
	}
	if len(serverIDs) == 1 {
		moduleReq.ServerID = serverIDs[0]
	}
	preflight, err := module.PreflightInstall(r.Context(), moduleReq, resources)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INSTALL_PREFLIGHT_FAILED", err.Error(), map[string]any{"app": def.Name})
		return
	}
	plan, err := module.PlanInstall(r.Context(), moduleReq, resources)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INSTALL_PLAN_FAILED", err.Error(), map[string]any{"app": def.Name})
		return
	}
	if err := module.ValidateInstall(r.Context(), moduleReq, resources); err != nil {
		writeError(w, http.StatusBadRequest, "INSTALL_VALIDATE_FAILED", err.Error(), map[string]any{"app": def.Name})
		return
	}
	task, err := a.store.CreateTask(store.Task{Type: "apps." + def.Name + ".install", Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "INSTALL_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": def.Name})
		return
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, appInstallOperationLockSpecs(def.Name, serverIDs))
	if !ok {
		return
	}
	installStartedAt := task.CreatedAt
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info(i18n.Text(lang, "api.installAccepted"), def.Name, req.Version)
		log.Info(i18n.Text(lang, "api.installTargets"), target)
		log.Info(i18n.Text(lang, "api.resourceRoot"), a.cfg.ResourceDir)
		for _, res := range matched {
			log.Info(i18n.Text(lang, "api.resourceFound"), res.Part, res.Path)
		}
		for _, warning := range preflight.Warnings {
			log.Info(i18n.Text(lang, "api.preflightWarning"), warning)
		}
		if len(plan) > 0 {
			log.Info(i18n.Text(lang, "api.installPlanPrepared"), len(plan))
		}
		if err := module.Install(ctx, moduleReq, registry.RunContext{
			TaskID:      task.ID,
			Resources:   resources,
			Log:         log,
			Concurrency: a.store.DeploymentConcurrency(a.cfg.DeploymentConcurrency),
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		}); err != nil {
			if count, recordErr := a.recordFailedInstallInstances(ctx, moduleReq, installStartedAt, task.ID, err); recordErr != nil {
				log.Error(i18n.Text(lang, "api.installFailedInstanceRecordFailed"), recordErr)
			} else if count > 0 {
				log.Info(i18n.Text(lang, "api.installFailedInstanceRecorded"), count)
			}
			return err
		}
		a.bindInstallCredentialReferences(def.Name, moduleReq, log)
		return nil
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	}
	if err == nil {
		a.audit(r, "apps."+def.Name+".install", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) storeInstallPlanOrDelete(taskID string, plan []registry.InstallStepPlan) error {
	return a.storeTaskPlanOrDelete(taskID, installPlanSteps(plan))
}

func (a *API) storeTaskPlanOrDelete(taskID string, plan []taskplan.Step) error {
	if err := taskplan.StorePlan(a.store, taskID, plan); err != nil {
		_ = a.store.DeleteTask(taskID)
		return err
	}
	return nil
}

func installPlanSteps(plan []registry.InstallStepPlan) []taskplan.Step {
	out := make([]taskplan.Step, 0, len(plan))
	for _, step := range plan {
		out = append(out, taskplan.Step{
			Target: step.Target,
			Name:   step.Name,
			Title:  step.Title,
			Order:  step.Order,
		})
	}
	return out
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

func (a *API) installConflicts(app string, serverIDs []string) ([]string, error) {
	if len(serverIDs) == 0 {
		return nil, nil
	}
	targets := map[string]bool{}
	for _, id := range serverIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			targets[id] = true
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}
	related := lifecycleRawAppNames(app)
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, instance := range instances {
		serverID := strings.TrimSpace(instance.ServerID)
		if serverID == "" || !targets[serverID] || !related[strings.ToLower(strings.TrimSpace(instance.App))] {
			continue
		}
		if !seen[serverID] {
			seen[serverID] = true
			out = append(out, serverID)
		}
	}
	return out, nil
}

func (a *API) ensureCompleteDeleteSelection(w http.ResponseWriter, lang string, selected []store.AppInstance) bool {
	if len(selected) == 0 {
		return true
	}
	all, err := a.store.ListAppInstances()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "APP_INSTANCE_LIST_FAILED", err.Error(), nil)
		return false
	}
	selectedIDs := map[string]bool{}
	for _, instance := range selected {
		selectedIDs[instance.ID] = true
	}
	groups := map[string][]store.AppInstance{}
	for _, instance := range all {
		if key := lifecycleDeleteGroupKey(instance); key != "" {
			groups[key] = append(groups[key], instance)
		}
	}
	for _, instance := range selected {
		key := lifecycleDeleteGroupKey(instance)
		if key == "" {
			continue
		}
		members := groups[key]
		if len(members) <= 1 {
			continue
		}
		var missing []string
		var required []string
		for _, member := range members {
			required = append(required, member.ID)
			if !selectedIDs[member.ID] {
				missing = append(missing, member.ID)
			}
		}
		if len(missing) > 0 {
			writeError(w, http.StatusConflict, "APP_CLUSTER_DELETE_REQUIRED", i18n.Text(lang, "api.appClusterDeleteRequired", lifecycleAppFamily(instance.App), len(members)), map[string]any{
				"app":         lifecycleAppFamily(instance.App),
				"group":       key,
				"requiredIds": required,
				"missingIds":  missing,
			})
			return false
		}
	}
	return true
}

func lifecycleRawAppNames(app string) map[string]bool {
	family := lifecycleAppFamily(app)
	if family == "mysql" {
		return map[string]bool{"mysql": true, "mysql-router": true}
	}
	return map[string]bool{family: true}
}

func lifecycleAppFamily(app string) string {
	app = strings.ToLower(strings.TrimSpace(app))
	if app == "mysql-router" {
		return "mysql"
	}
	return app
}

func lifecycleDeleteGroupKey(instance store.AppInstance) string {
	metadata := appInstanceMetadata(instance)
	groupID := metadataString(metadata, "clusterId")
	if groupID == "" {
		groupID = metadataString(metadata, "replicationGroupId")
	}
	if groupID == "" {
		groupID = metadataString(metadata, "replicaGroupId")
	}
	if groupID == "" {
		return ""
	}
	family := lifecycleAppFamily(instance.App)
	switch family {
	case "mysql", "redis", "minio", "nacos":
		return family + ":" + groupID
	default:
		return ""
	}
}

func appInstanceMetadata(instance store.AppInstance) map[string]any {
	var metadata map[string]any
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil || metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
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

func cleanStringIDs(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func requireExplicitInstallPasswords(app, lang string, params map[string]any) error {
	var missing []string
	requireAny := func(field string, keys ...string) {
		for _, key := range keys {
			if strings.TrimSpace(stringFromRaw(params[key])) != "" {
				return
			}
		}
		missing = append(missing, field)
	}
	switch strings.ToLower(strings.TrimSpace(app)) {
	case "mysql", "mysql-router":
		requireAny("rootPassword", "rootPassword", "password", "mysqlPassword")
	case "redis":
		requireAny("password", "password", "redisPassword")
	case "minio":
		requireAny("rootPassword", "rootPassword", "password", "minioPassword")
	case "nacos":
		requireAny("nacosPassword", "nacosPassword")
		dbSource := strings.TrimSpace(stringFromRaw(params["dbSource"]))
		if dbSource != "" && !strings.EqualFold(dbSource, "local") && !strings.EqualFold(dbSource, "embedded") && !strings.EqualFold(dbSource, "none") {
			requireAny("dbPassword", "dbPassword")
		}
	case "aifar":
		requireAny("nacosPassword", "nacosPassword")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", i18n.Text(lang, "api.installPasswordRequired"), strings.Join(missing, ", "))
}
