package runtimeagent

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestClassifyDeploymentAcceptanceFilesystemErrors(t *testing.T) {
	testCases := []struct {
		name       string
		targetPath string
		pathErr    error
		mode       os.FileMode
		mutate     func(*DeploymentManifest)
		want       error
		wantCause  error
		accepted   bool
	}{
		{name: "install root permission", targetPath: "/aifar/apps/app_123", pathErr: os.ErrPermission, want: errAgentStatePersistence, wantCause: os.ErrPermission},
		{name: "env leaf permission", targetPath: "/aifar/apps/app_123/runtime/env/permission.env", pathErr: os.ErrPermission, want: errAgentStatePersistence, wantCause: os.ErrPermission},
		{name: "volume parent EIO", targetPath: "/aifar/apps/app_123/runtime/logs", pathErr: &os.PathError{Op: "lstat", Path: "/secret/device", Err: syscall.EIO}, want: errAgentStatePersistence, wantCause: syscall.EIO},
		{name: "symlink parent", targetPath: "/aifar/apps/app_123/runtime", mode: os.ModeSymlink | 0o777, want: ErrInvalidDeploymentManifest},
		{name: "non directory parent", targetPath: "/aifar/apps/app_123/runtime", mode: 0o600, want: ErrInvalidDeploymentManifest},
		{name: "lexical escape", mutate: func(manifest *DeploymentManifest) { manifest.Spec.EnvFiles = []string{"/outside/permission.env"} }, want: ErrInvalidDeploymentManifest},
		{name: "ENOENT safe suffix", targetPath: "/aifar/apps/app_123/runtime/env/permission.env", pathErr: os.ErrNotExist, accepted: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			plainStore := ManifestStore{StateDir: stateDir}
			if err := plainStore.PutInstance(testInstanceConfig()); err != nil {
				t.Fatal(err)
			}
			observer := func(name string) (os.FileInfo, error) {
				if tc.targetPath != "" && path.Clean(name) == path.Clean(tc.targetPath) {
					if tc.pathErr != nil {
						return nil, tc.pathErr
					}
					return fakeManifestFileInfo{name: path.Base(name), mode: tc.mode}, nil
				}
				return fakeManifestFileInfo{name: path.Base(name), mode: os.ModeDir | 0o750}, nil
			}
			store := &ManifestStore{StateDir: stateDir, manifestPathLstat: observer}
			manager := NewManager(ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: &fakeRunner{}})
			manifest := testManifest("permission", 1, 1)
			if tc.mutate != nil {
				tc.mutate(&manifest)
			}
			_, acceptErr := manager.AcceptDeployment(context.Background(), manifest)
			if tc.accepted {
				if acceptErr != nil {
					t.Fatalf("safe ENOENT suffix rejected: %v", acceptErr)
				}
				return
			}
			if acceptErr == nil {
				t.Fatal("filesystem failure was accepted")
			}
			classified := manager.ClassifyDeploymentAcceptanceError(manifest, acceptErr)
			if !errors.Is(classified, tc.want) {
				t.Fatalf("classification=%v want errors.Is %v", classified, tc.want)
			}
			if tc.wantCause != nil && !errors.Is(classified, tc.wantCause) {
				t.Fatalf("classification lost operational cause %v: %v", tc.wantCause, classified)
			}
		})
	}
}

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
	entries, err := os.ReadDir(filepath.Join(stateDir, "admin", "deployments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != "gateway.json" || entries[1].Name() != "permission.json" {
		t.Fatalf("new deployment directory contains unexpected files: %v", entries)
	}
	for _, name := range []string{legacyBootstrapStageDirectory, legacyBootstrapBackupDirectory, legacyBootstrapMarkerFile} {
		if _, err := os.Lstat(filepath.Join(stateDir, "admin", name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("bootstrap artifact %s remains: %v", name, err)
		}
	}
	if _, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec()); !errors.Is(err, ErrLegacyRuntimeSpecDisabled) {
		t.Fatalf("second bootstrap error=%v", err)
	}
	if err := manager.EnsureLegacyRuntimeSpecEnabled("admin"); !errors.Is(err, ErrLegacyRuntimeSpecDisabled) {
		t.Fatalf("legacy write error=%v", err)
	}
}

