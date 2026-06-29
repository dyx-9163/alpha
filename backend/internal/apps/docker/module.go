package docker

import (
	"context"
	"errors"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	dockerinstaller "aifar-deployment/backend/internal/installer/docker"
	"aifar-deployment/backend/internal/store"
)

type Module struct {
	service Service
}

func init() {
	registry.RegisterFactory("docker", func(deps registry.Dependencies) registry.Module {
		return NewModule(deps.Store, adapter.SSHRemote{})
	})
}

func NewModule(s Store, remote dockerinstaller.Remote) Module {
	return Module{service: NewService(s, remote)}
}

func (m Module) Name() string {
	return "docker"
}

func (m Module) Manifest(lang string) registry.Manifest {
	def := Definition(lang)
	return registry.Manifest{
		Name:                  def.Name,
		Title:                 def.Title,
		Icon:                  def.Icon,
		Category:              def.Category,
		CategoryLabel:         def.CategoryLabel,
		SourceLabel:           def.SourceLabel,
		FallbackVersion:       def.FallbackVersion,
		Description:           def.Description,
		InstallName:           def.InstallName,
		ResourceApp:           def.ResourceApp,
		RequiresServer:        def.RequiresServer,
		SupportsMultiTarget:   true,
		BackendReady:          def.BackendReady,
		RequiredResourceParts: def.RequiredResourceParts,
		Topologies: []registry.Topology{
			{Name: "default", Label: def.Title, TargetMode: registry.TargetModeMultiple, MinTargets: 1, Default: true},
		},
		Capabilities: []string{
			"apps.docker.install",
			"servers.credential.use",
			"resources.docker.verify",
		},
	}
}

func (m Module) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	if ctx.Err() != nil {
		return registry.PreflightResult{}, ctx.Err()
	}
	bundle, err := dockerinstaller.SelectBundleWithLanguage(resources, req.Version, req.Language)
	if err != nil {
		return registry.PreflightResult{}, err
	}
	var warnings []string
	if len(req.TargetServerIDs()) > 1 {
		warnings = append(warnings, i18n.Text(req.Language, "docker.batchWarning", len(req.TargetServerIDs())))
	}
	if len(bundle.RPMPaths) == 0 {
		warnings = append(warnings, i18n.Text(req.Language, "docker.missingRPMWarning"))
	}
	return registry.PreflightResult{Warnings: warnings}, nil
}

func (m Module) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := CopyFor(req.Language)
	steps := dockerInstallSteps(copy)
	var plan []registry.InstallStepPlan
	for _, target := range req.TargetServerIDs() {
		for idx, step := range steps {
			plan = append(plan, registry.InstallStepPlan{
				Target: target,
				Name:   step.Name,
				Title:  step.Title,
				Order:  idx + 1,
			})
		}
	}
	return plan, nil
}

func (m Module) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(req.TargetServerIDs()) == 0 {
		return errors.New(i18n.Text(req.Language, "docker.targetRequired"))
	}
	bundle, err := dockerinstaller.SelectBundleWithLanguage(resources, req.Version, req.Language)
	if err != nil {
		return err
	}
	return dockerinstaller.VerifyBundleWithLanguage(bundle, req.Language)
}

func (m Module) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return m.service.Install(ctx, InstallRequest{
		Version:    req.Version,
		Topology:   req.Topology,
		Language:   req.Language,
		ServerIDs:  req.TargetServerIDs(),
		Parameters: req.Parameters,
	}, run.Resources, run.Log, func(target string) dockerinstaller.Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanDelete(ctx context.Context, req registry.DeleteRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := DeleteCopyFor(req.Language)
	steps := dockerDeleteSteps(copy)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
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

func (m Module) Delete(ctx context.Context, req registry.DeleteRequest, run registry.RunContext) error {
	if !registry.DeleteConfirmedWithServerPassword(req) {
		return errors.New(i18n.Text(req.Language, "api.deleteRequiresServerPasswordConfirmation"))
	}
	return m.service.Delete(ctx, DeleteRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
	}, run.Log, func(target string) dockerinstaller.Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanCheck(ctx context.Context, req registry.CheckRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := CheckCopyFor(req.Language)
	steps := dockerCheckSteps(copy)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
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

func (m Module) Check(ctx context.Context, req registry.CheckRequest, run registry.RunContext) (registry.InstanceStatus, error) {
	result, err := m.service.Check(ctx, CheckRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
	}, run.Log, func(target string) dockerinstaller.Logger {
		return run.LoggerForTarget(target)
	})
	return registry.InstanceStatus{
		Status:  result.Status,
		Message: result.Message,
		Details: result.Details,
	}, err
}
