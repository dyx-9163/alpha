package aifar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	mu                  sync.Mutex
	legacyJSON          []byte
	states              map[string]runtimeagent.DeploymentState
	agentInspection     string
	commands            []string
	uploadPaths         []string
	uploadBodies        [][]byte
	bootstrapCalls      int
	inputBootstrapCalls int
	inputBootstrapBody  []byte
	archiveCalls        int
	durableArchiveCalls int
	agentHasSwitched    bool
	snapshotInstance    runtimeagent.InstanceConfig
	snapshotStates      map[string]runtimeagent.DeploymentState
}

type runtimeMigrationExactStore struct {
	*fakeStore
	lockMu          sync.Mutex
	lock            store.AIFAROrchestrationLock
	returnEmptyLock bool
}

type migrationCASMutationStore struct {
	*runtimeMigrationExactStore
	mutateOnce func(*fakeStore)
}

type migrationCancelAfterCommitStore struct {
	*runtimeMigrationExactStore
	cancel context.CancelFunc
}

func (s *runtimeMigrationExactStore) AcquireAIFAROrchestrationLock(lock store.AIFAROrchestrationLock) (store.AIFAROrchestrationLock, error) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lock.ID != "" {
		return store.AIFAROrchestrationLock{}, errors.New("instance orchestration is locked")
	}
	if !s.returnEmptyLock && strings.TrimSpace(lock.ID) == "" {
		lock.ID = "lock-runtime-migration"
	}
	s.lock = lock
	return lock, nil
}

func (s *runtimeMigrationExactStore) RenewAIFAROrchestrationLock(id string, expiresAt time.Time) (bool, error) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lock.ID != id {
		return false, nil
	}
	s.lock.ExpiresAt = expiresAt
	return true, nil
}

func (s *runtimeMigrationExactStore) ReleaseAIFAROrchestrationLockByID(id string) (bool, error) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.lock.ID != id {
		return false, nil
	}
	s.lock = store.AIFAROrchestrationLock{}
	return true, nil
}

func (s *runtimeMigrationExactStore) ReleaseAIFAROrchestrationLock(_, _, _ string) (bool, error) {
	return false, errors.New("scope release must not be used by runtime migration")
}

func (s *runtimeMigrationExactStore) RecoverAIFAROrchestrationLocks(_ string, _ string) (int, error) {
	return 0, nil
}

type migrationReleaseFailureStore struct {
	*store.Store
	failures int
	calls    int
}

func (s *migrationReleaseFailureStore) ReleaseAIFAROrchestrationLockByID(id string) (bool, error) {
	s.calls++
	if s.calls <= s.failures {
		return false, errors.New("injected exact lock release failure")
	}
	return s.Store.ReleaseAIFAROrchestrationLockByID(id)
}

func (s *migrationCancelAfterCommitStore) CommitAIFARRuntimeMigrationWithLock(commit store.AIFARRuntimeMigrationCommit) (store.AppInstance, error) {
	saved, err := s.fakeStore.CommitAIFARRuntimeMigrationWithLock(commit)
	if err == nil && s.cancel != nil {
		s.cancel()
	}
	return saved, err
}