func TestRestartLegacyRuntimeSpecIsSerializedWithModelSwitch(t *testing.T) {
	stateDir := t.TempDir()
	runner := &blockingLegacyRestartRunner{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	manager := NewManager(ManagerOptions{StateDir: stateDir, Runner: runner})
	spec := legacyBootstrapTestSpec()
	restartDone := make(chan error, 1)
	go func() {
		restartDone <- manager.RestartLegacyRuntimeSpec(context.Background(), spec)
	}()
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("legacy restart did not enter the remote action")
	}
	bootstrapDone := make(chan error, 1)
	go func() {
		_, err := manager.BootstrapLegacyRuntime(context.Background(), spec)
		bootstrapDone <- err
	}()
	select {
	case err := <-bootstrapDone:
		t.Fatalf("model switch interleaved with legacy restart: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(runner.release)
	if err := <-restartDone; err == nil {
		t.Fatal("fake runner unexpectedly satisfied restart verification")
	}
	if err := <-bootstrapDone; err != nil {
		t.Fatal(err)
	}
}

type blockingLegacyRestartRunner struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (r *blockingLegacyRestartRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	r.once.Do(func() {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
		}
	})
	call := name + " " + strings.Join(args, " ")
	if strings.Contains(call, " inspect ") {
		return CommandResult{Stdout: "true|healthy\n"}, nil
	}
	if strings.Contains(call, " ps ") {
		return CommandResult{}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
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
		if filepath.Base(filepath.Dir(newPath)) == legacyBootstrapStageDirectory && deploymentRenames.Add(1) == 2 {
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

func TestBootstrapRuntimeHoldsMaintenanceThroughInitialEnqueue(t *testing.T) {
	stateDir := t.TempDir()
	markerWritten := make(chan struct{})
	releaseMarkerSync := make(chan struct{})
	var signaled atomic.Bool
	store := &ManifestStore{StateDir: stateDir, syncDirectory: func(path string) error {
		if filepath.Clean(path) == filepath.Join(stateDir, "admin") {
			if _, err := os.Lstat(filepath.Join(stateDir, "admin", "instance.json")); err == nil && signaled.CompareAndSwap(false, true) {
				close(markerWritten)
				<-releaseMarkerSync
			}
		}
		return nil
	}}
	manager := NewManager(ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: &fakeRunner{}})
	bootstrapDone := make(chan error, 1)
	go func() {
		_, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec())
		bootstrapDone <- err
	}()
	select {
	case <-markerWritten:
	case <-time.After(3 * time.Second):
		t.Fatal("bootstrap did not reach marker-written window")
	}
	gen2 := NormalizeDeploymentManifest(DeploymentManifest{
		Metadata: DeploymentMetadata{InstanceID: "admin", Name: "permission", Generation: 2},
		Spec:     DeploymentSpec{ServiceName: "permission", Image: "permission:rev-2", PodRevision: "rev-2", Replicas: 1},
		Service:  ServiceSpec{Name: "permission", AppName: "permission", Port: 8081},
	})
	acceptDone := make(chan error, 1)
	go func() {
		_, err := manager.AcceptDeployment(context.Background(), gen2)
		acceptDone <- err
	}()
	var acceptedEarly bool
	var earlyErr error
	select {
	case err := <-acceptDone:
		acceptedEarly = true
		earlyErr = err
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseMarkerSync)
	if err := <-bootstrapDone; err != nil {
		t.Fatal(err)
	}
	if acceptedEarly {
		t.Fatalf("generation 2 bypassed bootstrap maintenance lock: %v", earlyErr)
	}
	if err := <-acceptDone; err != nil {
		t.Fatal(err)
	}
	state, ok := manager.DeploymentState("admin", "permission")
	if !ok || state.Generation != 2 {
		t.Fatalf("final state=%+v exists=%v", state, ok)
	}
}

func TestBootstrapRuntimePreMarkerFailurePreservesLegacyDeploymentDirectory(t *testing.T) {
	stateDir := t.TempDir()
	legacyDir := filepath.Join(stateDir, "admin", "deployments")
	if err := os.MkdirAll(legacyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	diagnosticPath := filepath.Join(legacyDir, "diagnostic.json")
	if err := os.WriteFile(diagnosticPath, []byte(`{"owner":"legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &ManifestStore{StateDir: stateDir, renameFile: func(oldPath, newPath string) error {
		if filepath.Base(newPath) == "instance.json" {
			return errors.New("injected marker rename failure")
		}
		return os.Rename(oldPath, newPath)
	}}
	manager := NewManager(ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: &fakeRunner{}})
	if _, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec()); err == nil {
		t.Fatal("marker failure was ignored")
	}
	data, err := os.ReadFile(diagnosticPath)
	if err != nil || !strings.Contains(string(data), "legacy") {
		t.Fatalf("legacy diagnostic was not preserved: data=%q err=%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(stateDir, "admin", "instance.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker unexpectedly switched: %v", err)
	}
}

func TestBootstrapRuntimeRecoversQuarantineBeforeRetry(t *testing.T) {
	stateDir := t.TempDir()
	instanceDir := filepath.Join(stateDir, "admin")
	finalDir := filepath.Join(instanceDir, "deployments")
	backupDir := filepath.Join(instanceDir, "deployments.bootstrap-backup")
	if err := os.MkdirAll(finalDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(finalDir, "permission.json"), []byte(`{"staged":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "diagnostic.json"), []byte(`{"owner":"legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var markerFailed atomic.Bool
	store := &ManifestStore{StateDir: stateDir, renameFile: func(oldPath, newPath string) error {
		if filepath.Base(newPath) == "instance.json" && markerFailed.CompareAndSwap(false, true) {
			return errors.New("stop after recovery")
		}
		return os.Rename(oldPath, newPath)
	}}
	manager := NewManager(ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: &fakeRunner{}})
	if _, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec()); err == nil {
		t.Fatal("marker failure was ignored")
	}
	data, err := os.ReadFile(filepath.Join(finalDir, "diagnostic.json"))
	if err != nil || !strings.Contains(string(data), "legacy") {
		t.Fatalf("quarantined legacy directory was not recovered: data=%q err=%v", data, err)
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
		state, ok := manager.DeploymentState("admin", service)
		if !ok || state.Generation != 1 {
			t.Fatalf("controller was not enqueued after marker sync error for %s: %+v exists=%v", service, state, ok)
		}
	}
}

func TestBootstrapRuntimeMarkerSyncFailureCleansSuccessfulQuarantine(t *testing.T) {
	stateDir := t.TempDir()
	legacyDir := filepath.Join(stateDir, "admin", "deployments")
	if err := os.MkdirAll(legacyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "diagnostic.json"), []byte(`{"owner":"legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var failed atomic.Bool
	store := &ManifestStore{StateDir: stateDir, syncDirectory: func(path string) error {
		if filepath.Clean(path) == filepath.Join(stateDir, "admin") {
			if _, err := os.Lstat(filepath.Join(stateDir, "admin", "instance.json")); err == nil && failed.CompareAndSwap(false, true) {
				return errors.New("injected marker directory sync failure")
			}
		}
		return nil
	}}
	manager := NewManager(ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: &fakeRunner{}})
	if _, err := manager.BootstrapLegacyRuntime(context.Background(), legacyBootstrapTestSpec()); err == nil {
		t.Fatal("marker sync failure was ignored")
	}
	if _, err := os.Lstat(filepath.Join(stateDir, "admin", legacyBootstrapBackupDirectory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful quarantine remains after marker switch: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "admin", "deployments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != "gateway.json" || entries[1].Name() != "permission.json" {
		t.Fatalf("marker-complete deployment directory is not canonical: %v", entries)
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
