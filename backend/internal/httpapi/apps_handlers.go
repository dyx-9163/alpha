package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"aifar-deployment/backend/internal/appcatalog"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskplan"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

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
	parameters := map[string]any{}
	for key, value := range req.Parameters {
		parameters[key] = value
	}
	if req.RemoveMountedDisks != nil {
		parameters["removeMountedDisks"] = *req.RemoveMountedDisks
	}
	parameters[registry.DeleteParamConfirmedWithServerPassword] = true
	task, err := a.tasks.StartWithLanguage("apps."+instance.App+".delete", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		deleteReq := registry.DeleteRequest{
			Instance:   instance,
			Server:     server,
			Language:   lang,
			Actor:      actor,
			Parameters: parameters,
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

type preparedDeleteInstance struct {
	instance     store.AppInstance
	server       store.Server
	deleteModule registry.DeleteModule
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
	task, err := a.tasks.StartWithLanguage(taskType, target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		plannedTargets := map[string]bool{}
		for _, item := range items {
			deleteReq := registry.DeleteRequest{
				Instance:   item.instance,
				Server:     item.server,
				Language:   lang,
				Actor:      actor,
				Parameters: parameters,
			}
			plan, err := item.deleteModule.PlanDelete(ctx, deleteReq)
			if err != nil {
				return err
			}
			for _, step := range plan {
				if step.Target != "" && !plannedTargets[step.Target] {
					log.PlanTarget(step.Target)
					plannedTargets[step.Target] = true
				}
				log.PlanStep(step.Target, step.Name, step.Title, step.Order)
			}
		}
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
				Log: log,
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
		App:        def.Name,
		Version:    req.Version,
		Topology:   req.Topology,
		Language:   lang,
		ServerIDs:  serverIDs,
		Actor:      actor,
		Parameters: req.Parameters,
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
	if err := taskplan.StorePlan(a.store, task.ID, installPlanSteps(plan)); err != nil {
		_ = a.store.DeleteTask(task.ID)
		writeError(w, http.StatusInternalServerError, "INSTALL_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": def.Name})
		return
	}
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
		return module.Install(ctx, moduleReq, registry.RunContext{
			Resources:   resources,
			Log:         log,
			Concurrency: a.store.DeploymentConcurrency(a.cfg.DeploymentConcurrency),
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
