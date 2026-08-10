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
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

type fakeStore struct {
	mu                        sync.Mutex
	servers                   map[string]store.Server
	instances                 []store.AppInstance
	tasks                     map[string]store.Task
	releases                  []store.AppRelease
	deployments               []store.AIFARDeployment
	replicaSets               []store.AIFARReplicaSet
	pods                      []store.AIFARPod
	endpoints                 []store.AIFARServiceEndpoint
	saveCalls                 int
	failSaveOn                int
	releaseSaveCalls          int
	failReleaseSaveOn         int
	deleteOldReleaseErr       error
	directDeploymentSaveCalls int
	acceptDeploymentCalls     int
	failDeploymentAcceptOn    int
}

type rollbackLockRaceStore struct {
	*fakeStore
	instanceAfterLock store.AppInstance
}

func (s *rollbackLockRaceStore) AcquireAIFAROrchestrationLock(lock store.AIFAROrchestrationLock) (store.AIFAROrchestrationLock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for idx, instance := range s.instances {
		if instance.ID == s.instanceAfterLock.ID {
			s.instances[idx] = s.instanceAfterLock
			break
		}
	}
	return lock, nil
}

func (*rollbackLockRaceStore) ReleaseAIFAROrchestrationLock(string, string, string) (bool, error) {
	return true, nil
}

func (*rollbackLockRaceStore) RenewAIFAROrchestrationLock(string, time.Time) (bool, error) {
	return true, nil
}

func (*rollbackLockRaceStore) ReleaseAIFAROrchestrationLockByID(string) (bool, error) {
	return true, nil
}

func (*rollbackLockRaceStore) RecoverAIFAROrchestrationLocks(string, string) (int, error) {
	return 0, nil
}

type resourceFakeStore struct {
	*fakeStore
	resources []store.Resource
}

type barrierListStore struct {
	*store.Store
	mu                sync.Mutex
	remaining         int
	ready             chan struct{}
	release           chan struct{}
	firstLockTaskID   string
	firstLockAcquired chan struct{}
}

type barrierCASStore struct {
	*store.Store
	mu        sync.Mutex
	remaining int
	calls     int
	ready     chan struct{}
	release   chan struct{}
}

type staleProjectionCASStore struct {
	*store.Store
	once    sync.Once
	blocked chan struct{}
	release chan struct{}
}

type firstProjectionCASStore struct {
	*store.Store
	mu       sync.Mutex
	blocked  chan struct{}
	release  chan struct{}
	didBlock bool
}

func (s *firstProjectionCASStore) SaveAppInstanceIfUnchanged(next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error) {
	s.mu.Lock()
	block := !s.didBlock
	if block {
		s.didBlock = true
		close(s.blocked)
	}
	s.mu.Unlock()
	if block {
		<-s.release
	}
	return s.Store.SaveAppInstanceIfUnchanged(next, expectedUpdatedAt)
}

func (s *staleProjectionCASStore) SaveAppInstanceIfUnchanged(next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error) {
	desired := desiredReplicasFromMetadata(metadataFromInstance(next))
	if desired["permission"] == 2 {
		s.once.Do(func() {
			close(s.blocked)
			<-s.release
		})
	}
	return s.Store.SaveAppInstanceIfUnchanged(next, expectedUpdatedAt)
}

type alwaysConflictCASStore struct {
	*fakeStore
	mu    sync.Mutex
	calls int
}

type renewalFailureStore struct {
	*store.Store
	renewOnce sync.Once
	renewed   chan struct{}
	armed     chan struct{}
	mu        sync.Mutex
	casCalls  int
}

type postAcceptanceRenewalFailureStore struct {
	*store.Store
	mu       sync.Mutex
	renewals int
	casCalls int
}

func (s *postAcceptanceRenewalFailureStore) RenewAIFAROrchestrationLock(string, time.Time) (bool, error) {
	s.mu.Lock()
	s.renewals++
	s.mu.Unlock()
	return false, nil
}

func (s *postAcceptanceRenewalFailureStore) SaveAppInstanceIfUnchanged(next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error) {
	s.mu.Lock()
	s.casCalls++
	s.mu.Unlock()
	return s.Store.SaveAppInstanceIfUnchanged(next, expectedUpdatedAt)
}

func (s *postAcceptanceRenewalFailureStore) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renewals, s.casCalls
}

func (s *renewalFailureStore) RenewAIFAROrchestrationLock(string, time.Time) (bool, error) {
	s.renewOnce.Do(func() { close(s.renewed) })
	<-s.armed
	return false, nil
}

func (s *renewalFailureStore) SaveAppInstanceIfUnchanged(next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error) {
	s.mu.Lock()
	s.casCalls++
	s.mu.Unlock()
	return s.Store.SaveAppInstanceIfUnchanged(next, expectedUpdatedAt)
}

func (s *renewalFailureStore) appInstanceCASCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.casCalls
}

type heartbeatBlockingRemote struct {
	*fakeRemote
	blockContains string
	armed         chan struct{}
	reached       chan struct{}
	once          sync.Once
}

func (r *heartbeatBlockingRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if strings.Contains(command, r.blockContains) {
		r.once.Do(func() {
			close(r.reached)
			close(r.armed)
		})
		<-ctx.Done()
		return adapter.CommandResult{}, ctx.Err()
	}
	return r.fakeRemote.Run(ctx, server, command)
}

