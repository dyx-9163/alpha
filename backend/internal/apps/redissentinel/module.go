package redissentinel

import (
	"context"
	"strings"

	"aifar-deployment/backend/internal/adapter"
	redisapp "aifar-deployment/backend/internal/apps/redis"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

const moduleName = "redis-sentinel"

type Module struct {
	redis redisapp.Module
}

func init() {
	registry.RegisterFactory(moduleName, func(deps registry.Dependencies) registry.Module {
		return NewModule(deps.Store, adapter.SSHRemote{}, deps.DefaultPassword)
	})
}

func NewModule(s redisapp.Store, remote redisapp.Remote, defaultPassword ...string) Module {
	return Module{redis: redisapp.NewModule(s, remote, defaultPassword...)}
}

func (m Module) Name() string {
	return moduleName
}

func (m Module) Manifest(lang string) registry.Manifest {
	copy := redisapp.CopyFor(lang)
	title := "Redis Sentinel"
	description := "Install and configure Redis Sentinel for Redis base services from the Redis offline source archive."
	if normalizeLanguage(lang) != "en" {
		title = "Redis 哨兵"
		description = "基于 Redis 离线源码包安装并配置 Redis Sentinel，高可用监控独立于 Redis 基础服务安装。"
	}
	return registry.Manifest{
		Name:                moduleName,
		Title:               title,
		Icon:                "RS",
		Category:            "database",
		CategoryLabel:       copy.CategoryLabel,
		SourceLabel:         copy.SourceLabel,
		Description:         description,
		InstallName:         moduleName,
		ResourceApp:         "redis",
		RequiresServer:      true,
		SupportsMultiTarget: true,
		BackendReady:        true,
		RequiredResourceParts: []string{
			"backend",
		},
		Topologies: []registry.Topology{
			{Name: "sentinel", Label: "Sentinel", TargetMode: registry.TargetModeMultiple, MinTargets: 3, Default: true},
		},
		Capabilities: []string{
			"apps.redis-sentinel.install",
			"apps.redis.delete",
			"apps.redis.check",
			"resources.redis.verify",
			"databases.redis.register",
		},
	}
}

func (m Module) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	return m.redis.PreflightInstall(ctx, sentinelInstallRequest(req), resources)
}

func (m Module) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	return m.redis.PlanInstall(ctx, sentinelInstallRequest(req), resources)
}

func (m Module) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	return m.redis.ValidateInstall(ctx, sentinelInstallRequest(req), resources)
}

func (m Module) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return m.redis.Install(ctx, sentinelInstallRequest(req), run)
}

func (m Module) PlanDelete(ctx context.Context, req registry.DeleteRequest) ([]registry.InstallStepPlan, error) {
	return m.redis.PlanDelete(ctx, req)
}

func (m Module) Delete(ctx context.Context, req registry.DeleteRequest, run registry.RunContext) error {
	return m.redis.Delete(ctx, req, run)
}

func (m Module) PlanCheck(ctx context.Context, req registry.CheckRequest) ([]registry.InstallStepPlan, error) {
	return m.redis.PlanCheck(ctx, req)
}

func (m Module) Check(ctx context.Context, req registry.CheckRequest, run registry.RunContext) (registry.InstanceStatus, error) {
	return m.redis.Check(ctx, req, run)
}

func sentinelInstallRequest(req registry.InstallRequest) registry.InstallRequest {
	req.App = moduleName
	req.Topology = "sentinel"
	return req
}

func normalizeLanguage(lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en") {
		return "en"
	}
	return "zh"
}