func (s *migrationCASMutationStore) CommitAIFARRuntimeMigrationWithLock(commit store.AIFARRuntimeMigrationCommit) (store.AppInstance, error) {
	if s.mutateOnce != nil {
		s.fakeStore.mu.Lock()
		mutate := s.mutateOnce
		s.mutateOnce = nil
		mutate(s.fakeStore)
		s.fakeStore.mu.Unlock()
	}
	return s.fakeStore.CommitAIFARRuntimeMigrationWithLock(commit)
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
	case strings.Contains(command, "aifar-agent get-instance-snapshot"):
		states := r.snapshotStates
		if states == nil {
			states = r.states
		}
		ordered := make([]runtimeagent.RuntimeDeploymentSnapshot, 0, len(states))
		for _, serviceName := range sortedMigrationStateNames(states) {
			state := states[serviceName]
			ordered = append(ordered, runtimeagent.RuntimeDeploymentSnapshot{
				ServiceName: serviceName, ManifestGeneration: state.Generation,
				ManifestSpecHash: state.SpecHash, StateGeneration: state.Generation,
				ObservedGeneration: state.ObservedGeneration, StateSpecHash: state.SpecHash,
				DesiredReplicas: state.DesiredReplicas,
			})
		}
		data, _ := json.Marshal(map[string]any{"instance": r.snapshotInstance, "deployments": ordered})
		return adapter.CommandResult{Stdout: string(data)}, nil
	case strings.Contains(command, "aifar-agent get-deployment"):
		for serviceName, state := range r.states {
			if strings.Contains(command, "--service '"+serviceName+"'") {
				data, _ := json.Marshal(state)
				return adapter.CommandResult{Stdout: string(data)}, nil
			}
		}
		return adapter.CommandResult{}, errors.New("deployment not found")
	case strings.Contains(command, "aifar-agent archive-legacy-runtime"):
		r.durableArchiveCalls++
		return adapter.CommandResult{Stdout: `{"archived":true}`}, nil
	case strings.Contains(command, "AIFAR_RUNTIME_MIGRATION_ARCHIVE"):
		r.archiveCalls++
		return adapter.CommandResult{Stdout: "archived"}, nil
	default:
		return adapter.CommandResult{Stdout: "ok"}, nil
	}
}

func (r *runtimeMigrationRemote) RunWithInput(ctx context.Context, _ store.Server, command string, input []byte) (adapter.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return adapter.CommandResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	r.inputBootstrapCalls++
	r.inputBootstrapBody = append([]byte(nil), input...)
	if r.agentHasSwitched {
		return adapter.CommandResult{Stderr: "LEGACY_RUNTIME_SPEC_DISABLED"}, errors.New("agent rejected legacy writer")
	}
	r.agentHasSwitched = true
	r.bootstrapCalls++
	return adapter.CommandResult{Stdout: `{"accepted":true}`}, nil
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
			"per-service-restart", "service-conditions-v1", "runtime-instance-snapshot-v1", "durable-legacy-archive-v1",
			"verified-bootstrap-stream-v1",
		},
	})
	return "AIFAR_AGENT_CHECK\nagentFound=true\nstatus=" + string(status) + "\nsha256=" + strings.Repeat("a", 64) + "\n"
}

func runtimeMigrationFixture(t *testing.T, replicas int) (*runtimeMigrationExactStore, *runtimeMigrationRemote, RuntimeMigrationRequest) {
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
	}, snapshotInstance: runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{
		APIVersion: runtimeagent.ManifestAPIVersion, InstanceID: instance.ID, InstallRoot: installRoot,
		Network: defaultNetworkName, Ingress: legacy.Ingress,
	})}
	control := &runtimeMigrationExactStore{fakeStore: &fakeStore{
		servers: map[string]store.Server{server.ID: server}, instances: []store.AppInstance{instance},
		deployments: []store.AIFARDeployment{current},
	}}
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
	if remote.bootstrapCalls != 1 || remote.durableArchiveCalls != 1 || remote.archiveCalls != 0 {
		t.Fatalf("bootstrap=%d durable archive=%d shell archive=%d, want one atomic switch and one durable archive", remote.bootstrapCalls, remote.durableArchiveCalls, remote.archiveCalls)
	}
	if len(remote.uploadPaths) != 0 {
		t.Fatalf("migration left remote filesystem staging paths: %v", remote.uploadPaths)
	}
	if remote.inputBootstrapCalls != 1 || !bytes.Equal(remote.inputBootstrapBody, remote.legacyJSON) {
		t.Fatalf("typed Agent bootstrap did not consume exact validated bytes: calls=%d", remote.inputBootstrapCalls)
	}
	joined := remote.joinedCommands()
	digest := sha256.Sum256(remote.legacyJSON)
	wantBootstrap := "aifar-agent bootstrap-runtime-stdin --instance 'aifar-legacy' --sha256 '" + hex.EncodeToString(digest[:]) + "'"
	if !strings.Contains(joined, wantBootstrap) || strings.Contains(joined, "--spec") || strings.Contains(joined, ".aifar-stage-") {
		t.Fatalf("Agent bootstrap was not a fixed typed stdin command: %q", joined)
	}
	deployments, err := control.ListAIFARDeployments(req.Instance.ID)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("deployments=%+v err=%v", deployments, err)
	}
	if deployments[0].Generation != 1 || deployments[0].DesiredReplicas != 1 || deployments[0].SpecJSON == "" || deployments[0].Status != "Accepted" {
		t.Fatalf("migration did not persist accepted generation/hash-bound desired state: %+v", deployments[0])
	}
}

