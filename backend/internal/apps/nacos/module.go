package nacos

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
	service Service
}

func init() {
	registry.RegisterFactory("nacos", func(deps registry.Dependencies) registry.Module {
		return NewModule(deps.Store, adapter.SSHRemote{})
	})
}

func NewModule(s Store, remote Remote) Module {
	return Module{service: NewService(s, remote)}
}

func (m Module) Name() string {
	return "nacos"
}

func (m Module) Manifest(lang string) registry.Manifest {
	copy := CopyFor(lang)
	return registry.Manifest{
		Name:                "nacos",
		Title:               "Nacos",
		Icon:                "NA",
		Category:            "devops",
		CategoryLabel:       copy.CategoryLabel,
		SourceLabel:         copy.SourceLabel,
		Description:         copy.Description,
		InstallName:         "nacos",
		ResourceApp:         "nacos",
		RequiresServer:      true,
		SupportsMultiTarget: true,
		BackendReady:        true,
		RequiredResourceParts: []string{
			"backend",
		},
		Topologies: nacosTopologies(lang),
		Capabilities: []string{
			"apps.nacos.install",
			"apps.nacos.delete",
			"apps.nacos.check",
			"resources.nacos.verify",
		},
	}
}

func nacosTopologies(lang string) []registry.Topology {
	if normalizeLanguage(lang) == "en" {
		return []registry.Topology{
			{Name: "standalone", Label: "Standalone", TargetMode: registry.TargetModeSingle, MinTargets: 1, Default: true},
			{Name: "cluster", Label: "Cluster 3 nodes", TargetMode: registry.TargetModeMultiple, MinTargets: 3},
		}
	}
	return []registry.Topology{
		{Name: "standalone", Label: "单体", TargetMode: registry.TargetModeSingle, MinTargets: 1, Default: true},
		{Name: "cluster", Label: "Cluster 3 节点", TargetMode: registry.TargetModeMultiple, MinTargets: 3},
	}
}

func (m Module) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	if ctx.Err() != nil {
		return registry.PreflightResult{}, ctx.Err()
	}
	_, err := SelectBundle(resources, req.Version)
	return registry.PreflightResult{}, err
}

func (m Module) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := CopyFor(req.Language)
	topology := normalizeTopology(req.Topology)
	targets := req.TargetServerIDs()
	if topology == "cluster" {
		targets = clusterServerIDs(req.Parameters, targets)
	}
	steps := nacosInstallSteps(copy)
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
	topology := normalizeTopology(req.Topology)
	targets := req.TargetServerIDs()
	if topology == "cluster" {
		targets = clusterServerIDs(req.Parameters, targets)
	}
	switch topology {
	case "standalone":
		if len(targets) == 0 {
			return errors.New(copy.TargetRequired)
		}
		if len(targets) > 1 {
			return errors.New(copy.SingleTargetOnly)
		}
	case "cluster":
		if len(targets) != 3 {
			return errors.New(copy.ClusterNeedNodes)
		}
	default:
		return fmt.Errorf(copy.TopologyUnsupported, topology)
	}
	options := nacosOptions(req.Parameters, topology)
	resolvedOptions, err := m.service.resolveInstallOptions(options)
	if err != nil {
		return err
	}
	options = resolvedOptions
	if err := options.Validate(); err != nil {
		return err
	}
	bundle, err := SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	return VerifyBundle(bundle)
}

func (m Module) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return m.service.Install(ctx, InstallRequest{
		Version:     req.Version,
		Topology:    req.Topology,
		Language:    req.Language,
		ServerID:    req.ServerID,
		ServerIDs:   req.ServerIDs,
		Parameters:  req.Parameters,
		Concurrency: run.Concurrency,
	}, run.Resources, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanDelete(ctx context.Context, req registry.DeleteRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := DeleteCopyFor(req.Language)
	steps := nacosDeleteSteps(copy)
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
	steps := nacosCheckSteps(copy)
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
