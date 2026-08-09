package runtimeagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBootstrapRuntimeSplitsLegacySpecWithoutNacos(t *testing.T) {
	stateDir := t.TempDir()
	manager := NewManager(ManagerOptions{StateDir: stateDir, Runner: &fakeRunner{}})
	acceptance, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !acceptance.Accepted || len(acceptance.Deployments) != 2 {
		t.Fatalf("acceptance=%+v", acceptance)
	}
	store := ManifestStore{StateDir: stateDir}
	config, err := store.GetInstance("admin")
	if err != nil {
		t.Fatal(err)
	}
	if config.InstanceID != "admin" || config.InstallRoot != "/aifar/apps/admin" {
		t.Fatalf("config=%+v", config)
	}
	manifest, err := store.Get("admin", "permission")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Metadata.Generation != 1 || manifest.Spec.ServiceName != "permission" {
		t.Fatalf("manifest=%+v", manifest)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "admin", "deployments", "permission.json"))
	if err != nil {
		t.Fatal(err)
	}
	if containsAny(string(data), "nacos", "secret") {
		t.Fatalf("deployment copied discovery data: %s", data)
	}
	if _, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec()); !errors.Is(err, ErrLegacyRuntimeSpecDisabled) {
		t.Fatalf("second bootstrap error=%v", err)
	}
	if err := manager.EnsureLegacyRuntimeSpecEnabled("admin"); !errors.Is(err, ErrLegacyRuntimeSpecDisabled) {
		t.Fatalf("legacy write error=%v", err)
	}
}

func TestBootstrapRuntimeConcurrentOnlyOneSucceeds(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: &fakeRunner{}})
	var successes atomic.Int32
	var disabled atomic.Int32
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec())
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrLegacyRuntimeSpecDisabled):
				disabled.Add(1)
			default:
				t.Errorf("bootstrap: %v", err)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 || disabled.Load() != 7 {
		t.Fatalf("success=%d disabled=%d", successes.Load(), disabled.Load())
	}
}

func TestBootstrapRuntimePartialFailureLeavesLegacyEnabledAndRetries(t *testing.T) {
	stateDir := t.TempDir()
	var deploymentRenames atomic.Int32
	store := &ManifestStore{StateDir: stateDir, renameFile: func(oldPath, newPath string) error {
		if filepath.Base(filepath.Dir(newPath)) == "deployments" && deploymentRenames.Add(1) == 2 {
			return errors.New("injected deployment rename failure")
		}
		return os.Rename(oldPath, newPath)
	}}
	manager := NewManager(ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: &fakeRunner{}})
	if _, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec()); err == nil {
		t.Fatal("injected bootstrap failure was ignored")
	}
	if err := manager.EnsureLegacyRuntimeSpecEnabled("admin"); err != nil {
		t.Fatalf("legacy model disabled after partial failure: %v", err)
	}
	store.renameFile = os.Rename
	manager.manifestStore = *store
	acceptance, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec())
	if err != nil || !acceptance.Accepted {
		t.Fatalf("retry acceptance=%+v err=%v", acceptance, err)
	}
	for _, service := range []string{"gateway", "permission"} {
		if _, err := store.Get("admin", service); err != nil {
			t.Fatalf("missing %s after retry: %v", service, err)
		}
	}
}

func TestBootstrapRuntimeMarkerSyncFailureLeavesCompleteNewModel(t *testing.T) {
	stateDir := t.TempDir()
	store := &ManifestStore{StateDir: stateDir, syncDirectory: func(path string) error {
		marker := filepath.Join(stateDir, "admin", "instance.json")
		if filepath.Clean(path) == filepath.Join(stateDir, "admin") {
			if _, err := os.Lstat(marker); err == nil {
				return errors.New("injected marker directory sync failure")
			}
		}
		return nil
	}}
	manager := NewManager(ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: &fakeRunner{}})
	if _, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec()); err == nil {
		t.Fatal("marker durability failure was ignored")
	}
	if err := manager.EnsureLegacyRuntimeSpecEnabled("admin"); !errors.Is(err, ErrLegacyRuntimeSpecDisabled) {
		t.Fatalf("completed marker did not fail closed: %v", err)
	}
	for _, service := range []string{"gateway", "permission"} {
		if _, err := store.Get("admin", service); err != nil {
			t.Fatalf("new model is incomplete for %s: %v", service, err)
		}
	}
}

func TestBootstrapRuntimeRejectsLegacyNormalizationLoss(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: &fakeRunner{}})
	spec := legacyBootstrapTestSpec()
	spec.Deployments[0].Replicas = -1
	if _, err := manager.BootstrapLegacyRuntime(context.Background(), spec); err == nil {
		t.Fatal("negative replicas were silently normalized")
	}
	if err := manager.EnsureLegacyRuntimeSpecEnabled("admin"); err != nil {
		t.Fatalf("invalid bootstrap switched the marker: %v", err)
	}
}

func legacyBootstrapTestSpec() LegacyRuntimeSpec {
	return NormalizeSpec(LegacyRuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Ingress:     IngressSpec{GatewayService: "gateway", WebService: "gateway", GatewayPort: 38000, WebPort: 8080},
		Nacos:       NacosSpec{Namespace: "prod", Group: "SECRET-GROUP"},
		Services: []ServiceSpec{
			{Name: "gateway", AppName: "gateway", Port: 8080},
			{Name: "permission", AppName: "permission", Port: 8081},
		},
		Deployments: []DeploymentSpec{
			{ServiceName: "gateway", Image: "gateway:rev-1", PodRevision: "rev-1", Replicas: 1},
			{ServiceName: "permission", Image: "permission:rev-1", PodRevision: "rev-1", Replicas: 1},
		},
	})
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if len(needle) > 0 && strings.Contains(strings.ToLower(value), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}