func TestRuntimeMigrationUsesTypedAgentInputWithoutFilesystemStage(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	if err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if remote.inputBootstrapCalls != 1 || len(remote.uploadPaths) != 0 {
		t.Fatalf("typed input calls=%d staged paths=%v", remote.inputBootstrapCalls, remote.uploadPaths)
	}
}

func TestRuntimeMigrationRequiresExactLockStoreBeforeRemoteAction(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	err := NewService(control.fakeStore, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
	if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_EXACT_LOCK_STORE_REQUIRED" {
		t.Fatalf("missing exact lock store reason=%q err=%v", reasonCode(err), err)
	}
	if len(remote.commands) != 0 || remote.inputBootstrapCalls != 0 {
		t.Fatalf("migration reached remote before exact lock fencing: commands=%v", remote.commands)
	}
}

func TestRuntimeMigrationRejectsEmptyExactLockIDBeforeRemoteAction(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	control.returnEmptyLock = true
	err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
	if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_EXACT_LOCK_REQUIRED" {
		t.Fatalf("empty exact lock reason=%q err=%v", reasonCode(err), err)
	}
	if len(remote.commands) != 0 || remote.inputBootstrapCalls != 0 {
		t.Fatalf("migration reached remote with empty exact lock: commands=%v", remote.commands)
	}
}

func TestRuntimeMigrationRequiresTypedInputTransportBeforeRemoteAction(t *testing.T) {
	control, _, req := runtimeMigrationFixture(t, 1)
	remote := &fakeRemote{}
	err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
	if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_TYPED_BOOTSTRAP_UNAVAILABLE" {
		t.Fatalf("missing typed transport reason=%q err=%v", reasonCode(err), err)
	}
	if len(remote.commands) != 0 {
		t.Fatalf("migration reached remote without typed input transport: %v", remote.commands)
	}
}

func TestRuntimeMigrationLegacyBootstrapByteBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		size       int
		wantReason string
		wantInput  int
		wantSwitch bool
	}{
		{name: "exact limit", size: runtimeMigrationMaxLegacyBytes, wantInput: 1, wantSwitch: true},
		{name: "limit plus one", size: runtimeMigrationMaxLegacyBytes + 1, wantReason: "AIFAR_RUNTIME_MIGRATION_LEGACY_SPEC_TOO_LARGE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			control, remote, req := runtimeMigrationFixture(t, 1)
			if len(remote.legacyJSON) > test.size {
				t.Fatalf("fixture=%d exceeds target=%d", len(remote.legacyJSON), test.size)
			}
			remote.legacyJSON = append(remote.legacyJSON, bytes.Repeat([]byte(" "), test.size-len(remote.legacyJSON))...)
			err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
			if reasonCode(err) != test.wantReason {
				t.Fatalf("size=%d reason=%q want=%q err=%v", test.size, reasonCode(err), test.wantReason, err)
			}
			if remote.inputBootstrapCalls != test.wantInput || remote.agentHasSwitched != test.wantSwitch {
				t.Fatalf("size=%d typed SSH calls=%d switched=%t", test.size, remote.inputBootstrapCalls, remote.agentHasSwitched)
			}
		})
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

