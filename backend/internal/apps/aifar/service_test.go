package aifar

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type fakeStore struct {
	mu        sync.Mutex
	servers   map[string]store.Server
	instances []store.AppInstance
	releases  []store.AppRelease
}

func (f *fakeStore) GetServer(id string, includeSecret bool) (store.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servers[id], nil
}

func (f *fakeStore) ListAppInstances() ([]store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.AppInstance, len(f.instances))
	copy(out, f.instances)
	return out, nil
}

func (f *fakeStore) GetAppInstance(id string) (store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, instance := range f.instances {
		if instance.ID == id {
			return instance, nil
		}
	}
	return store.AppInstance{}, sql.ErrNoRows
}

func (f *fakeStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if v.ID == "" {
		v.ID = store.NewID("app")
		v.CreatedAt = now
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = now
	}
	for idx, existing := range f.instances {
		if existing.ID == v.ID {
			if v.CreatedAt.IsZero() {
				v.CreatedAt = existing.CreatedAt
			}
			f.instances[idx] = v
			return v, nil
		}
	}
	f.instances = append(f.instances, v)
	return v, nil
}

func (f *fakeStore) DeleteAppInstance(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.instances[:0]
	for _, instance := range f.instances {
		if instance.ID != id {
			next = append(next, instance)
		}
	}
	f.instances = next
	return nil
}

func (f *fakeStore) SaveAppRelease(v store.AppRelease) (store.AppRelease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v.ID == "" {
		v.ID = store.NewID("rel")
	}
	for idx, existing := range f.releases {
		if existing.InstanceID == v.InstanceID && existing.ReleaseID == v.ReleaseID {
			f.releases[idx] = v
			return v, nil
		}
	}
	f.releases = append(f.releases, v)
	return v, nil
}

func (f *fakeStore) DeleteOldAppReleases(instanceID string, keep int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if keep < 1 {
		keep = 1
	}
	count := 0
	next := f.releases[:0]
	deleted := 0
	for _, release := range f.releases {
		if release.InstanceID == instanceID && release.Status == "success" {
			count++
			if count > keep {
				deleted++
				continue
			}
		}
		next = append(next, release)
	}
	f.releases = next
	return deleted, nil
}

func TestEnsureK8sLikeMetadataTreatsMissingModelAsLegacy(t *testing.T) {
	err := ensureK8sLikeMetadata(map[string]any{}, UpdateCopy{
		LegacyUpdateUnsupported: "legacy model %s",
	})
	if err == nil || !strings.Contains(err.Error(), legacyOrchestrationModel) {
		t.Fatalf("expected missing orchestration model to be reported as legacy, got %v", err)
	}
	if strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("missing orchestration model should not leak <nil>: %v", err)
	}
}

func TestExistingAIFARInstanceIDRequiresExplicitInstallRoot(t *testing.T) {
	svc := Service{store: &fakeStore{instances: []store.AppInstance{
		{ID: "legacy", App: AppName, ServerID: "srv-1", Metadata: `{}`},
		{ID: "other-root", App: AppName, ServerID: "srv-1", Metadata: `{"installRoot":"/aifar/apps/other"}`},
		{ID: "same-root", App: AppName, ServerID: "srv-1", Metadata: `{"installRoot":"/aifar/apps/admin/"}`},
	}}}
	id, err := svc.existingAIFARInstanceID("srv-1", "/aifar/apps/admin")
	if err != nil {
		t.Fatal(err)
	}
	if id != "same-root" {
		t.Fatalf("expected explicit installRoot match, got %q", id)
	}

	svc = Service{store: &fakeStore{instances: []store.AppInstance{
		{ID: "legacy", App: AppName, ServerID: "srv-1", Metadata: `{}`},
	}}}
	id, err = svc.existingAIFARInstanceID("srv-1", "/aifar/apps/admin")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("legacy instance without installRoot must not be reused, got %q", id)
	}
}

type fakeRemote struct {
	mu                      sync.Mutex
	commands                []string
	uploads                 []string
	installScript           string
	updateScript            string
	bundleScript            string
	autoscaleScript         string
	statusStdout            string
	autoscaleStatusStdouts  []string
	autoscaleStatusFallback string
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	if strings.Contains(command, "AIFAR_SERVICE_STATUS") && f.statusStdout != "" {
		return adapter.CommandResult{Stdout: f.statusStdout}, nil
	}
	if strings.Contains(command, "AIFAR_AUTOSCALE_STATUS") {
		if len(f.autoscaleStatusStdouts) > 0 {
			stdout := f.autoscaleStatusStdouts[0]
			f.autoscaleStatusStdouts = f.autoscaleStatusStdouts[1:]
			return adapter.CommandResult{Stdout: stdout}, nil
		}
		return adapter.CommandResult{Stdout: f.autoscaleStatusFallback}, nil
	}
	if strings.Contains(command, "AIFAR_AUTOSCALE_OUT") {
		f.autoscaleScript = command
	}
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, filepath.Base(localPath)+"->"+remotePath)
	if strings.HasSuffix(remotePath, "/install-aifar.sh") {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		f.installScript = string(content)
	}
	if strings.HasSuffix(remotePath, "/update-aifar-artifact.sh") {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		f.updateScript = string(content)
	}
	if strings.HasSuffix(remotePath, "/update-aifar-artifact-bundle.sh") {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		f.bundleScript = string(content)
	}
	return nil
}

func (f *fakeRemote) joinedCommands() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.commands, "\n")
}

func (f *fakeRemote) joinedUploads() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.uploads, "\n")
}

type fakeLogger struct{}

func (fakeLogger) Info(format string, args ...any)  {}
func (fakeLogger) Error(format string, args ...any) {}

type bundleTestArtifact struct {
	Service  string
	Module   string
	FileName string
	Content  string
}

