package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestModuleManifestAndValidation(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "aifar-docker-static-24.0.9-linux-x86_64.tar")
	if err := os.WriteFile(archive, []byte("bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	module := NewModule(&fakeStore{servers: map[string]store.Server{}}, &fakeRemote{})
	manifest := module.Manifest("en")
	if manifest.Name != "docker" || !manifest.SupportsMultiTarget || !manifest.BackendReady {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	err := module.ValidateInstall(context.Background(), registry.InstallRequest{Version: "24.0.9"}, []store.Resource{{App: "docker", Part: "backend", Version: "24.0.9", Path: archive}})
	if err == nil {
		t.Fatal("expected target server validation error")
	}
	err = module.ValidateInstall(context.Background(), registry.InstallRequest{Version: "24.0.9", ServerIDs: []string{"srv-1"}}, []store.Resource{{App: "docker", Part: "backend", Version: "24.0.9", Path: archive}})
	if err != nil {
		t.Fatal(err)
	}
}
