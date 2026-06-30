package mysqlrouter

import (
	"context"
	"errors"
	"fmt"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
)

type Module struct {
	service         Service
	defaultPassword string
}

func init() {
	registry.RegisterFactory("mysql-router", func(deps registry.Dependencies) registry.Module {
		return NewModule(deps.Store, adapter.SSHRemote{}, deps.DefaultPassword)
	})
}

func NewModule(s Store, remote Remote, defaultPassword ...string) Module {
	password := ""
	if len(defaultPassword) > 0 {
		password = defaultPassword[0]
	}
	return Module{service: NewService(s, remote), defaultPassword: password}
}

func (m Module) Name() string {
	return "mysql-router"
}

func (m Module) Manifest(lang string) registry.Manifest {
	copy := CopyFor(lang)
	return registry.Manifest{
		Name:                "mysql-router",
		Title:               "MySQL Router",
		Icon:                "MR",
		Category:            "database",
		CategoryLabel:       copy.CategoryLabel,
		SourceLabel:         copy.SourceLabel,
		Description:         copy.Description,
		InstallName:         "mysql-router",
		ResourceApp:         "mysql",
		RequiresServer:      true,
		SupportsMultiTarget: true,
		BackendReady:        true,
		RequiredResourceParts: []string{
			"backend",
		},
		Topologies: mysqlRouterTopologies(lang),
		Capabilities: []string{
			"apps.mysql-router.install",
			"apps.mysql-router.delete",
			"apps.mysql-router.check",
			"resources.mysql.verify",
		},
	}
}

func mysqlRouterTopologies(lang string) []registry.Topology {
	label := "Router"
	if normalizeLanguage(lang) != "en" {
		label = "Router"
	}
	return []registry.Topology{
		{Name: "router", Label: label, TargetMode: registry.TargetModeMultiple, MinTargets: 1, Default: true},
	}
}

func (m Module) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	if ctx.Err() != nil {
		return registry.PreflightResult{}, ctx.Err()
	}
	bundle, err := SelectBundle(resources, req.Version)
	if err != nil {
		return registry.PreflightResult{}, err
	}
	var warnings []string
	if len(bundle.RPMPaths) == 0 {
		warnings = append(warnings, CopyFor(req.Language).MissingRPMWarning)
	}
	return registry.PreflightResult{Warnings: warnings}, nil
}

func (m Module) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := CopyFor(req.Language)
	targets := req.TargetServerIDs()
	if len(targets) == 0 {
		return nil, errors.New(copy.TargetRequired)
	}
	steps := mysqlRouterInstallSteps(copy)
	plan := make([]registry.InstallStepPlan, 0, len(targets)*len(steps))
	for _, target := range targets {
		for idx, step := range steps {
			plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
		}
	}
	return plan, nil
}

func (m Module) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	copy := CopyFor(req.Language)
	if topology := normalizeRouterTopology(req.Topology); topology != "router" {
		return fmt.Errorf(copy.RouterUnsupported, topology)
	}
	if len(req.TargetServerIDs()) == 0 {
		return errors.New(copy.TargetRequired)
	}
	bundle, err := SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	cluster, err := m.service.ResolveCluster(req.Parameters, copy)
	if err != nil {
		return err
	}
	options := mysqlRouterOptions(req.Parameters, fallbackPassword(req.DefaultPassword, m.defaultPassword), cluster)
	return options.Validate()
}

func (m Module) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return m.service.Install(ctx, InstallRequest{
		Version:         req.Version,
		Topology:        req.Topology,
		Language:        req.Language,
		ServerID:        req.ServerID,
		ServerIDs:       req.ServerIDs,
		DefaultPassword: fallbackPassword(req.DefaultPassword, m.defaultPassword),
		Parameters:      req.Parameters,
		Concurrency:     run.Concurrency,
	}, run.Resources, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanDelete(ctx context.Context, req registry.DeleteRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := DeleteCopyFor(req.Language)
	steps := mysqlRouterDeleteSteps(copy)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	plan := make([]registry.InstallStepPlan, 0, len(steps))
	for idx, step := range steps {
		plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
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
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanCheck(ctx context.Context, req registry.CheckRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := CopyFor(req.Language)
	steps := mysqlRouterCheckSteps(copy)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	plan := make([]registry.InstallStepPlan, 0, len(steps))
	for idx, step := range steps {
		plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
	}
	return plan, nil
}

func (m Module) Check(ctx context.Context, req registry.CheckRequest, run registry.RunContext) (registry.InstanceStatus, error) {
	result, err := m.service.Check(ctx, CheckRequest{
		Instance: req.Instance,
		Server:   req.Server,
		Language: req.Language,
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
	return registry.InstanceStatus{Status: result.Status, Message: result.Message, Details: result.Details}, err
}

func fallbackPassword(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