func requireHeartbeatRenewalCancellation(t *testing.T, renewed, remoteReached <-chan struct{}, done <-chan error, cancel context.CancelFunc) {
	t.Helper()
	select {
	case <-remoteReached:
	case err := <-done:
		t.Fatalf("operation ended before the blocking remote call: %v", err)
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("operation did not reach the blocking remote call")
	}
	select {
	case <-renewed:
	case err := <-done:
		t.Fatalf("operation ended before a heartbeat renewal was attempted: %v", err)
	case <-time.After(250 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("operation did not start lock heartbeat renewal")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("operation succeeded after lock renewal failed")
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("operation did not stop after lock renewal failed")
	}
}

func (s *alwaysConflictCASStore) SaveAppInstanceIfUnchanged(store.AppInstance, time.Time) (store.AppInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return store.AppInstance{}, store.ErrAppInstanceConflict
}

func (s *alwaysConflictCASStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *barrierCASStore) SaveAppInstanceIfUnchanged(next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error) {
	s.mu.Lock()
	s.calls++
	wait := s.remaining > 0
	if wait {
		s.remaining--
		if s.remaining == 0 {
			close(s.ready)
		}
	}
	s.mu.Unlock()
	if wait {
		<-s.release
	}
	return s.Store.SaveAppInstanceIfUnchanged(next, expectedUpdatedAt)
}

func (s *barrierCASStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestUpdateAppInstanceMetadataExhaustsCASConflictsAsRepairRequired(t *testing.T) {
	instance := installedAIFARInstance(t)
	instance.Status = "install_failed"
	instance.UpdatedAt = time.Now().UTC()
	conflicts := &alwaysConflictCASStore{fakeStore: &fakeStore{instances: []store.AppInstance{instance}}}

	_, err := NewService(conflicts, &fakeRemote{}).updateAppInstanceMetadata(instance.ID, "AIFAR_TEST_METADATA_REPAIR_REQUIRED", func(metadata map[string]any) error {
		metadata["desiredReplicas"] = map[string]int{"permission": 2}
		return nil
	})
	var controlErr *deploymentControlError
	if !errors.As(err, &controlErr) || controlErr.StableCode() != runtimeControlPlaneRepairCode || controlErr.ReasonCode() != "AIFAR_TEST_METADATA_REPAIR_REQUIRED" || !errors.Is(err, store.ErrAppInstanceConflict) {
		t.Fatalf("error=%v, want bounded repair-required conflict", err)
	}
	if calls := conflicts.callCount(); calls != appInstanceMetadataCASAttempts {
		t.Fatalf("CAS calls=%d, want bounded attempts=%d", calls, appInstanceMetadataCASAttempts)
	}
	saved, err := conflicts.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "install_failed" || saved.Metadata != instance.Metadata {
		t.Fatalf("exhausted conflicts modified app instance: before=%+v after=%+v", instance, saved)
	}
}

type firstDeploymentListBarrierStore struct {
	*fakeStore
	once    sync.Once
	listed  chan struct{}
	release chan struct{}
}

type realFirstDeploymentListBarrierStore struct {
	*store.Store
	once    sync.Once
	listed  chan struct{}
	release chan struct{}
}

func (s *realFirstDeploymentListBarrierStore) ListAIFARDeployments(instanceID string) ([]store.AIFARDeployment, error) {
	items, err := s.Store.ListAIFARDeployments(instanceID)
	if err != nil {
		return nil, err
	}
	s.once.Do(func() {
		close(s.listed)
		<-s.release
	})
	return items, nil
}

func (s *firstDeploymentListBarrierStore) ListAIFARDeployments(instanceID string) ([]store.AIFARDeployment, error) {
	items, err := s.fakeStore.ListAIFARDeployments(instanceID)
	if err != nil {
		return nil, err
	}
	s.once.Do(func() {
		close(s.listed)
		<-s.release
	})
	return items, nil
}

func (s *barrierListStore) ListAppInstances() ([]store.AppInstance, error) {
	instances, err := s.Store.ListAppInstances()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	wait := s.remaining > 0
	if wait {
		s.remaining--
		if s.remaining == 0 {
			close(s.ready)
		}
	}
	s.mu.Unlock()
	if wait {
		<-s.release
	}
	return instances, nil
}

func (s *barrierListStore) AcquireAIFAROrchestrationLock(lock store.AIFAROrchestrationLock) (store.AIFAROrchestrationLock, error) {
	if s.firstLockAcquired != nil && lock.TaskID != s.firstLockTaskID {
		<-s.firstLockAcquired
	}
	acquired, err := s.Store.AcquireAIFAROrchestrationLock(lock)
	if s.firstLockAcquired != nil && lock.TaskID == s.firstLockTaskID {
		close(s.firstLockAcquired)
	}
	return acquired, err
}

func (s *resourceFakeStore) ListResources() ([]store.Resource, error) {
	return append([]store.Resource(nil), s.resources...), nil
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

func (f *fakeStore) GetTask(id string) (store.Task, []store.TaskLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tasks == nil {
		return store.Task{}, nil, sql.ErrNoRows
	}
	task, ok := f.tasks[id]
	if !ok {
		return store.Task{}, nil, sql.ErrNoRows
	}
	return task, nil, nil
}

func (f *fakeStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.failSaveOn > 0 && f.saveCalls == f.failSaveOn {
		return store.AppInstance{}, errors.New("control-plane save failed")
	}
	now := time.Now()
	if v.ID == "" {
		v.ID = store.NewID("app")
		v.CreatedAt = now
	}
	for idx, existing := range f.instances {
		if existing.ID == v.ID {
			if v.CreatedAt.IsZero() {
				v.CreatedAt = existing.CreatedAt
			}
			if !now.After(existing.UpdatedAt) {
				now = existing.UpdatedAt.Add(time.Millisecond)
			}
			v.UpdatedAt = now
			f.instances[idx] = v
			return v, nil
		}
	}
	v.UpdatedAt = now
	f.instances = append(f.instances, v)
	return v, nil
}

func (f *fakeStore) SaveAppInstanceIfUnchanged(next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.failSaveOn > 0 && f.saveCalls == f.failSaveOn {
		return store.AppInstance{}, errors.New("control-plane save failed")
	}
	for idx, current := range f.instances {
		if current.ID != next.ID {
			continue
		}
		if !current.UpdatedAt.Equal(expectedUpdatedAt) {
			return store.AppInstance{}, store.ErrAppInstanceConflict
		}
		fresh := time.Now().UTC()
		if fresh.Sub(expectedUpdatedAt) < time.Millisecond {
			fresh = expectedUpdatedAt.Add(time.Millisecond)
		}
		next.ID = current.ID
		next.App = current.App
		next.CreatedAt = current.CreatedAt
		next.UpdatedAt = fresh
		f.instances[idx] = next
		return next, nil
	}
	return store.AppInstance{}, sql.ErrNoRows
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
	f.releaseSaveCalls++
	if f.failReleaseSaveOn > 0 && f.releaseSaveCalls == f.failReleaseSaveOn {
		return store.AppRelease{}, errors.New("required release persistence failed")
	}
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

func (f *fakeStore) ListAppReleases(instanceID string) ([]store.AppRelease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.AppRelease{}
	for _, release := range f.releases {
		if release.InstanceID == instanceID {
			out = append(out, release)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteOldAppReleases(instanceID string, keep int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteOldReleaseErr != nil {
		return 0, f.deleteOldReleaseErr
	}
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

func (f *fakeStore) SaveAIFARDeployment(v store.AIFARDeployment) (store.AIFARDeployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.directDeploymentSaveCalls++
	if v.ID == "" {
		v.ID = store.NewID("aifardeploy")
	}
	for idx, existing := range f.deployments {
		if existing.InstanceID == v.InstanceID && existing.ServiceName == v.ServiceName {
			f.deployments[idx] = v
			return v, nil
		}
	}
	f.deployments = append(f.deployments, v)
	return v, nil
}

func (f *fakeStore) SaveAIFARDeploymentGeneration(next store.AIFARDeployment, expectedGeneration int64) (store.AIFARDeployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for idx, current := range f.deployments {
		if current.InstanceID != next.InstanceID || current.ServiceName != next.ServiceName {
			continue
		}
		if current.Generation != expectedGeneration {
			return current, store.ErrAIFARDeploymentGenerationConflict
		}
		next.ID = current.ID
		next.CreatedAt = current.CreatedAt
		next.Generation = expectedGeneration + 1
		next.ObservedGeneration = 0
		f.deployments[idx] = next
		return next, nil
	}
	if expectedGeneration != 0 {
		return store.AIFARDeployment{}, store.ErrAIFARDeploymentNotFound
	}
	next.ID = store.NewID("aifardeploy")
	next.Generation = 1
	next.ObservedGeneration = 0
	next.CreatedAt = time.Now().UTC()
	f.deployments = append(f.deployments, next)
	return next, nil
}

func (f *fakeStore) AcceptAIFARDeployment(instanceID, serviceName string, generation int64, status, conditionsJSON string, at time.Time) (store.AIFARDeployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acceptDeploymentCalls++
	if f.failDeploymentAcceptOn > 0 && f.acceptDeploymentCalls == f.failDeploymentAcceptOn {
		return store.AIFARDeployment{}, errors.New("control-plane acceptance write failed")
	}
	for idx, current := range f.deployments {
		if current.InstanceID != instanceID || current.ServiceName != serviceName {
			continue
		}
		if current.Generation != generation {
			return current, store.ErrAIFARDeploymentGenerationConflict
		}
		current.Status = status
		current.ConditionsJSON = conditionsJSON
		current.LastTransitionAt = at
		f.deployments[idx] = current
		return current, nil
	}
	return store.AIFARDeployment{}, store.ErrAIFARDeploymentNotFound
}

func (f *fakeStore) SaveAIFARInitialDesiredWithLock(_ string, deployments []store.AIFARDeployment, replicaSets []store.AIFARReplicaSet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(deployments) == 0 || len(deployments) != len(replicaSets) {
		return errors.New("incomplete initial desired set")
	}
	instanceID := deployments[0].InstanceID
	existingDeployments := make([]store.AIFARDeployment, 0)
	for _, deployment := range f.deployments {
		if deployment.InstanceID == instanceID {
			existingDeployments = append(existingDeployments, deployment)
		}
	}
	existingReplicaSets := make([]store.AIFARReplicaSet, 0)
	for _, replicaSet := range f.replicaSets {
		if replicaSet.InstanceID == instanceID {
			existingReplicaSets = append(existingReplicaSets, replicaSet)
		}
	}
	if len(existingDeployments) != 0 || len(existingReplicaSets) != 0 {
		if len(existingDeployments) != len(deployments) || len(existingReplicaSets) != len(replicaSets) {
			return store.ErrAIFARDeploymentGenerationConflict
		}
		byService := make(map[string]store.AIFARDeployment, len(deployments))
		for _, desired := range deployments {
			byService[desired.ServiceName] = desired
		}
		for _, current := range existingDeployments {
			desired, ok := byService[current.ServiceName]
			if !ok || current.Generation != desired.Generation || current.DesiredReplicas != desired.DesiredReplicas || current.CurrentRevision != desired.CurrentRevision || current.SpecJSON != desired.SpecJSON || current.StrategyJSON != desired.StrategyJSON {
				return store.ErrAIFARDeploymentGenerationConflict
			}
			delete(byService, current.ServiceName)
		}
		if len(byService) != 0 {
			return store.ErrAIFARDeploymentGenerationConflict
		}
		replicaSetsByService := make(map[string]store.AIFARReplicaSet, len(replicaSets))
		for _, desired := range replicaSets {
			replicaSetsByService[desired.ServiceName] = desired
		}
		for _, current := range existingReplicaSets {
			desired, ok := replicaSetsByService[current.ServiceName]
			if !ok || current.Revision != desired.Revision || current.Image != desired.Image || current.ArtifactHash != desired.ArtifactHash || current.DesiredPods != desired.DesiredPods {
				return store.ErrAIFARDeploymentGenerationConflict
			}
			delete(replicaSetsByService, current.ServiceName)
		}
		if len(replicaSetsByService) != 0 {
			return store.ErrAIFARDeploymentGenerationConflict
		}
		return nil
	}
	for _, desired := range deployments {
		f.directDeploymentSaveCalls++
		if desired.ID == "" {
			desired.ID = store.NewID("aifardeploy")
		}
		f.deployments = append(f.deployments, desired)
	}
	for _, desired := range replicaSets {
		if desired.ID == "" {
			desired.ID = store.NewID("aifarrs")
		}
		f.replicaSets = append(f.replicaSets, desired)
	}
	return nil
}

func (f *fakeStore) AcceptAIFARDeploymentWithLock(_ string, expected store.AIFARDeployment, status, conditionsJSON string, at time.Time) (store.AIFARDeployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acceptDeploymentCalls++
	if f.failDeploymentAcceptOn > 0 && f.acceptDeploymentCalls == f.failDeploymentAcceptOn {
		return store.AIFARDeployment{}, errors.New("control-plane acceptance write failed")
	}
	for idx, current := range f.deployments {
		if current.InstanceID != expected.InstanceID || current.ServiceName != expected.ServiceName {
			continue
		}
		if current.Generation != expected.Generation || current.CurrentRevision != expected.CurrentRevision || current.SpecJSON != expected.SpecJSON {
			return current, store.ErrAIFARDeploymentGenerationConflict
		}
		if strings.EqualFold(current.Status, "Accepted") || current.ObservedGeneration >= current.Generation {
			return current, nil
		}
		current.Status = status
		current.ConditionsJSON = conditionsJSON
		current.LastTransitionAt = at
		f.deployments[idx] = current
		return current, nil
	}
	return store.AIFARDeployment{}, store.ErrAIFARDeploymentNotFound
}

func (f *fakeStore) ListAIFARDeployments(instanceID string) ([]store.AIFARDeployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.AIFARDeployment{}
	for _, item := range f.deployments {
		if item.InstanceID == instanceID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeStore) SaveAIFARReplicaSet(v store.AIFARReplicaSet) (store.AIFARReplicaSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v.ID == "" {
		v.ID = store.NewID("aifarrs")
	}
	for idx, existing := range f.replicaSets {
		if existing.InstanceID == v.InstanceID && existing.ServiceName == v.ServiceName && existing.Revision == v.Revision {
			f.replicaSets[idx] = v
			return v, nil
		}
	}
	f.replicaSets = append(f.replicaSets, v)
	return v, nil
}

func (f *fakeStore) ListAIFARReplicaSets(instanceID string) ([]store.AIFARReplicaSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.AIFARReplicaSet{}
	for _, item := range f.replicaSets {
		if item.InstanceID == instanceID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeStore) SaveAIFARPod(v store.AIFARPod) (store.AIFARPod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v.ID == "" {
		v.ID = store.NewID("aifarpod")
	}
	for idx, existing := range f.pods {
		if existing.InstanceID == v.InstanceID && existing.ServiceName == v.ServiceName && existing.PodID == v.PodID {
			f.pods[idx] = v
			return v, nil
		}
	}
	f.pods = append(f.pods, v)
	return v, nil
}

func (f *fakeStore) ListAIFARPods(instanceID string) ([]store.AIFARPod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.AIFARPod{}
	for _, item := range f.pods {
		if item.InstanceID == instanceID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeStore) ReplaceAIFARServiceEndpoints(instanceID, serviceName string, endpoints []store.AIFARServiceEndpoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.endpoints[:0]
	for _, item := range f.endpoints {
		if item.InstanceID == instanceID && item.ServiceName == serviceName {
			continue
		}
		next = append(next, item)
	}
	for _, endpoint := range endpoints {
		if endpoint.ID == "" {
			endpoint.ID = store.NewID("aifarendp")
		}
		next = append(next, endpoint)
	}
	f.endpoints = next
	return nil
}

func (f *fakeStore) ListAIFARServiceEndpoints(instanceID string) ([]store.AIFARServiceEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.AIFARServiceEndpoint{}
	for _, item := range f.endpoints {
		if item.InstanceID == instanceID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeStore) PruneAIFARPodRecords(instanceID string, existingContainerNames []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := stringSet(existingContainerNames)
	next := f.pods[:0]
	deleted := 0
	for _, item := range f.pods {
		if item.InstanceID == instanceID && !keep[item.ContainerName] {
			deleted++
			continue
		}
		next = append(next, item)
	}
	f.pods = next
	return deleted, nil
}

func (f *fakeStore) PruneAIFARServiceEndpointRecords(instanceID string, existingContainerNames []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := stringSet(existingContainerNames)
	next := f.endpoints[:0]
	deleted := 0
	for _, item := range f.endpoints {
		if item.InstanceID == instanceID && !keep[item.ContainerName] {
			deleted++
			continue
		}
		next = append(next, item)
	}
	f.endpoints = next
	return deleted, nil
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
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

func TestRevisionHelpersIgnoreNilMetadataValues(t *testing.T) {
	metadata := map[string]any{
		"activeEndpoints": map[string]any{
			"file": []any{map[string]any{"releaseId": nil}},
		},
		"serviceRevisions": map[string]any{
			"file":    nil,
			"gateway": "<no value>",
		},
		"currentRevision": nil,
		"releaseId":       nil,
	}
	if got := currentRevisionForService(metadata, "file"); got != "" {
		t.Fatalf("expected nil file revision to be ignored, got %q", got)
	}
	if got := endpointRevision(map[string]any{"revision": "<nil>", "releaseId": "rev-1"}); got != "rev-1" {
		t.Fatalf("expected endpoint revision to skip invalid revision, got %q", got)
	}
	revisions := serviceRevisionsFromMetadata(metadata)
	if _, ok := revisions["file"]; ok {
		t.Fatalf("expected invalid file revision to be omitted, got %+v", revisions)
	}
	if _, ok := revisions["gateway"]; ok {
		t.Fatalf("expected invalid gateway revision to be omitted, got %+v", revisions)
	}
	if got := stringFromMetadata(map[string]any{"value": nil}, "value", "fallback"); got != "fallback" {
		t.Fatalf("expected nil metadata string to use fallback, got %q", got)
	}
}

func TestParseAutoscaleStatusCleansInvalidReleaseID(t *testing.T) {
	status := parseAutoscaleStatus("endpoint=file|aifar-pod-admin-file--nil--r2|<no value>|2|38005|true|healthy|1|2147483648\n")
	if len(status.Endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %+v", status.Endpoints)
	}
	if status.Endpoints[0].ReleaseID != "" {
		t.Fatalf("expected invalid release id to be empty, got %q", status.Endpoints[0].ReleaseID)
	}
}

func TestReusableAIFARInstallInstanceIDRejectsActiveSameRoot(t *testing.T) {
	svc := Service{store: &fakeStore{instances: []store.AppInstance{
		{ID: "legacy", App: AppName, ServerID: "srv-1", Status: "installed", Metadata: `{}`},
		{ID: "other-root", App: AppName, ServerID: "srv-1", Status: "installed", Metadata: `{"installRoot":"/aifar/apps/other"}`},
		{ID: "same-root", App: AppName, ServerID: "srv-1", Status: "installed", Metadata: `{"installRoot":"/aifar/apps/admin/"}`},
	}}}
	if _, err := svc.reusableAIFARInstallInstanceID("srv-1", "/aifar/apps/admin"); err == nil {
		t.Fatal("expected active same-root AIFAR instance to block install")
	}

	svc = Service{store: &fakeStore{instances: []store.AppInstance{
		{ID: "failed-root", App: AppName, ServerID: "srv-1", Status: "install_failed", Metadata: `{"installRoot":"/aifar/apps/admin/"}`},
	}}}
	id, err := svc.reusableAIFARInstallInstanceID("srv-1", "/aifar/apps/admin")
	if err != nil {
		t.Fatal(err)
	}
	if id != "failed-root" {
		t.Fatalf("expected failed same-root install to be reusable, got %q", id)
	}

	svc = Service{store: &fakeStore{
		instances: []store.AppInstance{
			{ID: "stale-installing", App: AppName, ServerID: "srv-1", Status: "installing", Metadata: `{"installRoot":"/aifar/apps/admin/","installState":"installing","taskId":"task-failed"}`},
		},
		tasks: map[string]store.Task{
			"task-failed": {ID: "task-failed", Status: "failed"},
		},
	}}
	id, err = svc.reusableAIFARInstallInstanceID("srv-1", "/aifar/apps/admin")
	if err != nil {
		t.Fatal(err)
	}
	if id != "stale-installing" {
		t.Fatalf("expected failed installing task to be reusable, got %q", id)
	}

	svc = Service{store: &fakeStore{instances: []store.AppInstance{
		{ID: "legacy-installing", App: AppName, ServerID: "srv-1", Status: "installing", Metadata: `{"installRoot":"/aifar/apps/admin/","installState":"installing"}`},
	}}}
	id, err = svc.reusableAIFARInstallInstanceID("srv-1", "/aifar/apps/admin")
	if err != nil {
		t.Fatal(err)
	}
	if id != "legacy-installing" {
		t.Fatalf("expected legacy installing record without taskId to be reusable, got %q", id)
	}

	svc = Service{store: &fakeStore{
		instances: []store.AppInstance{
			{ID: "active-installing", App: AppName, ServerID: "srv-1", Status: "installing", Metadata: `{"installRoot":"/aifar/apps/admin/","installState":"installing","taskId":"task-running"}`},
		},
		tasks: map[string]store.Task{
			"task-running": {ID: "task-running", Status: "running"},
		},
	}}
	if _, err := svc.reusableAIFARInstallInstanceID("srv-1", "/aifar/apps/admin"); err == nil {
		t.Fatal("expected active installing task to block install")
	}

	svc = Service{store: &fakeStore{instances: []store.AppInstance{
		{ID: "legacy", App: AppName, ServerID: "srv-1", Status: "installed", Metadata: `{}`},
	}}}
	id, err = svc.reusableAIFARInstallInstanceID("srv-1", "/aifar/apps/admin")
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
	rollbackScript          string
	autoscaleScript         string
	runtimeConfigScript     string
	serviceInstallScript    string
	servicePrepareCounts    map[string]int
	servicePrepareRevisions map[string][]string
	failPreparedServices    map[string]bool
	scaleServiceScript      string
	runtimeReconcileScript  string
	runtimeRestartScript    string
	runtimePodScanStdout    string
	runtimeAgentUninstall   string
	runtimeAgentCheckStdout string
	installStdout           string
	installRuns             int
	bootstrapHashOverrides  map[string]string
	bootstrapNameOverrides  map[string]string
	deploymentManifests     map[string]runtimeagent.DeploymentManifest
	deploymentStates        map[string]runtimeagent.DeploymentState
	deploymentApplyFailures map[string]int
	deploymentApplyCounts   map[string]int
	statusStdout            string
	autoscaleStatusStdouts  []string
	autoscaleStatusFallback string
	failCommandContains     string
	scaleServiceErr         error
	scaleServiceRuns        int
	scaleFinalizeScript     string
}

type installStageBarrierRemote struct {
	*fakeRemote
	blockFirstUpload bool
	blockInstallRun  bool
	reached          chan struct{}
	release          chan struct{}
	once             sync.Once
	uploads          int
}

func (r *installStageBarrierRemote) wait(ctx context.Context) error {
	r.once.Do(func() { close(r.reached) })
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *installStageBarrierRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	r.uploads++
	if r.blockFirstUpload && r.uploads == 1 {
		if err := r.wait(ctx); err != nil {
			return err
		}
	}
	return r.fakeRemote.UploadFile(ctx, server, localPath, remotePath, mode)
}

func (r *installStageBarrierRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if r.blockInstallRun && strings.Contains(command, "install-aifar.sh") {
		if err := r.wait(ctx); err != nil {
			return adapter.CommandResult{}, err
		}
	}
	return r.fakeRemote.Run(ctx, server, command)
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return adapter.CommandResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	if strings.Contains(command, "AIFAR_SCALE_SERVICE") {
		f.scaleServiceScript = command
		f.scaleServiceRuns++
		if f.scaleServiceErr != nil {
			return adapter.CommandResult{}, f.scaleServiceErr
		}
	}
	if strings.Contains(command, "AIFAR_SCALE_FINALIZE") {
		f.scaleFinalizeScript = command
	}
	if f.failCommandContains != "" && strings.Contains(command, f.failCommandContains) {
		return adapter.CommandResult{}, errors.New("remote install failed")
	}
	if strings.Contains(command, "AIFAR_SERVICE_INSTALL") {
		f.serviceInstallScript = command
		revision := scriptAssignment(command, "REVISION")
		for _, serviceName := range strings.Fields(scriptAssignment(command, "NEW_SERVICES")) {
			if f.servicePrepareCounts == nil {
				f.servicePrepareCounts = map[string]int{}
			}
			if f.servicePrepareRevisions == nil {
				f.servicePrepareRevisions = map[string][]string{}
			}
			f.servicePrepareCounts[serviceName]++
			f.servicePrepareRevisions[serviceName] = append(f.servicePrepareRevisions[serviceName], revision)
			if f.failPreparedServices[serviceName] {
				return adapter.CommandResult{}, errors.New("existing accepted service must not be prepared")
			}
		}
	}
	if strings.Contains(command, "install-aifar.sh") {
		f.installRuns++
		stdout := f.installStdout
		if stdout == "" {
			stdout = fakeBootstrapAcceptanceOutput(f.installScript, f.bootstrapHashOverrides, f.bootstrapNameOverrides)
		}
		return adapter.CommandResult{Stdout: stdout}, nil
	}
	if strings.Contains(command, "aifar-agent apply-deployment --manifest") {
		for remotePath, manifest := range f.deploymentManifests {
			if !strings.Contains(command, remotePath) {
				continue
			}
			serviceName := manifest.Metadata.Name
			if f.deploymentApplyCounts == nil {
				f.deploymentApplyCounts = map[string]int{}
			}
			f.deploymentApplyCounts[serviceName]++
			if f.deploymentApplyFailures[serviceName] > 0 {
				f.deploymentApplyFailures[serviceName]--
				return adapter.CommandResult{}, errors.New("agent state persistence failed")
			}
			hash, _ := runtimeagent.DeploymentManifestSpecHash(manifest)
			if f.deploymentStates == nil {
				f.deploymentStates = map[string]runtimeagent.DeploymentState{}
			}
			f.deploymentStates[serviceName] = runtimeagent.DeploymentState{
				InstanceID: manifest.Metadata.InstanceID, ServiceName: serviceName,
				Generation: manifest.Metadata.Generation, SpecHash: hash,
				DesiredReplicas: manifest.Spec.Replicas,
			}
			data, _ := json.Marshal(runtimeagent.DeploymentAcceptance{Accepted: true, Generation: manifest.Metadata.Generation, SpecHash: hash})
			return adapter.CommandResult{Stdout: string(data)}, nil
		}
	}
	if strings.Contains(command, "aifar-agent get-deployment") {
		for serviceName, state := range f.deploymentStates {
			if strings.Contains(command, serviceName) {
				data, _ := json.Marshal(state)
				return adapter.CommandResult{Stdout: string(data)}, nil
			}
		}
		return adapter.CommandResult{}, errors.New("deployment not found")
	}
	if strings.Contains(command, "AIFAR_AGENT_CHECK") && f.runtimeAgentCheckStdout != "" {
		return adapter.CommandResult{Stdout: f.runtimeAgentCheckStdout}, nil
	}
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
	if strings.Contains(command, "AIFAR_RUNTIME_CONFIG") {
		f.runtimeConfigScript = command
	}
	if strings.Contains(command, "AIFAR_RUNTIME_RECONCILE") {
		f.runtimeReconcileScript = command
	}
	if strings.Contains(command, "AIFAR_RUNTIME_RESTART") {
		f.runtimeRestartScript = command
	}
	if strings.Contains(command, "AIFAR_RUNTIME_POD_SCAN") {
		return adapter.CommandResult{Stdout: f.runtimePodScanStdout}, nil
	}
	if strings.Contains(command, "AIFAR_AGENT_UNINSTALL") {
		f.runtimeAgentUninstall = command
	}
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func fakeBootstrapAcceptanceOutput(script string, hashOverrides, nameOverrides map[string]string) string {
	services := strings.Fields(scriptAssignment(script, "SERVICE_ORDER"))
	hashes := map[string]string{}
	for _, pair := range strings.Fields(scriptAssignment(script, "SERVICE_SPEC_HASHES")) {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			hashes[parts[0]] = parts[1]
		}
	}
	deployments := make([]bootstrapDeploymentProof, 0, len(services))
	instanceID := scriptAssignment(script, "INSTANCE_ID")
	for _, serviceName := range services {
		proofName := serviceName
		if nameOverrides[serviceName] != "" {
			proofName = nameOverrides[serviceName]
		}
		hash := hashes[serviceName]
		if hashOverrides[serviceName] != "" {
			hash = hashOverrides[serviceName]
		}
		deployments = append(deployments, bootstrapDeploymentProof{
			Accepted: true, InstanceID: instanceID, ServiceName: proofName, Generation: 1, SpecHash: hash,
		})
	}
	data, _ := json.Marshal(bootstrapAcceptanceProof{Accepted: true, InstanceID: instanceID, Deployments: deployments})
	return bootstrapAcceptanceMarker + string(data)
}

func scriptAssignment(script, key string) string {
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, key+"=")), "'")
		}
	}
	return ""
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, filepath.Base(localPath)+"->"+remotePath)
	if strings.Contains(remotePath, "/mutations/") && strings.HasSuffix(remotePath, ".json") {
		data, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		var manifest runtimeagent.DeploymentManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return err
		}
		if f.deploymentManifests == nil {
			f.deploymentManifests = map[string]runtimeagent.DeploymentManifest{}
		}
		f.deploymentManifests[remotePath] = manifest
	}
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
	if strings.Contains(remotePath, "/update-aifar-artifact-bundle-") && strings.HasSuffix(remotePath, ".sh") {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		f.bundleScript += string(content) + "\n"
	}
	if strings.Contains(remotePath, "/rollback-") && strings.HasSuffix(remotePath, ".sh") {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		f.rollbackScript = string(content)
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

type messageLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *messageLogger) Info(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, fmt.Sprintf(format, args...))
}

func (l *messageLogger) Error(format string, args ...any) {
	l.Info(format, args...)
}

func (l *messageLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.messages, "\n")
}

type rejectingCommitLogger struct{ fakeLogger }

func (rejectingCommitLogger) TryEnterCommit() bool { return false }

type recordingStepLogger struct {
	mu           sync.Mutex
	steps        []string
	targetStatus string
}

type targetStateRecorder struct {
	mu       sync.Mutex
	statuses map[string]string
	errors   map[string]string
	steps    map[string]string
}

func (*targetStateRecorder) Info(format string, args ...any)  {}
func (*targetStateRecorder) Error(format string, args ...any) {}
func (*targetStateRecorder) StartTarget(target string)        {}
func (l *targetStateRecorder) FinishTarget(target, status, errText string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.statuses == nil {
		l.statuses = map[string]string{}
	}
	l.statuses[target] = status
	if l.errors == nil {
		l.errors = map[string]string{}
	}
	l.errors[target] = errText
}
func (*targetStateRecorder) StartStep(target, name, title string, order int) {}
func (l *targetStateRecorder) FinishStep(target, name, status, errText string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.steps == nil {
		l.steps = map[string]string{}
	}
	l.steps[target+":"+name] = status
}
func (l *targetStateRecorder) snapshot() (map[string]string, map[string]string, map[string]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.statuses), maps.Clone(l.errors), maps.Clone(l.steps)
}

func (*recordingStepLogger) Info(format string, args ...any)  {}
func (*recordingStepLogger) Error(format string, args ...any) {}
func (*recordingStepLogger) StartTarget(target string)        {}
func (l *recordingStepLogger) FinishTarget(target, status, errText string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.targetStatus = status
}
func (l *recordingStepLogger) StartStep(target, name, title string, order int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = append(l.steps, name+"=running")
}
func (l *recordingStepLogger) FinishStep(target, name, status, errText string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = append(l.steps, name+"="+status)
}
func (l *recordingStepLogger) snapshot() ([]string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.steps...), l.targetStatus
}

func withFakeRuntimeAgentBinary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	agent := filepath.Join(root, "aifar-agent-linux-amd64")
	if err := os.WriteFile(agent, []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIFAR_AGENT_BINARY", agent)
	return agent
}

func runtimeAgentCheckOutput(t *testing.T, sha256Text string, features ...string) string {
	t.Helper()
	status, err := json.Marshal(map[string]any{
		"status":   "running",
		"version":  "runtime-v2",
		"features": features,
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("AIFAR_AGENT_CHECK\nagentFound=true\nagentPath=/usr/local/bin/aifar-agent\nstatus=%s\nsha256=%s\n", status, sha256Text)
}

func TestEnsureRuntimeAgentSkipsRestartWhenCurrent(t *testing.T) {
	agent := withFakeRuntimeAgentBinary(t)
	sum, _, err := fileSHA256(agent)
	if err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{runtimeAgentCheckStdout: runtimeAgentCheckOutput(t, sum, requiredRuntimeAgentFeatures...)}
	service := NewService(nil, remote)
	if err := service.ensureRuntimeAgent(context.Background(), store.Server{ID: "srv-1", DeployDir: "/aifar/apps"}, "/aifar/apps/_work/aifar-agent-runtime-v2-test", "", fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if uploads := remote.joinedUploads(); uploads != "" {
		t.Fatalf("current agent should not be uploaded again, uploads=%s", uploads)
	}
	commands := remote.joinedCommands()
	if strings.Contains(commands, "AIFAR_AGENT_UPGRADE") || strings.Contains(commands, "systemctl restart aifar-agent") {
		t.Fatalf("current agent should not be restarted, commands:\n%s", commands)
	}
}

func TestEnsureRuntimeAgentUpgradesWhenChecksumChanges(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	remote := &fakeRemote{runtimeAgentCheckStdout: runtimeAgentCheckOutput(t, strings.Repeat("0", 64), requiredRuntimeAgentFeatures...)}
	service := NewService(nil, remote)
	if err := service.ensureRuntimeAgent(context.Background(), store.Server{ID: "srv-1", DeployDir: "/aifar/apps"}, "/aifar/apps/_work/aifar-agent-runtime-v2-test", "", fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if uploads := remote.joinedUploads(); !strings.Contains(uploads, "aifar-agent-linux-amd64->/aifar/apps/_work/aifar-agent-runtime-v2-test/") {
		t.Fatalf("changed agent should be uploaded, uploads=%s", uploads)
	}
	commands := remote.joinedCommands()
	if !strings.Contains(commands, "AIFAR_AGENT_UPGRADE") || !strings.Contains(commands, "systemctl restart aifar-agent") {
		t.Fatalf("changed agent should be installed and restarted, commands:\n%s", commands)
	}
}

func TestEnsureRuntimeAgentUpgradesWhenFeatureMissing(t *testing.T) {
	agent := withFakeRuntimeAgentBinary(t)
	sum, _, err := fileSHA256(agent)
	if err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{runtimeAgentCheckStdout: runtimeAgentCheckOutput(t, sum, "reconcile-runtime", "endpoint-cache")}
	service := NewService(nil, remote)
	if err := service.ensureRuntimeAgent(context.Background(), store.Server{ID: "srv-1", DeployDir: "/aifar/apps"}, "/aifar/apps/_work/aifar-agent-runtime-v2-test", "", fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if uploads := remote.joinedUploads(); !strings.Contains(uploads, "aifar-agent-linux-amd64->/aifar/apps/_work/aifar-agent-runtime-v2-test/") {
		t.Fatalf("feature-incomplete agent should be uploaded, uploads=%s", uploads)
	}
	if commands := remote.joinedCommands(); !strings.Contains(commands, "systemctl restart aifar-agent") {
		t.Fatalf("feature-incomplete agent should be restarted, commands:\n%s", commands)
	}
}

func TestEnsureRuntimeAgentUpgradesWhenInteractiveDeltaFeaturesMissing(t *testing.T) {
	agent := withFakeRuntimeAgentBinary(t)
	sum, _, err := fileSHA256(agent)
	if err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{runtimeAgentCheckStdout: runtimeAgentCheckOutput(t, sum,
		"reconcile-runtime", "local-runtime-controller", "endpoint-cache", "restart-runtime",
	)}
	service := NewService(nil, remote)
	if err := service.ensureRuntimeAgent(context.Background(), store.Server{ID: "srv-1", DeployDir: "/aifar/apps"}, "/aifar/apps/_work/aifar-agent-runtime-v2-test", "", fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if uploads := remote.joinedUploads(); !strings.Contains(uploads, "aifar-agent-linux-amd64->/aifar/apps/_work/aifar-agent-runtime-v2-test/") {
		t.Fatalf("agent without interactive delta features should be upgraded, uploads=%s", uploads)
	}
}

type bundleTestArtifact struct {
	Service  string
	Module   string
	FileName string
	Content  string
}

func installedAIFARInstance(t *testing.T) store.AppInstance {
	t.Helper()
	releaseID := "20260701T010203.000000000Z-runtime-v2"
	metadata := map[string]any{
		"installRoot":           "/aifar/apps/admin",
		"runtimeDir":            "/aifar/apps/admin/runtime",
		"orchestrationModel":    orchestrationModelK8sLikeV1,
		"releaseId":             releaseID,
		"currentRevision":       releaseID,
		"releaseVersion":        "runtime-v2",
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
		Version:  "runtime-v2",
		ServerID: "srv-1",
		Status:   "installed",
		Topology: defaultTopology,
		Metadata: mustMetadata(t, metadata),
	}
}

func seedPerServiceDeployments(t *testing.T, instance store.AppInstance, desired map[string]int, generations map[string]int64) []store.AIFARDeployment {
	t.Helper()
	services := make([]string, 0, len(desired))
	for serviceName := range desired {
		services = append(services, serviceName)
	}
	slices.Sort(services)
	deployments := make([]store.AIFARDeployment, 0, len(services))
	for _, serviceName := range services {
		generation := generations[serviceName]
		if generation < 1 {
			generation = 1
		}
		row := store.AIFARDeployment{
			ID:                 "deployment-" + serviceName,
			InstanceID:         instance.ID,
			ServiceName:        serviceName,
			DesiredReplicas:    desired[serviceName],
			CurrentRevision:    "release-" + serviceName,
			Generation:         generation,
			ObservedGeneration: generation,
			Status:             "available",
			CreatedAt:          time.Now().UTC(),
		}
		manifest, err := buildRuntimeManifest(instance, row, generation)
		if err != nil {
			t.Fatalf("build %s manifest: %v", serviceName, err)
		}
		manifest.Spec.Replicas = desired[serviceName]
		manifest.Spec.RestartGeneration = 5
		raw, err := json.Marshal(runtimeagent.NormalizeDeploymentManifest(manifest))
		if err != nil {
			t.Fatalf("marshal %s manifest: %v", serviceName, err)
		}
		row.SpecJSON = string(raw)
		deployments = append(deployments, row)
	}
	return deployments
}

func deploymentsByService(items []store.AIFARDeployment) map[string]store.AIFARDeployment {
	out := make(map[string]store.AIFARDeployment, len(items))
	for _, item := range items {
		out[item.ServiceName] = item
	}
	return out
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
		InstallRoot:     "/aifar/apps/admin",
		ServiceName:     "permission",
		ReleaseID:       "rel-1",
		ReplicaID:       2,
		ContainerName:   "aifar-permission-rel-1-r2",
		IngressNetwork:  "aifar-network",
		MaxReplicas:     3,
		DesiredReplicas: "gateway=1 file=0 permission=2",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`--format '{{.Names}}'`,
		`"version": "runtime-v2"`,
		`"mode": "web-nginx"`,
		`CONTROL_PLANE_DESIRED_REPLICAS='gateway=1 file=0 permission=2'`,
		`desired_replicas_from_pairs`,
		`nacos_ephemeral`,
		`"ephemeral": $(nacos_ephemeral)`,
		`replicas_for_service`,
		`service_pod_count "$service"`,
		`if [ "$value" = "0" ]; then`,
		`write_runtime_spec`,
		`aifar-agent reconcile-runtime --spec "$spec"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("autoscale script missing %q:\n%s", want, script)
		}
	}
	for _, legacy := range []string{`docker run -d`, `--name "$CONTAINER_NAME"`, `ephemeral=false`} {
		if strings.Contains(script, legacy) {
			t.Fatalf("autoscale script should not contain legacy direct runtime action %q:\n%s", legacy, script)
		}
	}
}

func TestScaleOutCreatesReplicaAndUpdatesEndpointMetadata(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	desiredBefore := desiredReplicasFromMetadata(metadata)
	desiredBefore["file"] = 0
	metadata["desiredReplicas"] = desiredBefore
	instance.Metadata = mustMetadata(t, metadata)
	s := &fakeStore{
		servers: map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{
			instance,
		},
	}
	remote := &fakeRemote{autoscaleStatusStdouts: []string{
		"endpoint=permission|aifar-permission-rel|rel|1|38010|true|healthy|86|2147483648\nhostMemoryAvailableBytes=8589934592\n",
		"endpoint=permission|aifar-permission-rel|rel|1|38010|true|healthy|50|2147483648\nendpoint=permission|aifar-permission-rel-r2|rel|2|38010|true|healthy|5|2147483648\nendpoint=file|aifar-file-stale|rel|1|38005|true|healthy|5|2147483648\nhostMemoryAvailableBytes=6442450944\n",
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
	if !strings.Contains(remote.autoscaleScript, "AIFAR_AUTOSCALE_OUT") || !strings.Contains(remote.autoscaleScript, "aifar-pod-admin-permission-20260701t010203.000000000z-runtime-v2-r2") {
		t.Fatalf("expected autoscale remote script to run with replica container, got:\n%s", remote.autoscaleScript)
	}
	if !strings.Contains(remote.autoscaleScript, "CONTROL_PLANE_DESIRED_REPLICAS=") || !strings.Contains(remote.autoscaleScript, "file=0") || !strings.Contains(remote.autoscaleScript, "permission=2") {
		t.Fatalf("expected autoscale script to carry control-plane desired replicas, got:\n%s", remote.autoscaleScript)
	}
	uploads := remote.joinedUploads()
	if !strings.Contains(uploads, "aifar-agent-linux-amd64->/aifar/apps/_work/aifar-agent-runtime-v2-") {
		t.Fatalf("expected scale-out to upload current runtime agent, uploads=%s", uploads)
	}
	commands := remote.joinedCommands()
	upgradeIndex := strings.Index(commands, "AIFAR_AGENT_UPGRADE")
	scaleIndex := strings.Index(commands, "AIFAR_AUTOSCALE_OUT")
	if upgradeIndex < 0 || scaleIndex < 0 || upgradeIndex > scaleIndex {
		t.Fatalf("expected scale-out to upgrade agent before autoscale script, commands:\n%s", commands)
	}
	saved, err := s.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	savedMetadata := metadataFromInstance(saved)
	desired := desiredReplicasFromMetadata(savedMetadata)
	if desired["permission"] != 2 {
		t.Fatalf("expected permission desired replicas 2, got %v metadata=%s", desired["permission"], saved.Metadata)
	}
	if desired["file"] != 0 {
		t.Fatalf("expected offline file desired replicas to stay 0, got %v metadata=%s", desired["file"], saved.Metadata)
	}
	endpoints, ok := savedMetadata["activeEndpoints"].(map[string]any)
	if !ok {
		t.Fatalf("expected activeEndpoints metadata, got %s", saved.Metadata)
	}
	if endpointCount(endpoints["permission"]) != 2 {
		t.Fatalf("expected two permission endpoints, got %s", saved.Metadata)
	}
	if endpointCount(endpoints["file"]) != 0 {
		t.Fatalf("expected offline file endpoints to be ignored, got %s", saved.Metadata)
	}
	if _, locked := savedMetadata["orchestrationLock"]; locked {
		t.Fatalf("expected orchestration lock to be released, got %s", saved.Metadata)
	}
}

func TestScalePermissionDoesNotRewriteFileDeployment(t *testing.T) {
	agent := withFakeRuntimeAgentBinary(t)
	sum, _, err := fileSHA256(agent)
	if err != nil {
		t.Fatal(err)
	}
	instance := installedAIFARInstance(t)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{
		runtimeAgentCheckStdout: runtimeAgentCheckOutput(t, sum, requiredRuntimeAgentFeatures...),
		autoscaleStatusFallback: "endpoint=permission|permission-pod-1|release-permission|1|38010|true|healthy|10|64\nendpoint=permission|permission-pod-2|release-permission|2|38010|true|healthy|10|64\n",
	}

	err = NewService(s, remote).ScaleService(context.Background(), ScaleRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-scale-permission",
		ServiceName: "permission", Replicas: 2,
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := deploymentsByService(s.deployments)
	if got := after["permission"]; got.Generation != 4 || got.DesiredReplicas != 2 || got.Status != "Accepted" {
		t.Fatalf("permission mutation=%+v, want generation 4 replicas 2 accepted", got)
	}
	if got, want := after["file"], before["file"]; got.Generation != want.Generation || got.SpecJSON != want.SpecJSON || got.DesiredReplicas != want.DesiredReplicas || got.Status != want.Status {
		t.Fatalf("file deployment changed: before=%+v after=%+v", want, got)
	}
	commands := remote.joinedCommands()
	if strings.Contains(commands, "reconcile-runtime") || strings.Contains(commands, "runtime-spec.json") {
		t.Fatalf("scale must not submit an aggregate runtime spec:\n%s", commands)
	}
}

func TestConcurrentDifferentServiceScalesPreserveBothDesiredContributions(t *testing.T) {
	agent := withFakeRuntimeAgentBinary(t)
	sum, _, err := fileSHA256(agent)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance.Status = "install_failed"
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	barrier := &barrierCASStore{Store: db, remaining: 2, ready: make(chan struct{}), release: make(chan struct{})}
	server := store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}
	remote := &fakeRemote{
		runtimeAgentCheckStdout: runtimeAgentCheckOutput(t, sum, requiredRuntimeAgentFeatures...),
		autoscaleStatusFallback: "endpoint=permission|permission-pod-1|release-permission|1|38010|true|healthy|10|64\nendpoint=file|file-pod-1|release-file|1|38005|true|healthy|10|64\n",
	}
	service := NewService(barrier, remote)
	errs := make(chan error, 2)
	for _, request := range []ScaleRequest{
		{Instance: instance, Server: server, Actor: "operator-permission", TaskID: "task-scale-permission", ServiceName: "permission", Replicas: 2},
		{Instance: instance, Server: server, Actor: "operator-file", TaskID: "task-scale-file", ServiceName: "file", Replicas: 3},
	} {
		request := request
		go func() { errs <- service.ScaleService(context.Background(), request, fakeLogger{}, nil) }()
	}
	<-barrier.ready
	close(barrier.release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "install_failed" {
		t.Fatalf("runtime scale changed lifecycle status to %q", saved.Status)
	}
	desired := desiredReplicasFromMetadata(metadataFromInstance(saved))
	if desired["permission"] != 2 || desired["file"] != 3 {
		t.Fatalf("concurrent desired replicas lost a service contribution: %v", desired)
	}
	if calls := barrier.callCount(); calls != 3 {
		t.Fatalf("CAS calls=%d, want two initial attempts plus one conflict retry", calls)
	}
}

func TestOlderSameServiceScaleCannotProjectOverAcceptedSuccessor(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance.Status = "install_failed"
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1}, map[string]int64{"permission": 3},
	) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	blocking := &staleProjectionCASStore{Store: db, blocked: make(chan struct{}), release: make(chan struct{})}
	server := store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}
	service := NewService(blocking, &fakeRemote{})
	oldDone := make(chan error, 1)
	go func() {
		oldDone <- service.ScaleService(context.Background(), ScaleRequest{
			Instance: instance, Server: server, Actor: "old-owner", TaskID: "task-scale-old",
			ServiceName: "permission", Replicas: 2,
		}, fakeLogger{}, nil)
	}()
	select {
	case <-blocking.blocked:
	case err := <-oldDone:
		t.Fatalf("old operation ended before projection barrier: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("old operation did not reach projection barrier")
	}
	if _, err := db.RecoverAIFAROrchestrationLocks(instance.ID, "test successor takeover"); err != nil {
		t.Fatal(err)
	}
	if err := service.ScaleService(context.Background(), ScaleRequest{
		Instance: instance, Server: server, Actor: "new-owner", TaskID: "task-scale-new",
		ServiceName: "permission", Replicas: 3,
	}, fakeLogger{}, nil); err != nil {
		t.Fatal(err)
	}
	close(blocking.release)
	oldErr := <-oldDone
	var controlErr *deploymentControlError
	if !errors.As(oldErr, &controlErr) || controlErr.StableCode() != runtimeControlPlaneRepairCode {
		t.Fatalf("old operation error=%v, want forward-only repair-required", oldErr)
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "install_failed" || desiredReplicasFromMetadata(metadataFromInstance(saved))["permission"] != 3 {
		t.Fatalf("old projection regressed successor or lifecycle: %+v metadata=%s", saved, saved.Metadata)
	}
	deployments, err := db.ListAIFARDeployments(instance.ID)
	if err != nil || len(deployments) != 1 || deployments[0].DesiredReplicas != 3 || deployments[0].Generation != 5 {
		t.Fatalf("canonical successor desired state=%+v err=%v", deployments, err)
	}
}

func TestScaleValidAgentAcceptanceThenLockLossRequiresRepairWithoutProjection(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance.Status = "install_failed"
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1}, map[string]int64{"permission": 3},
	) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	wrapper := &postAcceptanceRenewalFailureStore{Store: db}
	service := NewService(wrapper, &fakeRemote{})
	service.orchestrationLockHeartbeatInterval = time.Hour
	recorder := &targetStateRecorder{}
	err = service.ScaleService(context.Background(), ScaleRequest{
		Instance: instance, Server: store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		Language: "en", Actor: "old-owner", TaskID: "task-scale-lost-after-acceptance", ServiceName: "permission", Replicas: 2,
	}, recorder, nil)
	var controlErr *deploymentControlError
	if !errors.As(err, &controlErr) || controlErr.StableCode() != runtimeControlPlaneRepairCode || controlErr.ReasonCode() != "AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST" {
		t.Fatalf("error=%v, want lock-loss repair-required", err)
	}
	deployments, listErr := db.ListAIFARDeployments(instance.ID)
	if listErr != nil || len(deployments) != 1 || deployments[0].Generation != 4 || deployments[0].DesiredReplicas != 2 || deployments[0].Status != "Accepted" {
		t.Fatalf("forward-only canonical acceptance=%+v err=%v", deployments, listErr)
	}
	saved, getErr := db.GetAppInstance(instance.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if saved.Status != "install_failed" || desiredReplicasFromMetadata(metadataFromInstance(saved))["permission"] != 1 {
		t.Fatalf("lost owner projected metadata or changed lifecycle: %+v metadata=%s", saved, saved.Metadata)
	}
	renewals, casCalls := wrapper.counts()
	if renewals != 1 || casCalls != 0 {
		t.Fatalf("renewals=%d CAS calls=%d, want one post-accept fence and no projection", renewals, casCalls)
	}
	statuses, _, _ := recorder.snapshot()
	if statuses[instance.ID+":permission"] != "failed" {
		t.Fatalf("lost owner target status=%q, want failed", statuses[instance.ID+":permission"])
	}
	locks, lockErr := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if lockErr != nil || len(locks) != 0 {
		t.Fatalf("lost owner lock was not released: locks=%+v err=%v", locks, lockErr)
	}
	if err := NewService(db, &fakeRemote{}).ScaleService(context.Background(), ScaleRequest{
		Instance: instance, Server: store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		Language: "en", Actor: "successor", TaskID: "task-scale-successor", ServiceName: "permission", Replicas: 3,
	}, fakeLogger{}, nil); err != nil {
		t.Fatalf("successor could not take over after stale owner failed: %v", err)
	}
	successor, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if desiredReplicasFromMetadata(metadataFromInstance(successor))["permission"] != 3 {
		t.Fatalf("successor projection missing after stale owner failure: %s", successor.Metadata)
	}
}

func TestParseAutoscaleStatusIncludesAgentDesiredReplicas(t *testing.T) {
	status := parseAutoscaleStatus("agentStatus={\"instances\":[{\"instanceId\":\"admin\",\"deploymentStatus\":[{\"serviceName\":\"permission\",\"desiredReplicas\":0,\"currentReplicas\":0,\"readyReplicas\":0,\"status\":\"offline\"}]}]}\n")
	got, ok := status.Deployments["permission"]
	if !ok || got.DesiredReplicas != 0 || got.CurrentReplicas != 0 || got.ReadyReplicas != 0 || got.Status != "offline" {
		t.Fatalf("unexpected parsed agent deployment status: %+v", status.Deployments)
	}
}

func TestServiceOrchestrationLocksAllowDifferentServices(t *testing.T) {
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationLocks"] = map[string]any{
		"file": map[string]any{
			"operation": "scale-service",
			"service":   "file",
			"actor":     "operator",
			"startedAt": time.Now().UTC().Format(time.RFC3339),
		},
	}
	instance.Metadata = mustMetadata(t, metadata)
	s := &fakeStore{instances: []store.AppInstance{instance}}
	service := NewService(s, &fakeRemote{})

	if _, _, err := service.acquireOrchestrationLock(instance.ID, "scale-service", "permission", "operator", ""); err != nil {
		t.Fatalf("expected file mutation not to block permission mutation, got %v", err)
	}
	saved, err := s.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	locks := serviceOrchestrationLocksFromMetadata(metadataFromInstance(saved))
	if _, ok := locks["file"]; !ok {
		t.Fatalf("expected existing file lock to be preserved, got %s", saved.Metadata)
	}
	if _, ok := locks["permission"]; !ok {
		t.Fatalf("expected permission lock to be recorded, got %s", saved.Metadata)
	}
	if _, ok := locks["file"]; !ok {
		t.Fatalf("expected unrelated file lock to remain, got %s", saved.Metadata)
	}
}

func TestAutoscalerAllowsDifferentServiceMutation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(store.AppInstance{App: AppName, Version: "runtime-v2", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID:  instance.ID,
		ServiceName: "file",
		Operation:   "scale-service",
		TaskID:      "task-file",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	autoscaler := &Autoscaler{store: db}
	if autoscaler.orchestrationLocked(instance.ID, "permission", time.Now().UTC()) {
		t.Fatal("file mutation must not block autoscaling another service")
	}
}

func TestAutoscalerTickStartsUnrelatedServiceDespiteServiceLock(t *testing.T) {
	db := openAIFARTestStore(t)
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	now := time.Now().UTC()
	metadata["autoscalePolicy"] = AutoscalePolicy{Enabled: true, MemoryThreshold: 80, Sustain: time.Minute, Cooldown: time.Minute, MaxReplicas: 3}.metadata()
	metadata["autoscaleSignals"] = map[string]any{"permission": map[string]any{"since": now.Add(-2 * time.Minute).Format(time.RFC3339)}}
	instance.Metadata = mustMetadata(t, metadata)
	if _, err := db.SaveAppInstance(instance); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServer(store.Server{ID: instance.ServerID, Name: "node-1", Host: "127.0.0.1", Username: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "file", Operation: "scale-service", ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{autoscaleStatusFallback: "endpoint=permission|aifar-permission-rel|rel|1|38010|true|healthy|90|2147483648\nhostMemoryAvailableBytes=8589934592\n"}
	autoscaler := NewAutoscaler(db, worker.NewManager(db), remote)
	autoscaler.tick(context.Background(), now)
	tasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Target != instance.ID+":permission" {
		t.Fatalf("unrelated service lock prevented permission autoscale task: %+v", tasks)
	}
}

func TestScaleServicesLockEachAffectedService(t *testing.T) {
	db := openAIFARTestStore(t)
	instance := installedAIFARInstance(t)
	if _, err := db.SaveAppInstance(instance); err != nil {
		t.Fatal(err)
	}
	service := NewService(db, &fakeRemote{})
	if _, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "file", Operation: "scale-service", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.acquireServiceOrchestrationLocks(instance.ID, "scale-service", []string{"file", "gateway"}, "operator", "task-file"); err == nil {
		t.Fatal("batch containing file must conflict with active file operation")
	}
	_, locks, err := service.acquireServiceOrchestrationLocks(instance.ID, "scale-service", []string{"gateway", "permission"}, "operator", "task-other")
	if err != nil {
		t.Fatalf("batch of unrelated services must proceed: %v", err)
	}
	defer service.releaseOrchestrationLocks(locks)
	if len(locks) != 2 || locks[0].ServiceName != "gateway" || locks[1].ServiceName != "permission" {
		t.Fatalf("batch locks=%+v, want deterministic gateway and permission scopes", locks)
	}
}

func TestServiceReleaseOrchestrationLockDoesNotReleaseSuccessor(t *testing.T) {
	db := openAIFARTestStore(t)
	instance := installedAIFARInstance(t)
	if _, err := db.SaveAppInstance(instance); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ownerA, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		ID: "owner-a", InstanceID: instance.ID, ServiceName: "file", Operation: "scale-service",
		StartedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		ID: "owner-b", InstanceID: instance.ID, ServiceName: "file", Operation: "scale-service", ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	NewService(db, &fakeRemote{}).releaseOrchestrationLock(ownerA)
	_, err = db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "file", Operation: "scale-service", ExpiresAt: now.Add(time.Hour),
	})
	var conflict store.AIFAROrchestrationLockConflict
	if !errors.As(err, &conflict) || conflict.Lock.ID != ownerB.ID {
		t.Fatalf("stale owner released successor: err=%v", err)
	}
}

func openAIFARTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestServiceOrchestrationLocksBlockSameServiceAndGlobalOperations(t *testing.T) {
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationLocks"] = map[string]any{
		"file": map[string]any{
			"operation": "scale-service",
			"service":   "file",
			"actor":     "operator",
			"startedAt": time.Now().UTC().Format(time.RFC3339),
		},
	}
	instance.Metadata = mustMetadata(t, metadata)
	s := &fakeStore{instances: []store.AppInstance{instance}}
	service := NewService(s, &fakeRemote{})

	if _, _, err := service.acquireOrchestrationLock(instance.ID, "scale-service", "file", "operator", ""); err == nil || !strings.Contains(err.Error(), "instance orchestration is locked") {
		t.Fatalf("expected instance mutation lock to block the same service, got %v", err)
	}
	if _, _, err := service.acquireOrchestrationLock(instance.ID, "runtime-config", "", "operator", ""); err == nil || !strings.Contains(err.Error(), "instance orchestration is locked") {
		t.Fatalf("expected global operation to wait for service lock, got %v", err)
	}
}

func TestGlobalOrchestrationLockBlocksServiceOperations(t *testing.T) {
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationLock"] = map[string]any{
		"operation": "runtime-config",
		"actor":     "operator",
		"startedAt": time.Now().UTC().Format(time.RFC3339),
	}
	instance.Metadata = mustMetadata(t, metadata)
	s := &fakeStore{instances: []store.AppInstance{instance}}
	service := NewService(s, &fakeRemote{})

	if _, _, err := service.acquireOrchestrationLock(instance.ID, "scale-service", "permission", "operator", ""); err == nil || !strings.Contains(err.Error(), "instance orchestration is locked") {
		t.Fatalf("expected global lock to block service operation, got %v", err)
	}
}

func TestRecoverInterruptedOrchestrationLocksClearsAIFARLocks(t *testing.T) {
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationLock"] = map[string]any{
		"operation": "runtime-config",
		"actor":     "operator",
		"startedAt": time.Now().UTC().Format(time.RFC3339),
	}
	metadata["orchestrationLocks"] = map[string]any{
		"file": map[string]any{
			"operation": "scale-service",
			"service":   "file",
			"actor":     "operator",
			"startedAt": time.Now().UTC().Format(time.RFC3339),
		},
	}
	instance.Metadata = mustMetadata(t, metadata)
	other := store.AppInstance{ID: "mysql-1", App: "mysql", Version: "8.0.36", Status: "running", Metadata: `{"orchestrationLock":{"operation":"check"}}`}
	s := &fakeStore{instances: []store.AppInstance{instance, other}}

	recovered, err := RecoverInterruptedOrchestrationLocks(s)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("expected one recovered AIFAR instance, got %d", recovered)
	}
	saved, err := s.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := metadataFromInstance(saved)
	if _, ok := next["orchestrationLock"]; ok {
		t.Fatalf("expected global lock to be cleared, got %s", saved.Metadata)
	}
	if locks := serviceOrchestrationLocksFromMetadata(next); len(locks) != 0 {
		t.Fatalf("expected service locks to be cleared, got %s", saved.Metadata)
	}
	if _, ok := next["lastOrchestrationRecovery"]; !ok {
		t.Fatalf("expected recovery marker, got %s", saved.Metadata)
	}
	savedOther, err := s.GetAppInstance(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(savedOther.Metadata, "orchestrationLock") {
		t.Fatalf("expected non-AIFAR instance metadata to remain untouched, got %s", savedOther.Metadata)
	}
}

func TestServiceMigratesMetadataOrchestrationLocksToStructuredStore(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	startedAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      AppName,
		Version:  "runtime-v2",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"orchestrationLocks":{"gateway":{"operation":"autoscale","service":"gateway","actor":"admin","taskId":"tsk-gateway","startedAt":"` + startedAt + `"}}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db, &fakeRemote{})
	if _, _, err := service.acquireOrchestrationLock(instance.ID, "delete", "", "admin", "tsk-delete"); err == nil || !strings.Contains(err.Error(), "instance orchestration is locked") {
		locks, listErr := db.ListAIFAROrchestrationLocks(instance.ID, false)
		t.Fatalf("expected migrated gateway lock to block global delete, got %v locks=%+v listErr=%v", err, locks, listErr)
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataFromInstance(saved)
	if _, ok := metadata["orchestrationLocks"]; ok {
		t.Fatalf("expected legacy metadata locks to be removed, got %s", saved.Metadata)
	}
	locks, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0].ServiceName != "gateway" || locks[0].TaskID != "tsk-gateway" {
		t.Fatalf("expected migrated gateway lock with task id, got %+v", locks)
	}
	if _, err := db.RecoverAIFAROrchestrationLocks(instance.ID, "test recovery"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.acquireOrchestrationLock(instance.ID, "delete", "", "admin", "tsk-delete"); err != nil {
		t.Fatalf("expected global delete lock after recovery, got %v", err)
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

func TestRuntimeConfigMergeValidationAndFallback(t *testing.T) {
	now := time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	base := runtimeConfigFromOptions(InstallOptions{
		AppCPUs:                 "2.0",
		AppMemoryLimit:          "2GB",
		JVMInitialRAMPercentage: 20,
		JVMMaxRAMPercentage:     70,
	}, "owner", now)
	next, err := normalizeRuntimeConfigPayload(RuntimeConfigPayload{
		Global: RuntimeConfigValues{
			AppCPUs:                 "3.0",
			AppMemoryLimit:          "3GB",
			JVMInitialRAMPercentage: 25,
			JVMMaxRAMPercentage:     75,
		},
		Services: map[string]RuntimeConfigValues{
			"permission": {AppMemoryLimit: "4GB"},
		},
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	permission := effectiveRuntimeConfigForService(next, "permission")
	if permission.AppCPUs != "3.0" || permission.AppMemoryLimit != "4GB" || permission.JVMMaxRAMPercentage != 75 {
		t.Fatalf("expected permission to inherit global and override memory, got %+v", permission)
	}
	web := effectiveRuntimeConfigForService(next, "web-vue3")
	if web.AppMemoryLimit != "3GB" || web.JVMInitialRAMPercentage != 25 {
		t.Fatalf("expected web-vue3 to use global runtime config, got %+v", web)
	}
	if _, err := normalizeRuntimeConfigPayload(RuntimeConfigPayload{
		Global:   next.Global,
		Services: map[string]RuntimeConfigValues{"unknown": {AppCPUs: "1"}},
	}, next); err == nil {
		t.Fatal("expected unknown service override to be rejected")
	}
}

func TestRuntimeConfigScriptRendersDynamicJavaApply(t *testing.T) {
	previous := runtimeConfigFromMetadata(map[string]any{})
	next := previous
	next.ConfigVersion = 2
	next.Global = RuntimeConfigValues{
		AppCPUs:                 "3.0",
		AppMemoryLimit:          "3GB",
		JVMInitialRAMPercentage: 25,
		JVMMaxRAMPercentage:     75,
	}
	data := runtimeConfigScriptDataFromState("/aifar/apps/admin", previous, next, []string{"permission", "web-vue3"})
	script, err := renderRuntimeConfigScript(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`AIFAR_RUNTIME_CONFIG_VERSION`,
		`resource.%s.env`,
		`java-jvm.options`,
		`java-jvm.$service.options`,
		`java-entrypoint.sh`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("runtime config script missing %q:\n%s", want, script)
		}
	}
	for _, legacy := range []string{`docker update --cpus`, `docker restart`, `docker run -d`, `runtime-spec.json`, `reconcile-runtime`} {
		if strings.Contains(script, legacy) {
			t.Fatalf("runtime config script should not contain legacy direct container mutation %q:\n%s", legacy, script)
		}
	}
}

func TestRuntimeMutationScriptsDoNotOwnAggregateDesiredState(t *testing.T) {
	for _, name := range []string{
		"scale-service.sh",
		"runtime-restart.sh",
		"runtime-reconcile.sh",
		"runtime-config.sh",
		"update-artifact.sh",
		"update-artifact-bundle.sh",
		"rollback-artifact.sh",
	} {
		t.Run(name, func(t *testing.T) {
			content, err := templateFS.ReadFile("templates/" + name)
			if err != nil {
				t.Fatal(err)
			}
			script := string(content)
			for _, forbidden := range []string{
				"runtime-spec.json",
				"reconcile-runtime",
				"restart-runtime",
				"write_runtime_spec",
				"restore_previous_runtime",
				"stop-all",
			} {
				if strings.Contains(script, forbidden) {
					t.Fatalf("runtime mutation script %s contains aggregate desired-state action %q:\n%s", name, forbidden, script)
				}
			}
		})
	}
}

func TestRuntimeSpecTemplatesMountPerServiceLogVolume(t *testing.T) {
	for _, name := range []string{
		"install.sh",
		"service-install.sh",
		"autoscale-out.sh",
	} {
		t.Run(name, func(t *testing.T) {
			content, err := templateFS.ReadFile("templates/" + name)
			if err != nil {
				t.Fatal(err)
			}
			script := string(content)
			logDir := `log_dir="$LOG_DIR/$service"`
			if name == "install.sh" {
				logDir = `log_dir="$INSTALL_ROOT/logs/$service"`
			}
			for _, want := range []string{
				`LOG_DIR="$RUNTIME_DIR/logs"`,
				logDir,
				`mkdir -p "$log_dir"`,
				`"target":"/opt/aifar/logs"`,
				`"target":"/var/log/nginx"`,
				`"target":"/data/aifarsoft/javaApi/aifar-%s/log"`,
				`"AIFAR_LOG_DIR":"/opt/aifar/logs"`,
				`"LOG_DIR":"/opt/aifar/logs"`,
			} {
				if !strings.Contains(script, want) {
					t.Fatalf("runtime spec template %s should include log volume policy %q:\n%s", name, want, script)
				}
			}
		})
	}
}

func TestRolloutTemplatesRefreshDockerfileJarInputs(t *testing.T) {
	for _, name := range []string{
		"update-artifact.sh",
		"update-artifact-bundle.sh",
	} {
		t.Run(name, func(t *testing.T) {
			content, err := templateFS.ReadFile("templates/" + name)
			if err != nil {
				t.Fatal(err)
			}
			script := string(content)
			for _, want := range []string{`docker build -t "$image"`, `target/*.jar`} {
				if !strings.Contains(script, want) {
					t.Fatalf("rollout template %s should refresh Java artifact input %q:\n%s", name, want, script)
				}
			}
		})
	}
}

func TestRuntimeTemplatesUseReadinessForBackendHealthChecks(t *testing.T) {
	for _, name := range []string{
		"install.sh",
		"service-install.sh",
		"autoscale-out.sh",
	} {
		t.Run(name, func(t *testing.T) {
			content, err := templateFS.ReadFile("templates/" + name)
			if err != nil {
				t.Fatal(err)
			}
			script := string(content)
			if !strings.Contains(script, `/actuator/health/readiness`) {
				t.Fatalf("runtime template %s should use backend readiness health checks:\n%s", name, script)
			}
		})
	}
}

func TestRuntimeConfigMutatesOnlyAffectedServiceGeneration(t *testing.T) {
	instance := installedAIFARInstance(t)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	global := defaultRuntimeConfigValues()
	permission := global
	permission.AppCPUs = "2"

	err := NewService(s, &fakeRemote{}).ApplyRuntimeConfig(context.Background(), RuntimeConfigRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-config-permission",
		Config: RuntimeConfigPayload{Global: global, Services: map[string]RuntimeConfigValues{"permission": permission}},
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := deploymentsByService(s.deployments)
	got := after["permission"]
	if got.Generation != before["permission"].Generation+1 || got.Status != "Accepted" {
		t.Fatalf("permission config mutation=%+v, want next generation accepted", got)
	}
	var manifest runtimeagent.DeploymentManifest
	if err := json.Unmarshal([]byte(got.SpecJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Spec.Resources.CPUs != "2" || manifest.Spec.RestartGeneration != 6 {
		t.Fatalf("permission resources/restart not updated: %+v", manifest.Spec)
	}
	if peer, want := after["file"], before["file"]; peer.Generation != want.Generation || peer.SpecJSON != want.SpecJSON {
		t.Fatalf("file deployment changed: before=%+v after=%+v", want, peer)
	}
}

func TestRuntimeConfigLockRenewalFailureCancelsRemoteAndStopsMetadataCAS(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance, map[string]int{"permission": 1}, nil) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	renewals := &renewalFailureStore{Store: db, renewed: make(chan struct{}), armed: make(chan struct{})}
	remote := &heartbeatBlockingRemote{
		fakeRemote:    &fakeRemote{},
		blockContains: "AIFAR_RUNTIME_CONFIG",
		armed:         renewals.armed,
		reached:       make(chan struct{}),
	}
	service := NewService(renewals, remote)
	service.orchestrationLockHeartbeatInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- service.ApplyRuntimeConfig(ctx, RuntimeConfigRequest{
			Instance: instance,
			Server:   store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
			Actor:    "operator",
			TaskID:   "task-config-renewal-loss",
			Config: RuntimeConfigPayload{Global: RuntimeConfigValues{
				AppCPUs: "2", AppMemoryLimit: "2GB", JVMInitialRAMPercentage: 25, JVMMaxRAMPercentage: 75,
			}},
		}, fakeLogger{}, nil)
	}()
	requireHeartbeatRenewalCancellation(t, renewals.renewed, remote.reached, done, cancel)
	if calls := renewals.appInstanceCASCalls(); calls != 1 {
		t.Fatalf("app-instance CAS calls=%d, want only the pre-remote pending write", calls)
	}
	active, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("lost runtime-config lock remained active: %+v", active)
	}
}

func TestServiceAppliesRuntimeConfigAndRecordsVersion(t *testing.T) {
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances:   []store.AppInstance{instance},
		deployments: seedPerServiceDeployments(t, instance, desiredReplicasFromMetadata(metadataFromInstance(instance)), nil),
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.ApplyRuntimeConfig(context.Background(), RuntimeConfigRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Actor:    "operator",
		Config: RuntimeConfigPayload{
			Global: RuntimeConfigValues{
				AppCPUs:                 "3.0",
				AppMemoryLimit:          "3GB",
				JVMInitialRAMPercentage: 25,
				JVMMaxRAMPercentage:     75,
			},
			Services: map[string]RuntimeConfigValues{
				"permission": {AppMemoryLimit: "4GB"},
			},
		},
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.runtimeConfigScript, "AIFAR_RUNTIME_CONFIG") || !strings.Contains(remote.runtimeConfigScript, "resource.%s.env") {
		t.Fatalf("expected runtime config script to be executed, got:\n%s", remote.runtimeConfigScript)
	}
	saved, err := s.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataFromInstance(saved)
	state := runtimeConfigFromMetadata(metadata)
	if state.ConfigVersion != 2 || state.AppliedVersion != 2 || state.LastApplyStatus != runtimeConfigStatusApplied {
		t.Fatalf("expected runtime config version 2 applied, got %+v metadata=%s", state, saved.Metadata)
	}
	permission := effectiveRuntimeConfigForService(state, "permission")
	if permission.AppMemoryLimit != "4GB" || permission.AppCPUs != "3.0" {
		t.Fatalf("expected permission override with global fallback, got %+v", permission)
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

func TestServiceInstallsAIFARServiceFromRuntimeV2Bundle(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
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
	}, aifarModuleValidationResources(root), fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one AIFAR instance, got %+v", s.instances)
	}
	instance := s.instances[0]
	if instance.App != "aifar" || instance.Version != "runtime-v2" || instance.ServerID != "srv-1" || instance.Status != "installed" {
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
	if metadata["runtimeSpecPath"] != "/aifar/apps/admin/runtime/agent/runtime-spec.json" {
		t.Fatalf("expected canonical runtime spec path, got %s", instance.Metadata)
	}
	if metadata["orchestrationModel"] != orchestrationModelK8sLikeV1 || !strings.Contains(instance.Metadata, "agent-proxy") || strings.Contains(instance.Metadata, "aifar-svc-admin-gateway") || strings.Contains(instance.Metadata, "aifar-pod-admin-gateway") {
		t.Fatalf("expected k8s-like agent proxy without fabricated pod metadata, got %s", instance.Metadata)
	}
	if len(s.releases) != 1 || s.releases[0].InstanceID != instance.ID || s.releases[0].Status != "success" {
		t.Fatalf("expected one recorded release, got %+v", s.releases)
	}
	if len(s.deployments) != len(serviceOrder) || len(s.replicaSets) != len(serviceOrder) || len(s.pods) != 0 || len(s.endpoints) != 0 {
		t.Fatalf("expected AIFAR control plane rows for every service, deployments=%d replicaSets=%d pods=%d endpoints=%d", len(s.deployments), len(s.replicaSets), len(s.pods), len(s.endpoints))
	}
	if metadata["nacosEndpoint"] != "10.0.0.50:8848" || metadata["nacosHost"] != "10.0.0.50" || int(metadata["nacosPort"].(float64)) != 8848 {
		t.Fatalf("expected external Nacos endpoint metadata, got %s", instance.Metadata)
	}
	for _, want := range []string{
		`ORCHESTRATION_MODEL="agent-runtime-v2"`,
		`AGENT_BINARY='/aifar/apps/_work/aifar-runtime-v2-`,
		`install -m 0755 "$AGENT_BINARY" /usr/local/bin/aifar-agent`,
		`installing or upgrading AIFAR runtime agent`,
		`ExecStart=/usr/local/bin/aifar-agent serve --addr $AGENT_LISTEN_ADDR`,
		`ExecStopPost=-/usr/local/bin/aifar-agent deregister-nacos --state-dir /var/lib/aifar-agent/instances`,
		`systemctl $agent_start_cmd aifar-agent`,
		`agent_has_runtime_features`,
		`service-manifest-v1`,
		`RUNTIME_DIR="$INSTALL_ROOT/runtime"`,
		`APP_DIR="$RUNTIME_DIR/services"`,
		`IMAGE_DIR="$RUNTIME_DIR/images"`,
		`DEFAULT_ENV="$BUNDLE_DIR/defaults.env"`,
		`[ -f "$TMP_DIR/manifest.json" ] || fail "runtime-v2 manifest.json is missing in bundle"`,
		`[ -d "$TMP_DIR/services" ] || fail "services directory is missing in runtime-v2 bundle"`,
		`[ -f "$TMP_DIR/runtime/defaults.env" ] || fail "runtime/defaults.env is missing in runtime-v2 bundle"`,
		`NACOS_REGISTRATION_MODE="agent-proxy"`,
		`check_agent_dependency`,
		`agent_runtime_status="$(aifar-agent status 2>/dev/null)"`,
		`SPRING_CLOUD_NACOS_DISCOVERY_REGISTER_ENABLED "false"`,
		`aifar-agent bootstrap-runtime --spec "$spec"`,
		`aifar-agent get-deployment --instance "$INSTANCE_ID" --service "$service"`,
		`AIFAR_BOOTSTRAP_ACCEPTANCE`,
		`runtime-spec.json`,
		`"version": "runtime-v2"`,
		`"deployments": [`,
		`"deploymentName": "`,
		`"podRevision": "`,
		`"services": [`,
		`"mode": "web-nginx"`,
		`GATEWAY_SERVICE='gateway'`,
		`"gatewayService": "${GATEWAY_SERVICE}"`,
		`AIFAR_NACOS_EPHEMERAL true`,
		`nacos_ephemeral`,
		`"ephemeral": $(nacos_ephemeral)`,
		`APP_BACKEND_HEALTH_PATH /actuator/health/readiness`,
		`curl -fsS --connect-timeout 3 'http://127.0.0.1:%s%s' >/dev/null || exit 1`,
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("AIFAR install script should include agent-runtime-v2 orchestration with %q:\n%s", want, remote.installScript)
		}
	}
	if strings.LastIndex(remote.installScript, "check_agent_dependency") > strings.LastIndex(remote.installScript, "build_images") {
		t.Fatalf("AIFAR install script should check aifar-agent before building images:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.joinedUploads(), "aifar-agent-linux-amd64->/aifar/apps/_work/aifar-runtime-v2-") {
		t.Fatalf("AIFAR install should upload runtime agent, uploads=%s", remote.joinedUploads())
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
		`register_nacos_proxy`,
		`start_pod "$service"`,
		`strip_web_nginx_runtime_routes`,
		`ephemeral=false`,
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
		"gateway=alpha-gateway",
		"permission=alpha-permission",
		`set_env SPRING_APPLICATION_NAME "$app_name" "$service_env"`,
		`set_env SERVER_PORT "$port_value" "$service_env"`,
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("AIFAR install script should force alpha service names with %q:\n%s", want, remote.installScript)
		}
	}
}

func TestInstallSucceedsAfterManifestAcceptanceWithoutObservedRuntime(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	instanceID := "install-boundary"
	previous := store.AppInstance{
		ID: instanceID, App: AppName, Version: appBundleVersion, ServerID: "srv-1", Status: "install_failed",
		Metadata: `{"installRoot":"/aifar/apps/admin","installState":"install_failed"}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{previous},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version: "latest", ServerID: "srv-1", Language: "en",
		Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway", "permission", "web-vue3"}},
	}, []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.instances[0].Status; got != "installed" {
		t.Fatalf("status=%s, want installed", got)
	}
	if len(s.deployments) != 3 || len(s.replicaSets) != 3 {
		t.Fatalf("deployments=%d replicaSets=%d, want 3 each", len(s.deployments), len(s.replicaSets))
	}
	for _, deployment := range s.deployments {
		if deployment.Generation != 1 || deployment.ObservedGeneration != 0 || deployment.Status != "Accepted" || deployment.SpecJSON == "" {
			t.Fatalf("deployment must be accepted desired state without an observation: %+v", deployment)
		}
	}
	for _, replicaSet := range s.replicaSets {
		if replicaSet.DesiredPods != 1 || replicaSet.ReadyPods != 0 {
			t.Fatalf("replicaSet must start at desired=1 ready=0: %+v", replicaSet)
		}
	}
	if len(s.pods) != 0 || len(s.endpoints) != 0 {
		t.Fatalf("install acceptance must not invent Pods or Endpoints: pods=%d endpoints=%d", len(s.pods), len(s.endpoints))
	}
	if s.directDeploymentSaveCalls != len(s.deployments) {
		t.Fatalf("accepted deployments must not be overwritten by a stale direct save: direct saves=%d deployments=%d", s.directDeploymentSaveCalls, len(s.deployments))
	}
	for _, forbidden := range []string{"reconcile_runtime", "wait_runtime_pods", "wait_runtime_ports", "check_nacos_dependency"} {
		if strings.Contains(remote.installScript, forbidden+"\n") || strings.Contains(remote.installScript, forbidden+" ") {
			t.Fatalf("install script must not execute readiness gate %q", forbidden)
		}
	}
	for _, required := range []string{"aifar-agent bootstrap-runtime --spec", "AIFAR_BOOTSTRAP_ACCEPTANCE", "SERVICE_SPEC_HASHES", "expected_hash"} {
		if !strings.Contains(remote.installScript, required) {
			t.Fatalf("install script must validate manifest acceptance with %q", required)
		}
	}
	if !strings.Contains(remote.installScript, `rm -f "$spec"`) {
		t.Fatal("install script must remove the temporary aggregate bootstrap spec after acceptance")
	}
}

func TestDecodeBootstrapAcceptanceRejectsEmptyOrDuplicateMarkers(t *testing.T) {
	hash := strings.Repeat("a", 64)
	expected := map[string]string{"gateway": hash}
	valid := bootstrapAcceptanceMarker + `{"accepted":true,"instanceId":"instance-1","deployments":[{"accepted":true,"instanceId":"instance-1","serviceName":"gateway","generation":1,"specHash":"` + hash + `"}]}`
	for _, tc := range []struct {
		name   string
		stdout string
	}{
		{name: "empty first", stdout: bootstrapAcceptanceMarker + "\n" + valid},
		{name: "empty second", stdout: valid + "\n" + bootstrapAcceptanceMarker},
		{name: "duplicate valid", stdout: valid + "\n" + valid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeBootstrapAcceptance(tc.stdout, "instance-1", expected); err == nil {
				t.Fatal("multiple bootstrap acceptance markers must be rejected even when one payload is empty")
			}
		})
	}
}

func TestDecodeBootstrapAcceptanceRequiresExactPerServiceProof(t *testing.T) {
	hashA := strings.Repeat("a", 64)
	hashB := strings.Repeat("b", 64)
	expected := map[string]string{"gateway": hashA, "web-vue3": hashB}
	proof := func(instanceA, serviceA, proofHashA, instanceB, serviceB, proofHashB string) string {
		return bootstrapAcceptanceMarker + `{"accepted":true,"instanceId":"instance-1","deployments":[` +
			`{"accepted":true,"instanceId":"` + instanceA + `","serviceName":"` + serviceA + `","generation":1,"specHash":"` + proofHashA + `"},` +
			`{"accepted":true,"instanceId":"` + instanceB + `","serviceName":"` + serviceB + `","generation":1,"specHash":"` + proofHashB + `"}]}`
	}
	valid := proof("instance-1", "gateway", hashA, "instance-1", "web-vue3", hashB)
	if _, err := decodeBootstrapAcceptance(valid, "instance-1", expected); err != nil {
		t.Fatalf("exact service-addressable proof must pass: %v", err)
	}
	for _, tc := range []struct {
		name   string
		stdout string
	}{
		{name: "wrong hash", stdout: proof("instance-1", "gateway", hashB, "instance-1", "web-vue3", hashA)},
		{name: "wrong service", stdout: proof("instance-1", "gateway", hashA, "instance-1", "system", hashB)},
		{name: "duplicate service", stdout: proof("instance-1", "gateway", hashA, "instance-1", "gateway", hashB)},
		{name: "wrong entry instance", stdout: proof("instance-1", "gateway", hashA, "other-instance", "web-vue3", hashB)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeBootstrapAcceptance(tc.stdout, "instance-1", expected); err == nil {
				t.Fatal("non-canonical bootstrap proof must be rejected")
			}
		})
	}
}

func TestInstallRetryReusesPendingBootstrapRevision(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{installStdout: `AIFAR_BOOTSTRAP_ACCEPTANCE={"accepted":true,"instanceId":"wrong-instance","deployments":[{"accepted":true,"generation":1,"specHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`}
	service := NewService(s, remote)
	req := InstallRequest{
		Version: "latest", ServerID: "srv-1", Language: "en",
		Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}},
	}
	resources := []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}
	if err := service.Install(context.Background(), req, resources, fakeLogger{}, nil); err == nil {
		t.Fatal("first install must fail when the bootstrap response cannot be associated with the instance")
	}
	if len(s.deployments) == 0 {
		t.Fatal("first attempt must retain pending generations")
	}
	pendingRevision := s.deployments[0].CurrentRevision
	for _, deployment := range s.deployments {
		if deployment.Generation != 1 || deployment.Status != "pending_acceptance" || deployment.CurrentRevision != pendingRevision {
			t.Fatalf("first attempt must retain one atomic pending revision: %+v", s.deployments)
		}
	}
	failedMetadata := metadataFromInstance(s.instances[0])
	if got := stringFromMetadata(failedMetadata, "releaseId", ""); got != pendingRevision {
		t.Fatalf("failed install releaseId=%q, want pending revision %q", got, pendingRevision)
	}

	remote.installStdout = ""
	if err := service.Install(context.Background(), req, resources, fakeLogger{}, nil); err != nil {
		t.Fatal(err)
	}
	for _, deployment := range s.deployments {
		if deployment.Generation != 1 || deployment.CurrentRevision != pendingRevision || deployment.Status != "Accepted" {
			t.Fatalf("retry must accept the original pending generation and revision: %+v", deployment)
		}
	}
	installedMetadata := metadataFromInstance(s.instances[0])
	if got := stringFromMetadata(installedMetadata, "releaseId", ""); got != pendingRevision {
		t.Fatalf("installed releaseId=%q, want retried revision %q", got, pendingRevision)
	}
}

func TestInstallChangedConfigRetryAfterAgentPersistenceFailsClosed(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	s := &fakeStore{
		servers:                map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		failDeploymentAcceptOn: 1,
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	resources := []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}
	req := InstallRequest{
		Version: "latest", ServerID: "srv-1", Language: "en",
		Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}, "appMemoryLimit": "2GB"},
	}
	if err := service.Install(context.Background(), req, resources, fakeLogger{}, nil); err == nil {
		t.Fatal("first install must fail after Agent persistence when the control-plane acceptance write fails")
	}
	if remote.installRuns != 1 || len(s.deployments) == 0 {
		t.Fatalf("first attempt must reach Agent persistence and retain desired rows: runs=%d deployments=%d", remote.installRuns, len(s.deployments))
	}
	before := append([]store.AIFARDeployment(nil), s.deployments...)
	req.Parameters["appMemoryLimit"] = "3GB"
	err := service.Install(context.Background(), req, resources, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("changed-config retry must fail closed instead of accepting stale Agent generation 1")
	}
	var typed *deploymentControlError
	if !errors.As(err, &typed) || typed.ReasonCode() != "AIFAR_RUNTIME_INSTALL_RETRY_CONFIG_CHANGED" {
		t.Fatalf("changed-config retry error=%v, want stable repair-required reason", err)
	}
	if remote.installRuns != 1 {
		t.Fatalf("changed-config retry must fail before another bootstrap, runs=%d", remote.installRuns)
	}
	if !reflect.DeepEqual(s.deployments, before) {
		t.Fatalf("changed-config retry must not replace generation-1 desired rows\nbefore=%+v\nafter=%+v", before, s.deployments)
	}
}

func TestInstallReleasePersistenceFailureRemainsRetryable(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	resources := []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}
	s := &fakeStore{
		servers:           map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		failReleaseSaveOn: 1,
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	req := InstallRequest{
		Version: "latest", ServerID: "srv-1", Language: "en",
		Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}},
	}
	if err := service.Install(context.Background(), req, resources, fakeLogger{}, nil); err == nil {
		t.Fatal("required release persistence failure must fail before installed")
	}
	if len(s.instances) != 1 || s.instances[0].Status != "install_failed" || len(s.releases) != 0 {
		t.Fatalf("release failure must leave a retryable instance: instances=%+v releases=%+v", s.instances, s.releases)
	}
	failedDeployments := append([]store.AIFARDeployment(nil), s.deployments...)
	if err := service.Install(context.Background(), req, resources, fakeLogger{}, nil); err != nil {
		t.Fatal(err)
	}
	if s.instances[0].Status != "installed" || len(s.releases) != 1 || s.releases[0].Status != "success" {
		t.Fatalf("same-attempt retry must repair release and install: instances=%+v releases=%+v", s.instances, s.releases)
	}
	if remote.installRuns != 2 {
		t.Fatalf("release repair retry must re-prove the same Agent manifests, runs=%d", remote.installRuns)
	}
	for index, deployment := range s.deployments {
		if deployment.Generation != 1 || deployment.CurrentRevision != failedDeployments[index].CurrentRevision || deployment.SpecJSON != failedDeployments[index].SpecJSON {
			t.Fatalf("release repair changed accepted desired state: before=%+v after=%+v", failedDeployments, s.deployments)
		}
	}
}

func TestInstallReleaseRetentionFailureIsBestEffort(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		t.Run(lang, func(t *testing.T) {
			withFakeRuntimeAgentBinary(t)
			root := createAIFARBundle(t)
			s := &fakeStore{
				servers:             map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
				deleteOldReleaseErr: errors.New("retention failed: secret=must-not-leak"),
			}
			logger := &messageLogger{}
			err := NewService(s, &fakeRemote{}).Install(context.Background(), InstallRequest{
				Version: "latest", ServerID: "srv-1", Language: lang,
				Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}},
			}, []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}, logger, nil)
			if err != nil {
				t.Fatalf("retention cleanup must not fail accepted install: %v", err)
			}
			if len(s.instances) != 1 || s.instances[0].Status != "installed" || len(s.releases) != 1 || s.releases[0].Status != "success" {
				t.Fatalf("retention cleanup changed required install outcome: instances=%+v releases=%+v", s.instances, s.releases)
			}
			const messageKey = "aifar.install.releaseRetentionCleanupWarning"
			want := i18n.Text(lang, messageKey)
			if want == "" || want == messageKey {
				t.Fatalf("retention warning translation is missing for %s: %q", lang, want)
			}
			messages := logger.joined()
			if !strings.Contains(messages, want) || strings.Contains(messages, "must-not-leak") || strings.Contains(messages, "secret=") {
				t.Fatalf("localized retention warning must be present and sanitized: want=%q messages=%s", want, messages)
			}
		})
	}
}

func TestInstallRetryFailsClosedWhenFailedServiceInstallLeftExtraDesired(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	bundleParent := createAIFARBundle(t)
	resource := store.Resource{
		App: AppName, Part: "backend", Version: appBundleVersion,
		Path: filepath.Join(bundleParent, appBundleVersion, bundleManifestName),
	}
	db := openAIFARTestStore(t)
	server, err := db.SaveServer(store.Server{ID: "srv-1", Name: "node-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertResource(resource); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{failCommandContains: "install-aifar.sh"}
	service := NewService(db, remote)
	request := InstallRequest{
		Version: "latest", ServerID: server.ID, Language: "en", TaskID: "initial-failure",
		Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}},
	}
	if err := service.Install(context.Background(), request, []store.Resource{resource}, fakeLogger{}, nil); err == nil {
		t.Fatal("initial install must fail after publishing its selected desired set")
	}
	instances, err := db.ListAppInstances()
	if err != nil || len(instances) != 1 || instances[0].Status != "install_failed" {
		t.Fatalf("failed initial instance=%+v err=%v", instances, err)
	}
	failedInstance := instances[0]
	originalDeployments, err := db.ListAIFARDeployments(failedInstance.ID)
	if err != nil || len(originalDeployments) == 0 {
		t.Fatalf("initial selected desired set=%+v err=%v", originalDeployments, err)
	}

	remote.failCommandContains = ""
	if err := service.InstallServices(context.Background(), InstallServicesRequest{
		Instance: failedInstance, Server: server, Language: "en", Actor: "admin", TaskID: "failed-add-service",
		Services: []string{"system"}, Reason: "reproduce interrupted add-service",
	}, fakeLogger{}, nil); err == nil {
		t.Fatal("add-service attempt must fail after leaving its extra desired row")
	}
	withExtra, err := db.ListAIFARDeployments(failedInstance.ID)
	if err != nil || len(withExtra) != len(originalDeployments)+1 {
		t.Fatalf("failed add-service did not leave one extra desired row: before=%+v after=%+v err=%v", originalDeployments, withExtra, err)
	}
	foundExtra := false
	for _, deployment := range withExtra {
		foundExtra = foundExtra || deployment.ServiceName == "system"
	}
	if !foundExtra {
		t.Fatalf("failed add-service extra row is missing: %+v", withExtra)
	}

	runsBeforeRetry := remote.installRuns
	request.TaskID = "same-attempt-retry"
	err = service.Install(context.Background(), request, []store.Resource{resource}, fakeLogger{}, nil)
	var controlErr *deploymentControlError
	if !errors.As(err, &controlErr) || controlErr.ReasonCode() != "AIFAR_RUNTIME_INSTALL_RETRY_SET_CHANGED" {
		t.Fatalf("same-attempt retry error=%v, want repair-required exact-set conflict", err)
	}
	current, getErr := db.GetAppInstance(failedInstance.ID)
	if getErr != nil || current.Status != "install_failed" {
		t.Fatalf("set-conflicted retry finalized installation: current=%+v err=%v", current, getErr)
	}
	if remote.installRuns != runsBeforeRetry {
		t.Fatalf("set-conflicted retry reached Agent bootstrap: before=%d after=%d", runsBeforeRetry, remote.installRuns)
	}
	afterRetry, err := db.ListAIFARDeployments(failedInstance.ID)
	if err != nil || len(afterRetry) != len(withExtra) {
		t.Fatalf("set-conflicted retry deleted or added desired rows: before=%+v after=%+v err=%v", withExtra, afterRetry, err)
	}
}

func takeOverBlockedInstall(t *testing.T, db *store.Store) (store.AppInstance, store.AIFAROrchestrationLock) {
	t.Helper()
	instances, err := db.ListAppInstances()
	if err != nil || len(instances) != 1 {
		t.Fatalf("blocked install instance=%+v err=%v", instances, err)
	}
	instance := instances[0]
	locks, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil || len(locks) != 1 || locks[0].Operation != "install" {
		t.Fatalf("blocked install lock=%+v err=%v", locks, err)
	}
	if released, err := db.ReleaseAIFAROrchestrationLockByID(locks[0].ID); err != nil || !released {
		t.Fatalf("release old install owner=%v err=%v", released, err)
	}
	if renewed, err := db.RenewAIFAROrchestrationLock(locks[0].ID, time.Now().UTC().Add(time.Hour)); err != nil || renewed {
		t.Fatalf("lost install owner renewed=%v err=%v", renewed, err)
	}
	ownerB, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		ID: "install-owner-b", InstanceID: instance.ID, Operation: "install", TaskID: "successor",
		StartedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataFromInstance(instance)
	metadata["installAttemptOwner"] = ownerB.ID
	metadata["installState"] = "installing"
	instance.Status = "installing"
	instance.Metadata = mustMetadata(t, metadata)
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	return instance, ownerB
}

func successorInitialDesired(instance store.AppInstance, revision string, now time.Time) ([]store.AIFARDeployment, []store.AIFARReplicaSet) {
	services := servicesFromMetadata(metadataFromInstance(instance))
	deployments := make([]store.AIFARDeployment, 0, len(services))
	replicaSets := make([]store.AIFARReplicaSet, 0, len(services))
	for _, serviceName := range services {
		spec := fmt.Sprintf(`{"owner":"b","service":%q}`, serviceName)
		deployments = append(deployments, store.AIFARDeployment{
			InstanceID: instance.ID, ServiceName: serviceName, DesiredReplicas: 1,
			CurrentRevision: revision, StrategyJSON: `{"type":"RollingUpdate"}`, SpecJSON: spec,
			Generation: 1, ObservedGeneration: 0, Status: "pending_acceptance", CreatedAt: now,
		})
		replicaSets = append(replicaSets, store.AIFARReplicaSet{
			InstanceID: instance.ID, ServiceName: serviceName, Revision: revision,
			Image: "successor/" + serviceName + ":" + revision, ArtifactHash: "owner-b-hash",
			DesiredPods: 1, ReadyPods: 0, Status: "pending", CreatedAt: now,
		})
	}
	return deployments, replicaSets
}

func TestLostInstallOwnerCannotPublishGenerationOneDesiredAfterSuccessorTakeover(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	db := openAIFARTestStore(t)
	if _, err := db.SaveServer(store.Server{ID: "srv-1", Name: "node-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}); err != nil {
		t.Fatal(err)
	}
	remote := &installStageBarrierRemote{
		fakeRemote: &fakeRemote{}, blockFirstUpload: true,
		reached: make(chan struct{}), release: make(chan struct{}),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewService(db, remote).Install(context.Background(), InstallRequest{
			Version: "latest", ServerID: "srv-1", Language: "en", TaskID: "owner-a",
			Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}},
		}, []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}, fakeLogger{}, nil)
	}()
	<-remote.reached
	instance, ownerB := takeOverBlockedInstall(t, db)
	ownerBRevision := "owner-b-revision"
	deploymentsB, replicaSetsB := successorInitialDesired(instance, ownerBRevision, time.Now().UTC())
	if err := db.SaveAIFARInitialDesiredWithLock(ownerB.ID, deploymentsB, replicaSetsB); err != nil {
		t.Fatal(err)
	}
	close(remote.release)
	if err := <-errCh; err == nil {
		t.Fatal("lost owner must fail instead of publishing generation-1 desired state")
	}
	current, err := db.ListAIFARDeployments(instance.ID)
	if err != nil || len(current) != len(deploymentsB) {
		t.Fatalf("successor desired=%+v err=%v", current, err)
	}
	for _, deployment := range current {
		if deployment.CurrentRevision != ownerBRevision || !strings.Contains(deployment.SpecJSON, `"owner":"b"`) || deployment.Status != "pending_acceptance" {
			t.Fatalf("lost owner overwrote successor desired: %+v", deployment)
		}
	}
}

func TestLostInstallOwnerCannotAcceptSuccessorGenerationOneDesired(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	db := openAIFARTestStore(t)
	if _, err := db.SaveServer(store.Server{ID: "srv-1", Name: "node-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}); err != nil {
		t.Fatal(err)
	}
	remote := &installStageBarrierRemote{
		fakeRemote: &fakeRemote{}, blockInstallRun: true,
		reached: make(chan struct{}), release: make(chan struct{}),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewService(db, remote).Install(context.Background(), InstallRequest{
			Version: "latest", ServerID: "srv-1", Language: "en", TaskID: "owner-a",
			Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}},
		}, []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}, fakeLogger{}, nil)
	}()
	<-remote.reached
	instance, _ := takeOverBlockedInstall(t, db)
	ownerBRevision := "owner-b-revision"
	current, err := db.ListAIFARDeployments(instance.ID)
	if err != nil || len(current) == 0 {
		t.Fatalf("owner A desired=%+v err=%v", current, err)
	}
	for _, deployment := range current {
		deployment.CurrentRevision = ownerBRevision
		deployment.SpecJSON = fmt.Sprintf(`{"owner":"b","service":%q}`, deployment.ServiceName)
		deployment.Status = "pending_acceptance"
		deployment.ObservedGeneration = 0
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	close(remote.release)
	if err := <-errCh; err == nil {
		t.Fatal("lost owner must fail instead of accepting successor generation-1 desired state")
	}
	current, err = db.ListAIFARDeployments(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range current {
		if deployment.CurrentRevision != ownerBRevision || !strings.Contains(deployment.SpecJSON, `"owner":"b"`) || deployment.Status != "pending_acceptance" {
			t.Fatalf("lost owner accepted or overwrote successor desired: %+v", deployment)
		}
	}
}

func TestConcurrentFailedInstallRetriesHaveOnePersistentAttemptOwner(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	resources := []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}
	db := openAIFARTestStore(t)
	if _, err := db.SaveServer(store.Server{ID: "srv-1", Name: "node-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}); err != nil {
		t.Fatal(err)
	}
	baseRequest := InstallRequest{
		Version: "latest", ServerID: "srv-1", Language: "en", TaskID: "initial-failure",
		Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}, "appMemoryLimit": "2GB"},
	}
	remote := &fakeRemote{failCommandContains: "mkdir -p"}
	if err := NewService(db, remote).Install(context.Background(), baseRequest, resources, fakeLogger{}, nil); err == nil {
		t.Fatal("initial attempt must fail before generation-1 desired state is created")
	}
	instances, err := db.ListAppInstances()
	if err != nil || len(instances) != 1 || instances[0].Status != "install_failed" {
		t.Fatalf("initial failed claim=%+v err=%v", instances, err)
	}
	deployments, err := db.ListAIFARDeployments(instances[0].ID)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("precondition requires no desired rows: deployments=%+v err=%v", deployments, err)
	}

	remote.failCommandContains = ""
	gated := &barrierListStore{
		Store: db, remaining: 2, ready: make(chan struct{}), release: make(chan struct{}),
		firstLockTaskID: "same-config-retry", firstLockAcquired: make(chan struct{}),
	}
	requests := []InstallRequest{baseRequest, baseRequest}
	requests[0].TaskID = "same-config-retry"
	requests[1].TaskID = "changed-config-retry"
	requests[1].Parameters = map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}, "appMemoryLimit": "3GB"}
	type retryResult struct {
		taskID string
		err    error
	}
	resultsCh := make(chan retryResult, len(requests))
	for _, req := range requests {
		req := req
		go func() {
			resultsCh <- retryResult{taskID: req.TaskID, err: NewService(gated, remote).Install(context.Background(), req, resources, fakeLogger{}, nil)}
		}()
	}
	<-gated.ready
	close(gated.release)
	results := []retryResult{<-resultsCh, <-resultsCh}
	successes := 0
	for _, result := range results {
		if result.err == nil {
			successes++
		}
		if result.taskID == "same-config-retry" && result.err != nil {
			t.Fatalf("same-config owner must win the ordered claim: %v", result.err)
		}
		if result.taskID == "changed-config-retry" && result.err == nil {
			t.Fatal("changed-config concurrent retry must fail closed")
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent retries must have exactly one owner, results=%v", results)
	}
	if remote.installRuns != 1 {
		t.Fatalf("only the persistent owner may bootstrap, runs=%d", remote.installRuns)
	}
	instances, err = db.ListAppInstances()
	if err != nil || len(instances) != 1 || instances[0].Status != "installed" {
		t.Fatalf("winning attempt must remain authoritative: instances=%+v err=%v", instances, err)
	}
	deployments, err = db.ListAIFARDeployments(instances[0].ID)
	if err != nil || len(deployments) == 0 {
		t.Fatalf("winning attempt must own one accepted generation: deployments=%+v err=%v", deployments, err)
	}
	winningRevision := deployments[0].CurrentRevision
	for _, deployment := range deployments {
		if deployment.Generation != 1 || deployment.Status != "Accepted" || deployment.CurrentRevision != winningRevision {
			t.Fatalf("winning attempt must own one accepted generation: deployments=%+v", deployments)
		}
	}
}

func TestConcurrentFreshInstallsShareOneAtomicClaim(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	resources := []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}
	db := openAIFARTestStore(t)
	if _, err := db.SaveServer(store.Server{ID: "srv-1", Name: "node-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}); err != nil {
		t.Fatal(err)
	}
	gated := &barrierListStore{Store: db, remaining: 2, ready: make(chan struct{}), release: make(chan struct{})}
	remote := &fakeRemote{}
	errs := make(chan error, 2)
	for _, taskID := range []string{"fresh-a", "fresh-b"} {
		req := InstallRequest{
			Version: "latest", ServerID: "srv-1", Language: "en", TaskID: taskID,
			Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}},
		}
		go func() {
			errs <- NewService(gated, remote).Install(context.Background(), req, resources, fakeLogger{}, nil)
		}()
	}
	<-gated.ready
	close(gated.release)
	results := []error{<-errs, <-errs}
	successes := 0
	for _, result := range results {
		if result == nil {
			successes++
		}
	}
	instances, err := db.ListAppInstances()
	if successes != 1 || err != nil || len(instances) != 1 || instances[0].Status != "installed" || remote.installRuns != 1 {
		t.Fatalf("fresh claim must have one owner: results=%v instances=%+v runs=%d err=%v", results, instances, remote.installRuns, err)
	}
	if instances[0].ID != installAttemptClaimInstanceID("srv-1", "/aifar/apps/admin") {
		t.Fatalf("fresh claim used nondeterministic identity %q", instances[0].ID)
	}
	if instances[0].CreatedAt.IsZero() {
		t.Fatal("fresh deterministic claim must retain a real creation timestamp")
	}
}

func TestLateInstallFailureCannotOverwriteInstalledOwner(t *testing.T) {
	staleMetadata := map[string]any{"installState": "installing", "installAttemptOwner": "owner-a"}
	currentMetadata := map[string]any{"installState": "installed", "installAttemptOwner": "owner-b"}
	s := &fakeStore{instances: []store.AppInstance{{
		ID: "instance-1", App: AppName, Version: "runtime-v2", ServerID: "srv-1", Status: "installed", Metadata: mustMetadata(t, currentMetadata),
	}}}
	stale := s.instances[0]
	stale.Status = "installing"
	stale.Metadata = mustMetadata(t, staleMetadata)
	if err := NewService(s, &fakeRemote{}).markInstallFailed(stale, staleMetadata, errors.New("late owner failure")); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetAppInstance("instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "installed" || stringFromMetadata(metadataFromInstance(current), "installAttemptOwner", "") != "owner-b" {
		t.Fatalf("late failure overwrote current owner: %+v", current)
	}
}

func TestExpiredInstallOwnerCannotFinalizeFailure(t *testing.T) {
	db := openAIFARTestStore(t)
	metadata := map[string]any{"installState": "installing", "installAttemptOwner": "owner-a"}
	instance, err := db.SaveAppInstance(store.AppInstance{
		ID: "instance-1", App: AppName, Version: "runtime-v2", ServerID: "srv-1", Status: "installing", Metadata: mustMetadata(t, metadata), CreatedAt: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		ID: "owner-a", InstanceID: instance.ID, Operation: "install",
		StartedAt: time.Now().UTC().Add(-2 * time.Hour), ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := NewService(db, &fakeRemote{}).markInstallFailed(instance, metadata, errors.New("expired owner failure")); err != nil {
		t.Fatal(err)
	}
	current, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "installing" || boolFromMetadata(metadataFromInstance(current), "installFailed") {
		t.Fatalf("expired owner finalized lifecycle state: %+v", current)
	}
}

func TestInstallReconcilesExactServiceAddressableProof(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	resources := []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}
	for _, tc := range []struct {
		name          string
		hashOverride  string
		nameOverride  string
		wantInstalled bool
	}{
		{name: "exact", wantInstalled: true},
		{name: "wrong service", nameOverride: "system"},
		{name: "wrong hash", hashOverride: strings.Repeat("f", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeStore{servers: map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}}}
			remote := &fakeRemote{}
			if tc.hashOverride != "" {
				remote.bootstrapHashOverrides = map[string]string{"gateway": tc.hashOverride}
			}
			if tc.nameOverride != "" {
				remote.bootstrapNameOverrides = map[string]string{"gateway": tc.nameOverride}
			}
			err := NewService(s, remote).Install(context.Background(), InstallRequest{
				Version: "latest", ServerID: "srv-1", Language: "en",
				Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}},
			}, resources, fakeLogger{}, nil)
			if tc.wantInstalled {
				if err != nil || len(s.instances) != 1 || s.instances[0].Status != "installed" {
					t.Fatalf("exact proof must install: err=%v instances=%+v", err, s.instances)
				}
				return
			}
			if err == nil || len(s.instances) != 1 || s.instances[0].Status != "install_failed" {
				t.Fatalf("mismatched proof must fail closed: err=%v instances=%+v", err, s.instances)
			}
		})
	}
}

func TestInstallFailsWhenBootstrapAcceptanceIsInvalid(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{{ID: "invalid-acceptance", App: AppName, ServerID: "srv-1", Status: "install_failed", Metadata: `{"installRoot":"/aifar/apps/admin","installState":"install_failed"}`}},
	}
	err := NewService(s, &fakeRemote{installStdout: `AIFAR_BOOTSTRAP_ACCEPTANCE={"accepted":true,"instanceId":"invalid-acceptance","deployments":[{"accepted":true,"generation":1,"specHash":"short"}]}`}).Install(
		context.Background(),
		InstallRequest{Version: "latest", ServerID: "srv-1", Language: "en", Parameters: map[string]any{"nacosHost": "10.0.0.50", "selectedServices": []string{"gateway"}}},
		[]store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(root, appBundleVersion, bundleManifestName)}},
		fakeLogger{}, nil,
	)
	if err == nil {
		t.Fatal("invalid Agent acceptance must fail installation")
	}
	if got := s.instances[0].Status; got != "install_failed" {
		t.Fatalf("status=%s, want install_failed", got)
	}
}

func TestServiceMarksAIFARInstallFailedWhenRemoteDeployFails(t *testing.T) {
	withFakeRuntimeAgentBinary(t)
	root := createAIFARBundle(t)
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{failCommandContains: "install-aifar.sh"}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "latest",
		ServerID: "srv-1",
		Language: "en",
		Parameters: map[string]any{
			"nacosHost": "10.0.0.50",
		},
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "runtime-v2", Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected remote deploy failure")
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one failed AIFAR instance, got %+v", s.instances)
	}
	instance := s.instances[0]
	if instance.Status != "install_failed" {
		t.Fatalf("expected install_failed status, got %+v", instance)
	}
	metadata := metadataFromInstance(instance)
	if !aifarInstallFailedInstance(instance, metadata) || metadata["installState"] != "install_failed" || metadata["runtimeSpecPath"] != "/aifar/apps/admin/runtime/agent/runtime-spec.json" {
		t.Fatalf("expected failed install metadata with runtime spec path, got %s", instance.Metadata)
	}
	if len(s.releases) != 0 || len(s.deployments) != len(serviceOrder) || len(s.replicaSets) != len(serviceOrder) || len(s.pods) != 0 || len(s.endpoints) != 0 {
		t.Fatalf("failed Agent acceptance must retain pending desired state without observed runtime, releases=%d deployments=%d replicaSets=%d pods=%d endpoints=%d", len(s.releases), len(s.deployments), len(s.replicaSets), len(s.pods), len(s.endpoints))
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
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "runtime-v2", Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.installScript, `SERVICE_ORDER='gateway oauth web-vue3'`) {
		t.Fatalf("install script should only iterate selected services, got:\n%s", remote.installScript)
	}
	if strings.Contains(remote.installScript, `SERVICE_ORDER='contacts file gateway im meeting message oauth permission system web-vue3'`) {
		t.Fatalf("install script should not use all services when selectedServices is provided:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `open_service_ports $SERVICE_ORDER`) {
		t.Fatalf("install script should open selected runtime service ports:\n%s", remote.installScript)
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one AIFAR instance, got %+v", s.instances)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(s.instances[0].Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if got := stringSliceFromAny(metadata["services"]); strings.Join(got, " ") != "gateway oauth web-vue3" {
		t.Fatalf("expected selected services in metadata, got %#v from %s", got, s.instances[0].Metadata)
	}
	containers := mapFromMetadataValue(metadata["containers"])
	if _, ok := containers["permission"]; ok {
		t.Fatalf("metadata should not include unselected permission container: %s", s.instances[0].Metadata)
	}
	if len(s.releases) != 1 {
		t.Fatalf("expected one recorded release, got %+v", s.releases)
	}
	if len(s.deployments) != 3 || len(s.replicaSets) != 3 || len(s.pods) != 0 || len(s.endpoints) != 0 {
		t.Fatalf("expected control plane rows only for selected services, deployments=%d replicaSets=%d pods=%d endpoints=%d", len(s.deployments), len(s.replicaSets), len(s.pods), len(s.endpoints))
	}
	manifest := map[string]any{}
	if err := json.Unmarshal([]byte(s.releases[0].ManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if got := stringSliceFromAny(manifest["services"]); strings.Join(got, " ") != "gateway oauth web-vue3" {
		t.Fatalf("expected selected services in manifest, got %#v from %s", got, s.releases[0].ManifestJSON)
	}
	releaseContainers := mapFromMetadataValue(manifest["containers"])
	if _, ok := releaseContainers["permission"]; ok {
		t.Fatalf("manifest should not include unselected permission container: %s", s.releases[0].ManifestJSON)
	}
}

func TestServiceInstallsMissingAIFARModulesAfterInitialInstall(t *testing.T) {
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	initialServices := []string{"gateway", "web-vue3"}
	for key, value := range releaseOrchestrationMetadata("/aifar/apps/admin", "20260701T010203.000000000Z-runtime-v2", defaultNetworkName, defaultGatewayPort, defaultWebPort, initialServices) {
		metadata[key] = value
	}
	metadata["services"] = initialServices
	instance.Metadata = mustMetadata(t, metadata)
	baseStore := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{instance},
	}
	bundleParent := createAIFARBundle(t)
	s := &resourceFakeStore{fakeStore: baseStore, resources: []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(bundleParent, appBundleVersion, bundleManifestName)}}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.InstallServices(context.Background(), InstallServicesRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
		Actor:    "admin",
		Services: []string{"file", "system"},
		Reason:   "install missed services",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`AIFAR_SERVICE_INSTALL`,
		`NEW_SERVICES='file system'`,
		`SERVICE_ORDER='file gateway system web-vue3'`,
		`docker build -t "$image" "$APP_DIR/$service"`,
		`open_service_ports $NEW_SERVICES`,
		`allow_selinux_ports http_port_t $ports`,
		`trap 'cleanup_failed_service_install' EXIT INT TERM`,
	} {
		if !strings.Contains(remote.serviceInstallScript, want) {
			t.Fatalf("service install script should contain %q:\n%s", want, remote.serviceInstallScript)
		}
	}
	if !strings.Contains(remote.joinedUploads(), "aifar-service-modules-") {
		t.Fatalf("service install should upload modules from the current resource directory, uploads=%s", remote.joinedUploads())
	}
	if !strings.Contains(remote.joinedCommands(), "tar -xzf") {
		t.Fatalf("service install should extract uploaded modules before rendering the runtime, commands=%s", remote.joinedCommands())
	}
	if len(s.instances) != 1 || s.instances[0].Status != "installed" {
		t.Fatalf("expected installed instance, got %+v", s.instances)
	}
	next := metadataFromInstance(s.instances[0])
	if got := strings.Join(servicesFromMetadata(next), " "); got != "file gateway system web-vue3" {
		t.Fatalf("expected merged services in metadata, got %q from %s", got, s.instances[0].Metadata)
	}
	if _, ok := next["orchestrationLock"]; ok {
		t.Fatalf("service install should clear orchestration lock: %s", s.instances[0].Metadata)
	}
	last, ok := next["lastServiceInstall"].(map[string]any)
	if !ok || strings.Join(stringSliceFromAny(last["services"]), " ") != "file system" {
		t.Fatalf("expected lastServiceInstall metadata, got %s", s.instances[0].Metadata)
	}
	desired := mapFromMetadataValue(next["desiredReplicas"])
	for _, serviceName := range []string{"system", "file", "gateway", "web-vue3"} {
		if intFromAny(desired[serviceName], 0) != 1 {
			t.Fatalf("expected desired replica for %s, got %#v in %s", serviceName, desired, s.instances[0].Metadata)
		}
	}
	if _, ok := desired["oauth"]; ok {
		t.Fatalf("metadata should not add uninstalled oauth desired replica: %#v", desired)
	}
	if len(s.deployments) != 2 || len(s.replicaSets) != 2 || len(s.pods) != 0 || len(s.endpoints) != 0 {
		t.Fatalf("expected control-plane rows for two newly installed services, deployments=%d replicaSets=%d pods=%d endpoints=%d", len(s.deployments), len(s.replicaSets), len(s.pods), len(s.endpoints))
	}
	if len(s.releases) != 1 || !strings.Contains(s.releases[0].ManifestJSON, `"kind":"service-install"`) || !strings.Contains(s.releases[0].ManifestJSON, `"installedServices":["file","system"]`) {
		t.Fatalf("expected service-install release manifest, got %+v", s.releases)
	}
}

func TestInstallServicesRetryKeepsAcceptedPeerGeneration(t *testing.T) {
	for _, phase := range []string{"Accepted", "Progressing", "Available", "Degraded", "Offline"} {
		t.Run(phase, func(t *testing.T) {
			instance := installedAIFARInstance(t)
			metadata := metadataFromInstance(instance)
			metadata["services"] = []string{"gateway", "web-vue3"}
			metadata["serviceCatalog"] = serviceCatalogMetadataForInstall(legacyServiceDefinitions(), defaultGatewayPort, defaultWebPort)
			instance.Metadata = mustMetadata(t, metadata)
			base := &fakeStore{
				servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
				instances: []store.AppInstance{instance},
			}
			bundleParent := createAIFARBundle(t)
			s := &resourceFakeStore{fakeStore: base, resources: []store.Resource{{App: AppName, Part: "backend", Version: appBundleVersion, Path: filepath.Join(bundleParent, appBundleVersion, bundleManifestName)}}}
			remote := &fakeRemote{deploymentApplyFailures: map[string]int{"system": 1}}
			service := NewService(s, remote)
			req := InstallServicesRequest{
				Instance: instance, Server: s.servers["srv-1"], Language: "en", Actor: "admin", TaskID: "install-services-retry",
				Services: []string{"file", "system"}, Reason: "complete missing services",
			}
			if err := service.InstallServices(context.Background(), req, fakeLogger{}, nil); err == nil {
				t.Fatal("first install-services attempt must fail when system Manifest persistence fails")
			}
			byService := map[string]store.AIFARDeployment{}
			for _, deployment := range s.deployments {
				byService[deployment.ServiceName] = deployment
			}
			if got := byService["file"]; got.Generation != 1 || got.Status != "Accepted" {
				t.Fatalf("accepted file peer must be retained at generation 1: %+v", got)
			}
			pending := byService["system"]
			if pending.Generation != 1 || pending.Status != "pending_acceptance" {
				t.Fatalf("failed system target must retain its generation-1 pending Manifest: %+v", pending)
			}
			if remote.deploymentApplyCounts["file"] != 1 || remote.deploymentApplyCounts["system"] != 1 {
				t.Fatalf("unexpected first apply counts: %+v", remote.deploymentApplyCounts)
			}

			for index := range s.deployments {
				if s.deployments[index].ServiceName == "file" {
					s.deployments[index].Status = phase
					if phase != "Accepted" {
						s.deployments[index].ObservedGeneration = s.deployments[index].Generation
					}
				}
			}
			s.pods = append(s.pods, store.AIFARPod{InstanceID: instance.ID, ServiceName: "file", PodID: "file-1", ContainerName: "existing-file-pod"})
			remote.failPreparedServices = map[string]bool{"file": true}

			if err := service.InstallServices(context.Background(), req, fakeLogger{}, nil); err != nil {
				t.Fatal(err)
			}
			byService = map[string]store.AIFARDeployment{}
			for _, deployment := range s.deployments {
				byService[deployment.ServiceName] = deployment
			}
			file := byService["file"]
			if file.Generation != 1 || file.Status != phase {
				t.Fatalf("retry must preserve accepted/observed file peer: %+v", file)
			}
			system := byService["system"]
			if system.Generation != 1 || system.Status != "Accepted" || system.ObservedGeneration != 0 {
				t.Fatalf("retry must accept the same pending system generation: %+v", system)
			}
			if remote.deploymentApplyCounts["file"] != 1 || remote.deploymentApplyCounts["system"] != 2 {
				t.Fatalf("retry must skip accepted file and resubmit only pending system: %+v", remote.deploymentApplyCounts)
			}
			if remote.servicePrepareCounts["file"] != 1 || remote.servicePrepareCounts["system"] != 1 {
				t.Fatalf("retry must not rewrite env or build a new revision for prepared peers: %+v", remote.servicePrepareCounts)
			}
			for _, serviceName := range []string{"file", "system"} {
				revisions := remote.servicePrepareRevisions[serviceName]
				if len(revisions) != 1 || revisions[0] != pending.CurrentRevision {
					t.Fatalf("%s prepare revisions=%v, want only persisted revision %s", serviceName, revisions, pending.CurrentRevision)
				}
			}
			installedMetadata := metadataFromInstance(s.instances[0])
			if got := stringFromMetadata(installedMetadata, "releaseId", ""); got != pending.CurrentRevision {
				t.Fatalf("retry releaseId=%q, want persisted pending revision %q", got, pending.CurrentRevision)
			}
			if len(s.pods) != 1 || s.pods[0].ContainerName != "existing-file-pod" || len(s.endpoints) != 0 {
				t.Fatalf("retry must preserve the existing peer Pod without inventing endpoints: pods=%+v endpoints=%+v", s.pods, s.endpoints)
			}
			if strings.Contains(remote.serviceInstallScript, "reconcile-runtime") || strings.Contains(remote.serviceInstallScript, "reconcile_runtime") {
				t.Fatal("service preparation script must not own desired-state mutation")
			}
		})
	}
}

func TestServiceRuntimeReconcileQueuesTypedPerServiceCommands(t *testing.T) {
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances:   []store.AppInstance{instance},
		deployments: seedPerServiceDeployments(t, instance, map[string]int{"permission": 1, "file": 1}, nil),
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.ReconcileRuntime(context.Background(), RuntimeReconcileRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
		Actor:    "admin",
		Reason:   "repair runtime entry",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`AIFAR_RUNTIME_RECONCILE`,
		`aifar-agent reconcile-deployment --instance "$INSTANCE_ID" --service "$SERVICE_NAME"`,
	} {
		if !strings.Contains(remote.runtimeReconcileScript, want) {
			t.Fatalf("runtime reconcile script should contain %q:\n%s", want, remote.runtimeReconcileScript)
		}
	}
	if strings.Contains(remote.runtimeReconcileScript, "reconcile-runtime") || strings.Contains(remote.runtimeReconcileScript, "runtime-spec.json") {
		t.Fatalf("runtime reconcile script must not submit aggregate desired state:\n%s", remote.runtimeReconcileScript)
	}
}

func TestRestartAllFansOutWithoutStopAll(t *testing.T) {
	agent := withFakeRuntimeAgentBinary(t)
	sum, _, err := fileSHA256(agent)
	if err != nil {
		t.Fatal(err)
	}
	instance := installedAIFARInstance(t)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "system": 2, "file": 0},
		map[string]int64{"permission": 3, "system": 4, "file": 7},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{runtimeAgentCheckStdout: runtimeAgentCheckOutput(t, sum, requiredRuntimeAgentFeatures...)}

	err = NewService(s, remote).RestartRuntime(context.Background(), RuntimeRestartRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-restart-all",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := deploymentsByService(s.deployments)
	for _, serviceName := range []string{"permission", "system"} {
		got := after[serviceName]
		want := before[serviceName]
		if got.Generation != want.Generation+1 || got.Status != "Accepted" {
			t.Fatalf("%s mutation=%+v, want generation %d accepted", serviceName, got, want.Generation+1)
		}
		var manifest runtimeagent.DeploymentManifest
		if err := json.Unmarshal([]byte(got.SpecJSON), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Spec.RestartGeneration != 6 {
			t.Fatalf("%s restartGeneration=%d, want 6", serviceName, manifest.Spec.RestartGeneration)
		}
	}
	if got, want := after["file"], before["file"]; got.Generation != want.Generation || got.SpecJSON != want.SpecJSON {
		t.Fatalf("offline file deployment changed: before=%+v after=%+v", want, got)
	}
	commands := remote.joinedCommands()
	for _, forbidden := range []string{"restart-runtime", "reconcile-runtime", "runtime-spec.json", "stop-all-pods"} {
		if strings.Contains(commands, forbidden) {
			t.Fatalf("restart-all used forbidden aggregate action %q:\n%s", forbidden, commands)
		}
	}
}

func TestRestartAllAggregatesFailureWithoutRollingBackAcceptedPeer(t *testing.T) {
	instance := installedAIFARInstance(t)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "system": 1, "file": 0},
		map[string]int64{"permission": 2, "system": 5, "file": 8},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{deploymentApplyFailures: map[string]int{"permission": 1}}

	err := NewService(s, remote).RestartRuntime(context.Background(), RuntimeRestartRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-restart-partial",
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected aggregated restart failure")
	}
	after := deploymentsByService(s.deployments)
	if got := after["permission"]; got.Generation != before["permission"].Generation+1 || got.Status != "pending_acceptance" {
		t.Fatalf("failed permission intent=%+v, want newer pending acceptance", got)
	}
	if got := after["system"]; got.Generation != before["system"].Generation+1 || got.Status != "Accepted" {
		t.Fatalf("accepted system intent was lost or rolled back: %+v", got)
	}
	if got, want := after["file"], before["file"]; got.Generation != want.Generation || got.SpecJSON != want.SpecJSON {
		t.Fatalf("offline peer changed: before=%+v after=%+v", want, got)
	}
	if remote.deploymentApplyCounts["permission"] != 1 || remote.deploymentApplyCounts["system"] != 1 {
		t.Fatalf("restart fan-out did not attempt every online service exactly once: %+v", remote.deploymentApplyCounts)
	}
}

func TestRestartAllBusyPeerDoesNotBlockFreePeer(t *testing.T) {
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationLocks"] = map[string]any{
		"permission": map[string]any{
			"operation": "update-artifact",
			"service":   "permission",
			"actor":     "another-operator",
			"taskId":    "task-busy-permission",
			"startedAt": time.Now().UTC().Format(time.RFC3339),
		},
	}
	instance.Metadata = mustMetadata(t, metadata)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{}
	recorder := &targetStateRecorder{}

	err := NewService(s, remote).RestartRuntime(context.Background(), RuntimeRestartRequest{
		Instance: instance, Server: s.servers["srv-1"], Language: "en", Actor: "operator", TaskID: "task-restart-independent",
	}, recorder, nil)
	if err == nil {
		t.Fatal("expected the busy permission target to fail")
	}
	after := deploymentsByService(s.deployments)
	if got := after["permission"]; got.Generation != before["permission"].Generation || got.SpecJSON != before["permission"].SpecJSON {
		t.Fatalf("busy permission deployment changed: before=%+v after=%+v", before["permission"], got)
	}
	if got := after["file"]; got.Generation != before["file"].Generation+1 || got.Status != "Accepted" {
		t.Fatalf("free file peer was not accepted independently: %+v", got)
	}
	statuses, _, steps := recorder.snapshot()
	if statuses[instance.ID+":permission"] != "failed" || statuses[instance.ID+":file"] != "success" {
		t.Fatalf("target statuses=%v, want permission failed and file success", statuses)
	}
	if steps[instance.ID+":permission:accept-service-intent"] != "failed" || steps[instance.ID+":file:accept-service-intent"] != "success" {
		t.Fatalf("step statuses=%v", steps)
	}
}

func TestRestartAllSkipsServiceOfflinedAfterInitialList(t *testing.T) {
	instance := installedAIFARInstance(t)
	base := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: seedPerServiceDeployments(t, instance, map[string]int{"permission": 1}, map[string]int64{"permission": 4}),
	}
	s := &firstDeploymentListBarrierStore{fakeStore: base, listed: make(chan struct{}), release: make(chan struct{})}
	remote := &fakeRemote{}
	recorder := &targetStateRecorder{}
	done := make(chan error, 1)
	go func() {
		done <- NewService(s, remote).RestartRuntime(context.Background(), RuntimeRestartRequest{
			Instance: instance, Server: base.servers["srv-1"], Language: "en", Actor: "operator", TaskID: "task-restart-offline-race",
		}, recorder, nil)
	}()
	<-s.listed
	base.mu.Lock()
	before := base.deployments[0]
	var manifest runtimeagent.DeploymentManifest
	if err := json.Unmarshal([]byte(before.SpecJSON), &manifest); err != nil {
		base.mu.Unlock()
		t.Fatal(err)
	}
	manifest.Spec.Replicas = 0
	raw, err := json.Marshal(manifest)
	if err != nil {
		base.mu.Unlock()
		t.Fatal(err)
	}
	base.deployments[0].DesiredReplicas = 0
	base.deployments[0].SpecJSON = string(raw)
	offline := base.deployments[0]
	base.mu.Unlock()
	close(s.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	base.mu.Lock()
	after := base.deployments[0]
	base.mu.Unlock()
	if after.Generation != offline.Generation || after.SpecJSON != offline.SpecJSON {
		t.Fatalf("offline race advanced desired intent: offline=%+v after=%+v", offline, after)
	}
	if remote.deploymentApplyCounts["permission"] != 0 {
		t.Fatalf("offline target applied %d times", remote.deploymentApplyCounts["permission"])
	}
	statuses, _, steps := recorder.snapshot()
	if statuses[instance.ID+":permission"] != "success" || steps[instance.ID+":permission:accept-service-intent"] != "success" {
		t.Fatalf("offline target terminal states=%v steps=%v", statuses, steps)
	}
}

func TestRestartOfflineNoOpUsesRealStoreSuccessTerminalAndLocalizedTargetLog(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1}, map[string]int64{"permission": 4},
	) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	barrier := &realFirstDeploymentListBarrierStore{Store: db, listed: make(chan struct{}), release: make(chan struct{})}
	service := NewService(barrier, &fakeRemote{})
	manager := worker.NewManager(db)
	task, err := manager.StartWithLanguage("apps.aifar.runtime.restart", instance.ID, "operator", "en", func(ctx context.Context, log worker.Logger) error {
		return service.RestartRuntime(ctx, RuntimeRestartRequest{
			Instance: instance, Server: store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
			Language: "en", Actor: "operator", TaskID: log.TaskID(),
		}, log, func(target string) Logger {
			targeted := log.Target(target)
			return targeted
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-barrier.listed:
	case <-time.After(2 * time.Second):
		t.Fatal("restart did not reach the initial deployment list")
	}
	deployments, err := db.ListAIFARDeployments(instance.ID)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("deployments=%+v err=%v", deployments, err)
	}
	offline := deployments[0]
	var manifest runtimeagent.DeploymentManifest
	if err := json.Unmarshal([]byte(offline.SpecJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Spec.Replicas = 0
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	offline.DesiredReplicas = 0
	offline.SpecJSON = string(raw)
	offline.Status = "Offline"
	offline.ObservedGeneration = offline.Generation
	if _, err := db.SaveAIFARDeployment(offline); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)
	deadline := time.Now().Add(3 * time.Second)
	var persisted store.Task
	var logs []store.TaskLog
	for time.Now().Before(deadline) {
		persisted, logs, err = db.GetTask(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.FinishedAt.IsZero() {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}
	if persisted.Status != "success" || persisted.FinishedAt.IsZero() {
		t.Fatalf("task=%+v", persisted)
	}
	targets, err := db.ListTaskTargets(task.ID)
	if err != nil || len(targets) != 1 || targets[0].Status != "success" || targets[0].FinishedAt.IsZero() {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	steps, err := db.ListTaskSteps(task.ID)
	if err != nil || len(steps) != 1 || steps[0].Status != "success" || steps[0].FinishedAt.IsZero() {
		t.Fatalf("steps=%+v err=%v", steps, err)
	}
	wantLog := i18n.Text("en", "aifar.runtimeMutation.skippedOffline")
	found := false
	for _, entry := range logs {
		if entry.Target == instance.ID+":permission" && strings.Contains(entry.Message, wantLog) {
			found = true
		}
	}
	if !found {
		t.Fatalf("localized offline no-op target log %q missing: %+v", wantLog, logs)
	}
}

func TestRunServiceFanOutRecoversTargetPanicAndContinuesPeers(t *testing.T) {
	recorder := &targetStateRecorder{}
	attempted := map[string]bool{}
	var attemptedMu sync.Mutex
	failures := runServiceFanOut(context.Background(), "aifar-1", []string{"permission", "file"}, 2, "en", recorder, nil, recorder,
		func(_ context.Context, serviceName string, _ Logger) error {
			if serviceName == "permission" {
				panic("secret panic payload")
			}
			attemptedMu.Lock()
			attempted[serviceName] = true
			attemptedMu.Unlock()
			return nil
		})
	if len(failures) != 1 || failures[0].service != "permission" {
		t.Fatalf("failures=%+v, want one permission failure", failures)
	}
	if strings.Contains(failures[0].err.Error(), "secret") || strings.Contains(failures[0].err.Error(), "payload") {
		t.Fatalf("panic value leaked: %v", failures[0].err)
	}
	attemptedMu.Lock()
	fileAttempted := attempted["file"]
	attemptedMu.Unlock()
	if !fileAttempted {
		t.Fatal("free peer was not attempted after panic")
	}
	statuses, errTexts, steps := recorder.snapshot()
	if statuses["aifar-1:permission"] != "failed" || statuses["aifar-1:file"] != "success" {
		t.Fatalf("target statuses=%v", statuses)
	}
	if strings.Contains(errTexts["aifar-1:permission"], "secret") || steps["aifar-1:permission:accept-service-intent"] != "failed" {
		t.Fatalf("panic target error/step=%q %v", errTexts["aifar-1:permission"], steps)
	}
}

func TestManualReconcileQueuesOnlySelectedServiceWithoutGenerationChange(t *testing.T) {
	instance := installedAIFARInstance(t)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{}

	err := NewService(s, remote).ReconcileRuntime(context.Background(), RuntimeReconcileRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-reconcile-permission", ServiceName: "permission",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := deploymentsByService(s.deployments)
	for _, serviceName := range []string{"permission", "file"} {
		if after[serviceName].Generation != before[serviceName].Generation || after[serviceName].SpecJSON != before[serviceName].SpecJSON {
			t.Fatalf("manual reconcile changed %s desired generation/spec", serviceName)
		}
	}
	commands := remote.joinedCommands()
	if !strings.Contains(commands, "INSTANCE_ID='aifar-1'") || !strings.Contains(commands, "SERVICE_NAME='permission'") || !strings.Contains(commands, `aifar-agent reconcile-deployment --instance "$INSTANCE_ID" --service "$SERVICE_NAME"`) {
		t.Fatalf("selected service did not use typed Agent reconcile:\n%s", commands)
	}
	for _, forbidden := range []string{"--service 'file'", "reconcile-runtime", "runtime-spec.json"} {
		if strings.Contains(commands, forbidden) {
			t.Fatalf("manual reconcile used forbidden peer/aggregate path %q:\n%s", forbidden, commands)
		}
	}
}

func TestServiceCleanupRuntimeStalePodsPrunesControlPlaneRecords(t *testing.T) {
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{instance},
		pods: []store.AIFARPod{
			{InstanceID: instance.ID, ServiceName: "gateway", Revision: "rev-1", PodID: "gateway-rev-1-r1", ContainerName: "aifar-pod-admin-gateway-rev-1-r1", Port: 38000, Ready: true},
			{InstanceID: instance.ID, ServiceName: "gateway", Revision: "rev-old", PodID: "gateway-rev-old-r1", ContainerName: "aifar-pod-admin-gateway-rev-old-r1", Port: 38000, Ready: true},
		},
		endpoints: []store.AIFARServiceEndpoint{
			{InstanceID: instance.ID, ServiceName: "gateway", PodID: "gateway-rev-1-r1", ContainerName: "aifar-pod-admin-gateway-rev-1-r1", Revision: "rev-1", Port: 38000, State: "active", Ready: true},
			{InstanceID: instance.ID, ServiceName: "gateway", PodID: "gateway-rev-old-r1", ContainerName: "aifar-pod-admin-gateway-rev-old-r1", Revision: "rev-old", Port: 38000, State: "active", Ready: true},
		},
	}
	remote := &fakeRemote{runtimePodScanStdout: "aifar-pod-admin-gateway-rev-1-r1\n"}
	service := NewService(s, remote)
	err := service.CleanupRuntimeStalePods(context.Background(), RuntimeCleanupRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
		Actor:    "admin",
		Reason:   "clear stale rows",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.pods) != 1 || s.pods[0].ContainerName != "aifar-pod-admin-gateway-rev-1-r1" {
		t.Fatalf("expected only existing pod record to remain, got %+v", s.pods)
	}
	if len(s.endpoints) != 1 || s.endpoints[0].ContainerName != "aifar-pod-admin-gateway-rev-1-r1" {
		t.Fatalf("expected only existing endpoint record to remain, got %+v", s.endpoints)
	}
	if !strings.Contains(remote.joinedCommands(), `label=aifar.install-root=$INSTALL_ROOT`) {
		t.Fatalf("expected cleanup scan to filter by install root label, commands=%s", remote.joinedCommands())
	}
	metadata := metadataFromInstance(s.instances[0])
	if _, ok := metadata["lastRuntimeCleanup"].(map[string]any); !ok {
		t.Fatalf("expected cleanup metadata, got %s", s.instances[0].Metadata)
	}
	if _, ok := metadata["orchestrationLock"]; ok {
		t.Fatalf("cleanup should clear orchestration lock: %s", s.instances[0].Metadata)
	}
}

func TestServiceUninstallsRuntimeAgentWithoutRemovingBusinessPods(t *testing.T) {
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.UninstallRuntimeAgent(context.Background(), RuntimeAgentUninstallRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
		Actor:    "admin",
		Reason:   "operator cleanup",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`AIFAR_AGENT_UNINSTALL`,
		`aifar-agent deregister-nacos --spec "$SPEC_PATH"`,
		`aifar-agent remove-instance --instance "$INSTANCE_ID"`,
		`systemctl stop aifar-agent`,
		`rm -f /etc/systemd/system/aifar-agent.service`,
		`rm -f /usr/local/bin/aifar-agent`,
	} {
		if !strings.Contains(remote.runtimeAgentUninstall, want) {
			t.Fatalf("agent uninstall command should contain %q:\n%s", want, remote.runtimeAgentUninstall)
		}
	}
	metadata := metadataFromInstance(s.instances[0])
	if metadata["agentUninstalledAt"] == nil {
		t.Fatalf("expected agent uninstall metadata, got %s", s.instances[0].Metadata)
	}
	if strings.Contains(remote.runtimeAgentUninstall, "docker rm") || strings.Contains(remote.runtimeAgentUninstall, "rm -rf \"$INSTALL_ROOT\"") {
		t.Fatalf("agent uninstall should not remove business pods or install root:\n%s", remote.runtimeAgentUninstall)
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
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "runtime-v2", Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}, fakeLogger{}, nil)
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

func TestArtifactUpdateMutatesOnlyTargetServiceGeneration(t *testing.T) {
	artifactPath := filepath.Join(t.TempDir(), "oauth.jar")
	if err := os.WriteFile(artifactPath, []byte("new oauth jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	instance := installedAIFARInstance(t)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"oauth": 1, "file": 1},
		map[string]int64{"oauth": 3, "file": 7},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	beforeEndpoints := metadataFromInstance(instance)["activeEndpoints"]
	remote := &fakeRemote{}

	err := NewService(s, remote).UpdateArtifact(context.Background(), ArtifactUpdateRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-update-oauth",
		ServiceName: "oauth", ArtifactLocalPath: artifactPath, ArtifactFileName: "oauth.jar",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := deploymentsByService(s.deployments)
	got := after["oauth"]
	if got.Generation != before["oauth"].Generation+1 || got.Status != "Accepted" {
		t.Fatalf("oauth rollout mutation=%+v, want next generation accepted", got)
	}
	var manifest runtimeagent.DeploymentManifest
	if err := json.Unmarshal([]byte(got.SpecJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Spec.PodRevision == before["oauth"].CurrentRevision || manifest.Spec.Image != "aifar-oauth:"+manifest.Spec.PodRevision {
		t.Fatalf("oauth artifact revision/image not advanced together: %+v", manifest.Spec)
	}
	if peer, want := after["file"], before["file"]; peer.Generation != want.Generation || peer.SpecJSON != want.SpecJSON {
		t.Fatalf("file deployment changed: before=%+v after=%+v", want, peer)
	}
	saved, err := s.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadataFromInstance(saved)["activeEndpoints"]; !reflect.DeepEqual(got, beforeEndpoints) {
		t.Fatalf("accepted artifact intent must not fabricate Ready endpoints: before=%+v after=%+v", beforeEndpoints, got)
	}
	for _, forbidden := range []string{"reconcile-runtime", "runtime-spec.json"} {
		if strings.Contains(remote.updateScript, forbidden) {
			t.Fatalf("artifact preparation script contains aggregate desired-state action %q:\n%s", forbidden, remote.updateScript)
		}
	}
}

func TestArtifactUpdateLockRenewalFailureCancelsPrepareAndSkipsMetadataCAS(t *testing.T) {
	artifactPath := filepath.Join(t.TempDir(), "permission.jar")
	if err := os.WriteFile(artifactPath, []byte("new permission jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance, map[string]int{"permission": 1}, nil) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	renewals := &renewalFailureStore{Store: db, renewed: make(chan struct{}), armed: make(chan struct{})}
	remote := &heartbeatBlockingRemote{
		fakeRemote:    &fakeRemote{},
		blockContains: "update-aifar-artifact.sh",
		armed:         renewals.armed,
		reached:       make(chan struct{}),
	}
	service := NewService(renewals, remote)
	service.orchestrationLockHeartbeatInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- service.UpdateArtifact(ctx, ArtifactUpdateRequest{
			Instance: instance, Server: store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
			Language: "en", Actor: "operator", TaskID: "task-update-renewal-loss",
			ServiceName: "permission", ArtifactLocalPath: artifactPath, ArtifactFileName: "permission.jar",
		}, fakeLogger{}, nil)
	}()
	requireHeartbeatRenewalCancellation(t, renewals.renewed, remote.reached, done, cancel)
	if calls := renewals.appInstanceCASCalls(); calls != 0 {
		t.Fatalf("app-instance CAS calls=%d after artifact lock loss, want 0", calls)
	}
	active, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("lost artifact-update lock remained active: %+v", active)
	}
}

func TestConcurrentDifferentServiceArtifactUpdatesPreserveMetadataAndLifecycleStatus(t *testing.T) {
	dir := t.TempDir()
	permissionArtifact := filepath.Join(dir, "permission.jar")
	fileArtifact := filepath.Join(dir, "file.jar")
	if err := os.WriteFile(permissionArtifact, []byte("new permission jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileArtifact, []byte("new file jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance.Status = "install_failed"
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	barrier := &barrierCASStore{Store: db, remaining: 2, ready: make(chan struct{}), release: make(chan struct{})}
	server := store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}
	service := NewService(barrier, &fakeRemote{})
	errs := make(chan error, 2)
	for _, request := range []ArtifactUpdateRequest{
		{Instance: instance, Server: server, Language: "en", Actor: "operator-permission", TaskID: "task-update-permission", ServiceName: "permission", ArtifactLocalPath: permissionArtifact, ArtifactFileName: "permission.jar"},
		{Instance: instance, Server: server, Language: "en", Actor: "operator-file", TaskID: "task-update-file", ServiceName: "file", ArtifactLocalPath: fileArtifact, ArtifactFileName: "file.jar"},
	} {
		request := request
		go func() {
			errs <- service.UpdateArtifact(context.Background(), request, fakeLogger{}, nil)
		}()
	}
	<-barrier.ready
	close(barrier.release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "install_failed" {
		t.Fatalf("runtime update changed lifecycle status to %q", saved.Status)
	}
	metadata := metadataFromInstance(saved)
	revisions := serviceRevisionsFromMetadata(metadata)
	for _, serviceName := range []string{"permission", "file"} {
		if revisions[serviceName] == "" || revisions[serviceName] == "release-"+serviceName {
			t.Fatalf("service revisions=%v, missing accepted %s update", revisions, serviceName)
		}
	}
	configHashes := mapFromMetadataValue(metadata["serviceConfigHashes"])
	rollouts := mapFromMetadataValue(metadata["serviceRollouts"])
	for _, serviceName := range []string{"permission", "file"} {
		if strings.TrimSpace(fmt.Sprint(configHashes[serviceName])) == "" {
			t.Fatalf("service config hashes=%v, missing %s", configHashes, serviceName)
		}
		rollout, ok := rollouts[serviceName].(map[string]any)
		if !ok || rollout["service"] != serviceName || strings.TrimSpace(fmt.Sprint(rollout["releaseId"])) == "" {
			t.Fatalf("service rollouts=%v, missing %s audit", rollouts, serviceName)
		}
	}
	permissionSHA, _, err := fileSHA256(permissionArtifact)
	if err != nil {
		t.Fatal(err)
	}
	fileSHA, _, err := fileSHA256(fileArtifact)
	if err != nil {
		t.Fatal(err)
	}
	permissionThenFile := partialUpdateConfigHash(partialUpdateConfigHash("base-config-hash", "permission", "permission.jar", permissionSHA), "file", "file.jar", fileSHA)
	fileThenPermission := partialUpdateConfigHash(partialUpdateConfigHash("base-config-hash", "file", "file.jar", fileSHA), "permission", "permission.jar", permissionSHA)
	globalConfigHash := stringFromMetadata(metadata, "configHash", "")
	if globalConfigHash != permissionThenFile && globalConfigHash != fileThenPermission {
		t.Fatalf("global configHash=%s, want both concurrent contributions", globalConfigHash)
	}
	if calls := barrier.callCount(); calls != 3 {
		t.Fatalf("CAS calls=%d, want two initial attempts plus one conflict retry", calls)
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
		instances:   []store.AppInstance{instance},
		deployments: seedPerServiceDeployments(t, instance, map[string]int{"oauth": 1}, nil),
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
		`docker build -t "$image" "$APP_DIR"`,
		`ARTIFACT_SHA256=`,
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
	revisions := serviceRevisionsFromMetadata(metadata)
	if revisions["oauth"] == "20260701T010203.000000000Z-runtime-v2" || revisions["file"] != "20260701T010203.000000000Z-runtime-v2" || metadata["configHash"] == "base-config-hash" {
		t.Fatalf("expected only oauth release metadata to change, got %s", s.instances[0].Metadata)
	}
	if metadata["releaseId"] != "20260701T010203.000000000Z-runtime-v2" {
		t.Fatalf("partial rollout must not replace the instance-wide release id: %s", s.instances[0].Metadata)
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
	if endpoints, _ := manifest["endpoints"].(map[string]any); len(endpoints) != 0 {
		t.Fatalf("accepted rollout release must not fabricate observed endpoints, got %s", s.releases[0].ManifestJSON)
	}
}

func TestServiceMarksFailedAIFARServiceArtifactRelease(t *testing.T) {
	artifactPath := filepath.Join(t.TempDir(), "oauth.jar")
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
	service := NewService(s, &fakeRemote{failCommandContains: "update-aifar-artifact.sh"})
	err := service.UpdateArtifact(context.Background(), ArtifactUpdateRequest{
		Instance:          instance,
		Server:            s.servers["srv-1"],
		Language:          "en",
		TaskID:            "task-single-failed",
		ServiceName:       "oauth",
		ArtifactLocalPath: artifactPath,
		ArtifactFileName:  "oauth.jar",
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected artifact update to fail")
	}
	assertLatestFailedRelease(t, s.releases)
}

func TestArtifactBundleFanOutAggregatesFailureWithoutRollingBackAcceptedPeer(t *testing.T) {
	bundlePath := writeAlphaJarBundle(t, []bundleTestArtifact{
		{Service: "oauth", Module: "alpha-oauth", FileName: "alpha-oauth.jar", Content: "new oauth jar"},
		{Service: "gateway", Module: "alpha-gateway", FileName: "alpha-gateway.jar", Content: "new gateway jar"},
	})
	instance := installedAIFARInstance(t)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"oauth": 1, "gateway": 1, "file": 1},
		map[string]int64{"oauth": 2, "gateway": 4, "file": 7},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{deploymentApplyFailures: map[string]int{"oauth": 1}}

	err := NewService(s, remote).UpdateArtifactBundle(context.Background(), ArtifactBundleUpdateRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-bundle-partial",
		BundleLocalPath: bundlePath, BundleFileName: filepath.Base(bundlePath), Concurrency: 2,
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected bundle acceptance aggregate failure")
	}
	after := deploymentsByService(s.deployments)
	if got := after["oauth"]; got.Generation != before["oauth"].Generation+1 || got.Status != "pending_acceptance" {
		t.Fatalf("failed oauth intent=%+v, want newer pending acceptance", got)
	}
	if got := after["gateway"]; got.Generation != before["gateway"].Generation+1 || got.Status != "Accepted" {
		t.Fatalf("accepted gateway intent was lost or rolled back: %+v", got)
	}
	if got, want := after["file"], before["file"]; got.Generation != want.Generation || got.SpecJSON != want.SpecJSON {
		t.Fatalf("unselected file changed: before=%+v after=%+v", want, got)
	}
	if remote.deploymentApplyCounts["oauth"] != 1 || remote.deploymentApplyCounts["gateway"] != 1 {
		t.Fatalf("bundle fan-out did not attempt both services once: %+v", remote.deploymentApplyCounts)
	}
}

func TestArtifactBundleBusyPeerDoesNotPrepareOrMutateBusyService(t *testing.T) {
	bundlePath := writeAlphaJarBundle(t, []bundleTestArtifact{
		{Service: "permission", Module: "alpha-permission", FileName: "alpha-permission.jar", Content: "new permission jar"},
		{Service: "file", Module: "alpha-file", FileName: "alpha-file.jar", Content: "new file jar"},
	})
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationLocks"] = map[string]any{
		"permission": map[string]any{
			"operation": "update-artifact",
			"service":   "permission",
			"actor":     "another-operator",
			"taskId":    "task-busy-permission",
			"startedAt": time.Now().UTC().Format(time.RFC3339),
		},
	}
	instance.Metadata = mustMetadata(t, metadata)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	)
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{}

	err := NewService(s, remote).UpdateArtifactBundle(context.Background(), ArtifactBundleUpdateRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-bundle-independent",
		BundleLocalPath: bundlePath, BundleFileName: filepath.Base(bundlePath), Concurrency: 2,
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected the busy permission target to fail")
	}
	after := deploymentsByService(s.deployments)
	if got := after["permission"]; got.Generation != before["permission"].Generation || got.SpecJSON != before["permission"].SpecJSON {
		t.Fatalf("busy permission deployment changed: before=%+v after=%+v", before["permission"], got)
	}
	if got := after["file"]; got.Generation != before["file"].Generation+1 || got.Status != "Accepted" {
		t.Fatalf("free file peer was not accepted independently: %+v", got)
	}
	commands := remote.joinedCommands()
	if strings.Contains(commands, "update-aifar-artifact-bundle-permission.sh") {
		t.Fatalf("busy permission artifact was prepared before its lock was acquired: %s", commands)
	}
	if !strings.Contains(commands, "update-aifar-artifact-bundle-file.sh") {
		t.Fatalf("free file artifact was not prepared under its own lock: %s", commands)
	}
	projected := metadataFromInstance(s.instances[0])
	if revisions := serviceRevisionsFromMetadata(projected); revisions["file"] != after["file"].CurrentRevision {
		t.Fatalf("free file accepted revision was not projected after busy peer failure: deployment=%+v metadata=%s", after["file"], s.instances[0].Metadata)
	}
	if hashes := mapFromMetadataValue(projected["serviceConfigHashes"]); strings.TrimSpace(fmt.Sprint(hashes["file"])) == "" {
		t.Fatalf("free file config hash contribution is missing: %s", s.instances[0].Metadata)
	}
	if rollouts := mapFromMetadataValue(projected["serviceRollouts"]); rollouts["file"] == nil {
		t.Fatalf("free file rollout audit contribution is missing: %s", s.instances[0].Metadata)
	}
}

func TestOlderSameServiceBundleCannotProjectOverAcceptedSuccessor(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance.Status = "install_failed"
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1}, map[string]int64{"permission": 3},
	) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	oldBundle := writeAlphaJarBundle(t, []bundleTestArtifact{{Service: "permission", Module: "alpha-permission", FileName: "alpha-permission.jar", Content: "old operation artifact"}})
	newBundle := writeAlphaJarBundle(t, []bundleTestArtifact{{Service: "permission", Module: "alpha-permission", FileName: "alpha-permission.jar", Content: "successor artifact"}})
	barrier := &firstProjectionCASStore{Store: db, blocked: make(chan struct{}), release: make(chan struct{})}
	server := store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}
	service := NewService(barrier, &fakeRemote{})
	oldDone := make(chan error, 1)
	go func() {
		oldDone <- service.UpdateArtifactBundle(context.Background(), ArtifactBundleUpdateRequest{
			Instance: instance, Server: server, Actor: "old-owner", TaskID: "task-bundle-old",
			BundleLocalPath: oldBundle, BundleFileName: filepath.Base(oldBundle), Concurrency: 1,
		}, fakeLogger{}, nil)
	}()
	select {
	case <-barrier.blocked:
	case err := <-oldDone:
		t.Fatalf("old bundle ended before projection barrier: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("old bundle did not reach projection barrier")
	}
	if _, err := db.RecoverAIFAROrchestrationLocks(instance.ID, "test bundle successor takeover"); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateArtifactBundle(context.Background(), ArtifactBundleUpdateRequest{
		Instance: instance, Server: server, Actor: "new-owner", TaskID: "task-bundle-new",
		BundleLocalPath: newBundle, BundleFileName: filepath.Base(newBundle), Concurrency: 1,
	}, fakeLogger{}, nil); err != nil {
		t.Fatal(err)
	}
	current, err := db.ListAIFARDeployments(instance.ID)
	if err != nil || len(current) != 1 {
		t.Fatalf("successor deployment=%+v err=%v", current, err)
	}
	successorRevision := current[0].CurrentRevision
	close(barrier.release)
	oldErr := <-oldDone
	var controlErr *deploymentControlError
	if !errors.As(oldErr, &controlErr) || controlErr.StableCode() != runtimeControlPlaneRepairCode {
		t.Fatalf("old bundle error=%v, want forward-only repair-required", oldErr)
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataFromInstance(saved)
	if saved.Status != "install_failed" || serviceRevisionsFromMetadata(metadata)["permission"] != successorRevision {
		t.Fatalf("old bundle regressed successor projection: deployment=%+v metadata=%s", current[0], saved.Metadata)
	}
	rollouts := mapFromMetadataValue(metadata["serviceRollouts"])
	permissionRollout := mapFromMetadataValue(rollouts["permission"])
	if fmt.Sprint(permissionRollout["releaseId"]) != successorRevision {
		t.Fatalf("old bundle regressed successor audit: %s", saved.Metadata)
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
		instances:   []store.AppInstance{instance},
		deployments: seedPerServiceDeployments(t, instance, map[string]int{"oauth": 1, "gateway": 1}, nil),
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
	if count := strings.Count(remote.joinedCommands(), "update-aifar-artifact-bundle-"); count != 2 {
		t.Fatalf("expected one locked preparation per changed service, commands=%s", remote.joinedCommands())
	}
	for _, want := range []string{
		`prepare_artifact 'oauth'`,
		`prepare_artifact 'gateway'`,
		`docker build -t "$image" "$app_dir"`,
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
	if endpoints, _ := manifest["endpoints"].(map[string]any); len(endpoints) != 0 {
		t.Fatalf("accepted bundle release must not fabricate observed endpoints, got %s", s.releases[0].ManifestJSON)
	}
	lastUpdate, ok := metadata["lastRollout"].(map[string]any)
	if !ok || lastUpdate["service"] != "bundle" || int(lastUpdate["deploymentConcurrency"].(float64)) != 3 {
		t.Fatalf("expected final metadata to point at bundle update, got %s", s.instances[0].Metadata)
	}
}

func TestServiceMarksFailedAIFARArtifactBundleRelease(t *testing.T) {
	bundlePath := writeAlphaJarBundle(t, []bundleTestArtifact{
		{Service: "oauth", Module: "alpha-oauth", FileName: "alpha-oauth.jar", Content: "new oauth jar"},
		{Service: "gateway", Module: "alpha-gateway", FileName: "alpha-gateway.jar", Content: "new gateway jar"},
	})
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances:   []store.AppInstance{instance},
		deployments: seedPerServiceDeployments(t, instance, map[string]int{"oauth": 1, "gateway": 1}, nil),
	}
	service := NewService(s, &fakeRemote{failCommandContains: "update-aifar-artifact-bundle-"})
	err := service.UpdateArtifactBundle(context.Background(), ArtifactBundleUpdateRequest{
		Instance:        instance,
		Server:          s.servers["srv-1"],
		Language:        "en",
		TaskID:          "task-bundle-failed",
		BundleLocalPath: bundlePath,
		BundleFileName:  filepath.Base(bundlePath),
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected artifact bundle update to fail")
	}
	assertLatestFailedRelease(t, s.releases)
}

func TestRollbackMutatesOnlySelectedServiceToNewerGeneration(t *testing.T) {
	instance := installedAIFARInstance(t)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"oauth": 1, "file": 1},
		map[string]int64{"oauth": 3, "file": 7},
	)
	targetReleaseID := "20260702T010203.000000000Z-rollout-oauth"
	targetArtifact := "/aifar/apps/admin/releases/" + targetReleaseID + "/services/oauth/artifact/oauth.jar"
	manifest, _ := json.Marshal(map[string]any{
		"schema": releaseManifestSchemaV2, "kind": "rollout", "releaseId": targetReleaseID,
		"changedServices": []string{"oauth"},
		"artifacts": map[string]any{"oauth": map[string]any{
			"file": "oauth.jar", "sha256": strings.Repeat("a", 64), "remotePath": targetArtifact,
		}},
	})
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
		releases: []store.AppRelease{{
			InstanceID: instance.ID, App: AppName, Version: appBundleVersion, ReleaseID: targetReleaseID,
			ServerID: "srv-1", Status: "success", ManifestJSON: string(manifest), CreatedAt: time.Now().Add(-time.Hour),
		}},
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{}

	err := NewService(s, remote).RollbackArtifact(context.Background(), ArtifactRollbackRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-rollback-oauth",
		TargetReleaseID: targetReleaseID, Services: []string{"oauth"}, Reason: "repair oauth",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := deploymentsByService(s.deployments)
	got := after["oauth"]
	if got.Generation != before["oauth"].Generation+1 || got.Status != "Accepted" {
		t.Fatalf("oauth rollback mutation=%+v, want next generation accepted", got)
	}
	var desired runtimeagent.DeploymentManifest
	if err := json.Unmarshal([]byte(got.SpecJSON), &desired); err != nil {
		t.Fatal(err)
	}
	if desired.Spec.PodRevision == before["oauth"].CurrentRevision || desired.Spec.Image != "aifar-oauth:"+desired.Spec.PodRevision {
		t.Fatalf("rollback did not create a newer target-service revision: %+v", desired.Spec)
	}
	if peer, want := after["file"], before["file"]; peer.Generation != want.Generation || peer.SpecJSON != want.SpecJSON {
		t.Fatalf("file deployment changed: before=%+v after=%+v", want, peer)
	}
	for _, forbidden := range []string{"reconcile-runtime", "runtime-spec.json", "restore_previous_runtime"} {
		if strings.Contains(remote.rollbackScript, forbidden) {
			t.Fatalf("rollback preparation script contains aggregate rollback action %q:\n%s", forbidden, remote.rollbackScript)
		}
	}
}

func TestRollbackBusyPeerDoesNotPrepareOrMutateBusyService(t *testing.T) {
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	metadata["orchestrationLocks"] = map[string]any{
		"permission": map[string]any{
			"operation": "update-artifact",
			"service":   "permission",
			"actor":     "another-operator",
			"taskId":    "task-busy-permission",
			"startedAt": time.Now().UTC().Format(time.RFC3339),
		},
	}
	instance.Metadata = mustMetadata(t, metadata)
	deployments := seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1, "file": 1},
		map[string]int64{"permission": 3, "file": 7},
	)
	const targetReleaseID = "20260702T010203.000000000Z-rollout-runtime"
	manifest, err := json.Marshal(map[string]any{
		"schema": releaseManifestSchemaV2, "kind": "rollout-bundle", "releaseId": targetReleaseID,
		"changedServices": []string{"permission", "file"},
		"artifacts": map[string]any{
			"permission": map[string]any{"file": "permission.jar", "sha256": strings.Repeat("a", 64), "remotePath": "/aifar/releases/permission.jar"},
			"file":       map[string]any{"file": "file.jar", "sha256": strings.Repeat("b", 64), "remotePath": "/aifar/releases/file.jar"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		deployments: deployments,
		releases: []store.AppRelease{{
			InstanceID: instance.ID, App: AppName, Version: appBundleVersion, ReleaseID: targetReleaseID,
			ServerID: "srv-1", Status: "success", ManifestJSON: string(manifest), CreatedAt: time.Now().Add(-time.Hour),
		}},
	}
	before := deploymentsByService(deployments)
	remote := &fakeRemote{}

	err = NewService(s, remote).RollbackArtifact(context.Background(), ArtifactRollbackRequest{
		Instance: instance, Server: s.servers["srv-1"], Actor: "operator", TaskID: "task-rollback-independent",
		TargetReleaseID: targetReleaseID, Services: []string{"permission", "file"}, Reason: "repair runtime services",
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected the busy permission target to fail")
	}
	after := deploymentsByService(s.deployments)
	if got := after["permission"]; got.Generation != before["permission"].Generation || got.SpecJSON != before["permission"].SpecJSON {
		t.Fatalf("busy permission deployment changed: before=%+v after=%+v", before["permission"], got)
	}
	if got := after["file"]; got.Generation != before["file"].Generation+1 || got.Status != "Accepted" {
		t.Fatalf("free file peer was not accepted independently: %+v", got)
	}
	commands := remote.joinedCommands()
	if strings.Contains(commands, "rollback-permission.sh") {
		t.Fatalf("busy permission rollback was prepared before its lock was acquired: %s", commands)
	}
	if !strings.Contains(commands, "rollback-file.sh") {
		t.Fatalf("free file rollback was not prepared under its own lock: %s", commands)
	}
	projected := metadataFromInstance(s.instances[0])
	if revisions := serviceRevisionsFromMetadata(projected); revisions["file"] != after["file"].CurrentRevision {
		t.Fatalf("free file accepted rollback revision was not projected after busy peer failure: deployment=%+v metadata=%s", after["file"], s.instances[0].Metadata)
	}
	if hashes := mapFromMetadataValue(projected["serviceConfigHashes"]); strings.TrimSpace(fmt.Sprint(hashes["file"])) == "" {
		t.Fatalf("free file rollback config hash contribution is missing: %s", s.instances[0].Metadata)
	}
	if rollouts := mapFromMetadataValue(projected["serviceRollouts"]); rollouts["file"] == nil {
		t.Fatalf("free file rollback audit contribution is missing: %s", s.instances[0].Metadata)
	}
}

func TestOlderSameServiceRollbackCannotProjectOverAcceptedSuccessor(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance := installedAIFARInstance(t)
	instance.Status = "install_failed"
	instance, err = db.SaveAppInstance(instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range seedPerServiceDeployments(t, instance,
		map[string]int{"permission": 1}, map[string]int64{"permission": 3},
	) {
		if _, err := db.SaveAIFARDeployment(deployment); err != nil {
			t.Fatal(err)
		}
	}
	const oldTarget = "20260701T010000.000000000Z-rollout-permission-old"
	const successorTarget = "20260701T020000.000000000Z-rollout-permission-new"
	for idx, releaseID := range []string{oldTarget, successorTarget} {
		manifest, err := json.Marshal(map[string]any{
			"schema": releaseManifestSchemaV2, "kind": "rollout", "releaseId": releaseID,
			"changedServices": []string{"permission"},
			"artifacts": map[string]any{"permission": map[string]any{
				"file": "permission.jar", "sha256": strings.Repeat(string(rune('a'+idx)), 64),
				"remotePath": "/aifar/releases/" + releaseID + "/permission.jar",
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppRelease(store.AppRelease{
			InstanceID: instance.ID, App: AppName, Version: appBundleVersion, ReleaseID: releaseID,
			ServerID: "srv-1", Status: "success", ManifestJSON: string(manifest), CreatedAt: time.Now().Add(time.Duration(idx-2) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	barrier := &firstProjectionCASStore{Store: db, blocked: make(chan struct{}), release: make(chan struct{})}
	server := store.Server{ID: "srv-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}
	service := NewService(barrier, &fakeRemote{})
	oldDone := make(chan error, 1)
	go func() {
		oldDone <- service.RollbackArtifact(context.Background(), ArtifactRollbackRequest{
			Instance: instance, Server: server, Actor: "old-owner", TaskID: "task-rollback-old",
			TargetReleaseID: oldTarget, Services: []string{"permission"}, Reason: "old rollback",
		}, fakeLogger{}, nil)
	}()
	select {
	case <-barrier.blocked:
	case err := <-oldDone:
		t.Fatalf("old rollback ended before projection barrier: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("old rollback did not reach projection barrier")
	}
	if _, err := db.RecoverAIFAROrchestrationLocks(instance.ID, "test rollback successor takeover"); err != nil {
		t.Fatal(err)
	}
	if err := service.RollbackArtifact(context.Background(), ArtifactRollbackRequest{
		Instance: instance, Server: server, Actor: "new-owner", TaskID: "task-rollback-new",
		TargetReleaseID: successorTarget, Services: []string{"permission"}, Reason: "new rollback",
	}, fakeLogger{}, nil); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)
	oldErr := <-oldDone
	var controlErr *deploymentControlError
	if !errors.As(oldErr, &controlErr) || controlErr.StableCode() != runtimeControlPlaneRepairCode {
		t.Fatalf("old rollback error=%v, want forward-only repair-required", oldErr)
	}
	deployments, err := db.ListAIFARDeployments(instance.ID)
	if err != nil || len(deployments) != 1 || deployments[0].CurrentRevision != successorTarget {
		t.Fatalf("canonical successor rollback=%+v err=%v", deployments, err)
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := metadataFromInstance(saved)
	if saved.Status != "install_failed" || serviceRevisionsFromMetadata(metadata)["permission"] != successorTarget {
		t.Fatalf("old rollback regressed successor projection: metadata=%s", saved.Metadata)
	}
	rollouts := mapFromMetadataValue(metadata["serviceRollouts"])
	permissionRollout := mapFromMetadataValue(rollouts["permission"])
	if fmt.Sprint(permissionRollout["rollbackTo"]) != successorTarget {
		t.Fatalf("old rollback regressed successor audit: %s", saved.Metadata)
	}
}

func TestServiceRollsBackAIFARServiceToReleaseArtifact(t *testing.T) {
	instance := installedAIFARInstance(t)
	targetReleaseID := "20260702T010203.000000000Z-rollout-oauth"
	targetArtifact := "/aifar/apps/admin/releases/" + targetReleaseID + "/services/oauth/artifact/oauth.jar"
	manifest, _ := json.Marshal(map[string]any{
		"schema":          releaseManifestSchemaV2,
		"kind":            "rollout",
		"releaseId":       targetReleaseID,
		"changedServices": []string{"oauth"},
		"artifacts": map[string]any{
			"oauth": map[string]any{
				"file":       "oauth.jar",
				"sha256":     strings.Repeat("a", 64),
				"remotePath": targetArtifact,
			},
		},
	})
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances:   []store.AppInstance{instance},
		deployments: seedPerServiceDeployments(t, instance, map[string]int{"oauth": 1}, nil),
		releases: []store.AppRelease{{
			InstanceID:   instance.ID,
			App:          AppName,
			Version:      appBundleVersion,
			ReleaseID:    targetReleaseID,
			ServerID:     "srv-1",
			Status:       "success",
			ManifestJSON: string(manifest),
			CreatedAt:    time.Now().Add(-time.Hour),
			ActivatedAt:  time.Now().Add(-time.Hour),
		}},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.RollbackArtifact(context.Background(), ArtifactRollbackRequest{
		Instance:        instance,
		Server:          s.servers["srv-1"],
		Language:        "en",
		Actor:           "admin",
		TargetReleaseID: targetReleaseID,
		Services:        []string{"oauth"},
		Reason:          "test rollback",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.joinedUploads(), "rollback-oauth.sh") || !strings.Contains(remote.joinedCommands(), "rollback-oauth.sh") {
		t.Fatalf("expected rollback script upload and run, uploads=%s commands=%s", remote.joinedUploads(), remote.joinedCommands())
	}
	for _, want := range []string{
		`TARGET_REVISION='` + targetReleaseID + `'`,
		`ARTIFACT_REMOTE='` + targetArtifact + `'`,
		`docker build -t "$image" "$APP_DIR"`,
	} {
		if !strings.Contains(remote.rollbackScript, want) {
			t.Fatalf("rollback script should contain %q:\n%s", want, remote.rollbackScript)
		}
	}
	if len(s.releases) != 2 {
		t.Fatalf("expected target and rollback release records, got %+v", s.releases)
	}
	rollback := s.releases[1]
	if rollback.Status != "success" || !strings.Contains(rollback.ManifestJSON, `"kind":"rollback"`) || !strings.Contains(rollback.ManifestJSON, `"rollbackTo":"`+targetReleaseID+`"`) {
		t.Fatalf("expected rollback release manifest, got %+v", rollback)
	}
	metadata := metadataFromInstance(s.instances[0])
	if revisions := serviceRevisionsFromMetadata(metadata); revisions["oauth"] != targetReleaseID {
		t.Fatalf("expected oauth metadata to point to target release, got %s", s.instances[0].Metadata)
	}
	if metadata["currentRevision"] == targetReleaseID {
		t.Fatalf("single-service rollback must not replace the instance-wide revision, got %s", s.instances[0].Metadata)
	}
}

func TestValidateArtifactRollbackRejectsAlreadyActiveRelease(t *testing.T) {
	const targetReleaseID = "release-target"
	instance := installedAIFARInstance(t)
	metadata := metadataFromInstance(instance)
	delete(metadata, "activeEndpoints")
	metadata["serviceRevisions"] = map[string]any{"oauth": targetReleaseID}
	instance.Metadata = mustMetadata(t, metadata)
	manifest := map[string]any{
		"kind":            "rollout",
		"changedServices": []string{"oauth"},
		"artifacts": map[string]any{
			"oauth": map[string]any{
				"file":       "oauth.jar",
				"sha256":     strings.Repeat("a", 64),
				"remotePath": "/aifar/apps/admin/releases/release-target/services/oauth/artifact/oauth.jar",
			},
		},
	}

	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeStore{instances: []store.AppInstance{instance}, releases: []store.AppRelease{{
		InstanceID: instance.ID, ReleaseID: targetReleaseID, Status: "success", ManifestJSON: string(raw),
	}}}, &fakeRemote{})
	err = service.ValidateArtifactRollback(ArtifactRollbackRequest{
		Instance: instance, Language: "en", TargetReleaseID: targetReleaseID, Services: []string{"oauth"}, Reason: "test validation",
	})
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("expected already active validation error, got %v", err)
	}
}

func TestValidateArtifactRollbackRejectsRollbackAuditRecord(t *testing.T) {
	const targetReleaseID = "release-target"
	instance := installedAIFARInstance(t)
	manifest := map[string]any{
		"kind":            "rollback",
		"changedServices": []string{"oauth"},
		"artifacts": map[string]any{
			"oauth": map[string]any{
				"file":       "oauth.jar",
				"sha256":     strings.Repeat("a", 64),
				"remotePath": "/aifar/apps/admin/releases/release-target/services/oauth/artifact/oauth.jar",
			},
		},
	}

	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeStore{instances: []store.AppInstance{instance}, releases: []store.AppRelease{{
		InstanceID: instance.ID, ReleaseID: targetReleaseID, Status: "success", ManifestJSON: string(raw),
	}}}, &fakeRemote{})
	err = service.ValidateArtifactRollback(ArtifactRollbackRequest{
		Instance: instance, Language: "en", TargetReleaseID: targetReleaseID, Services: []string{"oauth"}, Reason: "test validation",
	})
	if err == nil || !strings.Contains(err.Error(), "audit record") {
		t.Fatalf("expected audit record validation error, got %v", err)
	}
}

func TestRollbackArtifactRevalidatesAfterLock(t *testing.T) {
	const targetReleaseID = "release-target"
	const staleRevision = "release-current"
	staleRequestInstance := installedAIFARInstance(t)
	staleRequestInstance.Metadata = mustMetadata(t, map[string]any{
		"orchestrationModel": orchestrationModelK8sLikeV1,
		"currentRevision":    staleRevision,
		"serviceRevisions":   map[string]any{"oauth": staleRevision},
	})
	lockedInstance := staleRequestInstance
	lockedInstance.Metadata = mustMetadata(t, map[string]any{
		"orchestrationModel": orchestrationModelK8sLikeV1,
		"currentRevision":    targetReleaseID,
		"serviceRevisions":   map[string]any{"oauth": targetReleaseID},
	})
	manifest, err := json.Marshal(map[string]any{
		"schema":          releaseManifestSchemaV2,
		"kind":            "rollout",
		"releaseId":       targetReleaseID,
		"changedServices": []string{"oauth"},
		"artifacts": map[string]any{
			"oauth": map[string]any{
				"file":       "oauth.jar",
				"sha256":     strings.Repeat("a", 64),
				"remotePath": "/aifar/apps/admin/releases/release-target/services/oauth/artifact/oauth.jar",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseStore := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{staleRequestInstance},
		releases: []store.AppRelease{{
			InstanceID:   lockedInstance.ID,
			App:          AppName,
			Version:      appBundleVersion,
			ReleaseID:    targetReleaseID,
			ServerID:     "srv-1",
			Status:       "success",
			ManifestJSON: string(manifest),
			CreatedAt:    time.Now().Add(-time.Hour),
			ActivatedAt:  time.Now().Add(-time.Hour),
		}},
	}
	s := &rollbackLockRaceStore{fakeStore: baseStore, instanceAfterLock: lockedInstance}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err = service.RollbackArtifact(context.Background(), ArtifactRollbackRequest{
		Instance:        staleRequestInstance,
		Server:          s.servers["srv-1"],
		Language:        "en",
		Actor:           "admin",
		TargetReleaseID: targetReleaseID,
		Services:        []string{"oauth"},
		Reason:          "test lock-time revalidation",
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected lock-time revalidation to reject the active target")
	}
	if uploads := remote.joinedUploads(); uploads != "" {
		t.Fatalf("lock-time rejection must not upload rollback scripts, uploads=%s", uploads)
	}
	if commands := remote.joinedCommands(); strings.Contains(commands, "rollback-oauth.sh") {
		t.Fatalf("lock-time rejection must not run rollback scripts, commands=%s", commands)
	}
	for _, release := range s.releases {
		if release.Status == "success" && strings.Contains(release.ManifestJSON, `"kind":"rollback"`) {
			t.Fatalf("lock-time rejection must not record a successful rollback release, got %+v", release)
		}
	}
}

func TestInspectArtifactRollbackEligibilityByServiceRevision(t *testing.T) {
	const targetReleaseID = "release-target"
	const currentReleaseID = "release-current"
	instance := installedAIFARInstance(t)
	instance.Metadata = mustMetadata(t, map[string]any{
		"serviceRevisions": map[string]any{
			"oauth":   targetReleaseID,
			"gateway": currentReleaseID,
		},
	})
	completeManifest := func(kind string) map[string]any {
		return map[string]any{
			"kind":            kind,
			"changedServices": []string{"oauth", "gateway"},
			"artifacts": map[string]any{
				"oauth": map[string]any{
					"file":       "oauth.jar",
					"sha256":     strings.Repeat("a", 64),
					"remotePath": "/aifar/apps/admin/releases/release-target/services/oauth/artifact/oauth.jar",
				},
				"gateway": map[string]any{
					"file":       "gateway.jar",
					"sha256":     strings.Repeat("b", 64),
					"remotePath": "/aifar/apps/admin/releases/release-target/services/gateway/artifact/gateway.jar",
				},
			},
		}
	}

	module := Module{}
	inspection := module.InspectArtifactRollback(context.Background(), registry.ArtifactRollbackInspectionRequest{
		Instance: instance,
		Release:  store.AppRelease{ReleaseID: targetReleaseID, Status: "success"},
		Manifest: completeManifest("rollout-bundle"),
	})
	if got, want := inspection.CurrentServices, []string{"oauth"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("current services = %#v, want %#v", got, want)
	}
	if got, want := inspection.RollbackServices, []string{"gateway"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rollback services = %#v, want %#v", got, want)
	}
	if inspection.RollbackUnavailableReason != "" {
		t.Fatalf("rollback unavailable reason = %q, want empty", inspection.RollbackUnavailableReason)
	}

	allCurrent := installedAIFARInstance(t)
	allCurrent.Metadata = mustMetadata(t, map[string]any{
		"serviceRevisions": map[string]any{
			"oauth":   targetReleaseID,
			"gateway": targetReleaseID,
		},
	})
	for _, test := range []struct {
		name     string
		instance store.AppInstance
		release  store.AppRelease
		manifest map[string]any
		reason   string
		current  []string
	}{
		{
			name:     "rollback audit record",
			instance: instance,
			release:  store.AppRelease{ReleaseID: targetReleaseID, Status: "success"},
			manifest: completeManifest("rollback"),
			reason:   "ROLLBACK_RECORD",
		},
		{
			name:     "all changed services current",
			instance: allCurrent,
			release:  store.AppRelease{ReleaseID: targetReleaseID, Status: "success"},
			manifest: completeManifest("rollout-bundle"),
			reason:   "ALREADY_ACTIVE",
		},
		{
			name:     "missing artifact metadata",
			instance: instance,
			release:  store.AppRelease{ReleaseID: targetReleaseID, Status: "success"},
			manifest: map[string]any{
				"kind":            "rollout-bundle",
				"changedServices": []string{"oauth", "gateway"},
				"artifacts": map[string]any{
					"oauth": map[string]any{
						"file":       "oauth.jar",
						"sha256":     strings.Repeat("a", 64),
						"remotePath": "/aifar/apps/admin/releases/release-target/services/oauth/artifact/oauth.jar",
					},
					"gateway": map[string]any{
						"file": "gateway.jar",
					},
				},
			},
			reason:  "ARTIFACT_UNAVAILABLE",
			current: []string{"oauth"},
		},
		{
			name:     "short artifact checksum",
			instance: instance,
			release:  store.AppRelease{ReleaseID: targetReleaseID, Status: "success"},
			manifest: func() map[string]any {
				manifest := completeManifest("rollout-bundle")
				manifest["artifacts"].(map[string]any)["gateway"].(map[string]any)["sha256"] = "abc123"
				return manifest
			}(),
			reason: "ARTIFACT_UNAVAILABLE",
		},
		{
			name:     "non hexadecimal artifact checksum",
			instance: instance,
			release:  store.AppRelease{ReleaseID: targetReleaseID, Status: "success"},
			manifest: func() map[string]any {
				manifest := completeManifest("rollout-bundle")
				manifest["artifacts"].(map[string]any)["gateway"].(map[string]any)["sha256"] = strings.Repeat("z", 64)
				return manifest
			}(),
			reason: "ARTIFACT_UNAVAILABLE",
		},
		{
			name:     "missing artifact remote path",
			instance: instance,
			release:  store.AppRelease{ReleaseID: targetReleaseID, Status: "success"},
			manifest: func() map[string]any {
				manifest := completeManifest("rollout-bundle")
				delete(manifest["artifacts"].(map[string]any)["gateway"].(map[string]any), "remotePath")
				return manifest
			}(),
			reason: "ARTIFACT_UNAVAILABLE",
		},
		{
			name:     "failed release",
			instance: instance,
			release:  store.AppRelease{ReleaseID: targetReleaseID, Status: "failed"},
			manifest: completeManifest("rollout-bundle"),
			reason:   "ARTIFACT_UNAVAILABLE",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspection := module.InspectArtifactRollback(context.Background(), registry.ArtifactRollbackInspectionRequest{
				Instance: test.instance,
				Release:  test.release,
				Manifest: test.manifest,
			})
			if inspection.RollbackUnavailableReason != test.reason {
				t.Fatalf("rollback unavailable reason = %q, want %q", inspection.RollbackUnavailableReason, test.reason)
			}
			if len(inspection.RollbackServices) != 0 {
				t.Fatalf("rollback services = %#v, want none", inspection.RollbackServices)
			}
			if test.current != nil && !reflect.DeepEqual(inspection.CurrentServices, test.current) {
				t.Fatalf("current services = %#v, want %#v", inspection.CurrentServices, test.current)
			}
		})
	}
}

func TestServiceMarksFailedAIFARArtifactRollbackRelease(t *testing.T) {
	instance := installedAIFARInstance(t)
	targetReleaseID := "20260702T010203.000000000Z-rollout-oauth"
	targetArtifact := "/aifar/apps/admin/releases/" + targetReleaseID + "/services/oauth/artifact/oauth.jar"
	manifest, _ := json.Marshal(map[string]any{
		"schema":          releaseManifestSchemaV2,
		"kind":            "rollout",
		"releaseId":       targetReleaseID,
		"changedServices": []string{"oauth"},
		"artifacts": map[string]any{
			"oauth": map[string]any{
				"file":       "oauth.jar",
				"sha256":     strings.Repeat("a", 64),
				"remotePath": targetArtifact,
			},
		},
	})
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances:   []store.AppInstance{instance},
		deployments: seedPerServiceDeployments(t, instance, map[string]int{"oauth": 1}, nil),
		releases: []store.AppRelease{{
			InstanceID: instance.ID, App: AppName, Version: appBundleVersion,
			ReleaseID: targetReleaseID, ServerID: "srv-1", Status: "success",
			ManifestJSON: string(manifest), CreatedAt: time.Now().Add(-time.Hour), ActivatedAt: time.Now().Add(-time.Hour),
		}},
	}
	service := NewService(s, &fakeRemote{failCommandContains: "rollback-oauth.sh"})
	err := service.RollbackArtifact(context.Background(), ArtifactRollbackRequest{
		Instance: instance, Server: s.servers["srv-1"], Language: "en", Actor: "admin",
		TaskID: "task-rollback-failed", TargetReleaseID: targetReleaseID,
		Services: []string{"oauth"}, Reason: "test rollback failure",
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected artifact rollback to fail")
	}
	assertLatestFailedRelease(t, s.releases)
}

func assertLatestFailedRelease(t *testing.T, releases []store.AppRelease) {
	t.Helper()
	if len(releases) == 0 {
		t.Fatal("expected a release record")
	}
	release := releases[len(releases)-1]
	if release.Status != "failed" {
		t.Fatalf("expected failed release status, got %+v", release)
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(release.ManifestJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["status"] != "failed" || manifest["phase"] != "failed" || strings.TrimSpace(fmt.Sprint(manifest["error"])) == "" || strings.TrimSpace(fmt.Sprint(manifest["failedAt"])) == "" {
		t.Fatalf("expected failure details in release manifest, got %s", release.ManifestJSON)
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

func TestServiceDeleteUninstallsRuntimeAgentBeforeRemovingService(t *testing.T) {
	instance := installedAIFARInstance(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{statusStdout: strings.Join([]string{
		"status=missing",
		"installRootExists=false",
	}, "\n")}
	service := NewService(s, remote)
	err := service.Delete(context.Background(), DeleteRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	commands := remote.joinedCommands()
	agentIndex := strings.Index(commands, "AIFAR_AGENT_UNINSTALL")
	serviceIndex := strings.Index(commands, "AIFAR_SERVICE_UNINSTALL")
	if agentIndex < 0 || serviceIndex < 0 || agentIndex > serviceIndex {
		t.Fatalf("expected agent uninstall before service uninstall, commands:\n%s", commands)
	}
	if len(s.instances) != 0 {
		t.Fatalf("expected instance record to be deleted, got %+v", s.instances)
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
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "runtime-v2", Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}, fakeLogger{}, nil)
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

func TestModuleValidateInstallAllowsDockerWithoutCompose(t *testing.T) {
	root := createAIFARBundle(t)
	dockerInstance := store.AppInstance{
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
	}
	if !dockerRuntimeReady(dockerInstance) {
		t.Fatal("expected Docker runtime readiness to ignore missing composeVersion")
	}
	store := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{dockerInstance},
	}
	module := NewModule(store, &fakeRemote{})
	if err := module.service.ensureDockerRuntimeReady("srv-1", copyFor("en")); err != nil {
		t.Fatalf("expected service Docker runtime check to pass without Compose, got %v", err)
	}
	err := module.ValidateInstall(context.Background(), registry.InstallRequest{
		Version:    "latest",
		ServerID:   "srv-1",
		Language:   "en",
		Parameters: aifarModuleValidationParams(),
	}, aifarModuleValidationResources(root))
	if err != nil {
		t.Fatalf("expected Docker Engine validation to pass without Compose, got %v", err)
	}
}

func TestSelectBundleRequiresRuntimeV2ManifestResource(t *testing.T) {
	root := createAIFARBundle(t)
	resources := []store.Resource{
		{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")},
		{App: "aifar", Part: "backend", Version: "runtime-v2", Path: filepath.Join(root, appBundleVersion, bundleManifestName)},
	}
	bundle, err := SelectBundle(resources, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Version != "runtime-v2" || filepath.Base(bundle.Root) != "runtime-v2" || filepath.Base(bundle.AppDir) != "services" {
		t.Fatalf("expected runtime-v2 bundle, got %+v", bundle)
	}
	if _, err := SelectBundle(resources, "docker-apps"); err == nil {
		t.Fatal("expected docker-apps to be rejected as an installable AIFAR version")
	}
}

func TestCreateBundleArchiveExcludesBundledNacos(t *testing.T) {
	root := createAIFARBundle(t)
	nacosDir := filepath.Join(root, appBundleVersion, appBundleDir, "nacos")
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
		Version:      appBundleVersion,
		Root:         filepath.Join(root, appBundleVersion),
		AppDir:       filepath.Join(root, appBundleVersion, appBundleDir),
		ImageDir:     filepath.Join(root, appBundleVersion, imageBundleDir),
		RuntimeDir:   filepath.Join(root, appBundleVersion, runtimeBundleDir),
		ManifestPath: filepath.Join(root, appBundleVersion, bundleManifestName),
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
		if strings.HasPrefix(header.Name, "services/nacos") || strings.HasSuffix(header.Name, "/docker-compose.yaml") {
			t.Fatalf("archive should exclude legacy runtime assets, found %s", header.Name)
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
	return []store.Resource{{App: "aifar", Part: "backend", Version: "runtime-v2", Path: filepath.Join(root, appBundleVersion, bundleManifestName)}}
}

func aifarModuleValidationParams() map[string]any {
	return map[string]any{
		"nacosHost": "10.0.0.50",
	}
}

func TestServiceExpectationsUseOnlyPositiveDesiredReplicas(t *testing.T) {
	metadata := map[string]any{
		"services": []any{"alpha-gateway", "alpha-oauth", "web-vue3", "alpha-unused"},
		"desiredReplicas": map[string]any{
			"alpha-gateway": 1,
			"alpha-oauth":   2,
			"web-vue3":      1,
			"alpha-unused":  0,
		},
	}
	got := serviceExpectations(metadata)
	want := []serviceExpectation{
		{Name: "alpha-gateway", Replicas: 1},
		{Name: "alpha-oauth", Replicas: 2},
		{Name: "web-vue3", Replicas: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected selected positive replicas %#v, got %#v", want, got)
	}
}

func TestServiceExpectationsIgnoreLegacyDesiredReplicasOutsideSelectedServices(t *testing.T) {
	metadata := map[string]any{
		"services": []any{"oauth", "permission", "system", "gateway", "web-vue3"},
		"desiredReplicas": map[string]any{
			"contacts": 1, "file": 1, "gateway": 1, "im": 1, "meeting": 1,
			"message": 1, "oauth": 1, "permission": 1, "system": 1, "web-vue3": 1,
		},
	}
	want := []serviceExpectation{
		{Name: "oauth", Replicas: 1},
		{Name: "permission", Replicas: 1},
		{Name: "system", Replicas: 1},
		{Name: "gateway", Replicas: 1},
		{Name: "web-vue3", Replicas: 1},
	}
	if got := serviceExpectations(metadata); !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy replica entries outside selected services must be ignored: want %#v, got %#v", want, got)
	}
}

func TestServiceExpectationsTreatExplicitEmptyDesiredReplicasAsOffline(t *testing.T) {
	metadata := map[string]any{
		"services":        []any{"alpha-gateway", "web-vue3"},
		"desiredReplicas": map[string]any{},
	}
	if got := serviceExpectations(metadata); len(got) != 0 {
		t.Fatalf("explicit empty desired replicas should be offline, got %#v", got)
	}
}

func TestServiceExpectationsFallBackToSelectedServices(t *testing.T) {
	metadata := map[string]any{"services": []any{"alpha-gateway", "web-vue3"}}
	want := []serviceExpectation{{Name: "alpha-gateway", Replicas: 1}, {Name: "web-vue3", Replicas: 1}}
	if got := serviceExpectations(metadata); !reflect.DeepEqual(got, want) {
		t.Fatalf("expected selected-service fallback %#v, got %#v", want, got)
	}
}

func TestServiceChecksAIFARServiceAndUpdatesStatus(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-1",
		App:      "aifar",
		Version:  "runtime-v2",
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

func TestServiceCheckPassesSelectedServiceExpectationsToInspector(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-selected",
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"installRoot":"/aifar/apps/admin","services":["alpha-gateway","web-vue3","alpha-unused"],"desiredReplicas":{"alpha-gateway":1,"web-vue3":1,"alpha-unused":0}}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{statusStdout: strings.Join([]string{
		"status=running",
		"installRootExists=true",
		"expectedContainers=2",
		"missingContainers=0",
		"ingressRunning=true",
	}, "\n")}
	service := NewService(s, remote)
	if _, err := service.Check(context.Background(), CheckRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en"}, fakeLogger{}, nil); err != nil {
		t.Fatal(err)
	}
	command := remote.joinedCommands()
	if !strings.Contains(command, `EXPECTED_SERVICES='alpha-gateway=1 web-vue3=1'`) {
		t.Fatalf("service check should pass only selected positive replicas to inspector:\n%s", command)
	}
	if strings.Contains(command, "alpha-unused=0") {
		t.Fatalf("offline service should not be inspected:\n%s", command)
	}
}

func TestModuleCollectorCheckSkipsAutoscaleMetricsCollection(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-collector",
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"installRoot":"/aifar/apps/admin","services":["gateway","web-vue3"],"desiredReplicas":{"gateway":1,"web-vue3":1}}`,
	}
	server := store.Server{ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": server},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{statusStdout: strings.Join([]string{
		"status=running",
		"installRootExists=true",
		"expectedContainers=2",
		"missingContainers=0",
		"ingressRunning=true",
	}, "\n")}
	module := NewModule(s, remote)

	result, err := module.Check(context.Background(), registry.CheckRequest{
		Instance: instance,
		Server:   server,
		Language: "en",
		Actor:    "collector",
	}, registry.RunContext{Log: fakeLogger{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" {
		t.Fatalf("expected running status, got %+v", result)
	}
	if strings.Contains(remote.joinedCommands(), "AIFAR_AUTOSCALE_STATUS") {
		t.Fatalf("collector health check must not be blocked by optional autoscale metrics:\n%s", remote.joinedCommands())
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

func TestStatusCommandChecksOnlyExpectedDynamicServices(t *testing.T) {
	command := statusCommand("/aifar/apps/admin", []serviceExpectation{
		{Name: "alpha-gateway", Replicas: 1},
		{Name: "alpha-oauth", Replicas: 2},
		{Name: "web-vue3", Replicas: 1},
	})
	for _, want := range []string{
		`EXPECTED_SERVICES='alpha-gateway=1 alpha-oauth=2 web-vue3=1'`,
		`MISSING=$((MISSING + desired - service_healthy))`,
		`[ "$MISSING" -eq 0 ]`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("status command should contain %q:\n%s", want, command)
		}
	}
	if strings.Contains(command, "permission=") || strings.Contains(command, "message=") {
		t.Fatalf("status command should not inspect unselected services:\n%s", command)
	}
}

func TestParseStatusOutputIncludesExpectedAndMissingContainers(t *testing.T) {
	status := parseStatusOutput("status=degraded\nexpectedContainers=5\nmissingContainers=1\n")
	if status.ExpectedContainers != 5 || status.MissingContainers != 1 {
		t.Fatalf("expected replica diagnostics, got %+v", status)
	}
}

func TestStatusCommandScansK8sLikePodsAndAgentRuntime(t *testing.T) {
	command := statusCommand("/aifar/apps/admin", serviceExpectations(nil))
	for _, want := range []string{
		`MODEL_FILE="$INSTALL_ROOT/.aifar/model.json"`,
		`[ "$MODEL" = "agent-runtime-v2" ]`,
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
	bundleRoot := filepath.Join(root, appBundleVersion)
	appDir := filepath.Join(bundleRoot, appBundleDir)
	imageDir := filepath.Join(bundleRoot, imageBundleDir)
	runtimeDir := filepath.Join(bundleRoot, runtimeBundleDir)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema":  appBundleSchema,
		"version": appBundleVersion,
		"images":  []string{"openjre-rocky-21.tar", "nginx-stable-alpine.tar"},
		"layout": map[string]string{
			"services": appBundleDir,
			"images":   imageBundleDir,
			"runtime":  runtimeBundleDir,
		},
		"files": map[string]any{
			bundleManifestName: map[string]string{"part": "backend"},
		},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, bundleManifestName), manifestData, 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(runtimeDir, runtimeDefaultsName), []byte("APP_RESTART_POLICY=unless-stopped\nAPP_HEALTH_INTERVAL=15s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, service := range serviceOrder {
		dir := filepath.Join(appDir, service)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		definition := serviceDefinition{
			Schema: "aifar-runtime-service-v1", Name: service, Kind: "java",
			ApplicationName: "alpha-" + service, Port: serviceDefaultPort(service, defaultGatewayPort, defaultWebPort),
			ArtifactExtensions: []string{".jar"}, HealthPath: "/actuator/health/readiness", AffinityPolicy: "round-robin",
		}
		if service == "gateway" {
			definition.Required = true
			definition.Role = "gateway"
			definition.AffinityPolicy = "stable"
		}
		if service == "file" {
			definition.AffinityPolicy = "stable"
		}
		if service == "web-vue3" {
			definition.Kind = "web"
			definition.ApplicationName = ""
			definition.Required = true
			definition.Role = "web"
			definition.ArtifactExtensions = []string{".zip"}
			definition.HealthPath = "/"
		}
		definitionData, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, serviceDefinitionName), definitionData, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
