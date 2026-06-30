package minio

import (
	"context"
	"errors"
	"fmt"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	minioinstaller "aifar-deployment/backend/internal/installer/minio"
	"aifar-deployment/backend/internal/store"
)

type Module struct {
	service Service
}

func init() {
	registry.RegisterFactory("minio", func(deps registry.Dependencies) registry.Module {
		return NewModule(deps.Store, adapter.SSHRemote{})
	})
}

func NewModule(s Store, remote minioinstaller.Remote) Module {
	return Module{service: NewService(s, remote)}
}

func (m Module) Name() string {
	return "minio"
}

func (m Module) Manifest(lang string) registry.Manifest {
	copy := CopyFor(lang)
	return registry.Manifest{
		Name:                "minio",
		Title:               "MinIO",
		Icon:                "S3",
		Category:            "storage",
		CategoryLabel:       copy.CategoryLabel,
		SourceLabel:         copy.SourceLabel,
		FallbackVersion:     "2025-10-15T17-29-55Z",
		Description:         copy.Description,
		InstallName:         "minio",
		ResourceApp:         "minio",
		RequiresServer:      true,
		SupportsMultiTarget: true,
		BackendReady:        true,
		RequiredResourceParts: []string{
			"backend",
		},
		Topologies: minioTopologies(lang),
		Capabilities: []string{
			"apps.minio.install",
			"apps.minio.delete",
			"resources.minio.verify",
			"storage.minio.register",
		},
	}
}

func minioTopologies(lang string) []registry.Topology {
	if normalizeLanguage(lang) == "en" {
		return []registry.Topology{
			{Name: "standalone", Label: "Standalone", TargetMode: registry.TargetModeSingle, MinTargets: 1, Default: true},
			{Name: "distributed", Label: "Distributed", TargetMode: registry.TargetModeMultiple, MinTargets: 4},
		}
	}
	return []registry.Topology{
		{Name: "standalone", Label: "单体", TargetMode: registry.TargetModeSingle, MinTargets: 1, Default: true},
		{Name: "distributed", Label: "分布式", TargetMode: registry.TargetModeMultiple, MinTargets: 4},
	}
}

func (m Module) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	if ctx.Err() != nil {
		return registry.PreflightResult{}, ctx.Err()
	}
	bundle, err := minioinstaller.SelectBundle(resources, req.Version)
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
	topology := normalizeTopology(req.Topology)
	steps := minioInstallStepsFor(topology, copy)
	targets := req.TargetServerIDs()
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
	targets := req.TargetServerIDs()
	if len(targets) == 0 {
		return errors.New(CopyFor(req.Language).TargetRequired)
	}
	copy := CopyFor(req.Language)
	topology := normalizeTopology(req.Topology)
	switch topology {
	case "standalone":
		if len(targets) > 1 {
			return errors.New(copy.SingleTargetOnly)
		}
	case "distributed":
		if len(targets) < 4 {
			return errors.New(copy.DistributedNeedNodes)
		}
	default:
		return fmt.Errorf(copy.DistributedUnsupported, topology)
	}
	bundle, err := minioinstaller.SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := minioinstaller.VerifyBundle(bundle); err != nil {
		return err
	}
	if err := validateMinioStorage(req.Parameters, targets...); err != nil {
		return err
	}
	return minioOptions(req.Parameters, req.DefaultPassword).Validate()
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
	}, run.Resources, run.Log, func(target string) minioinstaller.Logger {
		return run.LoggerForTarget(target)
	})
}

func (m Module) PlanDelete(ctx context.Context, req registry.DeleteRequest) ([]registry.InstallStepPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	copy := DeleteCopyFor(req.Language)
	steps := minioDeleteSteps(copy)
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
	}, run.Log, func(target string) minioinstaller.Logger {
		return run.LoggerForTarget(target)
	})
}
