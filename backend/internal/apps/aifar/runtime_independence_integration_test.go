package aifar

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

func integrationDeploymentSpecHash(deployment runtimeagent.DeploymentSpec) string {
	type hashDeployment struct {
		ServiceName    string                       `json:"serviceName"`
		DeploymentName string                       `json:"deploymentName,omitempty"`
		Image          string                       `json:"image,omitempty"`
		PodRevision    string                       `json:"podRevision,omitempty"`
		Ports          []runtimeagent.ContainerPort `json:"ports,omitempty"`
		EnvFiles       []string                     `json:"envFiles,omitempty"`
		Volumes        []runtimeagent.VolumeMount   `json:"volumes,omitempty"`
		Resources      runtimeagent.ResourceSpec    `json:"resources,omitempty"`
		HealthCheck    runtimeagent.HealthCheckSpec `json:"healthCheck,omitempty"`
		Entrypoint     []string                     `json:"entrypoint,omitempty"`
		Command        []string                     `json:"command,omitempty"`
		Environment    map[string]string            `json:"environment,omitempty"`
		Labels         map[string]string            `json:"labels,omitempty"`
	}
	data, _ := json.Marshal(hashDeployment{
		ServiceName: deployment.ServiceName, DeploymentName: deployment.DeploymentName, Image: deployment.Image,
		PodRevision: deployment.PodRevision, Ports: deployment.Ports, EnvFiles: deployment.EnvFiles, Volumes: deployment.Volumes,
		Resources: deployment.Resources, HealthCheck: deployment.HealthCheck, Entrypoint: deployment.Entrypoint,
		Command: deployment.Command, Environment: deployment.Environment, Labels: deployment.Labels,
	})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// agentBridgeRemote exercises the Server mutation boundary through the same
// typed Manifest acceptance used by a real aifar-agent, while keeping SSH and
// Docker entirely local to the test.
type agentBridgeRemote struct {
	mu                 sync.Mutex
	agent              *runtimeagent.Manager
	control            *store.Store
	manifests          map[string]runtimeagent.DeploymentManifest
	commands           []string
	uploads            []string
	installScript      string
	agentCheckStdout   string
	legacyJSON         []byte
	legacyInstanceID   string
	bootstrapCallCount int
}

func (r *agentBridgeRemote) UploadFile(_ context.Context, _ store.Server, localPath, remotePath string, mode os.FileMode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploads = append(r.uploads, remotePath)
	if strings.HasSuffix(remotePath, "/install-aifar.sh") {
		data, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		r.installScript = string(data)
		return nil
	}
	if !strings.Contains(remotePath, "/mutations/") || !strings.HasSuffix(remotePath, ".json") {
		return nil
	}
	if mode != 0o600 {
		return errors.New("manifest upload mode is not 0600")
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	var manifest runtimeagent.DeploymentManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	r.manifests[remotePath] = manifest
	return nil
}

func (r *agentBridgeRemote) Run(ctx context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	switch {
	case strings.Contains(command, "AIFAR_AGENT_CHECK"):
		r.mu.Lock()
		stdout := r.agentCheckStdout
		r.mu.Unlock()
		return adapter.CommandResult{Stdout: stdout}, nil
	case strings.Contains(command, "install-aifar.sh"):
		r.mu.Lock()
		instanceID := scriptAssignment(r.installScript, "INSTANCE_ID")
		r.mu.Unlock()
		stdout, err := r.bootstrapInstalledDesired(ctx, instanceID)
		return adapter.CommandResult{Stdout: stdout}, err
	case strings.Contains(command, "apply-deployment"):
		r.mu.Lock()
		var manifest runtimeagent.DeploymentManifest
		found := false
		for remotePath, candidate := range r.manifests {
			if strings.Contains(command, remotePath) {
				manifest, found = candidate, true
				break
			}
		}
		r.mu.Unlock()
		if !found {
			return adapter.CommandResult{}, errors.New("typed manifest was not uploaded")
		}
		accepted, err := r.agent.AcceptDeployment(ctx, manifest)
		if err != nil {
			return adapter.CommandResult{}, err
		}
		data, err := json.Marshal(accepted)
		return adapter.CommandResult{Stdout: string(data)}, err
	case strings.Contains(command, "aifar-agent get-deployment"):
		r.mu.Lock()
		manifests := make([]runtimeagent.DeploymentManifest, 0, len(r.manifests))
		for _, manifest := range r.manifests {
			manifests = append(manifests, manifest)
		}
		r.mu.Unlock()
		for _, manifest := range manifests {
			if strings.Contains(command, manifest.Metadata.Name) {
				state, ok := r.agent.DeploymentState(manifest.Metadata.InstanceID, manifest.Metadata.Name)
				if !ok {
					return adapter.CommandResult{}, errors.New("deployment not found")
				}
				data, err := json.Marshal(state)
				return adapter.CommandResult{Stdout: string(data)}, err
			}
		}
		return adapter.CommandResult{}, errors.New("deployment not found")
	case strings.Contains(command, "AIFAR_RUNTIME_MIGRATION_READ"):
		r.mu.Lock()
		legacyJSON := append([]byte(nil), r.legacyJSON...)
		instanceID := r.legacyInstanceID
		r.mu.Unlock()
		model := "legacy"
		if _, err := r.agent.RuntimeInstanceSnapshot(instanceID); err == nil {
			model = "switched"
		}
		return adapter.CommandResult{Stdout: "model=" + model + "\nlegacy=" + base64.StdEncoding.EncodeToString(legacyJSON) + "\n"}, nil
	case strings.Contains(command, "aifar-agent get-instance-snapshot"):
		r.mu.Lock()
		instanceID := r.legacyInstanceID
		r.mu.Unlock()
		snapshot, err := r.agent.RuntimeInstanceSnapshot(instanceID)
		if err != nil {
			return adapter.CommandResult{}, err
		}
		data, err := json.Marshal(snapshot)
		return adapter.CommandResult{Stdout: string(data)}, err
	case strings.Contains(command, "aifar-agent archive-legacy-runtime"):
		return adapter.CommandResult{Stdout: `{"archived":true}`}, nil
	case strings.Contains(command, "rm -f --"):
		r.mu.Lock()
		for remotePath := range r.manifests {
			if strings.Contains(command, remotePath) {
				delete(r.manifests, remotePath)
			}
		}
		r.mu.Unlock()
	}
	return adapter.CommandResult{}, nil
}

func (r *agentBridgeRemote) RunWithInput(ctx context.Context, _ store.Server, command string, input []byte) (adapter.CommandResult, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.bootstrapCallCount++
	r.mu.Unlock()
	var legacy runtimeagent.LegacyRuntimeSpec
	if err := json.Unmarshal(input, &legacy); err != nil {
		return adapter.CommandResult{}, err
	}
	acceptance, err := r.agent.BootstrapLegacyRuntime(ctx, legacy)
	if err != nil {
		return adapter.CommandResult{}, err
	}
	data, err := json.Marshal(acceptance)
	return adapter.CommandResult{Stdout: string(data)}, err
}

func (r *agentBridgeRemote) bootstrapInstalledDesired(ctx context.Context, instanceID string) (string, error) {
	if r.control == nil {
		return "", errors.New("control store is required for install bootstrap")
	}
	instance, err := r.control.GetAppInstance(instanceID)
	if err != nil {
		return "", err
	}
	rows, err := r.control.ListAIFARDeployments(instanceID)
	if err != nil {
		return "", err
	}
	manifests := make([]runtimeagent.DeploymentManifest, 0, len(rows))
	for _, row := range rows {
		var manifest runtimeagent.DeploymentManifest
		if err := json.Unmarshal([]byte(row.SpecJSON), &manifest); err != nil {
			return "", err
		}
		manifests = append(manifests, runtimeagent.NormalizeDeploymentManifest(manifest))
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Metadata.Name < manifests[j].Metadata.Name })
	metadata := metadataFromInstance(instance)
	legacy := runtimeagent.LegacyRuntimeSpec{
		Version: runtimeagent.DefaultAgentVersion, InstanceID: instance.ID,
		InstallRoot: stringFromMetadata(metadata, "installRoot", "/aifar/apps/admin"),
		Network:     stringFromMetadata(metadata, "networkName", defaultNetworkName),
		Ingress: runtimeagent.IngressSpec{
			Mode: runtimeagent.DefaultIngressMode, GatewayService: "gateway", WebService: "web-vue3",
			GatewayPort: intFromMetadata(metadata, "gatewayPort", defaultGatewayPort),
			WebPort:     intFromMetadata(metadata, "webPort", defaultWebPort),
		},
	}
	proofs := make([]bootstrapDeploymentProof, 0, len(manifests))
	for _, manifest := range manifests {
		legacy.Deployments = append(legacy.Deployments, manifest.Spec)
		legacy.Services = append(legacy.Services, manifest.Service)
		hash, err := runtimeagent.DeploymentManifestSpecHash(manifest)
		if err != nil {
			return "", err
		}
		proofs = append(proofs, bootstrapDeploymentProof{
			Accepted: true, InstanceID: instance.ID, ServiceName: manifest.Metadata.Name,
			Generation: manifest.Metadata.Generation, SpecHash: hash,
		})
	}
	if _, err := r.agent.BootstrapLegacyRuntime(ctx, legacy); err != nil {
		return "", err
	}
	data, err := json.Marshal(bootstrapAcceptanceProof{Accepted: true, InstanceID: instance.ID, Deployments: proofs})
	if err != nil {
		return "", err
	}
	return bootstrapAcceptanceMarker + string(data), nil
}

func (r *agentBridgeRemote) joinedCommands() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.commands, "\n")
}