func installedAIFARInstance(t *testing.T) store.AppInstance {
	t.Helper()
	releaseID := "20260701T010203.000000000Z-docker-apps"
	metadata := map[string]any{
		"installRoot":           "/aifar/apps/admin",
		"runtimeDir":            "/aifar/apps/admin/runtime",
		"orchestrationModel":    orchestrationModelK8sLikeV1,
		"releaseId":             releaseID,
		"currentRevision":       releaseID,
		"releaseVersion":        "docker-apps",
		"configHash":            "base-config-hash",
		"services":              serviceOrder,
		"gatewayPort":           defaultGatewayPort,
		"webPort":               defaultWebPort,
		"nacosRegistrationMode": "agent-proxy",
	}
	for key, value := range releaseOrchestrationMetadata("/aifar/apps/admin", releaseID, defaultNetworkName, defaultGatewayPort, defaultWebPort, serviceOrder) {
		metadata[key] = value
	}
	return store.AppInstance{
		ID:       "aifar-1",
		App:      "aifar",
		Version:  "docker-apps",
		ServerID: "srv-1",
		Status:   "installed",
		Topology: defaultTopology,
		Metadata: mustMetadata(t, metadata),
	}
}

func writeAlphaJarBundle(t *testing.T, artifacts []bundleTestArtifact) string {
	t.Helper()
	return writeAlphaJarBundleWithManifestPrefix(t, artifacts, nil)
}

func writeAlphaJarBundleWithManifestPrefix(t *testing.T, artifacts []bundleTestArtifact, manifestPrefix []byte) string {
	t.Helper()
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "aifar-alpha-jars-test.zip")
	file, err := os.Create(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(file)
	manifestServices := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		rel := pathJoinSlash("artifacts", artifact.Service, artifact.FileName)
		writer, err := zipWriter.Create(rel)
		if err != nil {
			t.Fatal(err)
		}
		content := []byte(artifact.Content)
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		manifestServices = append(manifestServices, map[string]any{
			"service":  artifact.Service,
			"module":   artifact.Module,
			"artifact": rel,
			"fileName": artifact.FileName,
			"sha256":   hex.EncodeToString(sum[:]),
			"size":     len(content),
		})
	}
	manifest := map[string]any{
		"schema":   artifactBundleSchema,
		"app":      AppName,
		"kind":     "alpha-java-cloud-jars",
		"services": manifestServices,
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifestPrefix) > 0 {
		manifestData = append(append([]byte{}, manifestPrefix...), manifestData...)
	}
	writer, err := zipWriter.Create(artifactBundleManifestName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(manifestData); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return bundlePath
}

func TestAutoscalePolicyDefaultsAndTrigger(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	signals := map[string]any{
		"permission": map[string]any{"since": now.Add(-6 * time.Minute).Format(time.RFC3339)},
	}
	metadata["autoscaleSignals"] = signals
	status := autoscaleStatus{Endpoints: []autoscaleMetric{{
		Service:          "permission",
		Container:        "aifar-permission-rel",
		ReleaseID:        "rel",
		ReplicaID:        1,
		Port:             defaultPermissionPort,
		Running:          true,
		Health:           "healthy",
		MemoryPercent:    86,
		MemoryLimitBytes: 2 * 1024 * 1024 * 1024,
	}}}
	next, decision := evaluateAutoscale(instance, metadata, status, autoscalePolicyFromMetadata(metadata), now)
	if decision.Service != "permission" {
		t.Fatalf("expected permission scale decision, got %+v", decision)
	}
	policy := autoscalePolicyFromMetadata(next)
	if !policy.Enabled || policy.MemoryThreshold != 80 || policy.MaxReplicas != 3 || policy.ScaleIn {
		t.Fatalf("unexpected autoscale defaults: %+v", policy)
	}
}

func TestAutoscaleDoesNotTriggerWithoutMemoryLimitOrDuringCooldown(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["autoscaleSignals"] = map[string]any{
		"permission": map[string]any{
			"since":        now.Add(-10 * time.Minute).Format(time.RFC3339),
			"lastScaledAt": now.Add(-2 * time.Minute).Format(time.RFC3339),
		},
	}
	status := autoscaleStatus{Endpoints: []autoscaleMetric{{
		Service:          "permission",
		Container:        "aifar-permission-rel",
		ReleaseID:        "rel",
		ReplicaID:        1,
		Port:             defaultPermissionPort,
		Running:          true,
		Health:           "healthy",
		MemoryPercent:    90,
		MemoryLimitBytes: 2 * 1024 * 1024 * 1024,
	}}}
	_, decision := evaluateAutoscale(instance, metadata, status, autoscalePolicyFromMetadata(metadata), now)
	if decision.Service != "" {
		t.Fatalf("expected cooldown to suppress scale out, got %+v", decision)
	}
	metadata["autoscaleSignals"] = map[string]any{
		"permission": map[string]any{"since": now.Add(-10 * time.Minute).Format(time.RFC3339)},
	}
	status.Endpoints[0].MemoryLimitBytes = 0
	_, decision = evaluateAutoscale(instance, metadata, status, autoscalePolicyFromMetadata(metadata), now)
	if decision.Service != "" {
		t.Fatalf("expected missing memory limit to suppress scale out, got %+v", decision)
	}
}

func TestAutoscaleOutScriptUsesReplicaContainerAndEscapedDockerFormats(t *testing.T) {
	script, err := renderAutoscaleOutScript(autoscaleOutScriptData{
		InstallRoot:    "/aifar/apps/admin",
		ServiceName:    "permission",
		ReleaseID:      "rel-1",
		ReplicaID:      2,
		ContainerName:  "aifar-permission-rel-1-r2",
		IngressNetwork: "aifar-network",
		MaxReplicas:    3,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`--name "$CONTAINER_NAME"`,
		`--label "aifar.replica=$REPLICA_ID"`,
		`--format '{{.Names}}'`,
		`docker run -d`,
		`curl -sS --connect-timeout $health_connect_timeout -o /dev/null ${health_protocol}://${health_host}:${port}/ || exit 1`,
		`APP_STARTUP_TIMEOUT 300`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("autoscale script missing %q:\n%s", want, script)
		}
	}
}

