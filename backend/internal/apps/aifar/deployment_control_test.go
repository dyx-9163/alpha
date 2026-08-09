package aifar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

type deploymentControlTestStore struct {
	mu          sync.Mutex
	instance    store.AppInstance
	server      store.Server
	deployments map[string]store.AIFARDeployment
	markErr     error
}

func (s *deploymentControlTestStore) GetServer(id string, _ bool) (store.Server, error) {
	if id != s.server.ID {
		return store.Server{}, errors.New("server not found")
	}
	return s.server, nil
}
func (s *deploymentControlTestStore) GetAppInstance(id string) (store.AppInstance, error) {
	if id != s.instance.ID {
		return store.AppInstance{}, errors.New("instance not found")
	}
	return s.instance, nil
}
func (s *deploymentControlTestStore) ListAppInstances() ([]store.AppInstance, error) {
	return []store.AppInstance{s.instance}, nil
}
func (s *deploymentControlTestStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	s.instance = v
	return v, nil
}
func (s *deploymentControlTestStore) DeleteAppInstance(string) error { return nil }
func (s *deploymentControlTestStore) ListAIFARDeployments(instanceID string) ([]store.AIFARDeployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var values []store.AIFARDeployment
	for _, deployment := range s.deployments {
		if deployment.InstanceID == instanceID {
			values = append(values, deployment)
		}
	}
	return values, nil
}
func (s *deploymentControlTestStore) SaveAIFARDeploymentGeneration(next store.AIFARDeployment, expected int64) (store.AIFARDeployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.deployments[next.ServiceName]
	if !ok {
		return store.AIFARDeployment{}, store.ErrAIFARDeploymentNotFound
	}
	if current.Generation != expected {
		return store.AIFARDeployment{}, store.ErrAIFARDeploymentGenerationConflict
	}
	next.ID, next.CreatedAt, next.Generation, next.ObservedGeneration = current.ID, current.CreatedAt, expected+1, 0
	s.deployments[next.ServiceName] = next
	return next, nil
}
func (s *deploymentControlTestStore) ObserveAIFARDeployment(instanceID, serviceName string, generation int64, status, conditionsJSON string, at time.Time) (store.AIFARDeployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.markErr != nil {
		return store.AIFARDeployment{}, s.markErr
	}
	current, ok := s.deployments[serviceName]
	if !ok || current.InstanceID != instanceID {
		return store.AIFARDeployment{}, store.ErrAIFARDeploymentNotFound
	}
	if generation > current.Generation {
		return store.AIFARDeployment{}, store.ErrAIFARDeploymentGenerationConflict
	}
	if generation >= current.ObservedGeneration {
		current.Status, current.ConditionsJSON, current.LastTransitionAt = status, conditionsJSON, at
	}
	if generation > current.ObservedGeneration {
		current.ObservedGeneration = generation
	}
	s.deployments[serviceName] = current
	return current, nil
}

func (s *deploymentControlTestStore) AcceptAIFARDeployment(instanceID, serviceName string, generation int64, status, conditionsJSON string, at time.Time) (store.AIFARDeployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.markErr != nil {
		return store.AIFARDeployment{}, s.markErr
	}
	current, ok := s.deployments[serviceName]
	if !ok || current.InstanceID != instanceID {
		return store.AIFARDeployment{}, store.ErrAIFARDeploymentNotFound
	}
	if current.Generation != generation {
		return store.AIFARDeployment{}, store.ErrAIFARDeploymentGenerationConflict
	}
	current.Status, current.ConditionsJSON, current.LastTransitionAt = status, conditionsJSON, at
	s.deployments[serviceName] = current
	return current, nil
}

type deploymentControlTestRemote struct {
	mu                        sync.Mutex
	acceptance                runtimeagent.DeploymentAcceptance
	applyErr                  error
	applyStdout               string
	applyStdoutSet            bool
	readback                  runtimeagent.DeploymentState
	readbackErr               error
	readbackText              string
	uploadErr                 error
	cleanupErr                error
	applyCalls, readbackCalls int
	uploadCalls               int
	uploadMode                os.FileMode
	uploaded                  runtimeagent.DeploymentManifest
	commands                  []string
	applyStarted              chan struct{}
	releaseApply              chan struct{}
	applyStartOnce            sync.Once
}

