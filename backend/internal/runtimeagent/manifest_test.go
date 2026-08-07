package runtimeagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestManifestNormalizationKeepsLegacyNacosSeparate(t *testing.T) {
	legacy := LegacyRuntimeSpec{Nacos: NacosSpec{Namespace: "prod"}}
	if legacy.Nacos.Namespace != "prod" {
		t.Fatal("legacy runtime spec lost Nacos compatibility")
	}

	manifest := NormalizeDeploymentManifest(DeploymentManifest{
		Metadata: DeploymentMetadata{InstanceID: " app_123 ", Name: " Permission ", Generation: 1},
		Spec: DeploymentSpec{
			Replicas: 1,
			Image:    "aifar-permission:rev-1",
		},
		Service: ServiceSpec{ListenPort: 38010, TargetPort: 38010},
	})
	if manifest.APIVersion != ManifestAPIVersion || manifest.Kind != DeploymentManifestKind {
		t.Fatalf("schema=%q kind=%q", manifest.APIVersion, manifest.Kind)
	}
	if manifest.Metadata.InstanceID != "app_123" || manifest.Metadata.Name != "permission" {
		t.Fatalf("metadata=%#v", manifest.Metadata)
	}
	if manifest.Spec.ServiceName != "permission" || manifest.Spec.DeploymentName != "alpha-permission" {
		t.Fatalf("spec identity=%#v", manifest.Spec)
	}
	if manifest.Service.Name != "permission" || manifest.Service.AppName != "alpha-permission" {
		t.Fatalf("service identity=%#v", manifest.Service)
	}
	if manifest.Spec.Strategy.Type != DefaultDeploymentStrategyType {
		t.Fatalf("strategy=%#v", manifest.Spec.Strategy)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(data)), "nacos") {
		t.Fatalf("new deployment manifest contains Nacos state: %s", data)
	}
}

func TestDeploymentManifestSpecHashCanonicalizesResource(t *testing.T) {
	raw := testManifest("permission", 1, 1)
	raw.APIVersion = ""
	raw.Kind = ""
	raw.Spec.DeploymentName = ""
	raw.Spec.Strategy = DeploymentStrategySpec{}
	raw.Service.Name = ""
	raw.Service.AppName = ""
	raw.Service.Port = 0

	rawHash, err := DeploymentManifestSpecHash(raw)
	if err != nil {
		t.Fatal(err)
	}
	normalizedHash, err := DeploymentManifestSpecHash(NormalizeDeploymentManifest(raw))
	if err != nil {
		t.Fatal(err)
	}
	if rawHash != normalizedHash {
		t.Fatalf("raw hash=%s, normalized hash=%s", rawHash, normalizedHash)
	}
}

func TestNormalizeDeploymentManifestDoesNotMutateCaller(t *testing.T) {
	manifest := testManifest("permission", 1, 1)
	manifest.Spec.EnvFiles[0] = "  " + manifest.Spec.EnvFiles[0] + "  "
	manifest.Spec.Volumes[0].Source = "  " + manifest.Spec.Volumes[0].Source + "  "
	manifest.Spec.Volumes[0].Target = "  " + manifest.Spec.Volumes[0].Target + "  "
	wantEnv := manifest.Spec.EnvFiles[0]
	wantVolume := manifest.Spec.Volumes[0]

	normalized := NormalizeDeploymentManifest(manifest)
	if manifest.Spec.EnvFiles[0] != wantEnv || manifest.Spec.Volumes[0] != wantVolume {
		t.Fatalf("normalization mutated caller: env=%q volume=%#v", manifest.Spec.EnvFiles[0], manifest.Spec.Volumes[0])
	}
	if normalized.Spec.EnvFiles[0] == wantEnv || normalized.Spec.Volumes[0] == wantVolume {
		t.Fatalf("normalization did not trim copied values: %#v", normalized.Spec)
	}
}