type independentAgentRunner struct {
	mu          sync.Mutex
	pods        map[string]bool
	podMetadata map[string]integrationPodMetadata
	fileStarted chan struct{}
	fileOnce    sync.Once
	blocked     string
	failed      string
	calls       []string
}

type integrationPodMetadata struct {
	replica  string
	revision string
	specHash string
}

func newIndependentAgentRunner() *independentAgentRunner {
	return &independentAgentRunner{pods: map[string]bool{}, podMetadata: map[string]integrationPodMetadata{}, fileStarted: make(chan struct{}), blocked: "file"}
}

func (r *independentAgentRunner) Run(ctx context.Context, name string, args ...string) (runtimeagent.CommandResult, error) {
	if name != "docker" {
		return runtimeagent.CommandResult{}, nil
	}
	call := strings.Join(args, " ")
	service := integrationServiceFromCall(call)
	r.mu.Lock()
	r.calls = append(r.calls, name+" "+call)
	r.mu.Unlock()
	if len(args) > 0 && args[0] == "run" {
		if service == r.failed {
			return runtimeagent.CommandResult{}, errors.New("container create failed")
		}
		if service == r.blocked {
			r.fileOnce.Do(func() { close(r.fileStarted) })
			<-ctx.Done()
			return runtimeagent.CommandResult{}, ctx.Err()
		}
		container := integrationArgumentAfter(args, "--name")
		metadata := integrationPodMetadata{
			replica:  integrationLabelValue(args, "aifar.replica"),
			revision: integrationLabelValue(args, "aifar.revision"),
			specHash: integrationLabelValue(args, "aifar.spec-hash"),
		}
		r.mu.Lock()
		r.pods[container] = true
		r.podMetadata[container] = metadata
		r.mu.Unlock()
		return runtimeagent.CommandResult{Stdout: container}, nil
	}
	if len(args) > 0 && args[0] == "inspect" {
		container := args[len(args)-1]
		r.mu.Lock()
		exists := r.pods[container]
		r.mu.Unlock()
		if !exists {
			return runtimeagent.CommandResult{}, errors.New("No such container")
		}
		switch {
		case strings.Contains(call, ".State.Running") && strings.Contains(call, "NetworkSettings"):
			return runtimeagent.CommandResult{Stdout: "true|healthy|172.20.0.10"}, nil
		case strings.Contains(call, ".State.Running"):
			return runtimeagent.CommandResult{Stdout: "true|healthy"}, nil
		case strings.Contains(call, "aifar.spec-hash"):
			r.mu.Lock()
			metadata := r.podMetadata[container]
			r.mu.Unlock()
			return runtimeagent.CommandResult{Stdout: metadata.specHash}, nil
		default:
			return runtimeagent.CommandResult{Stdout: "container-id"}, nil
		}
	}
	if len(args) > 0 && args[0] == "ps" {
		r.mu.Lock()
		defer r.mu.Unlock()
		rows := make([]string, 0, len(r.pods))
		for pod := range r.pods {
			if service != "" && !strings.Contains(pod, "-"+service+"-") {
				continue
			}
			if strings.Contains(call, "aifar.replica") {
				metadata := r.podMetadata[pod]
				replica := metadata.replica
				if replica == "" {
					replica = "1"
				}
				rows = append(rows, pod+"|"+replica+"|"+metadata.revision+"|"+metadata.specHash)
			} else {
				rows = append(rows, pod+"|"+pod)
			}
		}
		return runtimeagent.CommandResult{Stdout: strings.Join(rows, "\n")}, nil
	}
	if len(args) > 0 && args[0] == "rm" {
		r.mu.Lock()
		delete(r.pods, args[len(args)-1])
		delete(r.podMetadata, args[len(args)-1])
		r.mu.Unlock()
	}
	return runtimeagent.CommandResult{}, nil
}