func (r *deploymentControlTestRemote) UploadFile(_ context.Context, _ store.Server, localPath, _ string, mode os.FileMode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploadMode = mode
	r.uploadCalls++
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &r.uploaded); err != nil {
		return err
	}
	return r.uploadErr
}
func (r *deploymentControlTestRemote) Run(ctx context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	if err := ctx.Err(); err != nil {
		return adapter.CommandResult{}, err
	}
	switch {
	case strings.Contains(command, "apply-deployment"):
		r.applyCalls++
		if r.applyStarted != nil {
			r.applyStartOnce.Do(func() { close(r.applyStarted) })
		}
		if r.releaseApply != nil {
			r.mu.Unlock()
			select {
			case <-r.releaseApply:
			case <-ctx.Done():
				r.mu.Lock()
				return adapter.CommandResult{}, ctx.Err()
			}
			r.mu.Lock()
		}
		stdout := r.applyStdout
		if !r.applyStdoutSet && stdout == "" {
			data, _ := json.Marshal(r.acceptance)
			stdout = string(data)
		}
		return adapter.CommandResult{Stdout: stdout}, r.applyErr
	case strings.Contains(command, "get-deployment"):
		r.readbackCalls++
		stdout := r.readbackText
		if stdout == "" {
			data, _ := json.Marshal(r.readback)
			stdout = string(data)
		}
		return adapter.CommandResult{Stdout: stdout}, r.readbackErr
	case strings.Contains(command, "rm -f"):
		return adapter.CommandResult{}, r.cleanupErr
	default:
		return adapter.CommandResult{}, nil
	}
}

type deploymentControlTestLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *deploymentControlTestLog) Info(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}
func (l *deploymentControlTestLog) Error(format string, args ...any) { l.Info(format, args...) }
func (l *deploymentControlTestLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

func newDeploymentControlTestService(t *testing.T, remote *deploymentControlTestRemote) (Service, *deploymentControlTestStore, DeploymentMutationRequest) {
	t.Helper()
	metadata := `{"installRoot":"/aifar/apps/admin","ingressNetwork":"aifar-network","timezone":"Asia/Shanghai","serviceCatalog":[{"name":"permission","kind":"java","applicationName":"alpha-permission","port":38010,"healthPath":"/actuator/health/readiness","affinityPolicy":"round-robin"}]}`
	instance := store.AppInstance{ID: "instance-1", ServerID: "server-1", App: AppName, Version: "runtime-v2", Metadata: metadata}
	server := store.Server{ID: "server-1", DeployDir: "/aifar/apps"}
	db := &deploymentControlTestStore{instance: instance, server: server, deployments: map[string]store.AIFARDeployment{
		"permission": {ID: "deployment-1", InstanceID: instance.ID, ServiceName: "permission", DesiredReplicas: 1, CurrentRevision: "release-1", Generation: 1, Status: "Available", CreatedAt: time.Unix(1, 0).UTC()},
	}}
	req := DeploymentMutationRequest{Instance: instance, Server: server, ServiceName: "permission", ExpectedGeneration: 1, Actor: "operator", TaskID: "task-123", Operation: "offline", Mutate: func(manifest *runtimeagent.DeploymentManifest) error { manifest.Spec.Replicas = 0; return nil }}
	return NewService(db, remote), db, req
}

func TestMutateDeploymentAcceptsManifestBeforeReturning(t *testing.T) {
	remote := &deploymentControlTestRemote{}
	service, _, req := newDeploymentControlTestService(t, remote)
	manifest := buildRuntimeManifestForTest(t, req, 2)
	hash, err := runtimeagent.DeploymentManifestSpecHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	remote.acceptance = runtimeagent.DeploymentAcceptance{Accepted: true, Generation: 2, SpecHash: hash}
	got, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 2 || got.ObservedGeneration != 0 || got.Status != "Accepted" {
		t.Fatalf("deployment=%+v", got)
	}
	if remote.applyCalls != 1 || remote.readbackCalls != 0 {
		t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
	}
	if remote.uploadMode != 0o600 {
		t.Fatalf("upload mode=%#o", remote.uploadMode)
	}
	if remote.uploaded.Metadata.Generation != 2 || remote.uploaded.Spec.Replicas != 0 || remote.uploaded.Metadata.InstanceID != req.Instance.ID {
		t.Fatalf("uploaded=%+v", remote.uploaded)
	}
	if !strings.Contains(strings.Join(remote.commands, "\n"), "task-123") {
		t.Fatalf("safe task id is missing from controlled temporary path: %v", remote.commands)
	}
}

func TestAcceptDeploymentRejectsManifestOutsideServerInstallRoot(t *testing.T) {
	remote := &deploymentControlTestRemote{}
	service, _, req := newDeploymentControlTestService(t, remote)
	manifest := buildRuntimeManifestForTest(t, req, 2)
	manifest.Spec.EnvFiles[0] = "/tmp/foreign/runtime/env/java-common.env"
	manifest.Spec.Volumes[0].Source = "/tmp/foreign/runtime/env"
	_, err := service.AcceptDeployment(context.Background(), req.Server, manifest)
	assertStableDeploymentControlCode(t, err, agentRejectedManifestCode)
	if remote.uploadCalls != 0 || len(remote.commands) != 0 {
		t.Fatalf("unsafe Manifest reached remote: uploads=%d commands=%v", remote.uploadCalls, remote.commands)
	}
}

func TestLostAcceptanceResponseUsesGenerationHashReadback(t *testing.T) {
	remote := &deploymentControlTestRemote{applyErr: errors.New("connection reset")}
	service, _, req := newDeploymentControlTestService(t, remote)
	hash, _ := runtimeagent.DeploymentManifestSpecHash(buildRuntimeManifestForTest(t, req, 2))
	remote.readback = runtimeagent.DeploymentState{InstanceID: req.Instance.ID, ServiceName: "permission", Generation: 2, SpecHash: hash}
	got, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "Accepted" || remote.applyCalls != 1 || remote.readbackCalls != 1 {
		t.Fatalf("status=%q apply=%d readback=%d", got.Status, remote.applyCalls, remote.readbackCalls)
	}
}

func TestAgentInternalDeadlineUsesGenerationHashReadback(t *testing.T) {
	remote := &deploymentControlTestRemote{applyErr: context.DeadlineExceeded}
	service, _, req := newDeploymentControlTestService(t, remote)
	hash, _ := runtimeagent.DeploymentManifestSpecHash(buildRuntimeManifestForTest(t, req, 2))
	remote.readback = runtimeagent.DeploymentState{InstanceID: req.Instance.ID, ServiceName: "permission", Generation: 2, SpecHash: hash}
	got, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "Accepted" || remote.readbackCalls != 1 {
		t.Fatalf("got=%+v readback=%d", got, remote.readbackCalls)
	}
}

func TestMutateDeploymentCASAllowsOnlyOneWriter(t *testing.T) {
	remote := &deploymentControlTestRemote{}
	service, _, req := newDeploymentControlTestService(t, remote)
	hash, _ := runtimeagent.DeploymentManifestSpecHash(buildRuntimeManifestForTest(t, req, 2))
	remote.acceptance = runtimeagent.DeploymentAcceptance{Accepted: true, Generation: 2, SpecHash: hash}
	start, errs := make(chan struct{}), make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
			errs <- err
		}()
	}
	close(start)
	var successes, conflicts int
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			successes++
			continue
		}
		var stable interface{ StableCode() string }
		if errors.As(err, &stable) && stable.StableCode() == "AIFAR_RUNTIME_DEPLOYMENT_GENERATION_CONFLICT" {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 || remote.applyCalls != 1 {
		t.Fatalf("success=%d conflict=%d apply=%d", successes, conflicts, remote.applyCalls)
	}
}