func TestManifestStorePersistsAndListsNormalizedResources(t *testing.T) {
	stateDir := t.TempDir()
	store := ManifestStore{StateDir: stateDir}
	config := testInstanceConfig()
	if err := store.PutInstance(config); err != nil {
		t.Fatal(err)
	}

	for _, service := range []string{"permission", "file"} {
		got, err := store.Put(testManifest(service, 1, 1))
		if err != nil {
			t.Fatalf("put %s: %v", service, err)
		}
		if !got.Accepted || got.Generation != 1 || len(got.SpecHash) != 64 {
			t.Fatalf("acceptance=%#v", got)
		}
	}

	instancePath := filepath.Join(stateDir, config.InstanceID, "instance.json")
	deploymentPath := filepath.Join(stateDir, config.InstanceID, "deployments", "permission.json")
	for _, path := range []string{instancePath, deploymentPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("mode for %s=%#o, want 0600", path, info.Mode().Perm())
		}
	}

	got, err := store.Get(config.InstanceID, "permission")
	if err != nil {
		t.Fatal(err)
	}
	want := NormalizeDeploymentManifest(testManifest("permission", 1, 1))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest=%#v, want %#v", got, want)
	}

	listed, err := store.List(config.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Metadata.Name != "file" || listed[1].Metadata.Name != "permission" {
		t.Fatalf("listed=%#v", listed)
	}
}

func TestManifestStoreGenerationRules(t *testing.T) {
	store := newTestManifestStore(t)
	first := testManifest("permission", 7, 1)
	accepted, err := store.Put(first)
	if err != nil {
		t.Fatal(err)
	}

	idempotent, err := store.Put(first)
	if err != nil {
		t.Fatalf("idempotent put: %v", err)
	}
	if idempotent != accepted {
		t.Fatalf("idempotent acceptance=%#v, want %#v", idempotent, accepted)
	}

	changed := first
	changed.Spec.Replicas = 0
	if _, err := store.Put(changed); !errors.Is(err, ErrDeploymentGenerationConflict) {
		t.Fatalf("error=%v, want generation conflict", err)
	}

	stale := first
	stale.Metadata.Generation = 6
	if _, err := store.Put(stale); !errors.Is(err, ErrStaleDeploymentGeneration) {
		t.Fatalf("error=%v, want stale generation", err)
	}

	next := changed
	next.Metadata.Generation = 8
	if _, err := store.Put(next); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("app_123", "permission")
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Generation != 8 || got.Spec.Replicas != 0 {
		t.Fatalf("manifest=%#v", got)
	}
}

func TestManifestStoreFailureLeavesPreviousManifestReadable(t *testing.T) {
	store := newTestManifestStore(t)
	if _, err := store.Put(testManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}

	store.renameFile = func(_, _ string) error { return errors.New("injected rename failure") }
	if _, err := store.Put(testManifest("permission", 2, 0)); err == nil || !strings.Contains(err.Error(), "injected rename failure") {
		t.Fatalf("error=%v", err)
	}
	got, err := store.Get("app_123", "permission")
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Generation != 1 || got.Spec.Replicas != 1 {
		t.Fatalf("previous manifest was not preserved: %#v", got)
	}
	entries, err := os.ReadDir(filepath.Join(store.StateDir, "app_123", "deployments"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary file was not cleaned up: %s", entry.Name())
		}
	}
}

func TestManifestStoreFileSyncFailureLeavesPreviousManifestReadable(t *testing.T) {
	store := newTestManifestStore(t)
	if _, err := store.Put(testManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}

	store.syncFile = func(*os.File) error { return errors.New("injected file sync failure") }
	if _, err := store.Put(testManifest("permission", 2, 0)); err == nil || !strings.Contains(err.Error(), "injected file sync failure") {
		t.Fatalf("error=%v", err)
	}
	got, err := store.Get("app_123", "permission")
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Generation != 1 || got.Spec.Replicas != 1 {
		t.Fatalf("previous manifest was not preserved: %#v", got)
	}
}

