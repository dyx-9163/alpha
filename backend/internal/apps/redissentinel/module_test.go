package redissentinel

import (
	"context"
	"testing"

	"aifar-deployment/backend/internal/apps/registry"
)

func TestManifestUsesRedisResourceAndSentinelTopology(t *testing.T) {
	manifest := NewModule(nil, nil).Manifest("en")
	if manifest.Name != moduleName {
		t.Fatalf("name = %q, want %q", manifest.Name, moduleName)
	}
	if manifest.InstallName != moduleName {
		t.Fatalf("installName = %q, want %q", manifest.InstallName, moduleName)
	}
	if manifest.ResourceApp != "redis" {
		t.Fatalf("resourceApp = %q, want redis", manifest.ResourceApp)
	}
	if len(manifest.Topologies) != 1 || manifest.Topologies[0].Name != "sentinel" {
		t.Fatalf("topologies = %#v, want sentinel only", manifest.Topologies)
	}
	if !manifest.Topologies[0].Default {
		t.Fatal("sentinel topology should be default")
	}
}

func TestPlanUsesIntegratedSentinelInstallSteps(t *testing.T) {
	plan, err := NewModule(nil, nil).PlanInstall(context.Background(), registry.InstallRequest{
		App:      moduleName,
		Topology: "sentinel",
		Language: "en",
		ServerIDs: []string{
			"srv-2",
			"srv-1",
			"srv-3",
		},
		Parameters: map[string]any{
			"sentinelMasterId": "srv-2",
			"password":         "Oversea.123",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stepByTarget := map[string]map[string]bool{}
	for _, step := range plan {
		if stepByTarget[step.Target] == nil {
			stepByTarget[step.Target] = map[string]bool{}
		}
		stepByTarget[step.Target][step.Name] = true
	}
	for _, target := range []string{"srv-1", "srv-2", "srv-3"} {
		if !stepByTarget[target]["install-redis"] {
			t.Fatalf("expected target %s to install Redis data service: %#v", target, plan)
		}
		if !stepByTarget[target]["configure-sentinel"] {
			t.Fatalf("expected target %s to configure Sentinel: %#v", target, plan)
		}
	}
	if stepByTarget["srv-1"]["verify-redis-base"] || stepByTarget["srv-3"]["install-sentinel-binaries"] {
		t.Fatalf("default Redis Sentinel install should be integrated, not existing-base mode: %#v", plan)
	}
}

func TestPlanCanUseExistingRedisBaseWhenRequested(t *testing.T) {
	plan, err := NewModule(nil, nil).PlanInstall(context.Background(), registry.InstallRequest{
		App:      moduleName,
		Topology: "sentinel",
		Language: "en",
		Parameters: map[string]any{
			"useExistingRedis":  true,
			"sentinelMasterId":  "srv-2",
			"replicaServerIds":  []string{"srv-1"},
			"sentinelServerIds": []string{"srv-2", "srv-1", "srv-3"},
			"password":          "Oversea.123",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stepByTarget := map[string]map[string]bool{}
	for _, step := range plan {
		if stepByTarget[step.Target] == nil {
			stepByTarget[step.Target] = map[string]bool{}
		}
		stepByTarget[step.Target][step.Name] = true
	}
	for _, target := range []string{"srv-1", "srv-2"} {
		if !stepByTarget[target]["verify-redis-base"] {
			t.Fatalf("expected data target %s to verify existing Redis base service: %#v", target, plan)
		}
		if stepByTarget[target]["install-redis"] {
			t.Fatalf("data target %s should not reinstall Redis base service: %#v", target, plan)
		}
	}
	if !stepByTarget["srv-3"]["install-sentinel-binaries"] {
		t.Fatalf("expected sentinel-only target to install Sentinel runtime binaries: %#v", plan)
	}
}
