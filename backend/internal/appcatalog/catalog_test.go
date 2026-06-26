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
