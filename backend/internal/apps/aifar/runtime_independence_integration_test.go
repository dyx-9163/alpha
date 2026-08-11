package aifar

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

// agentBridgeRemote exercises the Server mutation boundary through the same
// typed Manifest acceptance used by a real aifar-agent, while keeping SSH and
// Docker entirely local to the test.
type agentBridgeRemote struct {
	mu        sync.Mutex
	agent     *runtimeagent.Manager
	manifests map[string]runtimeagent.DeploymentManifest
}

func (r *agentBridgeRemote) UploadFile(_ context.Context, _ store.Server, localPath, remotePath string, mode os.FileMode) error {
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
	r.mu.Lock()
	r.manifests[remotePath] = manifest
	r.mu.Unlock()
	return nil
}

func (r *agentBridgeRemote) Run(ctx context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	switch {
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

type independentAgentRunner struct {
	mu          sync.Mutex
	pods        map[string]bool
	fileStarted chan struct{}
	fileOnce    sync.Once
}

func newIndependentAgentRunner() *independentAgentRunner {
	return &independentAgentRunner{pods: map[string]bool{}, fileStarted: make(chan struct{})}
}

func (r *independentAgentRunner) Run(ctx context.Context, name string, args ...string) (runtimeagent.CommandResult, error) {
	if name != "docker" {
		return runtimeagent.CommandResult{}, nil
	}
	call := strings.Join(args, " ")
	service := integrationServiceFromCall(call)
	if len(args) > 0 && args[0] == "run" {
		if service == "file" {
			r.fileOnce.Do(func() { close(r.fileStarted) })
			<-ctx.Done()
			return runtimeagent.CommandResult{}, ctx.Err()
		}
		container := integrationArgumentAfter(args, "--name")
		r.mu.Lock()
		r.pods[container] = true
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
			return runtimeagent.CommandResult{}, nil
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
				rows = append(rows, pod+"|1|rev-1|")
			} else {
				rows = append(rows, pod+"|"+pod)
			}
		}
		return runtimeagent.CommandResult{Stdout: strings.Join(rows, "\n")}, nil
	}
	if len(args) > 0 && args[0] == "rm" {
		r.mu.Lock()
		delete(r.pods, args[len(args)-1])
		r.mu.Unlock()
	}
	return runtimeagent.CommandResult{}, nil
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
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := manager.DeploymentState("aifar-1", serviceName)
		if ok {
			for _, condition := range state.Conditions {
				if condition.Status && condition.Type == conditionType {
					return state
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := manager.DeploymentState("aifar-1", serviceName)
	t.Fatalf("condition %s for %s not reached: %+v", conditionType, serviceName, state)
	return runtimeagent.DeploymentState{}
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
