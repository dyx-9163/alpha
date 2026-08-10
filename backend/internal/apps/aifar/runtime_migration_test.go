package aifar

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

type runtimeMigrationRemote struct {
	mu               sync.Mutex
	legacyJSON       []byte
	states           map[string]runtimeagent.DeploymentState
	agentInspection  string
	commands         []string
	uploadPaths      []string
	uploadBodies     [][]byte
	bootstrapCalls   int
	archiveCalls     int
	agentHasSwitched bool
}

func (f *fakeStore) CommitAIFARRuntimeMigrationWithLock(commit store.AIFARRuntimeMigrationCommit) (store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.failSaveOn > 0 && f.saveCalls == f.failSaveOn {
		return store.AppInstance{}, errors.New("control-plane save failed")
	}
	if len(commit.Deployments) == 0 {
		return store.AppInstance{}, store.ErrAIFARDeploymentGenerationConflict
	}
	indexes := make([]int, 0, len(commit.Deployments))
	for _, item := range commit.Deployments {
		found := -1
		for idx, current := range f.deployments {
			if current.InstanceID == commit.InstanceID && current.ServiceName == item.Expected.ServiceName {
				if current.Generation != item.Expected.Generation || current.DesiredReplicas != item.Expected.DesiredReplicas || current.CurrentRevision != item.Expected.CurrentRevision || current.SpecJSON != item.Expected.SpecJSON || current.Generation > 1 {
					return store.AppInstance{}, store.ErrAIFARDeploymentGenerationConflict
				}
				found = idx
				break
			}
		}
		if found < 0 {
			return store.AppInstance{}, store.ErrAIFARDeploymentGenerationConflict
		}
		indexes = append(indexes, found)
	}
	if len(indexes) != len(f.deployments) {
		return store.AppInstance{}, store.ErrAIFARDeploymentGenerationConflict
	}
	instanceIndex := -1
	for idx, current := range f.instances {
		if current.ID == commit.InstanceID {
			if !current.UpdatedAt.Equal(commit.ExpectedInstanceUpdatedAt) {
				return store.AppInstance{}, store.ErrAppInstanceConflict
			}
			instanceIndex = idx
			break
		}
	}
	if instanceIndex < 0 {
		return store.AppInstance{}, store.ErrAppInstanceConflict
	}
	for idx, deploymentIndex := range indexes {
		f.deployments[deploymentIndex] = commit.Deployments[idx].Next
	}
	saved := f.instances[instanceIndex]
	saved.Metadata = commit.NextMetadata
	saved.UpdatedAt = time.Now().UTC()
	if !saved.UpdatedAt.After(commit.ExpectedInstanceUpdatedAt) {
		saved.UpdatedAt = commit.ExpectedInstanceUpdatedAt.Add(time.Millisecond)
	}
	f.instances[instanceIndex] = saved
	return saved, nil
}

func (r *runtimeMigrationRemote) Run(ctx context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return adapter.CommandResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	switch {
	case strings.Contains(command, "AIFAR_AGENT_CHECK"):
		inspection := r.agentInspection
		if inspection == "" {
			inspection = runtimeMigrationAgentInspection()
		}
		return adapter.CommandResult{Stdout: inspection}, nil
	case strings.Contains(command, "AIFAR_RUNTIME_MIGRATION_READ"):
		marker := "legacy"
		if r.agentHasSwitched {
			marker = "switched"
		}
		return adapter.CommandResult{Stdout: fmt.Sprintf("model=%s\nlegacy=%s\n", marker, base64.StdEncoding.EncodeToString(r.legacyJSON))}, nil
	case strings.Contains(command, "aifar-agent bootstrap-runtime --spec"):
		r.bootstrapCalls++
		if r.agentHasSwitched {
			return adapter.CommandResult{Stderr: "LEGACY_RUNTIME_SPEC_DISABLED"}, errors.New("agent rejected legacy writer")
		}
		r.agentHasSwitched = true
		acceptance := runtimeagent.LegacyBootstrapAcceptance{Accepted: true, InstanceID: "aifar-legacy"}
		for _, serviceName := range sortedMigrationStateNames(r.states) {
			state := r.states[serviceName]
			acceptance.Deployments = append(acceptance.Deployments, runtimeagent.DeploymentAcceptance{
				Accepted: true, Generation: state.Generation, SpecHash: state.SpecHash,
			})
		}
		data, _ := json.Marshal(acceptance)
		return adapter.CommandResult{Stdout: string(data)}, nil
	case strings.Contains(command, "aifar-agent get-deployment"):
		for serviceName, state := range r.states {
			if strings.Contains(command, "--service '"+serviceName+"'") {
				data, _ := json.Marshal(state)
				return adapter.CommandResult{Stdout: string(data)}, nil
			}
		}
		return adapter.CommandResult{}, errors.New("deployment not found")
	case strings.Contains(command, "AIFAR_RUNTIME_MIGRATION_ARCHIVE"):
		r.archiveCalls++
		return adapter.CommandResult{Stdout: "archived"}, nil
	default:
		return adapter.CommandResult{Stdout: "ok"}, nil
	}
}