func integrationLabelValue(args []string, key string) string {
	prefix := key + "="
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--label" && strings.HasPrefix(args[index+1], prefix) {
			return strings.TrimPrefix(args[index+1], prefix)
		}
	}
	return ""
}

func (r *independentAgentRunner) snapshotPods() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.pods))
	for name := range r.pods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *independentAgentRunner) joinedCalls() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
}

func integrationArgumentAfter(args []string, wanted string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == wanted {
			return args[index+1]
		}
	}
	return ""
}

func integrationServiceFromCall(call string) string {
	const marker = "aifar.service="
	index := strings.Index(call, marker)
	if index < 0 {
		return ""
	}
	value := call[index+len(marker):]
	if end := strings.IndexByte(value, ' '); end >= 0 {
		value = value[:end]
	}
	return value
}

func waitForAgentCondition(t *testing.T, manager *runtimeagent.Manager, serviceName, conditionType string) runtimeagent.DeploymentState {
	return waitForAgentConditionForInstance(t, manager, "aifar-1", serviceName, conditionType)
}

func waitForAgentConditionForInstance(t *testing.T, manager *runtimeagent.Manager, instanceID, serviceName, conditionType string) runtimeagent.DeploymentState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := manager.DeploymentState(instanceID, serviceName)
		if ok {
			for _, condition := range state.Conditions {
				if condition.Status && condition.Type == conditionType {
					return state
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := manager.DeploymentState(instanceID, serviceName)
	t.Fatalf("condition %s for %s not reached: %+v", conditionType, serviceName, state)
	return runtimeagent.DeploymentState{}
}

func TestInstallAcceptanceDoesNotWaitForEveryRuntimeServiceToBecomeReady(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	bundleRoot := createAIFARBundle(t)
	db := openAIFARTestStore(t)
	server, err := db.SaveServer(store.Server{ID: "srv-install-integration", Name: "install-node", Host: "10.0.0.10", DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}

	stateDir := t.TempDir()
	runner := newIndependentAgentRunner()
	runner.blocked = ""
	runner.failed = "file"
	manifestStore := &runtimeagent.ManifestStore{StateDir: stateDir}
	agent := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, Runner: runner, ManifestStore: manifestStore})
	remote := &agentBridgeRemote{agent: agent, control: db, manifests: map[string]runtimeagent.DeploymentManifest{}}
	service := NewService(db, remote)
	resources := []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(bundleRoot, appBundleVersion, bundleManifestName)}}

	err = service.Install(context.Background(), InstallRequest{
		Version: appBundleVersion, ServerID: server.ID, Language: "en", Actor: "integration", TaskID: "task-install-acceptance",
		Parameters: map[string]any{
			"nacosHost":        "10.0.0.50",
			"selectedServices": []string{"gateway", "permission", "file", "web-vue3"},
		},
	}, resources, fakeLogger{}, nil)
	if err != nil {
		t.Fatalf("Agent accepted desired state, so install must complete without waiting for readiness: %v", err)
	}
	allInstances, err := db.ListAppInstances()
	instances := make([]store.AppInstance, 0, 1)
	for _, candidate := range allInstances {
		if candidate.App == AppName && candidate.ServerID == server.ID {
			instances = append(instances, candidate)
		}
	}
	if err != nil || len(instances) != 1 {
		t.Fatalf("installed instances: err=%v instances=%+v", err, instances)
	}
	instance := instances[0]
	t.Cleanup(func() { _ = agent.Remove(context.Background(), instance.ID) })
	if instance.Status != "installed" {
		t.Fatalf("acceptance must commit install lifecycle independently of convergence: %+v", instance)
	}
	degraded := waitForAgentConditionForInstance(t, agent, instance.ID, "file", "Degraded")
	available := waitForAgentConditionForInstance(t, agent, instance.ID, "permission", "Available")
	if degraded.Generation != 1 || available.Generation != 1 {
		t.Fatalf("Agent must independently reconcile accepted services: file=%+v permission=%+v", degraded, available)
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "installed" {
		t.Fatalf("a failed local service must not rewrite accepted install lifecycle: %+v", saved)
	}
}