func TestScaleOutCreatesReplicaAndUpdatesEndpointMetadata(t *testing.T) {
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{
			instance,
		},
	}
	remote := &fakeRemote{autoscaleStatusStdouts: []string{
		"endpoint=permission|aifar-permission-rel|rel|1|38010|true|healthy|86|2147483648\nhostMemoryAvailableBytes=8589934592\n",
		"endpoint=permission|aifar-permission-rel|rel|1|38010|true|healthy|50|2147483648\nendpoint=permission|aifar-permission-rel-r2|rel|2|38010|true|healthy|5|2147483648\nhostMemoryAvailableBytes=6442450944\n",
	}}
	service := NewService(s, remote)
	err := service.ScaleOut(context.Background(), ScaleOutRequest{
		Instance:    instance,
		Server:      s.servers["srv-1"],
		Actor:       "system",
		ServiceName: "permission",
		Reason:      "test",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.autoscaleScript, "AIFAR_AUTOSCALE_OUT") || !strings.Contains(remote.autoscaleScript, "aifar-pod-admin-permission-20260701t010203.000000000z-docker-apps-r2") {
		t.Fatalf("expected autoscale remote script to run with replica container, got:\n%s", remote.autoscaleScript)
	}
	saved, err := s.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataFromInstance(saved)
	desired := desiredReplicasFromMetadata(metadata)
	if desired["permission"] != 2 {
		t.Fatalf("expected permission desired replicas 2, got %v metadata=%s", desired["permission"], saved.Metadata)
	}
	endpoints, ok := metadata["activeEndpoints"].(map[string]any)
	if !ok {
		t.Fatalf("expected activeEndpoints metadata, got %s", saved.Metadata)
	}
	if endpointCount(endpoints["permission"]) != 2 {
		t.Fatalf("expected two permission endpoints, got %s", saved.Metadata)
	}
	if _, locked := metadata["orchestrationLock"]; locked {
		t.Fatalf("expected orchestration lock to be released, got %s", saved.Metadata)
	}
}

func TestRolloutOrchestrationPreservesDesiredReplicasForChangedService(t *testing.T) {
	current := map[string]any{
		"releaseId":   "base-release",
		"gatewayPort": float64(defaultGatewayPort),
		"webPort":     float64(defaultWebPort),
		"desiredReplicas": map[string]any{
			"permission": float64(2),
			"gateway":    float64(1),
		},
		"activeEndpoints": map[string]any{
			"permission": []any{
				map[string]any{"container": releaseContainerName("permission", "base-release"), "releaseId": "base-release", "replicaId": float64(1), "port": float64(defaultPermissionPort)},
				map[string]any{"container": releaseReplicaContainerName("permission", "base-release", 2), "releaseId": "base-release", "replicaId": float64(2), "port": float64(defaultPermissionPort)},
			},
			"gateway": []any{
				map[string]any{"container": releaseContainerName("gateway", "base-release"), "releaseId": "base-release", "replicaId": float64(1), "port": float64(defaultGatewayPort)},
			},
		},
	}
	next := rolloutOrchestrationMetadata(current, "/data/apps/admin", "new-release", defaultNetworkName, defaultGatewayPort, defaultWebPort, []string{"permission"})
	desired := desiredReplicasFromMetadata(next)
	if desired["permission"] != 2 {
		t.Fatalf("expected changed service desired replicas to stay 2, got %+v", desired)
	}
	endpoints := activeEndpointsFromMetadata(next)
	if endpointCount(endpoints["permission"]) != 2 {
		t.Fatalf("expected two new permission endpoints, got %+v", endpoints["permission"])
	}
	data, _ := json.Marshal(endpoints["permission"])
	if !strings.Contains(string(data), releaseContainerName("permission", "new-release")) ||
		!strings.Contains(string(data), releaseReplicaContainerName("permission", "new-release", 2)) {
		t.Fatalf("expected endpoints to point at new release replicas, got %s", data)
	}
}

func pathJoinSlash(parts ...string) string {
	return strings.Join(parts, "/")
}