func TestLateGenerationAcceptanceCannotOverwriteNewerDesiredGeneration(t *testing.T) {
	remote := &deploymentControlTestRemote{applyStarted: make(chan struct{}), releaseApply: make(chan struct{})}
	service, db, req := newDeploymentControlTestService(t, remote)
	hash, _ := runtimeagent.DeploymentManifestSpecHash(buildRuntimeManifestForTest(t, req, 2))
	remote.acceptance = runtimeagent.DeploymentAcceptance{Accepted: true, Generation: 2, SpecHash: hash}
	result := make(chan error, 1)
	go func() {
		_, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
		result <- err
	}()
	<-remote.applyStarted
	db.mu.Lock()
	gen2 := db.deployments["permission"]
	db.mu.Unlock()
	gen3 := gen2
	gen3.Status = "pending_acceptance"
	gen3.ConditionsJSON = `[{"type":"Accepted","status":false,"reason":"PendingAgentAcceptance","generation":3}]`
	if _, err := db.SaveAIFARDeploymentGeneration(gen3, 2); err != nil {
		t.Fatal(err)
	}
	close(remote.releaseApply)
	if err := <-result; err == nil {
		t.Fatal("late generation acceptance unexpectedly succeeded")
	}
	db.mu.Lock()
	stored := db.deployments["permission"]
	db.mu.Unlock()
	if stored.Generation != 3 || stored.Status != "pending_acceptance" || strings.Contains(stored.ConditionsJSON, `"generation":2`) {
		t.Fatalf("late acceptance overwrote newer desired state: %+v", stored)
	}
}

func TestMutateDeploymentAcceptedAgentThenStoreFailureRequiresRepair(t *testing.T) {
	remote := &deploymentControlTestRemote{}
	service, db, req := newDeploymentControlTestService(t, remote)
	hash, _ := runtimeagent.DeploymentManifestSpecHash(buildRuntimeManifestForTest(t, req, 2))
	remote.acceptance = runtimeagent.DeploymentAcceptance{Accepted: true, Generation: 2, SpecHash: hash}
	db.markErr = errors.New("database unavailable")
	_, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
	assertStableDeploymentControlCode(t, err, runtimeControlPlaneRepairCode)
	if remote.applyCalls != 1 || remote.readbackCalls != 0 {
		t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
	}
}

func TestMutateDeploymentUploadFailureLeavesPendingWithoutAgentApply(t *testing.T) {
	remote := &deploymentControlTestRemote{uploadErr: errors.New("upload failed with /private/path")}
	service, db, req := newDeploymentControlTestService(t, remote)
	got, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
	assertStableDeploymentControlCode(t, err, "AIFAR_RUNTIME_AGENT_UNAVAILABLE")
	if got.Generation != 2 || got.Status != "pending_acceptance" {
		t.Fatalf("deployment=%+v", got)
	}
	if remote.applyCalls != 0 || remote.readbackCalls != 0 {
		t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
	}
	if stored := db.deployments["permission"]; stored.Generation != 2 || stored.Status != "pending_acceptance" {
		t.Fatalf("stored=%+v", stored)
	}
}

func TestMutateDeploymentCleanupFailureAfterAcceptanceIsBestEffort(t *testing.T) {
	remote := &deploymentControlTestRemote{cleanupErr: errors.New("cleanup failed with /private/path")}
	service, _, req := newDeploymentControlTestService(t, remote)
	hash, _ := runtimeagent.DeploymentManifestSpecHash(buildRuntimeManifestForTest(t, req, 2))
	remote.acceptance = runtimeagent.DeploymentAcceptance{Accepted: true, Generation: 2, SpecHash: hash}
	log := &deploymentControlTestLog{}
	got, err := service.MutateDeployment(context.Background(), req, log)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "Accepted" || got.ObservedGeneration != 0 {
		t.Fatalf("deployment=%+v", got)
	}
	if !strings.Contains(strings.ToLower(log.String()), "cleanup") || strings.Contains(log.String(), "/private/path") {
		t.Fatalf("cleanup warning is absent or unsafe: %q", log.String())
	}
}

