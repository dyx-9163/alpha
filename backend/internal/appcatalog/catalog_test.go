package appcatalog

import (
	"context"
	"testing"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type fakeModule struct{}

func (fakeModule) Name() string {
	return "docker"
}

func (fakeModule) Manifest(lang string) registry.Manifest {
	return registry.Manifest{
		Name:                  "docker",
		Title:                 "Docker Engine + Compose",
		Icon:                  "D",
		Category:              "devops",
		CategoryLabel:         "DevOps",
		SourceLabel:           "Official binary only",
		FallbackVersion:       "24.0.9",
		Description:           "Install Docker Engine and Compose.",
		InstallName:           "docker",
		ResourceApp:           "docker",
		RequiresServer:        true,
		SupportsMultiTarget:   true,
		BackendReady:          true,
		RequiredResourceParts: []string{"backend"},
	}
}

func (fakeModule) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	return nil
}

func (fakeModule) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	return registry.PreflightResult{}, nil
}

func (fakeModule) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	return nil, nil
}

func (fakeModule) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return nil
}

func TestBuildCatalogRequiresRegisteredBackendModuleAndResource(t *testing.T) {
	catalog := BuildWithModules(
		[]store.Resource{{App: "docker", Version: "24.0.9", Part: "backend", Path: "bundle.tar"}},
		[]registry.Module{fakeModule{}},
		"en",
	)
	docker := catalog["docker"]
	if !docker.Deployable {
		t.Fatalf("expected docker to be deployable: %+v", docker)
	}
	if len(docker.Versions) != 1 || docker.Versions[0] != "24.0.9" {
		t.Fatalf("unexpected versions: %+v", docker.Versions)
	}
	if !docker.Parts["backend"] {
		t.Fatalf("expected backend part to be present: %+v", docker.Parts)
	}
}

type filteredModule struct{}

func (filteredModule) Name() string { return "aifar" }

func (filteredModule) Manifest(lang string) registry.Manifest {
	return registry.Manifest{
		Name:                   "aifar",
		Title:                  "AIFAR Service",
		Icon:                   "AF",
		Category:               "devops",
		CategoryLabel:          "Application",
		SourceLabel:            "Docker Compose bundle",
		InstallName:            "aifar",
		ResourceApp:            "aifar",
		ResourceVersionPattern: "^docker-apps$",
		RequiresServer:         true,
		BackendReady:           true,
		RequiredResourceParts:  []string{"backend"},
	}
}

func (filteredModule) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	return nil
}

func (filteredModule) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	return registry.PreflightResult{}, nil
}

func (filteredModule) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	return nil, nil
}

func (filteredModule) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return nil
}

func TestBuildCatalogFiltersResourceVersions(t *testing.T) {
	resources := []store.Resource{
		{App: "aifar", Version: "docker-apps", Part: "backend", Path: "docker-apps/.env"},
		{App: "aifar", Version: "docker-sql", Part: "backend", Path: "docker-sql/init.sql"},
	}
	catalog := BuildWithModules(resources, []registry.Module{filteredModule{}}, "en")
	item := catalog["aifar"]
	if len(item.Versions) != 1 || item.Versions[0] != "docker-apps" {
		t.Fatalf("expected only docker-apps version, got %+v", item.Versions)
	}
	def := DefinitionFromManifest(filteredModule{}.Manifest("en"))
	selected, matched := ResolveResources(def, resources, "latest")
	if selected != "docker-apps" || len(matched) != 1 || matched[0].Version != "docker-apps" {
		t.Fatalf("expected latest to resolve to docker-apps only, selected=%s matched=%+v", selected, matched)
	}
	_, matched = ResolveResources(def, resources, "docker-sql")
	if len(matched) != 0 {
		t.Fatalf("expected docker-sql to be filtered out, got %+v", matched)
	}
}