func TestManifestStoreSyncsContainingDirectoryAfterRename(t *testing.T) {
	store := newTestManifestStore(t)
	var synced string
	store.syncDirectory = func(path string) error {
		synced = path
		return nil
	}
	if _, err := store.Put(testManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(store.StateDir, "app_123", "deployments")
	if synced != want {
		t.Fatalf("synced directory=%q, want %q", synced, want)
	}
}

func TestManifestStoreSyncsParentsOfNewStateDirectories(t *testing.T) {
	stateDir := t.TempDir()
	var synced []string
	store := ManifestStore{
		StateDir: stateDir,
		syncDirectory: func(path string) error {
			synced = append(synced, path)
			return nil
		},
	}
	if err := store.PutInstance(testInstanceConfig()); err != nil {
		t.Fatal(err)
	}
	instanceDir := filepath.Join(stateDir, "app_123")
	if !containsPath(synced, stateDir) || !containsPath(synced, instanceDir) {
		t.Fatalf("instance directory syncs=%#v", synced)
	}

	synced = nil
	if _, err := store.Put(testManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	deploymentsDir := filepath.Join(instanceDir, "deployments")
	if !containsPath(synced, instanceDir) || !containsPath(synced, deploymentsDir) {
		t.Fatalf("deployment directory syncs=%#v", synced)
	}
}

func TestManifestStoreRetriesParentSyncForExistingDirectory(t *testing.T) {
	store := newTestManifestStore(t)
	instanceDir := filepath.Join(store.StateDir, "app_123")
	parentSyncs := 0
	store.syncDirectory = func(path string) error {
		if path == instanceDir {
			parentSyncs++
			if parentSyncs == 1 {
				return errors.New("injected parent sync failure")
			}
		}
		return nil
	}
	manifest := testManifest("permission", 1, 1)
	if _, err := store.Put(manifest); err == nil || !strings.Contains(err.Error(), "injected parent sync failure") {
		t.Fatalf("first error=%v", err)
	}
	if _, err := store.Put(manifest); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if parentSyncs != 2 {
		t.Fatalf("parent sync attempts=%d, want 2", parentSyncs)
	}
}

func TestManifestStoreIdempotentRetryResyncsManifestDirectory(t *testing.T) {
	store := newTestManifestStore(t)
	deploymentsDir := filepath.Join(store.StateDir, "app_123", "deployments")
	manifestSyncs := 0
	store.syncDirectory = func(path string) error {
		if path == deploymentsDir {
			manifestSyncs++
			if manifestSyncs == 1 {
				return errors.New("injected manifest directory sync failure")
			}
		}
		return nil
	}
	manifest := testManifest("permission", 1, 1)
	if _, err := store.Put(manifest); err == nil || !strings.Contains(err.Error(), "injected manifest directory sync failure") {
		t.Fatalf("first error=%v", err)
	}
	accepted, err := store.Put(manifest)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !accepted.Accepted || manifestSyncs != 2 {
		t.Fatalf("acceptance=%#v manifest sync attempts=%d", accepted, manifestSyncs)
	}
}

func TestManifestValidationRejectsUnsafeInput(t *testing.T) {
	config := testInstanceConfig()
	valid := NormalizeDeploymentManifest(testManifest("permission", 1, 1))
	tests := []struct {
		name   string
		mutate func(*DeploymentManifest)
	}{
		{name: "api version", mutate: func(m *DeploymentManifest) { m.APIVersion = "aifar.io/v2" }},
		{name: "kind", mutate: func(m *DeploymentManifest) { m.Kind = "Pod" }},
		{name: "instance traversal", mutate: func(m *DeploymentManifest) { m.Metadata.InstanceID = "../admin" }},
		{name: "service traversal", mutate: func(m *DeploymentManifest) { m.Metadata.Name = "../permission" }},
		{name: "identity mismatch", mutate: func(m *DeploymentManifest) { m.Spec.ServiceName = "file" }},
		{name: "zero generation", mutate: func(m *DeploymentManifest) { m.Metadata.Generation = 0 }},
		{name: "negative restart generation", mutate: func(m *DeploymentManifest) { m.Spec.RestartGeneration = -1 }},
		{name: "negative replicas", mutate: func(m *DeploymentManifest) { m.Spec.Replicas = -1 }},
		{name: "image option escape", mutate: func(m *DeploymentManifest) { m.Spec.Image = "--privileged" }},
		{name: "empty image", mutate: func(m *DeploymentManifest) { m.Spec.Image = "" }},
		{name: "revision control", mutate: func(m *DeploymentManifest) { m.Spec.PodRevision = "rev-1\nspoofed" }},
		{name: "revision path separator", mutate: func(m *DeploymentManifest) { m.Spec.PodRevision = "release/other" }},
		{name: "revision too long", mutate: func(m *DeploymentManifest) { m.Spec.PodRevision = "r" + strings.Repeat("a", 128) }},
		{name: "controller label override", mutate: func(m *DeploymentManifest) { m.Spec.Labels = map[string]string{"aifar.service": "file"} }},
		{name: "env outside install root", mutate: func(m *DeploymentManifest) { m.Spec.EnvFiles = []string{"/etc/shadow"} }},
		{name: "env traversal", mutate: func(m *DeploymentManifest) { m.Spec.EnvFiles = []string{"/aifar/apps/app_123/runtime/../outside.env"} }},
		{name: "volume outside install root", mutate: func(m *DeploymentManifest) { m.Spec.Volumes[0].Source = "/var/run/docker.sock" }},
		{name: "relative volume target", mutate: func(m *DeploymentManifest) { m.Spec.Volumes[0].Target = "opt/aifar/logs" }},
		{name: "service port zero", mutate: func(m *DeploymentManifest) { m.Service.ListenPort = 0 }},
		{name: "service port too high", mutate: func(m *DeploymentManifest) { m.Service.TargetPort = 65536 }},
		{name: "container port too high", mutate: func(m *DeploymentManifest) { m.Spec.Ports[0].ContainerPort = 65536 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			manifest := valid
			manifest.Spec.EnvFiles = append([]string(nil), valid.Spec.EnvFiles...)
			manifest.Spec.Volumes = append([]VolumeMount(nil), valid.Spec.Volumes...)
			manifest.Spec.Ports = append([]ContainerPort(nil), valid.Spec.Ports...)
			tc.mutate(&manifest)
			if err := ValidateDeploymentManifest(config, manifest); err == nil {
				t.Fatalf("unsafe manifest accepted: %#v", manifest)
			}
		})
	}
}

func TestManifestStoreRejectsPathIdentitiesBeforeFilesystemAccess(t *testing.T) {
	store := ManifestStore{StateDir: t.TempDir()}
	if _, err := store.Get("../escape", "permission"); err == nil {
		t.Fatal("unsafe instance identity accepted")
	}
	if _, err := store.Get("app_123", "../escape"); err == nil {
		t.Fatal("unsafe service identity accepted")
	}
	if _, err := store.List("../escape"); err == nil {
		t.Fatal("unsafe instance identity accepted for list")
	}
}

func TestManifestStoreRejectsPersistedResourceWithoutSchema(t *testing.T) {
	store := newTestManifestStore(t)
	if _, err := store.Put(testManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.StateDir, "app_123", "deployments", "permission.json")
	manifest := NormalizeDeploymentManifest(testManifest("permission", 1, 1))
	manifest.APIVersion = ""
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("app_123", "permission"); err == nil {
		t.Fatal("persisted deployment without apiVersion was accepted")
	}
}

func TestManifestStoreListReturnsValidPeersWithCorruptFileError(t *testing.T) {
	store := newTestManifestStore(t)
	for _, service := range []string{"permission", "file"} {
		if _, err := store.Put(testManifest(service, 1, 1)); err != nil {
			t.Fatal(err)
		}
	}
	corruptPath := filepath.Join(store.StateDir, "app_123", "deployments", "permission.json")
	if err := os.WriteFile(corruptPath, []byte(`{"apiVersion":`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifests, err := store.List("app_123")
	if err == nil {
		t.Fatal("corrupt manifest did not produce an error")
	}
	if len(manifests) != 1 || manifests[0].Metadata.Name != "file" {
		t.Fatalf("valid peer was not returned: %#v", manifests)
	}
}

func TestInstanceConfigValidationAndAtomicPersistence(t *testing.T) {
	store := ManifestStore{StateDir: t.TempDir()}
	config := testInstanceConfig()
	if err := store.PutInstance(config); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetInstance(config.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, NormalizeInstanceConfig(config)) {
		t.Fatalf("instance=%#v", got)
	}

	invalid := []InstanceConfig{
		{APIVersion: "wrong", InstanceID: "app_123", InstallRoot: "/aifar/apps/app_123", Network: "aifar-network", Ingress: config.Ingress},
		{APIVersion: ManifestAPIVersion, InstanceID: "../admin", InstallRoot: "/aifar/apps/app_123", Network: "aifar-network", Ingress: config.Ingress},
		{APIVersion: ManifestAPIVersion, InstanceID: "app_123", InstallRoot: "/", Network: "aifar-network", Ingress: config.Ingress},
		{APIVersion: ManifestAPIVersion, InstanceID: "app_123", InstallRoot: "/aifar/apps/app_123/../escape", Network: "aifar-network", Ingress: config.Ingress},
		{APIVersion: ManifestAPIVersion, InstanceID: "app_123", InstallRoot: "/aifar/apps/app_123", Network: "", Ingress: config.Ingress},
	}
	for _, item := range invalid {
		if err := store.PutInstance(item); err == nil {
			t.Fatalf("invalid instance accepted: %#v", item)
		}
	}
}

func newTestManifestStore(t *testing.T) ManifestStore {
	t.Helper()
	store := ManifestStore{StateDir: t.TempDir()}
	if err := store.PutInstance(testInstanceConfig()); err != nil {
		t.Fatal(err)
	}
	return store
}

func testInstanceConfig() InstanceConfig {
	return InstanceConfig{
		APIVersion:  ManifestAPIVersion,
		InstanceID:  "app_123",
		InstallRoot: "/aifar/apps/app_123",
		Network:     "aifar-network",
		Ingress: IngressSpec{
			Mode:           DefaultIngressMode,
			GatewayService: "gateway",
			WebService:     "web-vue3",
			GatewayPort:    38000,
			WebPort:        8080,
		},
	}
}

func testManifest(service string, generation int64, replicas int) DeploymentManifest {
	root := "/aifar/apps/app_123"
	return DeploymentManifest{
		APIVersion: ManifestAPIVersion,
		Kind:       DeploymentManifestKind,
		Metadata: DeploymentMetadata{
			InstanceID: "app_123",
			Name:       service,
			Generation: generation,
		},
		Spec: DeploymentSpec{
			ServiceName: service,
			Image:       "aifar-" + service + ":rev-1",
			PodRevision: "rev-1",
			Replicas:    replicas,
			Ports:       []ContainerPort{{Name: "http", ContainerPort: 38010}},
			EnvFiles:    []string{root + "/runtime/env/" + service + ".env"},
			Volumes:     []VolumeMount{{Source: root + "/runtime/logs/" + service, Target: "/opt/aifar/logs"}},
		},
		Service: ServiceSpec{Name: service, AppName: "alpha-" + service, ListenPort: 38010, TargetPort: 38010},
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}