func TestMutateDeploymentLostResponseMismatchRequiresRepairWithoutResubmit(t *testing.T) {
	tests := []struct {
		name     string
		readback runtimeagent.DeploymentState
	}{
		{name: "stale", readback: runtimeagent.DeploymentState{Generation: 1, SpecHash: strings.Repeat("a", 64)}},
		{name: "higher", readback: runtimeagent.DeploymentState{Generation: 3, SpecHash: strings.Repeat("b", 64)}},
		{name: "different hash", readback: runtimeagent.DeploymentState{Generation: 2, SpecHash: strings.Repeat("c", 64)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &deploymentControlTestRemote{applyErr: errors.New("EOF"), readback: tt.readback}
			service, _, req := newDeploymentControlTestService(t, remote)
			_, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
			assertStableDeploymentControlCode(t, err, runtimeControlPlaneRepairCode)
			if remote.applyCalls != 1 || remote.readbackCalls != 1 {
				t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
			}
		})
	}
}

func TestMutateDeploymentAmbiguousAcceptanceUsesExactlyOneReadback(t *testing.T) {
	responses := []struct {
		name, stdout string
	}{
		{name: "empty", stdout: ""},
		{name: "truncated", stdout: `{"accepted":true`},
		{name: "malformed", stdout: `{not-json}`},
	}
	for _, response := range responses {
		t.Run(response.name, func(t *testing.T) {
			for _, matching := range []bool{true, false} {
				name := "mismatch"
				if matching {
					name = "matching"
				}
				t.Run(name, func(t *testing.T) {
					remote := &deploymentControlTestRemote{applyStdout: response.stdout, applyStdoutSet: true}
					service, _, req := newDeploymentControlTestService(t, remote)
					hash, err := runtimeagent.DeploymentManifestSpecHash(buildRuntimeManifestForTest(t, req, 2))
					if err != nil {
						t.Fatal(err)
					}
					remote.readback = runtimeagent.DeploymentState{InstanceID: req.Instance.ID, ServiceName: req.ServiceName, Generation: 2, SpecHash: hash}
					if !matching {
						remote.readback.SpecHash = strings.Repeat("f", 64)
					}
					got, mutateErr := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
					if matching {
						if mutateErr != nil {
							t.Fatal(mutateErr)
						}
						if got.Status != "Accepted" {
							t.Fatalf("deployment=%+v", got)
						}
					} else {
						assertStableDeploymentControlCode(t, mutateErr, runtimeControlPlaneRepairCode)
					}
					if remote.applyCalls != 1 || remote.readbackCalls != 1 {
						t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
					}
				})
			}
		})
	}
}

func TestMutateDeploymentAmbiguousAcceptanceReadbackUnavailableRequiresRepair(t *testing.T) {
	remote := &deploymentControlTestRemote{applyStdout: `{"accepted":`, applyStdoutSet: true, readbackErr: errors.New("connection reset")}
	service, _, req := newDeploymentControlTestService(t, remote)
	_, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
	assertStableDeploymentControlCode(t, err, runtimeControlPlaneRepairCode)
	if remote.applyCalls != 1 || remote.readbackCalls != 1 {
		t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
	}
}