func (r *runtimeMigrationRemote) UploadFile(_ context.Context, _ store.Server, localPath, remotePath string, mode os.FileMode) error {
	if mode != 0o600 {
		return errors.New("migration staging must use mode 0600")
	}
	body, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploadPaths = append(r.uploadPaths, remotePath)
	r.uploadBodies = append(r.uploadBodies, body)
	return nil
}

func (r *runtimeMigrationRemote) joinedCommands() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.commands, "\n")
}

func sortedMigrationStateNames(states map[string]runtimeagent.DeploymentState) []string {
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	if len(names) == 2 && names[0] > names[1] {
		names[0], names[1] = names[1], names[0]
	}
	return names
}

func runtimeMigrationAgentInspection() string {
	status, _ := json.Marshal(map[string]any{
		"status": "running", "version": runtimeagent.DefaultAgentVersion,
		"features": []string{
			"service-manifest-v1", "service-generation-v1", "per-service-reconcile",
			"per-service-restart", "service-conditions-v1",
		},
	})
	return "AIFAR_AGENT_CHECK\nagentFound=true\nstatus=" + string(status) + "\nsha256=" + strings.Repeat("a", 64) + "\n"
}

func runtimeMigrationFixture(t *testing.T, replicas int) (*fakeStore, *runtimeMigrationRemote, RuntimeMigrationRequest) {
	t.Helper()
	revision := "20260701T010203.000000000Z-runtime-v2"
	installRoot := "/aifar/apps/admin"
	serviceName := "permission"
	metadata := map[string]any{
		"installRoot": installRoot, "runtimeSpecPath": runtimeSpecPath(installRoot),
		"runtimeDir": installRoot + "/runtime", "envDir": installRoot + "/runtime/env",
		"orchestrationModel": orchestrationModelK8sLikeV1,
		"services":           []string{serviceName}, "desiredReplicas": map[string]int{serviceName: replicas},
		"currentRevision": revision, "serviceRevisions": map[string]string{serviceName: revision},
		"gatewayPort": defaultGatewayPort, "webPort": defaultWebPort, "ingressNetwork": defaultNetworkName,
		"serviceCatalog": serviceCatalogMetadata([]serviceDefinition{{
			Name: serviceName, Kind: "java", ApplicationName: "alpha-permission", Port: defaultPermissionPort,
			HealthPath: "/actuator/health/readiness", AffinityPolicy: "round-robin",
		}}),
	}
	instance := store.AppInstance{
		ID: "aifar-legacy", App: AppName, Version: "runtime-v2", ServerID: "srv-legacy",
		Status: "installed", Topology: defaultTopology, Metadata: mustMetadata(t, metadata),
	}
	server := store.Server{ID: "srv-legacy", Host: "10.0.0.10", DeployDir: "/aifar/apps"}
	definition, _ := catalogDefinition(serviceDefinitionsFromMetadata(metadata), serviceName)
	current := store.AIFARDeployment{
		InstanceID: instance.ID, ServiceName: serviceName, DesiredReplicas: replicas,
		CurrentRevision: revision, Generation: 1, Status: "active",
	}
	manifest := runtimeagent.NormalizeDeploymentManifest(runtimeManifestDefaults(instance.ID, installRoot, definition, current, 1, metadata))
	legacy := runtimeagent.NormalizeSpec(runtimeagent.LegacyRuntimeSpec{
		Version: runtimeagent.DefaultAgentVersion, InstanceID: instance.ID, InstallRoot: installRoot,
		Network: defaultNetworkName, Deployments: []runtimeagent.DeploymentSpec{manifest.Spec},
		Services: []runtimeagent.ServiceSpec{manifest.Service},
		Ingress: runtimeagent.IngressSpec{
			Mode: runtimeagent.DefaultIngressMode, GatewayService: "gateway", WebService: "web-vue3",
			GatewayPort: defaultGatewayPort, WebPort: defaultWebPort,
		},
	})
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := runtimeagent.DeploymentManifestSpecHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	remote := &runtimeMigrationRemote{legacyJSON: legacyJSON, states: map[string]runtimeagent.DeploymentState{
		serviceName: {
			InstanceID: instance.ID, ServiceName: serviceName, Generation: 1,
			SpecHash: hash, DesiredReplicas: replicas,
		},
	}}
	control := &fakeStore{
		servers: map[string]store.Server{server.ID: server}, instances: []store.AppInstance{instance},
		deployments: []store.AIFARDeployment{current},
	}
	return control, remote, RuntimeMigrationRequest{
		Instance: instance, Server: server, Actor: "operator", TaskID: "task-migrate", Reason: "upgrade runtime model",
	}
}