func TestMigrationPreservesObservedGenerationOneRuntimeProjection(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	originalTransition := time.Date(2026, 7, 1, 2, 3, 4, 0, time.UTC)
	originalConditions := `[{"type":"Available","status":true,"reason":"ContainersReady","generation":1}]`
	control.deployments[0].ObservedGeneration = 1
	control.deployments[0].Status = "Available"
	control.deployments[0].MetadataJSON = `{"runtime":"peer"}`
	control.deployments[0].ConditionsJSON = originalConditions
	control.deployments[0].LastTransitionAt = originalTransition
	remote.states["permission"] = runtimeagent.DeploymentState{
		InstanceID: req.Instance.ID, ServiceName: "permission", Generation: 1, ObservedGeneration: 1,
		SpecHash: remote.states["permission"].SpecHash, DesiredReplicas: 1,
	}
	if err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	deployments, _ := control.ListAIFARDeployments(req.Instance.ID)
	got := deployments[0]
	if got.Status != "Available" || got.MetadataJSON != `{"runtime":"peer"}` || got.ConditionsJSON != originalConditions || !got.LastTransitionAt.Equal(originalTransition) {
		t.Fatalf("observed runtime projection regressed: %+v", got)
	}
}

func TestMigrationResumesAfterAgentSwitchBeforeServerCommit(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	control.failSaveOn = 1 // exact lock is separate; first app metadata write is the final commit.
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

func TestRuntimeMigrationUsesAgentDurableArchiveInsteadOfShellMove(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	remote.agentHasSwitched = true
	if err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if remote.durableArchiveCalls != 1 || remote.archiveCalls != 0 {
		t.Fatalf("durable Agent archive=%d shell archive=%d", remote.durableArchiveCalls, remote.archiveCalls)
	}
}

func TestMigrationSwitchedRepairRejectsUntrackedAgentSuccessor(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	remote.agentHasSwitched = true
	remote.snapshotStates = map[string]runtimeagent.DeploymentState{}
	for name, state := range remote.states {
		remote.snapshotStates[name] = state
	}
	remote.snapshotStates["file"] = runtimeagent.DeploymentState{
		InstanceID: req.Instance.ID, ServiceName: "file", Generation: 2, ObservedGeneration: 2,
		SpecHash: strings.Repeat("b", 64), DesiredReplicas: 1,
	}
	err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
	if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_FORWARD_ONLY" {
		t.Fatalf("extra Agent successor was not rejected: %v", err)
	}
	saved, _ := control.GetAppInstance(req.Instance.ID)
	if got := stringFromMetadata(metadataFromInstance(saved), "orchestrationModel", ""); got != orchestrationModelK8sLikeV1 {
		t.Fatalf("switched repair committed Server model after extra successor: %q", got)
	}
}

func TestMigrationSwitchedRepairRejectsDivergentAgentInstanceConfig(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	remote.agentHasSwitched = true
	remote.snapshotInstance.Network = "other-network"
	err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
	if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_AGENT_INSTANCE_DIVERGED" {
		t.Fatalf("divergent Agent instance config was not rejected: %v", err)
	}
	saved, _ := control.GetAppInstance(req.Instance.ID)
	if got := stringFromMetadata(metadataFromInstance(saved), "orchestrationModel", ""); got != orchestrationModelK8sLikeV1 {
		t.Fatalf("divergent Agent instance committed Server model: %q", got)
	}
}

func TestMigrationSwitchedRepairRequiresExactAgentServiceSet(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]runtimeagent.DeploymentState)
	}{
		{name: "missing expected service", mutate: func(states map[string]runtimeagent.DeploymentState) { delete(states, "permission") }},
		{name: "extra generation one service", mutate: func(states map[string]runtimeagent.DeploymentState) {
			states["file"] = runtimeagent.DeploymentState{InstanceID: "aifar-legacy", ServiceName: "file", Generation: 1, SpecHash: strings.Repeat("b", 64), DesiredReplicas: 1}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			control, remote, req := runtimeMigrationFixture(t, 1)
			remote.agentHasSwitched = true
			remote.snapshotStates = map[string]runtimeagent.DeploymentState{}
			for name, state := range remote.states {
				remote.snapshotStates[name] = state
			}
			test.mutate(remote.snapshotStates)
			err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
			if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_AGENT_SERVICE_SET_DIVERGED" {
				t.Fatalf("non-exact Agent service set was not rejected: %v", err)
			}
		})
	}
}