func TestMutateDeploymentAmbiguousAcceptanceCleanupFailureAfterMatchingReadbackIsBestEffort(t *testing.T) {
	remote := &deploymentControlTestRemote{applyStdoutSet: true, cleanupErr: errors.New("cleanup failed with /private/path")}
	service, _, req := newDeploymentControlTestService(t, remote)
	hash, err := runtimeagent.DeploymentManifestSpecHash(buildRuntimeManifestForTest(t, req, 2))
	if err != nil {
		t.Fatal(err)
	}
	remote.readback = runtimeagent.DeploymentState{InstanceID: req.Instance.ID, ServiceName: req.ServiceName, Generation: 2, SpecHash: hash}
	log := &deploymentControlTestLog{}
	got, err := service.MutateDeployment(context.Background(), req, log)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "Accepted" || remote.applyCalls != 1 || remote.readbackCalls != 1 {
		t.Fatalf("deployment=%+v apply=%d readback=%d", got, remote.applyCalls, remote.readbackCalls)
	}
	if !strings.Contains(strings.ToLower(log.String()), "cleanup") || strings.Contains(log.String(), "/private/path") {
		t.Fatalf("cleanup warning is absent or unsafe: %q", log.String())
	}
}

func TestMutateDeploymentExplicitRejectedAcceptanceDoesNotReadBack(t *testing.T) {
	remote := &deploymentControlTestRemote{applyStdout: `{"accepted":false,"generation":2,"specHash":"` + strings.Repeat("a", 64) + `"}`, applyStdoutSet: true}
	service, _, req := newDeploymentControlTestService(t, remote)
	_, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
	assertStableDeploymentControlCode(t, err, agentAcceptanceInvalidCode)
	if remote.applyCalls != 1 || remote.readbackCalls != 0 {
		t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
	}
}

func TestMutateDeploymentClassifiesExplicitAgentRejectsAndInvalidAcceptance(t *testing.T) {
	tests := []struct {
		name, stdout string
		err          error
		code         string
	}{
		{name: "stale", err: errors.New("aifar-agent request failed: 409 Conflict STALE_DEPLOYMENT_GENERATION"), code: "AIFAR_RUNTIME_AGENT_STALE_GENERATION"},
		{name: "conflict", err: errors.New("aifar-agent request failed: 409 Conflict DEPLOYMENT_GENERATION_CONFLICT"), code: "AIFAR_RUNTIME_AGENT_GENERATION_CONFLICT"},
		{name: "explicit invalid", err: errors.New("aifar-agent request failed: 400 Bad Request INVALID_DEPLOYMENT_MANIFEST"), code: "AIFAR_RUNTIME_AGENT_REJECTED_MANIFEST"},
		{name: "invalid acceptance", stdout: `{"accepted":true,"generation":2,"specHash":"short"}`, code: "AIFAR_RUNTIME_AGENT_ACCEPTANCE_INVALID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &deploymentControlTestRemote{applyErr: tt.err, applyStdout: tt.stdout}
			service, _, req := newDeploymentControlTestService(t, remote)
			_, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
			assertStableDeploymentControlCode(t, err, tt.code)
		})
	}
}

func TestMutateDeploymentRejectsMismatchedAcceptance(t *testing.T) {
	remote := &deploymentControlTestRemote{acceptance: runtimeagent.DeploymentAcceptance{Accepted: true, Generation: 9, SpecHash: strings.Repeat("a", 64)}}
	service, _, req := newDeploymentControlTestService(t, remote)
	_, err := service.MutateDeployment(context.Background(), req, &deploymentControlTestLog{})
	assertStableDeploymentControlCode(t, err, runtimeControlPlaneRepairCode)
	if remote.applyCalls != 1 || remote.readbackCalls != 0 {
		t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
	}
}

