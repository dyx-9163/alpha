package registry

import "testing"

func TestManifestAllowsMultiTargetBySelectedTopology(t *testing.T) {
	manifest := Manifest{
		SupportsMultiTarget: false,
		Topologies: []Topology{
			{Name: "standalone", TargetMode: TargetModeSingle, Default: true},
			{Name: "cluster", TargetMode: TargetModeMultiple, MinTargets: 3},
		},
	}
	if manifest.AllowsMultiTargetFor("standalone") {
		t.Fatal("standalone topology must not allow multiple targets")
	}
	if !manifest.AllowsMultiTargetFor("cluster") {
		t.Fatal("cluster topology should allow multiple targets")
	}
	if manifest.AllowsMultiTargetFor("") {
		t.Fatal("empty topology should use default standalone and reject multiple targets")
	}
}

func TestManifestFallsBackToLegacyMultiTargetFlagWithoutTopologies(t *testing.T) {
	manifest := Manifest{SupportsMultiTarget: true}
	if !manifest.AllowsMultiTargetFor("anything") {
		t.Fatal("legacy manifest without topology metadata should use SupportsMultiTarget")
	}
}