func TestMigrationSwitchedRepairRejectsAnySuccessorObservation(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	remote.agentHasSwitched = true
	state := remote.states["permission"]
	state.ObservedGeneration = 2
	remote.snapshotStates = map[string]runtimeagent.DeploymentState{"permission": state}
	err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
	if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_FORWARD_ONLY" {
		t.Fatalf("successor observation was not rejected: %v", err)
	}
}

func TestMigrationSwitchedRepairChecksAllSharedInstanceFields(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*runtimeagent.InstanceConfig)
	}{
		{name: "install root", mutate: func(config *runtimeagent.InstanceConfig) { config.InstallRoot = "/aifar/apps/other" }},
		{name: "ingress mode", mutate: func(config *runtimeagent.InstanceConfig) { config.Ingress.Mode = "disabled" }},
		{name: "ingress gateway", mutate: func(config *runtimeagent.InstanceConfig) { config.Ingress.GatewayService = "other" }},
		{name: "ingress port", mutate: func(config *runtimeagent.InstanceConfig) { config.Ingress.GatewayPort++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			control, remote, req := runtimeMigrationFixture(t, 1)
			remote.agentHasSwitched = true
			test.mutate(&remote.snapshotInstance)
			err := NewService(control, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
			if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_AGENT_INSTANCE_DIVERGED" {
				t.Fatalf("divergent shared Agent field was not rejected: %v", err)
			}
		})
	}
}