func TestMutateDeploymentHonorsCancelledContextBeforeCAS(t *testing.T) {
	remote := &deploymentControlTestRemote{}
	service, db, req := newDeploymentControlTestService(t, remote)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.MutateDeployment(ctx, req, &deploymentControlTestLog{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if got := db.deployments["permission"].Generation; got != 1 {
		t.Fatalf("generation=%d", got)
	}
	if remote.applyCalls != 0 {
		t.Fatalf("apply=%d", remote.applyCalls)
	}
}

func TestMutateDeploymentPreservesApplyContextCancellation(t *testing.T) {
	remote := &deploymentControlTestRemote{applyStarted: make(chan struct{}), releaseApply: make(chan struct{})}
	service, db, req := newDeploymentControlTestService(t, remote)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		deployment store.AIFARDeployment
		err        error
	}, 1)
	go func() {
		got, err := service.MutateDeployment(ctx, req, &deploymentControlTestLog{})
		result <- struct {
			deployment store.AIFARDeployment
			err        error
		}{got, err}
	}()
	<-remote.applyStarted
	cancel()
	completed := <-result
	got, err := completed.deployment, completed.err
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context canceled", err)
	}
	if got.Generation != 2 || got.Status != "pending_acceptance" || db.deployments["permission"].Status != "pending_acceptance" {
		t.Fatalf("got=%+v stored=%+v", got, db.deployments["permission"])
	}
	if remote.applyCalls != 1 || remote.readbackCalls != 0 {
		t.Fatalf("apply=%d readback=%d", remote.applyCalls, remote.readbackCalls)
	}
}

func TestMutateDeploymentSanitizesLogsAndErrors(t *testing.T) {
	const secret = "PASSWORD=super-secret-token"
	remote := &deploymentControlTestRemote{applyErr: errors.New("transport failed " + secret), readbackErr: errors.New("readback failed " + secret)}
	service, _, req := newDeploymentControlTestService(t, remote)
	req.TaskID = "task'; printf super-secret-token"
	log := &deploymentControlTestLog{}
	_, err := service.MutateDeployment(context.Background(), req, log)
	assertStableDeploymentControlCode(t, err, runtimeControlPlaneRepairCode)
	combined := err.Error() + "\n" + log.String() + "\n" + strings.Join(remote.commands, "\n")
	for _, forbidden := range []string{secret, "super-secret-token", `\"spec\"`, `\"environment\"`, req.Instance.Metadata} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("sensitive value %q leaked in %q", forbidden, combined)
		}
	}
}

func TestDeploymentControlMessagesAreBilingual(t *testing.T) {
	for _, key := range []string{"aifar.deploymentControl.generationConflict", "aifar.deploymentControl.agentUnavailable", "aifar.deploymentControl.acceptanceMismatch", "aifar.deploymentControl.readbackMismatch", "aifar.deploymentControl.repairRequired"} {
		if got := i18n.Text("zh", key); got == key || strings.TrimSpace(got) == "" {
			t.Fatalf("missing zh %s", key)
		}
		if got := i18n.Text("en", key); got == key || strings.TrimSpace(got) == "" {
			t.Fatalf("missing en %s", key)
		}
	}
}

func TestBuildRuntimeManifestUsesCanonicalPerServiceAllowlistWithoutNacos(t *testing.T) {
	remote := &deploymentControlTestRemote{}
	_, db, req := newDeploymentControlTestService(t, remote)
	db.instance.Metadata = `{"installRoot":"/aifar/apps/admin","ingressNetwork":"aifar-network","timezone":"Asia/Shanghai","desiredReplicas":{"permission":99},"nacosReady":true,"serviceCatalog":[{"name":"permission","kind":"java","applicationName":"alpha-permission","port":38010,"healthPath":"/actuator/health/readiness","affinityPolicy":"round-robin","resources":{"cpus":"1.5","memory":"768m"}}]}`
	req.Instance = db.instance
	manifest, err := buildRuntimeManifest(req.Instance, db.deployments["permission"], 2)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Spec.Replicas != 1 || manifest.Spec.Resources.CPUs != "1.5" || manifest.Spec.Resources.Memory != "768m" {
		t.Fatalf("spec=%+v", manifest.Spec)
	}
	for _, envFile := range manifest.Spec.EnvFiles {
		if !strings.HasPrefix(envFile, "/aifar/apps/admin/runtime/env/") {
			t.Fatalf("env file escaped allowlist: %s", envFile)
		}
	}
	for _, volume := range manifest.Spec.Volumes {
		if !strings.HasPrefix(volume.Source, "/aifar/apps/admin/") {
			t.Fatalf("volume escaped allowlist: %s", volume.Source)
		}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"nacos", "desiredReplicas", "other-service"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("forbidden aggregate field %q in %s", forbidden, text)
		}
	}
	hash1, _ := runtimeagent.DeploymentManifestSpecHash(manifest)
	hash2, _ := runtimeagent.DeploymentManifestSpecHash(runtimeagent.NormalizeDeploymentManifest(manifest))
	if hash1 != hash2 || !deploymentSpecHashPattern.MatchString(hash1) {
		t.Fatalf("non-canonical hashes %q %q", hash1, hash2)
	}
}

