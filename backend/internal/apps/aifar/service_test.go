package aifar

import (
	"archive/tar"
	"compress/gzip"
	"context"
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

type fakeRemote struct {
	mu            sync.Mutex
	commands      []string
	uploads       []string
	installScript string
	updateScript  string
	statusStdout  string
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	if strings.Contains(command, "AIFAR_SERVICE_STATUS") && f.statusStdout != "" {
		return adapter.CommandResult{Stdout: f.statusStdout}, nil
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

func TestOptionsDefaultsUseRequestedAIFARValues(t *testing.T) {
	opts := optionsFromParameters(nil)
	if opts.Timezone != "system" || opts.NacosWebPort != 8848 || opts.NacosNamespace != "prod" || opts.NacosSource != dependencyManual || !opts.MinioEnableStorage {
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
			"dbHost":        "10.0.0.20",
			"dbPort":        3306,
			"nacosHost":     "10.0.0.50",
			"webPort":       18080,
			"minioEndpoint": "http://10.0.0.60:9000",
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
	if strings.Contains(instance.Metadata, "secret-value") || strings.Contains(instance.Metadata, "minio-secret") || strings.Contains(instance.Metadata, "aifar-file") {
		t.Fatalf("metadata must not store database password or MinIO credentials: %s", instance.Metadata)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["endpoint"] != "10.0.0.10:18080" || metadata["networkName"] != defaultNetworkName {
		t.Fatalf("unexpected metadata: %s", instance.Metadata)
	}
	if metadata["layout"] != releaseLayout || metadata["releaseId"] == "" || metadata["releaseRetention"].(float64) != releaseKeepCount {
		t.Fatalf("expected release layout metadata, got %s", instance.Metadata)
	}
	if len(s.releases) != 1 || s.releases[0].InstanceID != instance.ID || s.releases[0].Status != "success" {
		t.Fatalf("expected one recorded release, got %+v", s.releases)
	}
	if metadata["nacosEndpoint"] != "10.0.0.50:8848" || metadata["nacosHost"] != "10.0.0.50" || int(metadata["nacosPort"].(float64)) != 8848 {
		t.Fatalf("expected external Nacos endpoint metadata, got %s", instance.Metadata)
	}
	if metadata["minioEndpoint"] != "http://10.0.0.60:9000" || metadata["minioBucketName"] != "aifar" || metadata["minioDomain"] != "http://10.0.0.60:9000/aifar/" {
		t.Fatalf("expected MinIO metadata without credentials, got %s", instance.Metadata)
	}
	if !strings.Contains(remote.joinedUploads(), "aifar-service-bundle-") || !strings.Contains(remote.joinedCommands(), "install-aifar.sh") {
		t.Fatalf("expected bundle upload and install script run, uploads=%s commands=%s", remote.joinedUploads(), remote.joinedCommands())
	}
	if !strings.Contains(remote.installScript, `open_firewall_ports "$GATEWAY_PORT" "$WEB_VUE3_PORT"`) ||
		!strings.Contains(remote.installScript, `allow_selinux_ports http_port_t "$GATEWAY_PORT" "$WEB_VUE3_PORT"`) ||
		strings.Contains(remote.installScript, `open_firewall_ports "$GATEWAY_PORT" "$WEB_VUE3_PORT" "$NACOS_PORT_WEB"`) {
		t.Fatalf("AIFAR install script should open only AIFAR service ports:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, "ensure_network") ||
		!strings.Contains(remote.installScript, "external: true") ||
		!strings.Contains(remote.installScript, "name: ${APP_NETWORK_NAME}") {
		t.Fatalf("AIFAR install script should create and use the shared Docker network as external:\n%s", remote.installScript)
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
		"check_dependencies",
		"check_nacos_dependency",
		"check_mysql_dependency",
		"check_redis_dependency",
		"check_minio_dependency",
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
	} {
		if strings.Contains(remote.installScript, forbidden) {
			t.Fatalf("AIFAR install script should not inject business runtime env %q:\n%s", forbidden, remote.installScript)
		}
	}
	if strings.Contains(remote.installScript, "patch_nacos_server_port") || !strings.Contains(remote.installScript, "patch_nacos_sql_namespace") {
		t.Fatalf("AIFAR install script should only patch Nacos SQL namespace defaults:\n%s", remote.installScript)
	}
	for _, want := range []string{
		"patch_nacos_sql_service_names",
		"alpha_service_name",
		"gateway alpha-gateway",
		"permission alpha-permission",
		`set_env SPRING_APPLICATION_NAME "$app_name" "$service_env"`,
		`sed "s/aifar-${service}/${app_name}/g"`,
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("AIFAR install script should force alpha service names with %q:\n%s", want, remote.installScript)
		}
	}
	for _, want := range []string{
		`RELEASES_DIR="$INSTALL_ROOT/releases"`,
		`CURRENT_LINK="$INSTALL_ROOT/current"`,
		`java-common.env`,
		`java-secrets.env`,
		`APP_RESTART_POLICY=no`,
		`wait_release_ready`,
		`apply_restart_policy`,
		`cleanup_old_releases`,
		`RELEASE_KEEP_COUNT=3`,
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("AIFAR install script should include release-based layout with %q:\n%s", want, remote.installScript)
		}
	}
}

func TestServiceUsesManualBusinessDependencyParameters(t *testing.T) {
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
	if strings.Contains(instance.Metadata, "secret-value") || strings.Contains(instance.Metadata, "redis-secret") || strings.Contains(instance.Metadata, "minio-secret") || strings.Contains(instance.Metadata, "aifar-file") {
		t.Fatalf("metadata must not store database, redis, or minio credentials: %s", instance.Metadata)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["dbHost"] != "10.0.0.30" || int(metadata["dbPort"].(float64)) != 6446 {
		t.Fatalf("expected manual MySQL endpoint to be used, got %s", instance.Metadata)
	}
	if metadata["redisMode"] != "sentinel" || metadata["redisHost"] != "10.0.0.41" || int(metadata["redisPort"].(float64)) != 26379 {
		t.Fatalf("expected manual Redis Sentinel endpoint to be used, got %s", instance.Metadata)
	}
	if metadata["minioEndpoint"] != "http://10.0.0.60:9000" {
		t.Fatalf("expected manual MinIO endpoint to be used, got %s", instance.Metadata)
	}
	for _, removed := range []string{"dbSource", "dbInstanceId", "redisSource", "redisInstanceId", "minioSource", "minioInstanceId"} {
		if _, ok := metadata[removed]; ok {
			t.Fatalf("metadata should not keep managed dependency field %s: %s", removed, instance.Metadata)
		}
	}
	for _, want := range []string{
		"DB_HOST='10.0.0.30'",
		"DB_PORT='6446'",
		"REDIS_MODE='sentinel'",
		"REDIS_SENTINEL_MASTER='alpha-master'",
		"REDIS_SENTINEL_NODES='10.0.0.41:26379,10.0.0.42:26379'",
		"MINIO_ENDPOINT='http://10.0.0.60:9000'",
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("install script should contain %q:\n%s", want, remote.installScript)
		}
	}
}

func TestServiceUpdatesAIFARServiceArtifactAsPartialRelease(t *testing.T) {
	artifactDir := t.TempDir()
	artifactPath := filepath.Join(artifactDir, "oauth.jar")
	if err := os.WriteFile(artifactPath, []byte("new oauth jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	instance := store.AppInstance{
		ID:       "aifar-1",
		App:      "aifar",
		Version:  "docker-apps",
		ServerID: "srv-1",
		Status:   "installed",
		Topology: defaultTopology,
		Metadata: mustMetadata(t, map[string]any{
			"installRoot":      "/aifar/apps/admin",
			"layout":           releaseLayout,
			"releaseId":        "20260701T010203.000000000Z-docker-apps",
			"releaseVersion":   "docker-apps",
			"releasePath":      "/aifar/apps/admin/releases/20260701T010203.000000000Z-docker-apps",
			"currentRelease":   "/aifar/apps/admin/current",
			"configHash":       "base-config-hash",
			"releaseRetention": releaseKeepCount,
			"services":         serviceOrder,
		}),
	}
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
		`cp -a "$BASE_RELEASE/." "$RELEASE_DIR/"`,
		`apply_java_artifact`,
		`retag_selected_service`,
		`compose --env-file env/compose.env -f compose.yaml up -d --build "$SERVICE_NAME"`,
		`rollback_service "$BASE_RELEASE"`,
		`"kind": "partial"`,
		`"changedServices": ["$SERVICE_NAME"]`,
	} {
		if !strings.Contains(remote.updateScript, want) {
			t.Fatalf("update script should contain %q:\n%s", want, remote.updateScript)
		}
	}
	if strings.Contains(remote.updateScript, "cleanup_release_images") || strings.Contains(remote.updateScript, "docker image rm") {
		t.Fatalf("partial update script should not delete inherited image refs:\n%s", remote.updateScript)
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
	lastUpdate, ok := metadata["lastPartialUpdate"].(map[string]any)
	if !ok || lastUpdate["service"] != "oauth" || lastUpdate["artifactFile"] != "oauth.jar" || lastUpdate["artifactSHA256"] == "" {
		t.Fatalf("expected lastPartialUpdate metadata, got %s", s.instances[0].Metadata)
	}
	if len(s.releases) != 1 || s.releases[0].InstanceID != instance.ID || s.releases[0].Status != "success" {
		t.Fatalf("expected one partial release, got %+v", s.releases)
	}
	if !strings.Contains(s.releases[0].ManifestJSON, `"kind":"partial"`) || !strings.Contains(s.releases[0].ManifestJSON, `"changedServices":["oauth"]`) {
		t.Fatalf("expected partial release manifest, got %s", s.releases[0].ManifestJSON)
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
			"dbHost":          "10.0.0.20",
			"dbPort":          3306,
			"minioEndpoint":   "http://10.0.0.60:9000",
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

func TestCreateBundleArchiveExcludesBundledNacos(t *testing.T) {
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
		if strings.HasPrefix(header.Name, "docker-apps/nacos") {
			t.Fatalf("archive should exclude bundled Nacos, found %s", header.Name)
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

func aifarModuleValidationResources(root string) []store.Resource {
	return []store.Resource{{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")}}
}

func aifarModuleValidationParams() map[string]any {
	return map[string]any{
		"nacosHost":     "10.0.0.50",
		"dbHost":        "10.0.0.20",
		"dbPort":        3306,
		"minioEndpoint": "http://10.0.0.60:9000",
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
