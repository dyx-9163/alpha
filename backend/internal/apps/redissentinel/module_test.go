package redissentinel

import "testing"

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