func TestServiceCatalogMetadataPreservesRuntimeResources(t *testing.T) {
	definitions := []serviceDefinition{{
		Name: "permission", Kind: "java", ApplicationName: "alpha-permission", Port: 38010,
		HealthPath: "/ready", AffinityPolicy: "round-robin",
		Resources: runtimeagent.ResourceSpec{CPUs: "1.5", Memory: "768m"},
	}}
	metadata := map[string]any{"serviceCatalog": serviceCatalogMetadata(definitions)}
	got := serviceDefinitionsFromMetadata(metadata)
	if len(got) != 1 || got[0].Resources != definitions[0].Resources {
		t.Fatalf("resources were lost across catalog metadata: %+v", got)
	}
}

func TestBuildRuntimeManifestRejectsUnsafeHealthAndResources(t *testing.T) {
	tests := []struct {
		name    string
		catalog string
	}{
		{name: "health command injection", catalog: `{"name":"permission","kind":"java","applicationName":"alpha-permission","port":38010,"healthPath":"/ready; touch /tmp/pwned","affinityPolicy":"round-robin"}`},
		{name: "cpu flag injection", catalog: `{"name":"permission","kind":"java","applicationName":"alpha-permission","port":38010,"healthPath":"/ready","affinityPolicy":"round-robin","resources":{"cpus":"1 --privileged","memory":"1g"}}`},
		{name: "memory flag injection", catalog: `{"name":"permission","kind":"java","applicationName":"alpha-permission","port":38010,"healthPath":"/ready","affinityPolicy":"round-robin","resources":{"cpus":"1","memory":"1g;id"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &deploymentControlTestRemote{}
			_, db, _ := newDeploymentControlTestService(t, remote)
			db.instance.Metadata = `{"installRoot":"/aifar/apps/admin","ingressNetwork":"aifar-network","serviceCatalog":[` + tt.catalog + `]}`
			_, err := buildRuntimeManifest(db.instance, db.deployments["permission"], 2)
			if err == nil {
				t.Fatal("unsafe service catalog was accepted")
			}
		})
	}
}

func TestBuildRuntimeManifestRejectsMalformedTrailingStoredSpec(t *testing.T) {
	remote := &deploymentControlTestRemote{}
	_, db, req := newDeploymentControlTestService(t, remote)
	current := db.deployments["permission"]
	valid := buildRuntimeManifestForTest(t, req, 2)
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	current.SpecJSON = string(data) + `{`
	if _, err := buildRuntimeManifest(req.Instance, current, 2); err == nil {
		t.Fatal("malformed trailing stored Manifest was accepted")
	}
}

func buildRuntimeManifestForTest(t *testing.T, req DeploymentMutationRequest, generation int64) runtimeagent.DeploymentManifest {
	t.Helper()
	current := store.AIFARDeployment{InstanceID: req.Instance.ID, ServiceName: req.ServiceName, DesiredReplicas: 1, CurrentRevision: "release-1", Generation: generation - 1}
	manifest, err := buildRuntimeManifest(req.Instance, current, generation)
	if err != nil {
		t.Fatal(err)
	}
	if req.Mutate != nil {
		if err := req.Mutate(&manifest); err != nil {
			t.Fatal(err)
		}
	}
	return runtimeagent.NormalizeDeploymentManifest(manifest)
}

func assertStableDeploymentControlCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err=nil, want %s", want)
	}
	var stable interface{ StableCode() string }
	if !errors.As(err, &stable) || stable.StableCode() != want {
		t.Fatalf("err=%v, want code %s", err, want)
	}
}