func TestRuntimeMigrationAdoptsExistingContainersWithoutRestart(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	service := NewService(control, remote)
	if err := service.MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	commands := remote.joinedCommands()
	for _, forbidden := range []string{"docker restart", "docker rm", "docker run", "docker create"} {
		if strings.Contains(commands, forbidden) {
			t.Fatalf("migration restarted, removed, or recreated containers: %s", commands)
		}
	}
	saved, err := control.GetAppInstance(req.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringFromMetadata(metadataFromInstance(saved), "orchestrationModel", ""); got != orchestrationModelServiceControllerV1 {
		t.Fatalf("model=%q, want %q", got, orchestrationModelServiceControllerV1)
	}
	if remote.bootstrapCalls != 1 || remote.archiveCalls != 1 {
		t.Fatalf("bootstrap=%d archive=%d, want one atomic switch and one archive", remote.bootstrapCalls, remote.archiveCalls)
	}
	if len(remote.uploadPaths) != 1 || !strings.HasSuffix(remote.uploadPaths[0], "/migration-legacy-spec.json") || !bytes.Equal(remote.uploadBodies[0], remote.legacyJSON) {
		t.Fatalf("migration did not stage the exact validated legacy bytes: paths=%v", remote.uploadPaths)
	}
	bootstrapCommand := ""
	for _, command := range remote.commands {
		if strings.Contains(command, "aifar-agent bootstrap-runtime --spec") {
			bootstrapCommand = command
			break
		}
	}
	if !strings.Contains(bootstrapCommand, "/migration-legacy-spec.json") || strings.HasSuffix(strings.TrimSpace(bootstrapCommand), "/runtime-spec.json'") {
		t.Fatalf("Agent bootstrap did not use exact staged input: %q", bootstrapCommand)
	}
	deployments, err := control.ListAIFARDeployments(req.Instance.ID)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("deployments=%+v err=%v", deployments, err)
	}
	if deployments[0].Generation != 1 || deployments[0].DesiredReplicas != 1 || deployments[0].SpecJSON == "" || deployments[0].Status != "Accepted" {
		t.Fatalf("migration did not persist accepted generation/hash-bound desired state: %+v", deployments[0])
	}
}

