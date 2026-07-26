package aifar

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type Module struct {
	service Service
}

func init() {
	registry.RegisterFactory(AppName, func(deps registry.Dependencies) registry.Module {
		return NewModule(deps.Store, adapter.SSHRemote{})
	})
}

func NewModule(s Store, remote Remote) Module {
	return Module{service: NewService(s, remote)}
}

func (m Module) Name() string {
	return AppName
}

func (m Module) InstallModules(resources []store.Resource, version, language string) ([]registry.InstallModuleDefinition, error) {
	bundle, err := SelectBundle(resources, version)
	if err != nil {
		return nil, err
	}
	definitions, err := discoverBundleServices(bundle)
	if err != nil {
		return nil, err
	}
	return installModuleDefinitions(definitions, language), nil
}

func (m Module) Manifest(lang string) registry.Manifest {
	copy := copyFor(lang)
	return registry.Manifest{
		Name:                   AppName,
		Title:                  copy.Title,
		Icon:                   "AF",
		Category:               "devops",
		CategoryLabel:          copy.CategoryLabel,
		SourceLabel:            copy.SourceLabel,
		Description:            copy.Description,
		InstallName:            AppName,
		ResourceApp:            resourceApp,
		ResourceVersionPattern: "^" + appBundleVersion + "$",
		RequiresServer:         true,
		SupportsMultiTarget:    false,
		BackendReady:           true,
		RequiredResourceParts:  []string{"backend"},
		Topologies: []registry.Topology{
			{Name: defaultTopology, Label: copy.TopologySingle, TargetMode: registry.TargetModeSingle, MinTargets: 1, Default: true},
		},
		Capabilities: []string{
			"apps.aifar.install",
			"apps.aifar.check",
			"apps.aifar.update-artifact",
			"apps.aifar.delete",
			"servers.credential.use",
			"aifar.runtime-v2.deploy",
		},
	}
}

func (m Module) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	if ctx.Err() != nil {
		return registry.PreflightResult{}, ctx.Err()
	}
	if _, err := SelectBundle(resources, req.Version); err != nil {
		return registry.PreflightResult{}, err
	}
	copy := copyFor(req.Language)
	return registry.PreflightResult{Warnings: []string{copy.DockerRuntimeWarning}}, nil
}

func (m Module) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := copyFor(req.Language)
	target := strings.TrimSpace(req.ServerID)
	if target == "" && len(req.TargetServerIDs()) > 0 {
		target = req.TargetServerIDs()[0]
	}
	steps := installSteps(copy)
	plan := make([]registry.InstallStepPlan, 0, len(steps))
	for idx, step := range steps {
		plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
	}
	return plan, nil
}

func (m Module) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	copy := copyFor(req.Language)
	if len(req.TargetServerIDs()) == 0 {
		return errors.New(copy.TargetRequired)
	}
	if len(req.TargetServerIDs()) > 1 {
		return errors.New(copy.SingleTargetOnly)
	}
	target := req.TargetServerIDs()[0]
	if err := m.service.ensureDockerRuntimeReady(target, copy); err != nil {
		return err
	}
	server, err := m.service.store.GetServer(target, false)
	if err != nil {
		return err
	}
	if _, err := m.service.reusableAIFARInstallInstanceID(target, installRootFromDeployDir(server.DeployDir)); err != nil {
		return err
	}
	if topology := strings.TrimSpace(req.Topology); topology != "" && !strings.EqualFold(topology, defaultTopology) {
		return fmt.Errorf(copy.TopologyUnsupported, topology)
	}
	bundle, err := SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	definitions, err := discoverBundleServices(bundle)
	if err != nil {
		return err
	}
	selected := sliceParam(req.Parameters, "selectedServices", serviceNames(definitions))
	if err := validateSelectedServicesForCatalog(selected, definitions); err != nil {
		return err
	}
	opts := optionsFromParameters(req.Parameters)
	opts.SelectedServices = normalizeSelectedServicesForCatalog(selected, definitions)
	opts, err = m.service.resolveInstallOptions(opts)
	if err != nil {
		return err
	}
	if err := opts.Validate(); err != nil {
		return err
	}
	return nil
}