func TestRestartRuntimeFansOutThroughTypedAgentWithoutStopAll(t *testing.T) {
	agentBinary := withFakeRuntimeAgentBinary(t)
	agentSum, _, err := fileSHA256(agentBinary)
	if err != nil {
		t.Fatal(err)
	}
	db := openAIFARTestStore(t)
	server, err := db.SaveServer(store.Server{ID: "srv-1", Name: "restart-node", Host: "10.0.0.10", DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationModel"] = orchestrationModelServiceControllerV1
	instance.Metadata = mustMetadata(t, metadata)
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	seeded := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "system": 2, "file": 0},
		map[string]int64{"permission": 3, "system": 4, "file": 7},
	)
	for _, deployment := range seeded {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}

	stateDir := t.TempDir()
	manifestStore := &runtimeagent.ManifestStore{StateDir: stateDir}
	if err := manifestStore.PutInstance(runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{
		APIVersion: runtimeagent.ManifestAPIVersion, InstanceID: instance.ID, InstallRoot: "/aifar/apps/admin", Network: defaultNetworkName,
	})); err != nil {
		t.Fatal(err)
	}
	runner := newIndependentAgentRunner()
	runner.blocked = ""
	agent := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, Runner: runner, ManifestStore: manifestStore})
	t.Cleanup(func() { _ = agent.Remove(context.Background(), instance.ID) })
	for _, deployment := range seeded {
		var manifest runtimeagent.DeploymentManifest
		if err := json.Unmarshal([]byte(deployment.SpecJSON), &manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := agent.AcceptDeployment(context.Background(), manifest); err != nil {
			t.Fatal(err)
		}
	}
	waitForAgentCondition(t, agent, "permission", "Available")
	waitForAgentCondition(t, agent, "system", "Available")
	waitForAgentCondition(t, agent, "file", "Offline")

	before := deploymentsByService(seeded)
	remote := &agentBridgeRemote{
		agent: agent, manifests: map[string]runtimeagent.DeploymentManifest{},
		agentCheckStdout: runtimeAgentCheckOutput(t, agentSum, requiredRuntimeAgentFeatures...),
	}
	err = NewService(db, remote).RestartRuntime(context.Background(), RuntimeRestartRequest{
		Instance: instance, Server: server, Actor: "integration", TaskID: "task-restart-fanout", Language: "en",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	afterRows, err := db.ListAIFARDeployments(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	after := deploymentsByService(afterRows)
	for _, serviceName := range []string{"permission", "system"} {
		if after[serviceName].Generation != before[serviceName].Generation+1 || after[serviceName].Status != "Accepted" {
			t.Fatalf("%s was not independently accepted: before=%+v after=%+v", serviceName, before[serviceName], after[serviceName])
		}
		manifest, err := manifestStore.Get(instance.ID, serviceName)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.Spec.RestartGeneration != 6 {
			t.Fatalf("%s restart generation=%d, want 6", serviceName, manifest.Spec.RestartGeneration)
		}
	}
	if got, want := after["file"], before["file"]; got.Generation != want.Generation || got.SpecJSON != want.SpecJSON || got.CurrentRevision != want.CurrentRevision {
		t.Fatalf("offline service changed during restart fan-out: before=%+v after=%+v", want, got)
	}
	commands := remote.joinedCommands()
	runnerCalls := runner.joinedCalls()
	for _, forbidden := range []string{"restart-runtime", "reconcile-runtime", "runtime-spec.json", "stop-all", "stop-all-pods"} {
		if strings.Contains(commands, forbidden) || strings.Contains(runnerCalls, forbidden) {
			t.Fatalf("restart fan-out crossed forbidden aggregate path %q:\nremote:\n%s\nrunner:\n%s", forbidden, commands, runnerCalls)
		}
	}
}

type operationMatrixFixture struct {
	db            *store.Store
	instance      store.AppInstance
	server        store.Server
	agent         *runtimeagent.Manager
	manifestStore *runtimeagent.ManifestStore
	runner        *independentAgentRunner
	remote        *agentBridgeRemote
	service       Service
	before        map[string]store.AIFARDeployment
	peerManifest  []byte
	peerPods      []string
}

func newOperationMatrixFixture(t *testing.T) *operationMatrixFixture {
	t.Helper()
	agentBinary := withFakeRuntimeAgentBinary(t)
	agentSum, _, err := fileSHA256(agentBinary)
	if err != nil {
		t.Fatal(err)
	}
	db := openAIFARTestStore(t)
	server, err := db.SaveServer(store.Server{ID: "srv-1", Name: "operation-node", Host: "10.0.0.10", DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationModel"] = orchestrationModelServiceControllerV1
	metadata["services"] = []string{"permission", "file"}
	instance.Metadata = mustMetadata(t, metadata)
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	seeded := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	)
	for _, deployment := range seeded {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}

	stateDir := t.TempDir()
	manifestStore := &runtimeagent.ManifestStore{StateDir: stateDir}
	if err := manifestStore.PutInstance(runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{
		APIVersion: runtimeagent.ManifestAPIVersion, InstanceID: instance.ID, InstallRoot: "/aifar/apps/admin", Network: defaultNetworkName,
	})); err != nil {
		t.Fatal(err)
	}
	runner := newIndependentAgentRunner()
	runner.blocked = ""
	agent := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, Runner: runner, ManifestStore: manifestStore})
	t.Cleanup(func() { _ = agent.Remove(context.Background(), instance.ID) })
	for _, deployment := range seeded {
		var manifest runtimeagent.DeploymentManifest
		if err := json.Unmarshal([]byte(deployment.SpecJSON), &manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := agent.AcceptDeployment(context.Background(), manifest); err != nil {
			t.Fatal(err)
		}
	}
	waitForAgentCondition(t, agent, "permission", "Available")
	waitForAgentCondition(t, agent, "file", "Available")
	peerManifest, err := manifestStore.Get(instance.ID, "file")
	if err != nil {
		t.Fatal(err)
	}
	peerManifestJSON, err := json.Marshal(peerManifest)
	if err != nil {
		t.Fatal(err)
	}
	remote := &agentBridgeRemote{
		agent: agent, manifests: map[string]runtimeagent.DeploymentManifest{},
		agentCheckStdout: runtimeAgentCheckOutput(t, agentSum, requiredRuntimeAgentFeatures...),
	}
	return &operationMatrixFixture{
		db: db, instance: instance, server: server, agent: agent, manifestStore: manifestStore, runner: runner,
		remote: remote, service: NewService(db, remote), before: deploymentsByService(seeded),
		peerManifest: peerManifestJSON, peerPods: runner.snapshotServicePods("file"),
	}
}

func (r *independentAgentRunner) snapshotServicePods(serviceName string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0)
	for name := range r.pods {
		if strings.Contains(name, "-"+serviceName+"-") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (f *operationMatrixFixture) assertPeerUnchanged(t *testing.T) {
	t.Helper()
	rows, err := f.db.ListAIFARDeployments(f.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	peer := deploymentsByService(rows)["file"]
	want := f.before["file"]
	if peer.Generation != want.Generation || peer.CurrentRevision != want.CurrentRevision || peer.SpecJSON != want.SpecJSON || peer.DesiredReplicas != want.DesiredReplicas {
		t.Fatalf("peer control-plane desired state changed: before=%+v after=%+v", want, peer)
	}
	peerManifest, err := f.manifestStore.Get(f.instance.ID, "file")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(peerManifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(f.peerManifest) {
		t.Fatalf("peer Agent manifest changed:\nbefore=%s\nafter=%s", f.peerManifest, raw)
	}
	if got := f.runner.snapshotServicePods("file"); !slices.Equal(got, f.peerPods) {
		t.Fatalf("peer container set changed: before=%v after=%v", f.peerPods, got)
	}
}

func TestTypedRuntimeOperationMatrixPreservesPeerServiceAcrossServerAndAgent(t *testing.T) {
	for _, operation := range []string{"upload", "config", "scale", "offline", "rollback"} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			fixture := newOperationMatrixFixture(t)
			beforeTarget := fixture.before["permission"]
			switch operation {
			case "upload":
				artifact := filepath.Join(t.TempDir(), "permission.jar")
				if err := os.WriteFile(artifact, []byte("permission-v2"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := fixture.service.UpdateArtifact(context.Background(), ArtifactUpdateRequest{
					Instance: fixture.instance, Server: fixture.server, Language: "en", Actor: "integration", TaskID: "task-matrix-upload",
					ServiceName: "permission", ExpectedGeneration: beforeTarget.Generation, ArtifactLocalPath: artifact, ArtifactFileName: filepath.Base(artifact),
				}, fakeLogger{}, nil); err != nil {
					t.Fatal(err)
				}
			case "config":
				if err := fixture.service.ApplyRuntimeConfig(context.Background(), RuntimeConfigRequest{
					Instance: fixture.instance, Server: fixture.server, Language: "en", Actor: "integration", TaskID: "task-matrix-config",
					Config: RuntimeConfigPayload{Services: map[string]RuntimeConfigValues{"permission": {AppCPUs: "2"}}},
				}, fakeLogger{}, nil); err != nil {
					t.Fatal(err)
				}
			case "scale":
				if err := fixture.service.ScaleService(context.Background(), ScaleRequest{
					Instance: fixture.instance, Server: fixture.server, Language: "en", Actor: "integration", TaskID: "task-matrix-scale", ServiceName: "permission", Replicas: 2,
				}, fakeLogger{}, nil); err != nil {
					t.Fatal(err)
				}
			case "offline":
				if err := fixture.service.ScaleService(context.Background(), ScaleRequest{
					Instance: fixture.instance, Server: fixture.server, Language: "en", Actor: "integration", TaskID: "task-matrix-offline", ServiceName: "permission", Replicas: 0,
				}, fakeLogger{}, nil); err != nil {
					t.Fatal(err)
				}
			case "rollback":
				targetReleaseID := "20260702T010203.000000000Z-rollout-permission"
				targetArtifact := "/aifar/apps/admin/releases/" + targetReleaseID + "/services/permission/artifact/permission.jar"
				manifest, _ := json.Marshal(map[string]any{
					"schema": releaseManifestSchemaV2, "kind": "rollout", "releaseId": targetReleaseID, "changedServices": []string{"permission"},
					"artifacts": map[string]any{"permission": map[string]any{"file": "permission.jar", "sha256": strings.Repeat("a", 64), "remotePath": targetArtifact}},
				})
				if _, err := fixture.db.SaveAppRelease(store.AppRelease{
					InstanceID: fixture.instance.ID, App: AppName, Version: appBundleVersion, ReleaseID: targetReleaseID,
					ServerID: fixture.server.ID, Status: "success", ManifestJSON: string(manifest), CreatedAt: time.Now().Add(-time.Hour), ActivatedAt: time.Now().Add(-time.Hour),
				}); err != nil {
					t.Fatal(err)
				}
				if err := fixture.service.RollbackArtifact(context.Background(), ArtifactRollbackRequest{
					Instance: fixture.instance, Server: fixture.server, Language: "en", Actor: "integration", TaskID: "task-matrix-rollback",
					TargetReleaseID: targetReleaseID, Services: []string{"permission"}, Reason: "integration rollback",
				}, fakeLogger{}, nil); err != nil {
					t.Fatal(err)
				}
			}

			rows, err := fixture.db.ListAIFARDeployments(fixture.instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			target := deploymentsByService(rows)["permission"]
			if target.Generation != beforeTarget.Generation+1 || target.Status != "Accepted" || target.SpecJSON == beforeTarget.SpecJSON {
				t.Fatalf("target operation %s did not cross typed acceptance: before=%+v after=%+v", operation, beforeTarget, target)
			}
			fixture.assertPeerUnchanged(t)
		})
	}
}

func TestRuntimeMigrationAdoptsExistingPodsWithoutRestartAndEnablesTypedMutation(t *testing.T) {
	agentBinary := withFakeRuntimeAgentBinary(t)
	agentSum, _, err := fileSHA256(agentBinary)
	if err != nil {
		t.Fatal(err)
	}
	const (
		instanceID  = "aifar-legacy-integration"
		serverID    = "srv-legacy-integration"
		installRoot = "/aifar/apps/admin"
		revision    = "20260701T010203.000000000Z-runtime-v2"
	)
	services := []string{"permission", "file"}
	desired := map[string]int{"permission": 1, "file": 0}
	definitions := make([]serviceDefinition, 0, len(services))
	for _, definition := range legacyServiceDefinitions() {
		if slices.Contains(services, definition.Name) {
			definitions = append(definitions, definition)
		}
	}
	metadata := map[string]any{
		"installRoot": installRoot, "runtimeSpecPath": runtimeSpecPath(installRoot), "runtimeDir": installRoot + "/runtime", "envDir": installRoot + "/runtime/env",
		"orchestrationModel": orchestrationModelK8sLikeV1, "services": services, "desiredReplicas": desired,
		"currentRevision": revision, "serviceRevisions": map[string]string{"permission": revision, "file": revision},
		"gatewayPort": defaultGatewayPort, "webPort": defaultWebPort, "ingressNetwork": defaultNetworkName,
		"serviceCatalog": serviceCatalogMetadata(definitions),
	}
	instance := store.AppInstance{
		ID: instanceID, App: AppName, Version: appBundleVersion, ServerID: serverID, Status: "installed", Topology: defaultTopology, Metadata: mustMetadata(t, metadata),
	}
	db := openAIFARTestStore(t)
	server, err := db.SaveServer(store.Server{ID: serverID, Name: "legacy-node", Host: "10.0.0.10", DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	legacy := runtimeagent.NormalizeSpec(runtimeagent.LegacyRuntimeSpec{
		Version: runtimeagent.DefaultAgentVersion, InstanceID: instance.ID, InstallRoot: installRoot, Network: defaultNetworkName,
		Ingress: runtimeagent.IngressSpec{
			Mode: runtimeagent.DefaultIngressMode, GatewayService: "gateway", WebService: "web-vue3", GatewayPort: defaultGatewayPort, WebPort: defaultWebPort,
		},
	})
	manifests := map[string]runtimeagent.DeploymentManifest{}
	manifestHashes := map[string]string{}
	for _, serviceName := range services {
		definition, found := catalogDefinition(definitions, serviceName)
		if !found {
			t.Fatalf("missing definition for %s", serviceName)
		}
		deployment := store.AIFARDeployment{
			InstanceID: instance.ID, ServiceName: serviceName, DesiredReplicas: desired[serviceName], CurrentRevision: revision, Generation: 1, Status: "active",
		}
		manifest := runtimeagent.NormalizeDeploymentManifest(runtimeManifestDefaults(instance.ID, installRoot, definition, deployment, 1, metadata))
		manifest.Spec.Replicas = desired[serviceName]
		manifest = runtimeagent.NormalizeDeploymentManifest(manifest)
		manifests[serviceName] = manifest
		hashPayload, err := json.Marshal(struct {
			Spec    runtimeagent.DeploymentSpec `json:"spec"`
			Service runtimeagent.ServiceSpec    `json:"service"`
		}{Spec: manifest.Spec, Service: manifest.Service})
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(hashPayload)
		manifestHashes[serviceName] = fmt.Sprintf("%x", hash)
		legacy.Deployments = append(legacy.Deployments, manifest.Spec)
		legacy.Services = append(legacy.Services, manifest.Service)
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	runner := newIndependentAgentRunner()
	runner.blocked = ""
	permissionManifest := manifests["permission"]
	existingName := sanitizeContainerName(fmt.Sprintf("aifar-pod-%s-permission-%s-r1", instance.ID, revision))
	runner.pods[existingName] = true
	runner.podMetadata[existingName] = integrationPodMetadata{
		replica: "1", revision: revision, specHash: integrationDeploymentSpecHash(permissionManifest.Spec),
	}
	stateDir := t.TempDir()
	manifestStore := &runtimeagent.ManifestStore{StateDir: stateDir}
	agent := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, Runner: runner, ManifestStore: manifestStore})
	t.Cleanup(func() { _ = agent.Remove(context.Background(), instance.ID) })
	features := append([]string{}, requiredRuntimeAgentFeatures...)
	features = append(features, requiredRuntimeMigrationAgentFeatures...)
	remote := &agentBridgeRemote{
		agent: agent, control: db, manifests: map[string]runtimeagent.DeploymentManifest{}, legacyJSON: legacyJSON, legacyInstanceID: instance.ID,
		agentCheckStdout: runtimeAgentCheckOutput(t, agentSum, features...),
	}
	service := NewService(db, remote)
	if err := service.MigrateRuntimeModel(context.Background(), RuntimeMigrationRequest{
		Instance: instance, Server: server, Actor: "integration", TaskID: "task-migration-integration", Reason: "switch controller model",
	}, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	waitForAgentConditionForInstance(t, agent, instance.ID, "permission", "Available")
	waitForAgentConditionForInstance(t, agent, instance.ID, "file", "Offline")
	if remote.bootstrapCallCount != 1 {
		t.Fatalf("migration must bootstrap the real Agent exactly once: calls=%d", remote.bootstrapCallCount)
	}
	for _, call := range strings.Split(runner.joinedCalls(), "\n") {
		fields := strings.Fields(call)
		if len(fields) > 1 && fields[0] == "docker" && slices.Contains([]string{"run", "restart", "rm"}, fields[1]) {
			t.Fatalf("migration restarted or recreated an adopted container: %s\nall calls:\n%s", call, runner.joinedCalls())
		}
	}
	if got := runner.snapshotServicePods("permission"); !slices.Equal(got, []string{existingName}) {
		t.Fatalf("migration changed adopted permission container set: %v", got)
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	savedMetadata := metadataFromInstance(saved)
	if savedMetadata["orchestrationModel"] != orchestrationModelServiceControllerV1 {
		t.Fatalf("control plane did not switch model: %s", saved.Metadata)
	}
	if savedMetadata["legacyRuntimeSpecReadOnly"] != true || stringFromMetadata(savedMetadata, "legacyRuntimeSpecBackupPath", "") == "" {
		t.Fatalf("switched model did not fence the legacy aggregate spec read-only: %s", saved.Metadata)
	}
	rows, err := db.ListAIFARDeployments(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	byService := deploymentsByService(rows)
	if byService["permission"].Generation != 1 || byService["permission"].DesiredReplicas != 1 || byService["file"].Generation != 1 || byService["file"].DesiredReplicas != 0 {
		t.Fatalf("migration changed generation or desired replicas: %+v", byService)
	}
	for _, serviceName := range services {
		wantManifest := manifests[serviceName]
		row, found := byService[serviceName]
		if !found {
			t.Fatalf("migration lost control-plane deployment %q: %+v", serviceName, byService)
		}
		if row.CurrentRevision != revision {
			t.Fatalf("migration changed %s control-plane revision: got=%q want=%q", serviceName, row.CurrentRevision, revision)
		}
		wantSpecJSON, err := json.Marshal(wantManifest)
		if err != nil {
			t.Fatal(err)
		}
		if row.SpecJSON != string(wantSpecJSON) {
			t.Fatalf("migration changed %s canonical SpecJSON:\ngot:  %s\nwant: %s", serviceName, row.SpecJSON, wantSpecJSON)
		}
		var storedManifest runtimeagent.DeploymentManifest
		if err := json.Unmarshal([]byte(row.SpecJSON), &storedManifest); err != nil {
			t.Fatalf("decode migrated %s control-plane SpecJSON: %v", serviceName, err)
		}
		if !reflect.DeepEqual(storedManifest, wantManifest) {
			t.Fatalf("migration changed %s parsed control-plane manifest:\ngot:  %+v\nwant: %+v", serviceName, storedManifest, wantManifest)
		}

		rawManifest, err := os.ReadFile(filepath.Join(stateDir, instance.ID, "deployments", serviceName+".json"))
		if err != nil {
			t.Fatalf("read migrated %s raw Agent manifest: %v", serviceName, err)
		}
		wantRawManifest, err := json.MarshalIndent(wantManifest, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		wantRawManifest = append(wantRawManifest, '\n')
		if string(rawManifest) != string(wantRawManifest) {
			t.Fatalf("migration changed %s raw Agent manifest:\ngot:  %s\nwant: %s", serviceName, rawManifest, wantRawManifest)
		}
		agentManifest, err := manifestStore.Get(instance.ID, serviceName)
		if err != nil {
			t.Fatalf("read migrated %s parsed Agent manifest: %v", serviceName, err)
		}
		if !reflect.DeepEqual(agentManifest, wantManifest) {
			t.Fatalf("migration changed %s parsed Agent manifest:\ngot:  %+v\nwant: %+v", serviceName, agentManifest, wantManifest)
		}
	}
	wantServiceRevisions := map[string]any{"permission": revision, "file": revision}
	if got, ok := savedMetadata["serviceRevisions"].(map[string]any); !ok || !reflect.DeepEqual(got, wantServiceRevisions) {
		t.Fatalf("migration changed service revisions metadata: got=%v want=%v", got, wantServiceRevisions)
	}
	snapshot, err := agent.RuntimeInstanceSnapshot(instance.ID)
	if err != nil {
		t.Fatalf("real Agent snapshot after migration: err=%v snapshot=%+v", err, snapshot)
	}
	snapshotByService := make(map[string]runtimeagent.RuntimeDeploymentSnapshot, len(snapshot.Deployments))
	for _, deployment := range snapshot.Deployments {
		if _, duplicate := snapshotByService[deployment.ServiceName]; duplicate {
			t.Fatalf("real Agent snapshot duplicated service %q: %+v", deployment.ServiceName, snapshot.Deployments)
		}
		snapshotByService[deployment.ServiceName] = deployment
	}
	if len(snapshotByService) != len(services) {
		t.Fatalf("real Agent snapshot service set changed: %+v", snapshot.Deployments)
	}
	for _, serviceName := range services {
		want := runtimeagent.RuntimeDeploymentSnapshot{
			ServiceName: serviceName, ManifestGeneration: 1, ManifestSpecHash: manifestHashes[serviceName],
			StateGeneration: 1, ObservedGeneration: 1, StateSpecHash: manifestHashes[serviceName],
			DesiredReplicas: desired[serviceName],
		}
		if got, found := snapshotByService[serviceName]; !found || !reflect.DeepEqual(got, want) {
			t.Fatalf("real Agent snapshot changed %s desired-state proof: got=%+v found=%t want=%+v", serviceName, got, found, want)
		}
	}

	lock, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "permission", Operation: "scale", TaskID: "task-post-migration", Actor: "integration", ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ReleaseAIFAROrchestrationLockByID(lock.ID) })
	accepted, err := service.MutateDeployment(context.Background(), DeploymentMutationRequest{
		Instance: saved, Server: server, ServiceName: "permission", ExpectedGeneration: 1, TaskID: "task-post-migration", Operation: "scale", LockID: lock.ID,
		Mutate: func(manifest *runtimeagent.DeploymentManifest) error { manifest.Spec.Replicas = 2; return nil },
	}, fakeLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Generation != 2 || accepted.DesiredReplicas != 2 || accepted.Status != "Accepted" {
		t.Fatalf("post-switch typed mutation was not accepted: %+v", accepted)
	}
	agentManifest, err := manifestStore.Get(instance.ID, "permission")
	if err != nil || agentManifest.Metadata.Generation != 2 || agentManifest.Spec.Replicas != 2 {
		t.Fatalf("post-switch Agent manifest: err=%v manifest=%+v", err, agentManifest)
	}
	for _, forbidden := range []string{"reconcile-runtime --spec", "restart-runtime --spec"} {
		if strings.Contains(remote.joinedCommands(), forbidden) {
			t.Fatalf("post-switch mutation used legacy aggregate writer %q:\n%s", forbidden, remote.joinedCommands())
		}
	}
}

func TestServerAndAgentAcceptConcurrentIndependentServiceMutations(t *testing.T) {
	db := openAIFARTestStore(t)
	instance := installedAIFARInstance(t)
	var err error
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	seeded := seedPerServiceDeployments(t, instance,
		map[string]int{"file": 1, "permission": 0},
		map[string]int64{"file": 7, "permission": 3},
	)
	for _, deployment := range seeded {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}

	stateDir := t.TempDir()
	manifestStore := &runtimeagent.ManifestStore{StateDir: stateDir}
	if err := manifestStore.PutInstance(runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{
		APIVersion:  runtimeagent.ManifestAPIVersion,
		InstanceID:  instance.ID,
		InstallRoot: "/aifar/apps/admin",
		Network:     defaultNetworkName,
	})); err != nil {
		t.Fatal(err)
	}
	runner := newIndependentAgentRunner()
	agent := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, Runner: runner, ManifestStore: manifestStore})
	t.Cleanup(func() { _ = agent.Remove(context.Background(), instance.ID) })
	for _, deployment := range seeded {
		var manifest runtimeagent.DeploymentManifest
		if err := json.Unmarshal([]byte(deployment.SpecJSON), &manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := agent.AcceptDeployment(context.Background(), manifest); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-runner.fileStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("file did not enter its independent unready reconcile")
	}
	waitForAgentCondition(t, agent, "permission", "Offline")

	server := store.Server{ID: instance.ServerID, Host: "10.0.0.10", DeployDir: "/aifar/apps"}
	remote := &agentBridgeRemote{agent: agent, manifests: map[string]runtimeagent.DeploymentManifest{}}
	service := NewService(db, remote)
	now := time.Now().UTC()
	fileLock, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "file", Operation: "offline", TaskID: "task-file-offline", ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	permissionLock, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "permission", Operation: "scale", TaskID: "task-permission-start", ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ReleaseAIFAROrchestrationLockByID(fileLock.ID)
		_, _ = db.ReleaseAIFAROrchestrationLockByID(permissionLock.ID)
	})

	type mutationResult struct {
		service    string
		deployment store.AIFARDeployment
		err        error
	}
	results := make(chan mutationResult, 2)
	requests := []DeploymentMutationRequest{
		{
			Instance: instance, Server: server, ServiceName: "file", ExpectedGeneration: 7, TaskID: "task-file-offline", Operation: "offline", LockID: fileLock.ID,
			Mutate: func(manifest *runtimeagent.DeploymentManifest) error { manifest.Spec.Replicas = 0; return nil },
		},
		{
			Instance: instance, Server: server, ServiceName: "permission", ExpectedGeneration: 3, TaskID: "task-permission-start", Operation: "scale", LockID: permissionLock.ID,
			Mutate: func(manifest *runtimeagent.DeploymentManifest) error { manifest.Spec.Replicas = 1; return nil },
		},
	}
	for _, request := range requests {
		request := request
		go func() {
			deployment, mutationErr := service.MutateDeployment(context.Background(), request, fakeLogger{})
			results <- mutationResult{service: request.ServiceName, deployment: deployment, err: mutationErr}
		}()
	}
	accepted := map[string]store.AIFARDeployment{}
	for range requests {
		result := <-results
		if result.err != nil {
			t.Fatalf("%s mutation: %v", result.service, result.err)
		}
		accepted[result.service] = result.deployment
	}
	if accepted["file"].Generation != 8 || accepted["file"].DesiredReplicas != 0 || accepted["file"].Status != "Accepted" {
		t.Fatalf("file acceptance=%+v", accepted["file"])
	}
	if accepted["permission"].Generation != 4 || accepted["permission"].DesiredReplicas != 1 || accepted["permission"].Status != "Accepted" {
		t.Fatalf("permission acceptance=%+v", accepted["permission"])
	}

	fileState := waitForAgentCondition(t, agent, "file", "Offline")
	permissionState := waitForAgentCondition(t, agent, "permission", "Available")
	if fileState.Generation != 8 || permissionState.Generation != 4 {
		t.Fatalf("agent generations: file=%+v permission=%+v", fileState, permissionState)
	}
	persisted, err := db.ListAIFARDeployments(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	byService := deploymentsByService(persisted)
	if byService["file"].Generation != 8 || byService["permission"].Generation != 4 {
		t.Fatalf("canonical generations were not independently accepted: %+v", byService)
	}
	savedInstance, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if savedInstance.Status != "installed" {
		t.Fatalf("runtime convergence rewrote install lifecycle: %+v", savedInstance)
	}
}