func TestRuntimeMigrationCommitsThroughExactSQLiteMaintenanceOwner(t *testing.T) {
	fixture, remote, req := runtimeMigrationFixture(t, 1)
	db := openAIFARTestStore(t)
	instance, err := db.SaveAppInstance(fixture.instances[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(fixture.deployments[0]); err != nil {
		t.Fatal(err)
	}
	req.Instance = instance
	if err := NewService(db, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataFromInstance(saved)
	if stringFromMetadata(metadata, "orchestrationModel", "") != orchestrationModelServiceControllerV1 {
		t.Fatalf("real Store did not commit model marker: %s", saved.Metadata)
	}
	locks, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Fatalf("migration maintenance lock was not released: %+v", locks)
	}
}

func TestMigrationPreservesOfflineReplicaZero(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 0)
	if err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	deployments, _ := control.ListAIFARDeployments(req.Instance.ID)
	if len(deployments) != 1 || deployments[0].DesiredReplicas != 0 {
		t.Fatalf("offline desired replicas were not preserved: %+v", deployments)
	}
}

func TestMigrationResumesAfterAgentSwitchBeforeServerCommit(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	control.failSaveOn = 2 // lock metadata succeeds; final app metadata commit fails.
	service := NewService(control, remote)
	if err := service.MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err == nil {
		t.Fatal("expected first Server commit to fail after Agent switch")
	}
	if !remote.agentHasSwitched {
		t.Fatal("first attempt did not reach Agent marker switch")
	}
	control.failSaveOn = 0
	if err := service.MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err != nil {
		t.Fatalf("idempotent repair failed: %v", err)
	}
	if remote.bootstrapCalls != 1 {
		t.Fatalf("repair attempted to bootstrap Agent again: calls=%d", remote.bootstrapCalls)
	}
	saved, err := control.GetAppInstance(req.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringFromMetadata(metadataFromInstance(saved), "orchestrationModel", ""); got != orchestrationModelServiceControllerV1 {
		t.Fatalf("repaired model=%q", got)
	}
}

func TestRuntimeMigrationFailsClosedBeforeSwitchOnReplicaDivergence(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	control.deployments[0].DesiredReplicas = 2
	err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
	if err == nil {
		t.Fatal("expected divergent replicas to block migration")
	}
	if remote.bootstrapCalls != 0 || remote.agentHasSwitched {
		t.Fatalf("fail-closed preflight switched Agent: calls=%d switched=%v", remote.bootstrapCalls, remote.agentHasSwitched)
	}
	saved, _ := control.GetAppInstance(req.Instance.ID)
	if got := stringFromMetadata(metadataFromInstance(saved), "orchestrationModel", ""); got != orchestrationModelK8sLikeV1 {
		t.Fatalf("fail-closed preflight changed model to %q", got)
	}
}

func TestRuntimeMigrationFailsClosedBeforeSwitchOnMetadataEnvPathDivergence(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	metadata := metadataFromInstance(control.instances[0])
	metadata["envDir"] = "/aifar/apps/other/runtime/env"
	control.instances[0].Metadata = mustMetadata(t, metadata)
	err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
	if err == nil {
		t.Fatal("expected metadata env path divergence to block migration")
	}
	if remote.bootstrapCalls != 0 || remote.agentHasSwitched {
		t.Fatal("env path divergence switched Agent")
	}
}

func TestRuntimeMigrationFailsClosedWhenAgentFeatureGateIsIncomplete(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	status, _ := json.Marshal(map[string]any{
		"status": "running", "version": runtimeagent.DefaultAgentVersion,
		"features": []string{"service-manifest-v1", "service-generation-v1"},
	})
	remote.agentInspection = "AIFAR_AGENT_CHECK\nagentFound=true\nstatus=" + string(status) + "\nsha256=" + strings.Repeat("a", 64) + "\n"
	if err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err == nil {
		t.Fatal("expected incomplete Agent feature gate to block migration")
	}
	if remote.bootstrapCalls != 0 || remote.agentHasSwitched {
		t.Fatal("feature-gate failure switched Agent")
	}
}

func TestRuntimeMigrationNeverDowngradesAcceptedGeneration(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	control.deployments[0].Generation = 2
	if err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err == nil {
		t.Fatal("expected generation 2 to block one-way generation 1 migration")
	}
	if remote.bootstrapCalls != 0 || remote.agentHasSwitched {
		t.Fatal("forward-only gate attempted a downgrade")
	}
}

func TestInstallTemplateArchivesLegacySpecReadOnlyAfterAcceptance(t *testing.T) {
	script, err := templateFS.ReadFile("templates/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{"runtime-spec.legacy-readonly.json", "chmod 0400", "AIFAR_BOOTSTRAP_ACCEPTANCE"} {
		if !strings.Contains(text, required) {
			t.Fatalf("install template is missing migration-safe legacy archive step %q", required)
		}
	}
}