func (m Module) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return m.service.Install(ctx, InstallRequest{
		Version:    req.Version,
		Topology:   req.Topology,
		Language:   req.Language,
		Actor:      req.Actor,
		TaskID:     run.TaskID,
		ServerID:   firstTarget(req),
		Parameters: req.Parameters,
	}, run.Resources, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanDelete(ctx context.Context, req registry.DeleteRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := deleteCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	steps := deleteSteps(copy)
	plan := make([]registry.InstallStepPlan, 0, len(steps))
	for idx, step := range steps {
		plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
	}
	return plan, nil
}

func (m Module) Delete(ctx context.Context, req registry.DeleteRequest, run registry.RunContext) error {
	if !registry.DeleteConfirmedWithServerPassword(req) {
		return errors.New(deleteCopyFor(req.Language).PasswordConfirmationRequired)
	}
	return m.service.Delete(ctx, DeleteRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
		TaskID:   run.TaskID,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanCheck(ctx context.Context, req registry.CheckRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := checkCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	steps := checkSteps(copy)
	plan := make([]registry.InstallStepPlan, 0, len(steps))
	for idx, step := range steps {
		plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
	}
	return plan, nil
}

func (m Module) Check(ctx context.Context, req registry.CheckRequest, run registry.RunContext) (registry.InstanceStatus, error) {
	status, err := m.service.Check(ctx, CheckRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
	return registry.InstanceStatus{Status: status.Status, Message: status.Message, Details: status.Details}, err
}

func (m Module) PlanArtifactUpdate(ctx context.Context, req registry.ArtifactUpdateRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := updateCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	steps := updateSteps(copy)
	plan := make([]registry.InstallStepPlan, 0, len(steps))
	for idx, step := range steps {
		plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
	}
	return plan, nil
}

func (m Module) ValidateArtifactUpdate(ctx context.Context, req registry.ArtifactUpdateRequest) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return m.service.ValidateArtifactUpdate(ArtifactUpdateRequest{
		Instance:          req.Instance,
		Server:            req.Server,
		Language:          req.Language,
		Actor:             req.Actor,
		ServiceName:       req.ServiceName,
		ArtifactLocalPath: req.ArtifactLocalPath,
		ArtifactFileName:  req.ArtifactFileName,
	})
}

func (m Module) UpdateArtifact(ctx context.Context, req registry.ArtifactUpdateRequest, run registry.RunContext) error {
	return m.service.UpdateArtifact(ctx, ArtifactUpdateRequest{
		Instance:          req.Instance,
		Server:            req.Server,
		Language:          req.Language,
		Actor:             req.Actor,
		ServiceName:       req.ServiceName,
		ArtifactLocalPath: req.ArtifactLocalPath,
		ArtifactFileName:  req.ArtifactFileName,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanArtifactBundleUpdate(ctx context.Context, req registry.ArtifactBundleUpdateRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := updateCopyFor(req.Language)
	_, cleanup, err := m.service.artifactBundleItemsFromRequest(ArtifactBundleUpdateRequest{
		Instance:        req.Instance,
		Server:          req.Server,
		Language:        req.Language,
		Actor:           req.Actor,
		BundleLocalPath: req.BundleLocalPath,
		BundleFileName:  req.BundleFileName,
	}, copy, false)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	steps := updateSteps(copy)
	plan := make([]registry.InstallStepPlan, 0, len(steps))
	for idx, step := range steps {
		plan = append(plan, registry.InstallStepPlan{
			Target: target,
			Name:   step.Name,
			Title:  step.Title,
			Order:  idx + 1,
		})
	}
	return plan, nil
}

func (m Module) ValidateArtifactBundleUpdate(ctx context.Context, req registry.ArtifactBundleUpdateRequest) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return m.service.ValidateArtifactBundleUpdate(ArtifactBundleUpdateRequest{
		Instance:        req.Instance,
		Server:          req.Server,
		Language:        req.Language,
		Actor:           req.Actor,
		BundleLocalPath: req.BundleLocalPath,
		BundleFileName:  req.BundleFileName,
	})
}

func (m Module) UpdateArtifactBundle(ctx context.Context, req registry.ArtifactBundleUpdateRequest, run registry.RunContext) error {
	return m.service.UpdateArtifactBundle(ctx, ArtifactBundleUpdateRequest{
		Instance:        req.Instance,
		Server:          req.Server,
		Language:        req.Language,
		Actor:           req.Actor,
		TaskID:          run.TaskID,
		BundleLocalPath: req.BundleLocalPath,
		BundleFileName:  req.BundleFileName,
		Concurrency:     run.Concurrency,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanArtifactRollback(ctx context.Context, req registry.ArtifactRollbackRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := updateCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	steps := updateSteps(copy)
	plan := make([]registry.InstallStepPlan, 0, len(steps))
	for idx, step := range steps {
		plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
	}
	return plan, nil
}

func (m Module) ValidateArtifactRollback(ctx context.Context, req registry.ArtifactRollbackRequest) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return m.service.ValidateArtifactRollback(ArtifactRollbackRequest{
		Instance:        req.Instance,
		Server:          req.Server,
		Language:        req.Language,
		Actor:           req.Actor,
		TargetReleaseID: req.TargetReleaseID,
		Services:        req.Services,
		Reason:          req.Reason,
		Force:           req.Force,
	})
}

func (m Module) RollbackArtifact(ctx context.Context, req registry.ArtifactRollbackRequest, run registry.RunContext) error {
	return m.service.RollbackArtifact(ctx, ArtifactRollbackRequest{
		Instance:        req.Instance,
		Server:          req.Server,
		Language:        req.Language,
		Actor:           req.Actor,
		TaskID:          run.TaskID,
		TargetReleaseID: req.TargetReleaseID,
		Services:        req.Services,
		Reason:          req.Reason,
		Force:           req.Force,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) ScaleOutService(ctx context.Context, req registry.ServiceScaleOutRequest, run registry.RunContext) error {
	return m.service.ScaleOut(ctx, ScaleOutRequest{
		Instance:    req.Instance,
		Server:      req.Server,
		Language:    req.Language,
		Actor:       req.Actor,
		TaskID:      run.TaskID,
		ServiceName: req.ServiceName,
		Reason:      req.Reason,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) ScaleService(ctx context.Context, req registry.ServiceScaleRequest, run registry.RunContext) error {
	return m.service.ScaleService(ctx, ScaleRequest{
		Instance:    req.Instance,
		Server:      req.Server,
		Language:    req.Language,
		Actor:       req.Actor,
		TaskID:      run.TaskID,
		ServiceName: req.ServiceName,
		Replicas:    req.Replicas,
		Reason:      req.Reason,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) InstallServices(ctx context.Context, req registry.ServiceInstallRequest, run registry.RunContext) error {
	return m.service.InstallServices(ctx, InstallServicesRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
		TaskID:   run.TaskID,
		Services: req.Services,
		Reason:   req.Reason,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) ReconcileRuntime(ctx context.Context, req registry.RuntimeReconcileRequest, run registry.RunContext) error {
	return m.service.ReconcileRuntime(ctx, RuntimeReconcileRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
		TaskID:   run.TaskID,
		Reason:   req.Reason,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) RestartRuntime(ctx context.Context, req registry.RuntimeRestartRequest, run registry.RunContext) error {
	return m.service.RestartRuntime(ctx, RuntimeRestartRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
		TaskID:   run.TaskID,
		Reason:   req.Reason,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) ValidateRuntimeConfig(ctx context.Context, req registry.RuntimeConfigRequest) error {
	return m.service.ValidateRuntimeConfig(ctx, RuntimeConfigRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
		Reason:   req.Reason,
		Config:   req.Config,
	})
}

func (m Module) ApplyRuntimeConfig(ctx context.Context, req registry.RuntimeConfigRequest, run registry.RunContext) error {
	return m.service.ApplyRuntimeConfig(ctx, RuntimeConfigRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
		TaskID:   run.TaskID,
		Reason:   req.Reason,
		Config:   req.Config,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) CleanupRuntimeStalePods(ctx context.Context, req registry.RuntimeCleanupRequest, run registry.RunContext) error {
	return m.service.CleanupRuntimeStalePods(ctx, RuntimeCleanupRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
		TaskID:   run.TaskID,
		Reason:   req.Reason,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) UninstallRuntimeAgent(ctx context.Context, req registry.RuntimeAgentUninstallRequest, run registry.RunContext) error {
	return m.service.UninstallRuntimeAgent(ctx, RuntimeAgentUninstallRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
		Actor:    req.Actor,
		TaskID:   run.TaskID,
		Reason:   req.Reason,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) EstimateRuntimeDiagnostics(ctx context.Context, req registry.RuntimeDiagnosticRequest, run registry.RunContext) (registry.RuntimeDiagnosticEstimateResult, error) {
	return m.service.EstimateRuntimeDiagnostics(ctx, req, run.Log)
}

func firstTarget(req registry.InstallRequest) string {
	targets := req.TargetServerIDs()
	if len(targets) == 0 {
		return ""
	}
	return targets[0]
}