func TestOptionsDefaultsUseRequestedAIFARValues(t *testing.T) {
	opts := optionsFromParameters(nil)
	if opts.Timezone != "system" || opts.NacosWebPort != 8848 || opts.NacosNamespace != "prod" || opts.NacosSource != dependencyManual {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
	if got := installRootFromDeployDir("/aifar/apps"); got != "/aifar/apps/admin" {
		t.Fatalf("expected AIFAR install root /aifar/apps/admin, got %s", got)
	}
}

func TestServiceInstallsAIFARServiceFromDockerAppsBundle(t *testing.T) {
	root := createAIFARBundle(t)
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "latest",
		ServerID: "srv-1",
		Language: "en",
		Parameters: map[string]any{
			"nacosHost": "10.0.0.50",
			"webPort":   18080,
		},
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one AIFAR instance, got %+v", s.instances)
	}
	instance := s.instances[0]
	if instance.App != "aifar" || instance.Version != "docker-apps" || instance.ServerID != "srv-1" || instance.Status != "installed" {
		t.Fatalf("unexpected instance: %+v", instance)
	}
	if strings.Contains(instance.Metadata, "secret-value") || strings.Contains(instance.Metadata, "minio-secret") {
		t.Fatalf("metadata must not store database password or MinIO credentials: %s", instance.Metadata)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["endpoint"] != "10.0.0.10:18080" || metadata["networkName"] != defaultNetworkName {
		t.Fatalf("unexpected metadata: %s", instance.Metadata)
	}
	if metadata["releaseId"] == "" || metadata["runtimeService"] != "aifar-agent" || metadata["ingressNetwork"] != defaultNetworkName {
		t.Fatalf("expected orchestration metadata, got %s", instance.Metadata)
	}
	if metadata["orchestrationModel"] != orchestrationModelK8sLikeV1 || !strings.Contains(instance.Metadata, "agent-proxy") || strings.Contains(instance.Metadata, "aifar-svc-admin-gateway") || !strings.Contains(instance.Metadata, "aifar-pod-admin-gateway") {
		t.Fatalf("expected k8s-like agent proxy and pod metadata, got %s", instance.Metadata)
	}
	if len(s.releases) != 1 || s.releases[0].InstanceID != instance.ID || s.releases[0].Status != "success" {
		t.Fatalf("expected one recorded release, got %+v", s.releases)
	}
	if metadata["nacosEndpoint"] != "10.0.0.50:8848" || metadata["nacosHost"] != "10.0.0.50" || int(metadata["nacosPort"].(float64)) != 8848 {
		t.Fatalf("expected external Nacos endpoint metadata, got %s", instance.Metadata)
	}
	for _, want := range []string{
		`ORCHESTRATION_MODEL="k8s-like-v1"`,
		`RUNTIME_DIR="$INSTALL_ROOT/runtime"`,
		`NACOS_REGISTRATION_MODE="agent-proxy"`,
		`register_nacos_proxy`,
		`SPRING_CLOUD_NACOS_DISCOVERY_REGISTER_ENABLED "false"`,
		`start_pod "$service" 1`,
		`reconcile_ingress`,
		`aifar-agent reconcile-ingress --spec "$spec"`,
		`runtime-spec.json`,
		`"services": [`,
		`"gatewayService": "gateway"`,
		`container_status()`,
		`docker inspect --format '{{.State.Status}}'`,
		`docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}'`,
		`docker logs --tail 120 "$container"`,
		`curl -sS --connect-timeout %s -o /dev/null %s://%s:%s/ || exit 1`,
		`health="$(container_health "$container")"`,
		`strip_web_nginx_runtime_routes`,
		`/^[[:space:]]*location[[:space:]]+\/api\/[[:space:]]*\{/`,
		`/^[[:space:]]*location[[:space:]]+\/im\/ws\/[[:space:]]*\{/`,
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("AIFAR install script should include k8s-like orchestration with %q:\n%s", want, remote.installScript)
		}
	}
	if strings.Contains(remote.installScript, `/"Status"/ {print $4; exit}`) {
		t.Fatalf("AIFAR install script should not parse Docker health from the first JSON Status field:\n%s", remote.installScript)
	}
	for _, legacy := range []string{
		`patch_web_nginx_gateway_target`,
		`aifar-gateway`,
		`proxy_pass http://aifar_gateway;`,
		`aifar-admin-ingress`,
		`aifar-svc-admin-`,
		`remove_runtime_infra_containers`,
		`nginx -s reload`,
		`CURRENT_LINK="$INSTALL_ROOT/current"`,
		`RELEASES_DIR="$INSTALL_ROOT/releases"`,
	} {
		if strings.Contains(remote.installScript, legacy) {
			t.Fatalf("AIFAR install script should not make web-vue3 depend on gateway DNS %q:\n%s", legacy, remote.installScript)
		}
	}
	if strings.Contains(remote.installScript, `      - "${GATEWAY_PORT}:${GATEWAY_PORT}"`) ||
		strings.Contains(remote.installScript, `      - "${WEB_VUE3_PORT}:${WEB_VUE3_PORT}"`) {
		t.Fatalf("AIFAR business services should not bind host ports directly:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, "resolve_system_timezone") || !strings.Contains(remote.installScript, "timedatectl show -p Timezone") {
		t.Fatalf("AIFAR install script should resolve system timezone:\n%s", remote.installScript)
	}
	if strings.Contains(remote.installScript, "aifar-nacos:${NACOS_PORT_WEB}") || strings.Contains(remote.installScript, `NACOS_ENV="$APP_DIR/nacos/.env"`) {
		t.Fatalf("AIFAR install script should not configure bundled Nacos:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, "NACOS_CONNECT_HOST='10.0.0.50'") ||
		!strings.Contains(remote.installScript, `set_env NACOS_HOST "${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}" "$common_env"`) {
		t.Fatalf("AIFAR install script should use the external Nacos endpoint:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, "load_docker_images") ||
		!strings.Contains(remote.installScript, `require_local_image "bellsoft/liberica-openjre-rocky:21"`) ||
		!strings.Contains(remote.installScript, `require_local_image "nginx:stable-alpine"`) {
		t.Fatalf("AIFAR install script should load offline Docker base images before build:\n%s", remote.installScript)
	}
	for _, want := range []string{
		"check_nacos_dependency",
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("AIFAR install script should include dependency checks with %q:\n%s", want, remote.installScript)
		}
	}
	for _, want := range []string{
		`set_env NACOS_HOST "${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}" "$common_env"`,
		`set_env NACOS_PASSWORD "$NACOS_PASSWORD" "$secrets_env"`,
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("AIFAR install script should keep Nacos bootstrap env with %q:\n%s", want, remote.installScript)
		}
	}
	for _, forbidden := range []string{
		`set_env AIFAR_DB_`,
		`set_env SPRING_DATASOURCE`,
		`set_env AIFAR_REDIS`,
		`set_env SPRING_DATA_REDIS`,
		`set_env AIFAR_MINIO`,
		`set_env DROMARA_X_FILE_STORAGE`,
		`DB_HOST=`,
		`REDIS_`,
		`MINIO_`,
		`INIT_SQL`,
		`check_mysql_dependency`,
		`check_redis_dependency`,
		`check_minio_dependency`,
		`docker-sql`,
		`patch_nacos_sql`,
	} {
		if strings.Contains(remote.installScript, forbidden) {
			t.Fatalf("AIFAR install script should not inject business runtime env %q:\n%s", forbidden, remote.installScript)
		}
	}
	for _, want := range []string{
		"alpha_service_name",
		"gateway alpha-gateway",
		"permission alpha-permission",
		`set_env SPRING_APPLICATION_NAME "$app_name" "$service_env"`,
		`set_env SERVER_PORT "$port_value" "$service_env"`,
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("AIFAR install script should force alpha service names with %q:\n%s", want, remote.installScript)
		}
	}
}

func TestServiceInstallsSelectedAIFARModules(t *testing.T) {
	root := createAIFARBundle(t)
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "latest",
		ServerID: "srv-1",
		Language: "en",
		Parameters: map[string]any{
			"nacosHost":        "10.0.0.50",
			"selectedServices": []string{"oauth", "gateway", "web-vue3"},
		},
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.installScript, `SERVICE_ORDER='oauth gateway web-vue3'`) {
		t.Fatalf("install script should only iterate selected services, got:\n%s", remote.installScript)
	}
	if strings.Contains(remote.installScript, `SERVICE_ORDER='oauth permission system file message im contacts meeting gateway web-vue3'`) {
		t.Fatalf("install script should not use all services when selectedServices is provided:\n%s", remote.installScript)
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one AIFAR instance, got %+v", s.instances)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(s.instances[0].Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if got := stringSliceFromAny(metadata["services"]); strings.Join(got, " ") != "oauth gateway web-vue3" {
		t.Fatalf("expected selected services in metadata, got %#v from %s", got, s.instances[0].Metadata)
	}
	containers := mapFromMetadataValue(metadata["containers"])
	if _, ok := containers["permission"]; ok {
		t.Fatalf("metadata should not include unselected permission container: %s", s.instances[0].Metadata)
	}
	if len(s.releases) != 1 {
		t.Fatalf("expected one recorded release, got %+v", s.releases)
	}
	manifest := map[string]any{}
	if err := json.Unmarshal([]byte(s.releases[0].ManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if got := stringSliceFromAny(manifest["services"]); strings.Join(got, " ") != "oauth gateway web-vue3" {
		t.Fatalf("expected selected services in manifest, got %#v from %s", got, s.releases[0].ManifestJSON)
	}
	releaseContainers := mapFromMetadataValue(manifest["containers"])
	if _, ok := releaseContainers["permission"]; ok {
		t.Fatalf("manifest should not include unselected permission container: %s", s.releases[0].ManifestJSON)
	}
}

func TestServiceIgnoresBusinessDependencyParameters(t *testing.T) {
	root := createAIFARBundle(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"app-1": {ID: "app-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "latest",
		ServerID: "app-1",
		Language: "en",
		Parameters: map[string]any{
			"dbHost":                  "10.0.0.30",
			"dbPort":                  6446,
			"nacosHost":               "10.0.0.50",
			"redisMode":               "sentinel",
			"redisHost":               "10.0.0.41",
			"redisPort":               26379,
			"redisSentinelMasterName": "alpha-master",
			"redisSentinelNodes":      "10.0.0.41:26379,10.0.0.42:26379",
			"minioEndpoint":           "http://10.0.0.60:9000",
		},
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var instance store.AppInstance
	for _, candidate := range s.instances {
		if candidate.App == "aifar" {
			instance = candidate
			break
		}
	}
	if instance.ID == "" {
		t.Fatalf("expected AIFAR instance, got %+v", s.instances)
	}
	if strings.Contains(instance.Metadata, "secret-value") || strings.Contains(instance.Metadata, "redis-secret") || strings.Contains(instance.Metadata, "minio-secret") {
		t.Fatalf("metadata must not store database, redis, or minio credentials: %s", instance.Metadata)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{
		"dbHost",
		"dbPort",
		"dbNameNacos",
		"dbUser",
		"dbSource",
		"dbInstanceId",
		"redisMode",
		"redisHost",
		"redisPort",
		"redisDatabase",
		"redisSentinelMasterName",
		"redisSentinelNodes",
		"redisClusterNodes",
		"redisSource",
		"redisInstanceId",
		"minioEnableStorage",
		"minioPlatform",
		"minioEndpoint",
		"minioBucketName",
		"minioDomain",
		"minioBasePath",
		"minioSource",
		"minioInstanceId",
		"initSql",
	} {
		if _, ok := metadata[removed]; ok {
			t.Fatalf("metadata should not keep business dependency field %s: %s", removed, instance.Metadata)
		}
	}
	for _, forbidden := range []string{
		"DB_HOST='10.0.0.30'",
		"DB_PORT='6446'",
		"DB_USER=",
		"DB_PASSWORD=",
		"REDIS_MODE='sentinel'",
		"REDIS_SENTINEL_MASTER='alpha-master'",
		"REDIS_SENTINEL_NODES='10.0.0.41:26379,10.0.0.42:26379'",
		"MINIO_ENDPOINT='http://10.0.0.60:9000'",
		"INIT_SQL=",
	} {
		if strings.Contains(remote.installScript, forbidden) {
			t.Fatalf("install script should ignore business dependency parameter %q:\n%s", forbidden, remote.installScript)
		}
	}
}

func TestServiceUpdatesAIFARServiceArtifactAsPartialRelease(t *testing.T) {
	artifactDir := t.TempDir()
	artifactPath := filepath.Join(artifactDir, "oauth.jar")
	if err := os.WriteFile(artifactPath, []byte("new oauth jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.UpdateArtifact(context.Background(), ArtifactUpdateRequest{
		Instance:          instance,
		Server:            s.servers["srv-1"],
		Language:          "en",
		ServiceName:       "oauth",
		ArtifactLocalPath: artifactPath,
		ArtifactFileName:  "oauth.jar",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.joinedUploads(), "oauth.jar") || !strings.Contains(remote.joinedCommands(), "update-aifar-artifact.sh") {
		t.Fatalf("expected artifact upload and update script run, uploads=%s commands=%s", remote.joinedUploads(), remote.joinedCommands())
	}
	for _, want := range []string{
		`SERVICE_NAME='oauth'`,
		`RUNTIME_DIR="$INSTALL_ROOT/runtime"`,
		`apply_artifact`,
		`docker build -t "$image" "$APP_DIR/$SERVICE_NAME"`,
		`start_pod "$replica"`,
		`reconcile_runtime`,
		`aifar-agent reconcile-ingress --spec "$spec"`,
		`stop_old_pods`,
		`strip_web_nginx_runtime_routes "$service_dir"`,
		`curl -sS --connect-timeout $health_connect_timeout -o /dev/null ${health_protocol}://${health_host}:${port}/ || exit 1`,
		`APP_STARTUP_TIMEOUT 300`,
		`"kind": "rollout"`,
	} {
		if !strings.Contains(remote.updateScript, want) {
			t.Fatalf("rollout update script should contain %q:\n%s", want, remote.updateScript)
		}
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one instance, got %+v", s.instances)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(s.instances[0].Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["releaseId"] == "20260701T010203.000000000Z-docker-apps" || metadata["configHash"] == "base-config-hash" {
		t.Fatalf("expected release metadata to change, got %s", s.instances[0].Metadata)
	}
	lastUpdate, ok := metadata["lastRollout"].(map[string]any)
	if !ok || lastUpdate["service"] != "oauth" || lastUpdate["artifactFile"] != "oauth.jar" || lastUpdate["artifactSHA256"] == "" {
		t.Fatalf("expected lastRollout metadata, got %s", s.instances[0].Metadata)
	}
	if len(s.releases) != 1 || s.releases[0].InstanceID != instance.ID || s.releases[0].Status != "success" {
		t.Fatalf("expected one rollout release, got %+v", s.releases)
	}
	if !strings.Contains(s.releases[0].ManifestJSON, `"kind":"rollout"`) || !strings.Contains(s.releases[0].ManifestJSON, `"changedServices":["oauth"]`) {
		t.Fatalf("expected rollout release manifest, got %s", s.releases[0].ManifestJSON)
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(s.releases[0].ManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	releaseID, _ := metadata["releaseId"].(string)
	containers, _ := manifest["containers"].(map[string]any)
	if containers["oauth"] != releaseContainerName("oauth", releaseID) {
		t.Fatalf("expected oauth container to point at rollout revision, got %s", s.releases[0].ManifestJSON)
	}
}

func TestServiceUpdatesAIFARArtifactBundleAsSingleMultiServicePartialRelease(t *testing.T) {
	bundlePath := writeAlphaJarBundle(t, []bundleTestArtifact{
		{Service: "oauth", Module: "alpha-oauth", FileName: "alpha-oauth.jar", Content: "new oauth jar"},
		{Service: "gateway", Module: "alpha-gateway", FileName: "alpha-gateway.jar", Content: "new gateway jar"},
	})
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.UpdateArtifactBundle(context.Background(), ArtifactBundleUpdateRequest{
		Instance:        instance,
		Server:          s.servers["srv-1"],
		Language:        "en",
		BundleLocalPath: bundlePath,
		BundleFileName:  filepath.Base(bundlePath),
		Concurrency:     3,
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	uploads := remote.joinedUploads()
	if !strings.Contains(uploads, "alpha-oauth.jar") || !strings.Contains(uploads, "alpha-gateway.jar") {
		t.Fatalf("expected both service jars to be uploaded, uploads=%s", uploads)
	}
	if count := strings.Count(remote.joinedCommands(), "update-aifar-artifact-bundle.sh"); count != 1 {
		t.Fatalf("expected one bundle update script run, commands=%s", remote.joinedCommands())
	}
	for _, want := range []string{
		`CHANGED_SERVICES='oauth gateway'`,
		`DEPLOYMENT_CONCURRENCY=3`,
		`run_parallel_group "$non_entry"`,
		`service_changed gateway && rollout_service gateway`,
		`service_changed web-vue3 && rollout_service web-vue3`,
		`reconcile_runtime`,
		`aifar-agent reconcile-ingress --spec "$spec"`,
		`stop_old_pods "$service"`,
		`strip_web_nginx_runtime_routes "$service_dir"`,
		`curl -sS --connect-timeout $health_connect_timeout -o /dev/null ${health_protocol}://${health_host}:${port}/ || exit 1`,
		`APP_STARTUP_TIMEOUT 300`,
		`"kind": "rollout-bundle"`,
	} {
		if !strings.Contains(remote.bundleScript, want) {
			t.Fatalf("bundle rollout script should contain %q:\n%s", want, remote.bundleScript)
		}
	}
	if len(s.releases) != 1 {
		t.Fatalf("expected one multi-service rollout release, got %+v", s.releases)
	}
	if !strings.Contains(s.releases[0].ManifestJSON, `"kind":"rollout-bundle"`) ||
		!strings.Contains(s.releases[0].ManifestJSON, `"changedServices":["oauth","gateway"]`) ||
		!strings.Contains(s.releases[0].ManifestJSON, `"deploymentConcurrency":3`) {
		t.Fatalf("expected multi-service release manifest, got %+v", s.releases)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(s.instances[0].Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(s.releases[0].ManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	releaseID, _ := metadata["releaseId"].(string)
	containers, _ := manifest["containers"].(map[string]any)
	if containers["gateway"] != releaseContainerName("gateway", releaseID) {
		t.Fatalf("expected effective bundle containers, got %s", s.releases[0].ManifestJSON)
	}
	lastUpdate, ok := metadata["lastRollout"].(map[string]any)
	if !ok || lastUpdate["service"] != "bundle" || int(lastUpdate["deploymentConcurrency"].(float64)) != 3 {
		t.Fatalf("expected final metadata to point at bundle update, got %s", s.instances[0].Metadata)
	}
}

func TestServiceRejectsMismatchedJavaArtifactFileName(t *testing.T) {
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "alpha-oauth.jar")
	if err := os.WriteFile(artifactPath, []byte("oauth jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := Service{}
	err := service.ValidateArtifactUpdate(ArtifactUpdateRequest{
		Instance:          installedAIFARInstance(t),
		Server:            store.Server{ID: "srv-1"},
		Language:          "en",
		ServiceName:       "gateway",
		ArtifactLocalPath: artifactPath,
		ArtifactFileName:  "alpha-oauth.jar",
	})
	if err == nil {
		t.Fatal("expected mismatched gateway/oauth artifact to be rejected")
	}
}

func TestServiceAcceptsArtifactBundleManifestWithUTF8BOM(t *testing.T) {
	bundlePath := writeAlphaJarBundleWithManifestPrefix(t, []bundleTestArtifact{
		{Service: "oauth", Module: "alpha-oauth", FileName: "alpha-oauth.jar", Content: "new oauth jar"},
	}, []byte{0xEF, 0xBB, 0xBF})
	service := Service{}
	err := service.ValidateArtifactBundleUpdate(ArtifactBundleUpdateRequest{
		Instance:        installedAIFARInstance(t),
		Server:          store.Server{ID: "srv-1"},
		Language:        "en",
		BundleLocalPath: bundlePath,
		BundleFileName:  filepath.Base(bundlePath),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServiceRejectsMismatchedArtifactBundleFileName(t *testing.T) {
	bundlePath := writeAlphaJarBundle(t, []bundleTestArtifact{
		{Service: "gateway", Module: "alpha-gateway", FileName: "alpha-oauth.jar", Content: "wrong jar"},
	})
	service := Service{}
	err := service.ValidateArtifactBundleUpdate(ArtifactBundleUpdateRequest{
		Instance:        installedAIFARInstance(t),
		Server:          store.Server{ID: "srv-1"},
		Language:        "en",
		BundleLocalPath: bundlePath,
		BundleFileName:  filepath.Base(bundlePath),
	})
	if err == nil {
		t.Fatal("expected mismatched bundle artifact file name to be rejected")
	}
}

func TestModulePlansArtifactBundleUpdateAsSinglePartialRelease(t *testing.T) {
	bundlePath := writeAlphaJarBundle(t, []bundleTestArtifact{
		{Service: "oauth", Module: "alpha-oauth", FileName: "alpha-oauth.jar", Content: "new oauth jar"},
		{Service: "gateway", Module: "alpha-gateway", FileName: "alpha-gateway.jar", Content: "new gateway jar"},
	})
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{instance},
	}
	module := NewModule(s, &fakeRemote{})
	plan, err := module.PlanArtifactBundleUpdate(context.Background(), registry.ArtifactBundleUpdateRequest{
		Instance:        instance,
		Server:          s.servers["srv-1"],
		Language:        "en",
		BundleLocalPath: bundlePath,
		BundleFileName:  filepath.Base(bundlePath),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 4 {
		t.Fatalf("expected 4 planned steps, got %+v", plan)
	}
	wantNames := []string{
		"validate-artifact",
		"upload-artifact",
		"apply-update",
		"record-release",
	}
	for idx, want := range wantNames {
		if plan[idx].Name != want || plan[idx].Order != idx+1 {
			t.Fatalf("unexpected plan step %d: %+v", idx, plan[idx])
		}
	}
}

func TestServiceResolvesManagedNacosInstance(t *testing.T) {
	root := createAIFARBundle(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"app-1":   {ID: "app-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
			"nacos-1": {ID: "nacos-1", Name: "nacos-1", Host: "10.0.0.50", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{
			{
				ID:       "nacos-node-1",
				App:      "nacos",
				Version:  "2.4.3",
				ServerID: "nacos-1",
				Status:   "running",
				Topology: "standalone",
				Metadata: mustMetadata(t, map[string]any{
					"endpoint": "http://10.0.0.50:9849/nacos",
					"port":     9849,
				}),
			},
		},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "latest",
		ServerID: "app-1",
		Language: "en",
		Parameters: map[string]any{
			"nacosSource":     "existing",
			"nacosInstanceId": "nacos-node-1",
		},
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var instance store.AppInstance
	for _, candidate := range s.instances {
		if candidate.App == "aifar" {
			instance = candidate
			break
		}
	}
	if instance.ID == "" {
		t.Fatalf("expected AIFAR instance, got %+v", s.instances)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["nacosSource"] != "existing" || metadata["nacosInstanceId"] != "nacos-node-1" {
		t.Fatalf("expected managed Nacos source metadata, got %s", instance.Metadata)
	}
	if metadata["nacosHost"] != "10.0.0.50" || int(metadata["nacosPort"].(float64)) != 9849 || metadata["nacosEndpoint"] != "10.0.0.50:9849" || int(metadata["nacosApiPort"].(float64)) != 10849 {
		t.Fatalf("expected selected Nacos host and ports from instance metadata, got %s", instance.Metadata)
	}
	for _, want := range []string{
		"NACOS_CONNECT_HOST='10.0.0.50'",
		"NACOS_PORT_WEB='9849'",
		"NACOS_PORT_API='10849'",
		`set_env NACOS_HOST "${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}" "$common_env"`,
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("install script should contain %q:\n%s", want, remote.installScript)
		}
	}
}

func TestModuleValidateInstallRequiresDockerRuntime(t *testing.T) {
	root := createAIFARBundle(t)
	module := NewModule(&fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
	}}, &fakeRemote{})
	err := module.ValidateInstall(context.Background(), registry.InstallRequest{
		Version:    "latest",
		ServerID:   "srv-1",
		Language:   "en",
		Parameters: aifarModuleValidationParams(),
	}, aifarModuleValidationResources(root))
	if err == nil || !strings.Contains(err.Error(), "Docker Engine") {
		t.Fatalf("expected Docker runtime validation error, got %v", err)
	}
}

func TestModuleValidateInstallAcceptsDockerRuntime(t *testing.T) {
	root := createAIFARBundle(t)
	module := NewModule(&fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{
			{
				ID:       "docker-1",
				App:      "docker",
				Version:  "24.0.9",
				ServerID: "srv-1",
				Status:   "installed",
				Metadata: mustMetadata(t, map[string]any{"dockerHost": "tcp://10.0.0.10:2375"}),
			},
		},
	}, &fakeRemote{})
	err := module.ValidateInstall(context.Background(), registry.InstallRequest{
		Version:    "latest",
		ServerID:   "srv-1",
		Language:   "en",
		Parameters: aifarModuleValidationParams(),
	}, aifarModuleValidationResources(root))
	if err != nil {
		t.Fatal(err)
	}
}

func TestModuleValidateInstallRejectsDockerWithoutCompose(t *testing.T) {
	root := createAIFARBundle(t)
	module := NewModule(&fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{
			{
				ID:       "docker-1",
				App:      "docker",
				Version:  "24.0.9",
				ServerID: "srv-1",
				Status:   "running",
				Metadata: mustMetadata(t, map[string]any{
					"lastCheck": map[string]any{
						"dockerVersion":  "Docker version 24.0.9",
						"composeVersion": "",
					},
				}),
			},
		},
	}, &fakeRemote{})
	err := module.ValidateInstall(context.Background(), registry.InstallRequest{
		Version:    "latest",
		ServerID:   "srv-1",
		Language:   "en",
		Parameters: aifarModuleValidationParams(),
	}, aifarModuleValidationResources(root))
	if err == nil || !strings.Contains(err.Error(), "Docker Engine") {
		t.Fatalf("expected Docker Compose validation error, got %v", err)
	}
}

func TestSelectBundleIgnoresDockerSQLVersion(t *testing.T) {
	root := createAIFARBundle(t)
	resources := []store.Resource{
		{App: "aifar", Part: "backend", Version: "docker-sql", Path: filepath.Join(root, "docker-sql", "aifar_init.sql")},
		{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")},
	}
	bundle, err := SelectBundle(resources, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Version != "docker-apps" || filepath.Base(bundle.AppDir) != "docker-apps" {
		t.Fatalf("expected docker-apps bundle, got %+v", bundle)
	}
	if _, err := SelectBundle(resources, "docker-sql"); err == nil {
		t.Fatal("expected docker-sql to be rejected as an installable AIFAR version")
	}
}

func TestCreateBundleArchiveExcludesBundledNacosAndSQL(t *testing.T) {
	root := createAIFARBundle(t)
	nacosDir := filepath.Join(root, "docker-apps", "nacos")
	if err := os.MkdirAll(nacosDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nacosDir, "docker-compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nacosDir, ".env"), []byte("APP_CONTAINER_NAME=aifar-nacos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	archivePath, err := CreateBundleArchive(Bundle{
		Version: appBundleVersion,
		Root:    root,
		AppDir:  filepath.Join(root, appBundleDir),
		SQLDir:  filepath.Join(root, sqlBundleDir),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(archivePath)

	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(header.Name, "docker-apps/nacos") || strings.HasPrefix(header.Name, "docker-sql") {
			t.Fatalf("archive should exclude bundled Nacos and SQL assets, found %s", header.Name)
		}
	}
}

func mustMetadata(t *testing.T, value map[string]any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func stringSliceFromAny(value any) []string {
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...)
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	}
	return nil
}

func aifarModuleValidationResources(root string) []store.Resource {
	return []store.Resource{{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")}}
}

func aifarModuleValidationParams() map[string]any {
	return map[string]any{
		"nacosHost": "10.0.0.50",
	}
}

func TestServiceChecksAIFARServiceAndUpdatesStatus(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-1",
		App:      "aifar",
		Version:  "docker-apps",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"installRoot":"/aifar/apps/admin"}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{statusStdout: strings.Join([]string{
		"status=degraded",
		"installRootExists=true",
		"totalContainers=2",
		"runningContainers=1",
		"unhealthyContainers=1",
		"containers=aifar-gateway:false:,aifar-web-vue3:true:unhealthy,",
	}, "\n")}
	service := NewService(s, remote)
	result, err := service.Check(context.Background(), CheckRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "degraded" {
		t.Fatalf("expected degraded status, got %+v", result)
	}
	if len(s.instances) != 1 || s.instances[0].Status != "degraded" || !strings.Contains(s.instances[0].Metadata, "aifar-gateway") {
		t.Fatalf("expected status to be persisted: %+v", s.instances)
	}
}

func TestParseStatusOutputIncludesIngressAndStaleContainers(t *testing.T) {
	status := parseStatusOutput(strings.Join([]string{
		"status=running",
		"installRootExists=true",
		"releaseId=rel-new",
		"totalContainers=2",
		"runningContainers=2",
		"unhealthyContainers=0",
		"staleContainers=1",
		"ingressRunning=true",
		"containers=aifar-gateway-rel-new:true:healthy,aifar-web-vue3-rel-new:true:healthy,",
	}, "\n"))
	if status.Status != "running" || !status.IngressRunning || status.StaleContainers != 1 {
		t.Fatalf("expected ingress and stale status fields, got %+v", status)
	}
	if len(status.Containers) != 2 {
		t.Fatalf("expected parsed current containers, got %+v", status.Containers)
	}
}

func TestStatusCommandScansK8sLikePodsAndAgentRuntime(t *testing.T) {
	command := statusCommand("/aifar/apps/admin")
	for _, want := range []string{
		`MODEL_FILE="$INSTALL_ROOT/.aifar/model.json"`,
		`[ "$MODEL" = "k8s-like-v1" ]`,
		`aifar-agent status`,
		`label=aifar.component=pod`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("status command should inspect k8s-like orchestration with %q:\n%s", want, command)
		}
	}
	for _, forbidden := range []string{
		`legacy-release-v1`,
		`serviceProxies=`,
		`currentRelease=`,
	} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("status command should be agent-only and not include %q:\n%s", forbidden, command)
		}
	}
}

func createAIFARBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	appDir := filepath.Join(root, "docker-apps")
	sqlDir := filepath.Join(root, "docker-sql")
	imageDir := filepath.Join(root, "docker-images")
	if err := os.MkdirAll(sqlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sqlDir, "aifar_cloud_nacos.sql"), []byte("select 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sqlDir, "aifar_init.sql"), []byte("select 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{"openjre-rocky-21.tar", "nginx-stable-alpine.tar"} {
		if err := os.WriteFile(filepath.Join(imageDir, image), []byte("fake image tar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("APP_NETWORK_NAME=aifar-network\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"gateway", "web-vue3"} {
		dir := filepath.Join(appDir, service)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_CONTAINER_NAME=aifar-"+service+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
