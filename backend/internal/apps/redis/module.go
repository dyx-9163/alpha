package redis

import (
	"context"
	"errors"
	"fmt"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	redisinstaller "aifar-deployment/backend/internal/installer/redis"
	"aifar-deployment/backend/internal/store"
)

type Module struct {
	service         Service
	defaultPassword string
}

func init() {
	registry.RegisterFactory("redis", func(deps registry.Dependencies) registry.Module {
		return NewModule(deps.Store, adapter.SSHRemote{}, deps.DefaultPassword)
	})
}

func NewModule(s Store, remote redisinstaller.Remote, defaultPassword ...string) Module {
	password := ""
	if len(defaultPassword) > 0 {
		password = defaultPassword[0]
	}
	return Module{service: NewService(s, remote), defaultPassword: password}
}

func (m Module) Name() string {
	return "redis"
}

func (m Module) Manifest(lang string) registry.Manifest {
	copy := CopyFor(lang)
	return registry.Manifest{
		Name:                "redis",
		Title:               "Redis",
		Icon:                "RE",
		Category:            "database",
		CategoryLabel:       copy.CategoryLabel,
		SourceLabel:         copy.SourceLabel,
		FallbackVersion:     "7.2.14",
		Description:         copy.Description,
		InstallName:         "redis",
		ResourceApp:         "redis",
		RequiresServer:      true,
		SupportsMultiTarget: true,
		BackendReady:        true,
		RequiredResourceParts: []string{
			"backend",
		},
		Topologies: redisTopologies(lang),
		Capabilities: []string{
			"apps.redis.install",
			"apps.redis.delete",
			"apps.redis.check",
			"resources.redis.verify",
			"databases.redis.register",
		},
	}
}

func redisTopologies(lang string) []registry.Topology {
	if normalizeLanguage(lang) == "en" {
		return []registry.Topology{
			{Name: "standalone", Label: "Standalone", TargetMode: registry.TargetModeSingle, MinTargets: 1, Default: true},
			{Name: "sentinel", Label: "Sentinel", TargetMode: registry.TargetModeMultiple, MinTargets: 3},
			{Name: "cluster", Label: "Cluster", TargetMode: registry.TargetModeMultiple, MinTargets: 3},
		}
	}
	return []registry.Topology{
		{Name: "standalone", Label: "单体", TargetMode: registry.TargetModeSingle, MinTargets: 1, Default: true},
		{Name: "sentinel", Label: "Sentinel", TargetMode: registry.TargetModeMultiple, MinTargets: 3},
		{Name: "cluster", Label: "Cluster", TargetMode: registry.TargetModeMultiple, MinTargets: 3},
	}
}

func (m Module) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	if ctx.Err() != nil {
		return registry.PreflightResult{}, ctx.Err()
	}
	bundle, err := redisinstaller.SelectBundle(resources, req.Version)
	if err != nil {
		return registry.PreflightResult{}, err
	}
	warnings := []string{}
	if len(bundle.RPMPaths) == 0 {
		warnings = append(warnings, i18n.Text(req.Language, "redis.missingRPMWarning"))
	}
	return registry.PreflightResult{Warnings: warnings}, nil
}

func (m Module) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := CopyFor(req.Language)
	topology := normalizeTopology(req.Topology)
	targets := req.TargetServerIDs()
	if topology == "sentinel" {
		roles, err := redisSentinelRoles(req.Parameters, targets, copy)
		if err != nil {
			return nil, err
		}
		plan := make([]registry.InstallStepPlan, 0, len(roles.AllIDs)*len(redisInstallStepsFor(topology, copy)))
		for _, target := range roles.AllIDs {
			steps := redisSentinelStepsForTarget(copy, roles.IsSentinel(target))
			for idx, step := range steps {
				plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
			}
		}
		return plan, nil
	}
	steps := redisInstallStepsFor(topology, copy)
	plan := make([]registry.InstallStepPlan, 0, len(targets)*len(steps))
	for targetIdx, target := range targets {
		for idx, step := range steps {
			if topology == "cluster" && step.Name == "bootstrap-cluster" && targetIdx != 0 {
				continue
			}
			plan = append(plan, registry.InstallStepPlan{Target: target, Name: step.Name, Title: step.Title, Order: idx + 1})
		}
	}
	return plan, nil
}

func (m Module) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	topology := normalizeTopology(req.Topology)
	copy := CopyFor(req.Language)
	targets := req.TargetServerIDs()
	switch topology {
	case "standalone":
		if len(targets) == 0 {
			return errors.New(i18n.Text(req.Language, "redis.targetRequired"))
		}
		if len(targets) > 1 {
			return errors.New(copy.SingleTargetOnly)
		}
	case "sentinel":
		if _, err := redisSentinelMasterName(req.Parameters, copy.SentinelMasterNameInvalid); err != nil {
			return err
		}
		if _, err := redisSentinelRoles(req.Parameters, targets, copy); err != nil {
			return err
		}
	case "cluster":
		if len(targets) == 0 {
			return errors.New(i18n.Text(req.Language, "redis.targetRequired"))
		}
		if len(targets) < 3 {
			return errors.New(copy.ClusterNeedNodes)
		}
	default:
		return errors.New(fmt.Sprintf(copy.TopologyUnsupported, topology))
	}
	bundle, err := redisinstaller.SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	return redisinstaller.VerifyBundle(bundle)
}

func (m Module) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return m.service.Install(ctx, InstallRequest{
		Version:         req.Version,
		Topology:        req.Topology,
		Language:        req.Language,
		ServerID:        req.ServerID,
		ServerIDs:       req.ServerIDs,
		DefaultPassword: req.DefaultPassword,
		Parameters:      req.Parameters,
		Concurrency:     run.Concurrency,
	}, run.Resources, run.Log, func(target string) redisinstaller.Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanDelete(ctx context.Context, req registry.DeleteRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := DeleteCopyFor(req.Language)
	steps := redisDeleteSteps(copy)
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
	}, run.Log, func(target string) redisinstaller.Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanCheck(ctx context.Context, req registry.CheckRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := CopyFor(req.Language)
	steps := redisCheckStepsFor(instanceTopology(req.Instance), copy)
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
		Instance:        req.Instance,
		Server:          req.Server,
		Language:        req.Language,
		DefaultPassword: m.defaultPassword,
	}, run.Log, func(target string) redisinstaller.Logger {
		return run.LoggerForTarget(target)
	})
	return registry.InstanceStatus{Status: result.Status, Message: result.Message, Details: result.Details}, err
}