func TestMigrationMetadataCASRetryRevalidatesEveryManifestAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*store.AppInstance, map[string]any)
	}{
		{name: "server identity", mutate: func(instance *store.AppInstance, _ map[string]any) { instance.ServerID = "srv-successor" }},
		{name: "install root", mutate: func(_ *store.AppInstance, metadata map[string]any) { metadata["installRoot"] = "/aifar/apps/changed" }},
		{name: "runtime directory", mutate: func(_ *store.AppInstance, metadata map[string]any) {
			metadata["runtimeDir"] = "/aifar/apps/changed/runtime"
		}},
		{name: "environment directory", mutate: func(_ *store.AppInstance, metadata map[string]any) {
			metadata["envDir"] = "/aifar/apps/changed/runtime/env"
		}},
		{name: "network", mutate: func(_ *store.AppInstance, metadata map[string]any) { metadata["ingressNetwork"] = "changed-network" }},
		{name: "gateway port", mutate: func(_ *store.AppInstance, metadata map[string]any) { metadata["gatewayPort"] = defaultGatewayPort + 1 }},
		{name: "web port", mutate: func(_ *store.AppInstance, metadata map[string]any) { metadata["webPort"] = defaultWebPort + 1 }},
		{name: "catalog", mutate: func(_ *store.AppInstance, metadata map[string]any) {
			metadata["serviceCatalog"] = serviceCatalogMetadata([]serviceDefinition{{
				Name: "permission", Kind: "java", ApplicationName: "alpha-permission", Port: defaultPermissionPort + 1,
				HealthPath: "/actuator/health/readiness", AffinityPolicy: "round-robin",
			}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			control, remote, req := runtimeMigrationFixture(t, 1)
			wrapped := &migrationCASMutationStore{runtimeMigrationExactStore: control}
			wrapped.mutateOnce = func(db *fakeStore) {
				instance := &db.instances[0]
				metadata := metadataFromInstance(*instance)
				test.mutate(instance, metadata)
				instance.Metadata = mustMetadata(t, metadata)
				instance.UpdatedAt = instance.UpdatedAt.Add(time.Second)
			}
			err := NewService(wrapped, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
			if reasonCode(err) != "AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED" {
				t.Fatalf("authoritative CAS change was not rejected: %v", err)
			}
		})
	}
}

func TestMigrationMetadataCASRetryMergesLifecycleAndUnknownPeerFields(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	wrapped := &migrationCASMutationStore{runtimeMigrationExactStore: control}
	wrapped.mutateOnce = func(db *fakeStore) {
		instance := &db.instances[0]
		metadata := metadataFromInstance(*instance)
		metadata["peerUnknown"] = map[string]any{"keep": true}
		instance.Metadata = mustMetadata(t, metadata)
		instance.Status = "install_warning"
		instance.UpdatedAt = instance.UpdatedAt.Add(time.Second)
	}
	if err := NewService(wrapped, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	saved, _ := control.GetAppInstance(req.Instance.ID)
	metadata := metadataFromInstance(saved)
	peer, _ := metadata["peerUnknown"].(map[string]any)
	if saved.Status != "install_warning" || peer["keep"] != true || stringFromMetadata(metadata, "orchestrationModel", "") != orchestrationModelServiceControllerV1 {
		t.Fatalf("CAS merge lost lifecycle or unknown peer metadata: %+v metadata=%v", saved, metadata)
	}
}

func TestMigrationDoesNotReportFailureWhenContextCancelsAfterDurableCommit(t *testing.T) {
	control, remote, req := runtimeMigrationFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := &migrationCancelAfterCommitStore{runtimeMigrationExactStore: control, cancel: cancel}
	if err := NewService(wrapped, remote).MigrateRuntimeModel(ctx, req, fakeLogger{}); err != nil {
		t.Fatalf("durably committed migration was reported failed: %v", err)
	}
	saved, _ := control.GetAppInstance(req.Instance.ID)
	if got := stringFromMetadata(metadataFromInstance(saved), "orchestrationModel", ""); got != orchestrationModelServiceControllerV1 {
		t.Fatalf("durable model marker=%q", got)
	}
}

func TestMigrationRetriesExactLockReleaseAndReportsStableCleanupFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		failures   int
		wantReason string
		wantCalls  int
	}{
		{name: "transient release failure", failures: 2, wantCalls: 3},
		{name: "persistent release failure", failures: 3, wantReason: "AIFAR_RUNTIME_MIGRATION_LOCK_RELEASE_FAILED", wantCalls: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			wrapped := &migrationReleaseFailureStore{Store: db, failures: test.failures}
			err = NewService(wrapped, remote).MigrateRuntimeModel(context.Background(), req, fakeLogger{})
			if reasonCode(err) != test.wantReason {
				t.Fatalf("release result reason=%q want=%q err=%v", reasonCode(err), test.wantReason, err)
			}
			if wrapped.calls != test.wantCalls {
				t.Fatalf("exact release calls=%d want=%d", wrapped.calls, test.wantCalls)
			}
			locks, listErr := db.ListAIFAROrchestrationLocks(req.Instance.ID, true)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if test.wantReason == "" && len(locks) != 0 {
				t.Fatalf("transient cleanup left lock: %+v", locks)
			}
			if test.wantReason != "" && len(locks) != 1 {
				t.Fatalf("persistent cleanup failure did not leave exact owner visible: %+v", locks)
			}
			audits, auditErr := db.ListAudit()
			if auditErr != nil || len(audits) == 0 {
				t.Fatalf("migration cleanup result was not audited: audits=%v err=%v", audits, auditErr)
			}
			wantAuditStatus := "success"
			if test.wantReason != "" {
				wantAuditStatus = "failed"
			}
			if audits[0].Status != wantAuditStatus {
				t.Fatalf("audit status=%q want=%q", audits[0].Status, wantAuditStatus)
			}
		})
	}
}

func reasonCode(err error) string {
	var coded interface{ ReasonCode() string }
	if errors.As(err, &coded) {
		return coded.ReasonCode()
	}
	return ""
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
	for _, required := range []string{"runtime-spec.legacy-readonly.json", "aifar-agent archive-legacy-runtime --instance", "--sha256", "chmod 0400", "AIFAR_BOOTSTRAP_ACCEPTANCE"} {
		if !strings.Contains(text, required) {
			t.Fatalf("install template is missing migration-safe legacy archive step %q", required)
		}
	}
}
