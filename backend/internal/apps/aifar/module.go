package aifar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
			"apps.aifar.delete",
			"servers.credential.use",
			"docker.compose.deploy",
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
	opts := optionsFromParameters(req.Parameters)
	opts, err = m.service.resolveInstallOptions(opts)
	if err != nil {
		return err
	}
	if err := opts.Validate(); err != nil {
		return err
	}
	if opts.InitSQL {
		sqlPath := filepath.Join(bundle.SQLDir, "aifar_cloud_nacos.sql")
		if _, err := os.Stat(sqlPath); err != nil {
			return fmt.Errorf("SQL initialization requires %s: %w", sqlPath, err)
		}
	}
	return nil
}

func (m Module) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return m.service.Install(ctx, InstallRequest{
		Version:    req.Version,
		Topology:   req.Topology,
		Language:   req.Language,
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
	}, run.Log, func(target string) Logger {
		return run.LoggerForTarget(target)
	})
	return registry.InstanceStatus{Status: status.Status, Message: status.Message, Details: status.Details}, err
}

func firstTarget(req registry.InstallRequest) string {
	targets := req.TargetServerIDs()
	if len(targets) == 0 {
		return ""
	}
	return targets[0]
}
