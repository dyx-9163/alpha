package runtimeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	deploymentConditionAccepted    = "Accepted"
	deploymentConditionProgressing = "Progressing"
	deploymentConditionAvailable   = "Available"
	deploymentConditionDegraded    = "Degraded"
	deploymentConditionOffline     = "Offline"
)

var deploymentRetryBackoff = [...]time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

type controllerTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type controllerClock interface {
	Now() time.Time
	NewTimer(time.Duration) controllerTimer
}

type realControllerClock struct{}

func (realControllerClock) Now() time.Time { return time.Now() }

func (realControllerClock) NewTimer(duration time.Duration) controllerTimer {
	return realControllerTimer{Timer: time.NewTimer(duration)}
}

type realControllerTimer struct{ *time.Timer }

func (timer realControllerTimer) C() <-chan time.Time { return timer.Timer.C }

type serviceController struct {
	manager    *Manager
	instanceID string
	service    string
	wake       chan struct{}
	stop       chan struct{}

	mu                  sync.Mutex
	cancel              context.CancelFunc
	activeGeneration    int64
	requestedGeneration int64
	rejectedGeneration  int64
	failures            int
	stopped             bool
}

func (m *Manager) AcceptDeployment(ctx context.Context, manifest DeploymentManifest) (DeploymentAcceptance, error) {
	if err := ctx.Err(); err != nil {
		return DeploymentAcceptance{}, err
	}
	manifest = NormalizeDeploymentManifest(manifest)
	maintenance := m.instanceMaintenanceLock(manifest.Metadata.InstanceID)
	maintenance.RLock()
	defer maintenance.RUnlock()
	if err := ctx.Err(); err != nil {
		return DeploymentAcceptance{}, err
	}
	if _, err := m.manifestStore.GetInstance(manifest.Metadata.InstanceID); err != nil {
		return DeploymentAcceptance{}, err
	}
	acceptance, err := m.manifestStore.Put(manifest)
	if err != nil {
		return acceptance, err
	}
	if m.controllerAfterManifestPut != nil {
		m.controllerAfterManifestPut(manifest)
	}

	key := endpointKey(manifest.Metadata.InstanceID, manifest.Metadata.Name)
	m.controllerMu.Lock()
	cached, cachedOK := m.manifestCache[key]
	previous, stateOK := m.controllerStates[key]
	if (cachedOK && cached.Metadata.Generation > manifest.Metadata.Generation) || (stateOK && previous.Generation > manifest.Metadata.Generation) {
		m.controllerMu.Unlock()
		return acceptance, nil
	}
	controller := m.controllerForLocked(manifest.Metadata.InstanceID, manifest.Metadata.Name)
	alreadyObserved := cachedOK && cached.Metadata.Generation == manifest.Metadata.Generation && stateOK && previous.Generation == manifest.Metadata.Generation && previous.SpecHash == acceptance.SpecHash && previous.ObservedGeneration >= manifest.Metadata.Generation
	if !cachedOK || cached.Metadata.Generation < manifest.Metadata.Generation {
		m.manifestCache[key] = manifest
	}
	if !stateOK || previous.Generation < manifest.Metadata.Generation || previous.SpecHash != acceptance.SpecHash {
		m.controllerStates[key] = transitionDeploymentState(previous, manifest, acceptance.SpecHash, deploymentConditionAccepted, "ManifestAccepted", 0, 0, m.controllerClock.Now())
	}
	m.controllerMu.Unlock()

	controller.supersede(manifest.Metadata.Generation)
	if !alreadyObserved {
		controller.enqueue()
	}
	return acceptance, nil
}

func (m *Manager) enqueuePersistedDeployment(manifest DeploymentManifest) error {
	manifest = NormalizeDeploymentManifest(manifest)
	hash, err := DeploymentManifestSpecHash(manifest)
	if err != nil {
		return err
	}
	key := endpointKey(manifest.Metadata.InstanceID, manifest.Metadata.Name)
	m.controllerMu.Lock()
	m.manifestCache[key] = manifest
	controller := m.controllerForLocked(manifest.Metadata.InstanceID, manifest.Metadata.Name)
	previous := m.controllerStates[key]
	m.controllerStates[key] = transitionDeploymentState(previous, manifest, hash, deploymentConditionAccepted, "ManifestAccepted", 0, 0, m.controllerClock.Now())
	m.controllerMu.Unlock()
	controller.supersede(manifest.Metadata.Generation)
	controller.enqueue()
	return nil
}

func (m *Manager) ReconcileDeployment(instanceID, serviceName string) {
	instanceID = strings.TrimSpace(instanceID)
	serviceName = normalizeServiceManifestName(serviceName)
	if validateInstanceManifestName(instanceID) != nil || validateServiceManifestName(serviceName) != nil {
		return
	}
	m.controllerMu.Lock()
	controller := m.controllerForLocked(instanceID, serviceName)
	m.controllerMu.Unlock()
	controller.resetBackoff()
	controller.enqueue()
}

func (m *Manager) DeploymentState(instanceID, serviceName string) (DeploymentState, bool) {
	key := endpointKey(strings.TrimSpace(instanceID), normalizeServiceManifestName(serviceName))
	m.controllerMu.Lock()
	defer m.controllerMu.Unlock()
	state, ok := m.controllerStates[key]
	if !ok {
		return DeploymentState{}, false
	}
	state.Conditions = append([]DeploymentCondition(nil), state.Conditions...)
	return state, true
}

func (m *Manager) RuntimeInstanceSnapshot(instanceID string) (RuntimeInstanceSnapshot, error) {
	instanceID = strings.TrimSpace(instanceID)
	maintenance := m.instanceMaintenanceLock(instanceID)
	maintenance.RLock()
	defer maintenance.RUnlock()
	instance, err := m.manifestStore.GetInstance(instanceID)
	if err != nil {
		return RuntimeInstanceSnapshot{}, err
	}
	manifests, err := m.manifestStore.List(instanceID)
	if err != nil {
		return RuntimeInstanceSnapshot{}, err
	}
	if len(manifests) > 256 {
		return RuntimeInstanceSnapshot{}, errors.New("runtime instance snapshot exceeds deployment limit")
	}
	snapshot := RuntimeInstanceSnapshot{Instance: instance, Deployments: make([]RuntimeDeploymentSnapshot, 0, len(manifests))}
	for _, manifest := range manifests {
		hash, hashErr := DeploymentManifestSpecHash(manifest)
		if hashErr != nil {
			return RuntimeInstanceSnapshot{}, hashErr
		}
		state, exists := m.DeploymentState(instanceID, manifest.Metadata.Name)
		if !exists {
			state = DeploymentState{
				InstanceID: instanceID, ServiceName: manifest.Metadata.Name,
				Generation: manifest.Metadata.Generation, SpecHash: hash,
				DesiredReplicas: manifest.Spec.Replicas,
			}
		}
		snapshot.Deployments = append(snapshot.Deployments, RuntimeDeploymentSnapshot{
			ServiceName: manifest.Metadata.Name, ManifestGeneration: manifest.Metadata.Generation,
			ManifestSpecHash: hash, StateGeneration: state.Generation,
			ObservedGeneration: state.ObservedGeneration, StateSpecHash: state.SpecHash,
			DesiredReplicas: state.DesiredReplicas,
		})
	}
	return snapshot, nil
}

type legacyRuntimeArchiveOps struct {
	lstat      func(string) (os.FileInfo, error)
	open       func(string) (*os.File, error)
	createTemp func(string, string) (*os.File, error)
	rename     func(string, string) error
	remove     func(string) error
	chmodFile  func(*os.File, os.FileMode) error
	syncFile   func(*os.File) error
	syncDir    func(string) error
}

func defaultLegacyRuntimeArchiveOps() legacyRuntimeArchiveOps {
	return legacyRuntimeArchiveOps{
		lstat: os.Lstat, open: os.Open, createTemp: os.CreateTemp,
		rename: os.Rename, remove: os.Remove,
		chmodFile: func(file *os.File, mode os.FileMode) error {
			if runtime.GOOS == "windows" {
				return os.Chmod(file.Name(), mode)
			}
			return file.Chmod(mode)
		},
		syncFile: func(file *os.File) error {
			if runtime.GOOS == "windows" {
				return nil
			}
			return file.Sync()
		},
		syncDir: func(directory string) error {
			if runtime.GOOS == "windows" {
				return nil
			}
			file, err := os.Open(directory)
			if err != nil {
				return err
			}
			defer file.Close()
			return file.Sync()
		},
	}
}

func (m *Manager) ArchiveLegacyRuntimeSpec(instanceID, expectedSHA256 string) error {
	instanceID = strings.TrimSpace(instanceID)
	maintenance := m.instanceMaintenanceLock(instanceID)
	maintenance.Lock()
	defer maintenance.Unlock()
	config, err := m.manifestStore.GetInstance(instanceID)
	if err != nil {
		return err
	}
	return durableArchiveLegacyRuntimeSpec(config, expectedSHA256, defaultLegacyRuntimeArchiveOps())
}

func durableArchiveLegacyRuntimeSpec(config InstanceConfig, expectedSHA256 string, ops legacyRuntimeArchiveOps) (returnErr error) {
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	decoded, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("legacy runtime archive hash is invalid")
	}
	directory := filepath.Join(config.InstallRoot, "runtime", "agent")
	legacyPath := filepath.Join(directory, "runtime-spec.json")
	backupPath := filepath.Join(directory, "runtime-spec.legacy-readonly.json")
	if err := validateArchivePathComponents(directory, ops.lstat); err != nil {
		return fmt.Errorf("validate legacy runtime archive path: %w", err)
	}

	backupExists, err := verifiedRegularFile(backupPath, expectedSHA256, ops)
	if err != nil {
		return err
	}
	legacyExists, err := verifiedRegularFile(legacyPath, expectedSHA256, ops)
	if err != nil {
		return err
	}
	if !backupExists && !legacyExists {
		return errors.New("verified legacy runtime source is unavailable")
	}
	if !backupExists {
		source, err := ops.open(legacyPath)
		if err != nil {
			return err
		}
		defer source.Close()
		temporary, err := ops.createTemp(directory, ".runtime-spec.legacy-*")
		if err != nil {
			return err
		}
		temporaryPath := temporary.Name()
		defer func() {
			_ = temporary.Close()
			if cleanupErr := ops.remove(temporaryPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				returnErr = fmt.Errorf("legacy runtime archive cleanup failed: %w", errors.Join(returnErr, cleanupErr))
			}
		}()
		if err := temporary.Chmod(0o400); err != nil {
			return err
		}
		if _, err := io.Copy(temporary, source); err != nil {
			return err
		}
		if _, err := temporary.Seek(0, io.SeekStart); err != nil {
			return err
		}
		hash := sha256.New()
		written, err := io.Copy(hash, io.LimitReader(temporary, (4<<20)+1))
		if err != nil || written > 4<<20 || hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
			return errors.New("legacy runtime archive copy verification failed")
		}
		if err := ops.syncFile(temporary); err != nil {
			return err
		}
		if err := temporary.Close(); err != nil {
			return err
		}
		if err := ops.rename(temporaryPath, backupPath); err != nil {
			return err
		}
		if err := ops.syncDir(directory); err != nil {
			return err
		}
		backupExists = true
	}
	if backupExists {
		if err := makeVerifiedLegacyBackupReadOnly(backupPath, expectedSHA256, ops); err != nil {
			return err
		}
		if err := ops.syncDir(directory); err != nil {
			return err
		}
	}
	if legacyExists {
		if err := ops.remove(legacyPath); err != nil {
			return err
		}
		if err := ops.syncDir(directory); err != nil {
			return err
		}
	}
	return nil
}

func makeVerifiedLegacyBackupReadOnly(filePath, expectedSHA256 string, ops legacyRuntimeArchiveOps) error {
	pathInfo, err := ops.lstat(filePath)
	if err != nil {
		return err
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("legacy runtime backup is not a regular file")
	}
	file, err := ops.open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return errors.New("legacy runtime backup changed before read-only commit")
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, (4<<20)+1))
	if err != nil || written > 4<<20 || hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return errors.New("legacy runtime backup verification failed")
	}
	if err := ops.chmodFile(file, 0o400); err != nil {
		return err
	}
	if err := ops.syncFile(file); err != nil {
		return err
	}
	fdInfo, err := file.Stat()
	if err != nil || !fdInfo.Mode().IsRegular() || !os.SameFile(openedInfo, fdInfo) {
		return errors.New("legacy runtime backup inode changed during read-only commit")
	}
	pathInfo, err = ops.lstat(filePath)
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(fdInfo, pathInfo) {
		return errors.New("legacy runtime backup path changed during read-only commit")
	}
	wantMode := os.FileMode(0o400)
	if runtime.GOOS == "windows" {
		wantMode = 0o444
	}
	if fdInfo.Mode().Perm() != wantMode || pathInfo.Mode().Perm() != wantMode {
		return errors.New("legacy runtime backup mode is not read-only")
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func validateArchivePathComponents(value string, lstat func(string) (os.FileInfo, error)) error {
	value = filepath.Clean(value)
	if !filepath.IsAbs(value) {
		return errors.New("legacy runtime archive path is not absolute")
	}
	components := []string{}
	for current := value; ; current = filepath.Dir(current) {
		components = append(components, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	for left, right := 0, len(components)-1; left < right; left, right = left+1, right-1 {
		components[left], components[right] = components[right], components[left]
	}
	for index, component := range components {
		info, err := lstat(component)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect legacy runtime archive path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (index < len(components)-1 && !info.IsDir()) {
			return errors.New("legacy runtime archive path has unsafe filesystem shape")
		}
	}
	return nil
}

func verifiedRegularFile(filePath, expectedSHA256 string, ops legacyRuntimeArchiveOps) (bool, error) {
	info, err := ops.lstat(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("legacy runtime source is not a regular file")
	}
	file, err := ops.open(filePath)
	if err != nil {
		return false, err
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return false, errors.New("legacy runtime source changed during verification")
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, (4<<20)+1))
	closeErr := file.Close()
	if copyErr != nil {
		return false, copyErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if written > 4<<20 {
		return false, errors.New("legacy runtime source exceeds size limit")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return false, errors.New("legacy runtime source hash differs")
	}
	return true, nil
}

func (m *Manager) controllerForLocked(instanceID, serviceName string) *serviceController {
	key := endpointKey(instanceID, serviceName)
	if controller := m.controllers[key]; controller != nil {
		return controller
	}
	controller := &serviceController{
		manager:    m,
		instanceID: instanceID,
		service:    serviceName,
		wake:       make(chan struct{}, 1),
		stop:       make(chan struct{}),
	}
	m.controllers[key] = controller
	go controller.run()
	return controller
}

func (m *Manager) instanceMaintenanceLock(instanceID string) *sync.RWMutex {
	instanceID = strings.TrimSpace(instanceID)
	m.maintenanceMu.Lock()
	defer m.maintenanceMu.Unlock()
	lock := m.maintenanceLocks[instanceID]
	if lock == nil {
		lock = &sync.RWMutex{}
		m.maintenanceLocks[instanceID] = lock
	}
	return lock
}

func (controller *serviceController) enqueue() {
	controller.mu.Lock()
	stopped := controller.stopped
	controller.mu.Unlock()
	if stopped {
		return
	}
	select {
	case controller.wake <- struct{}{}:
	default:
	}
}

func (controller *serviceController) supersede(generation int64) {
	controller.mu.Lock()
	if generation > controller.requestedGeneration {
		controller.requestedGeneration = generation
	}
	if controller.cancel != nil && controller.requestedGeneration > controller.activeGeneration {
		controller.cancel()
	}
	controller.mu.Unlock()
}

func (controller *serviceController) resetBackoff() {
	controller.mu.Lock()
	controller.failures = 0
	controller.mu.Unlock()
}

func (controller *serviceController) run() {
	for {
		select {
		case <-controller.stop:
			return
		case <-controller.wake:
			controller.reconcileUntilStable()
		}
	}
}

func (controller *serviceController) reconcileUntilStable() {
	for {
		if controller.isStopped() {
			return
		}
		result := controller.reconcileIteration()
		switch result.kind {
		case reconcileSuperseded:
			continue
		case reconcileStable, reconcileSpecRejected:
			return
		case reconcileRetry:
			delay := controller.nextBackoff()
			timer := controller.manager.controllerClock.NewTimer(delay)
			select {
			case <-controller.stop:
				timer.Stop()
				return
			case <-controller.wake:
				timer.Stop()
				continue
			case <-timer.C():
				continue
			}
		}
	}
}

func (controller *serviceController) reconcileIteration() (result reconcileResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			controller.manager.setControllerPanic(controller, controller.activeGenerationSnapshot())
			logf(controller.manager.log, "AIFAR deployment reconcile panic recovered instance=%s service=%s\n", controller.instanceID, controller.service)
			result = reconcileResult{kind: reconcileRetry}
		}
	}()
	return controller.reconcileOnce()
}

func (controller *serviceController) activeGenerationSnapshot() int64 {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.activeGeneration
}

func (controller *serviceController) isStopped() bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.stopped
}

func (controller *serviceController) stopController() {
	controller.mu.Lock()
	if controller.stopped {
		controller.mu.Unlock()
		return
	}
	controller.stopped = true
	if controller.cancel != nil {
		controller.cancel()
	}
	close(controller.stop)
	controller.mu.Unlock()
}

type reconcileResultKind uint8

const (
	reconcileStable reconcileResultKind = iota
	reconcileRetry
	reconcileSpecRejected
	reconcileSuperseded
)

type reconcileResult struct{ kind reconcileResultKind }

func (controller *serviceController) reconcileOnce() reconcileResult {
	manager := controller.manager
	cachedGeneration := manager.cachedControllerGeneration(controller.instanceID, controller.service)
	if controller.rejectsGeneration(cachedGeneration) {
		return reconcileResult{kind: reconcileSpecRejected}
	}
	maintenance := manager.instanceMaintenanceLock(controller.instanceID)
	maintenance.RLock()
	defer maintenance.RUnlock()
	if manager.controllerBeforeRead != nil {
		manager.controllerBeforeRead(controller.instanceID, controller.service)
	}

	manifest, err := manager.manifestStore.Get(controller.instanceID, controller.service)
	if err != nil {
		if manifestReadFailureIsPermanent(err) {
			controller.markRejected(cachedGeneration)
			manager.setControllerRejected(controller.instanceID, controller.service, err)
			return reconcileResult{kind: reconcileSpecRejected}
		}
		manager.setControllerReadUnavailable(controller.instanceID, controller.service)
		return reconcileResult{kind: reconcileRetry}
	}
	config, err := manager.manifestStore.GetInstance(controller.instanceID)
	if err != nil {
		if manifestReadFailureIsPermanent(err) {
			controller.markRejected(manifest.Metadata.Generation)
			manager.setControllerRejected(controller.instanceID, controller.service, err)
			return reconcileResult{kind: reconcileSpecRejected}
		}
		manager.setControllerReadUnavailable(controller.instanceID, controller.service)
		return reconcileResult{kind: reconcileRetry}
	}
	if manager.controllerBeforeActivate != nil {
		manager.controllerBeforeActivate(manifest)
	}

	ctx, cancel := context.WithCancel(context.Background())
	controller.mu.Lock()
	controller.cancel = cancel
	controller.activeGeneration = manifest.Metadata.Generation
	superseded := controller.stopped || controller.requestedGeneration > manifest.Metadata.Generation
	if superseded {
		cancel()
	}
	controller.mu.Unlock()
	defer func() {
		cancel()
		controller.mu.Lock()
		if controller.activeGeneration == manifest.Metadata.Generation {
			controller.cancel = nil
		}
		controller.mu.Unlock()
	}()
	if superseded {
		return reconcileResult{kind: reconcileSuperseded}
	}

	hash, hashErr := DeploymentManifestSpecHash(manifest)
	if hashErr != nil {
		controller.markRejected(manifest.Metadata.Generation)
		manager.setControllerRejected(controller.instanceID, controller.service, hashErr)
		return reconcileResult{kind: reconcileSpecRejected}
	}
	manager.setControllerCondition(manifest, hash, deploymentConditionProgressing, "Reconciling", false)
	spec := runtimeSpecForDeployment(config, manifest)
	err = manager.ensureDeployment(ctx, spec, manifest.Spec)
	manager.publishDeploymentEndpoints(config, manifest)
	if errors.Is(err, context.Canceled) && controller.isSuperseded(manifest.Metadata.Generation) {
		return reconcileResult{kind: reconcileSuperseded}
	}
	if err != nil {
		manager.setControllerCondition(manifest, hash, deploymentConditionDegraded, deploymentFailureReason(err), true)
		return reconcileResult{kind: reconcileRetry}
	}

	controller.resetBackoff()
	controller.clearRejected(manifest.Metadata.Generation)
	if manifest.Spec.Replicas == 0 {
		manager.setControllerCondition(manifest, hash, deploymentConditionOffline, "DesiredReplicasZero", true)
	} else {
		manager.setControllerCondition(manifest, hash, deploymentConditionAvailable, "MinimumReplicasAvailable", true)
	}
	return reconcileResult{kind: reconcileStable}
}

func manifestReadFailureIsPermanent(err error) bool {
	return errors.Is(err, errInvalidManifestStateContent) || errors.Is(err, errUnsafeManifestFilesystemShape)
}

func (controller *serviceController) rejectsGeneration(generation int64) bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return generation > 0 && generation <= controller.rejectedGeneration
}

func (controller *serviceController) markRejected(generation int64) {
	controller.mu.Lock()
	if generation > controller.rejectedGeneration {
		controller.rejectedGeneration = generation
	}
	controller.mu.Unlock()
}

func (controller *serviceController) clearRejected(generation int64) {
	controller.mu.Lock()
	if generation >= controller.rejectedGeneration {
		controller.rejectedGeneration = 0
	}
	controller.mu.Unlock()
}

func (controller *serviceController) isSuperseded(generation int64) bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.stopped || controller.requestedGeneration > generation
}

func (controller *serviceController) nextBackoff() time.Duration {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	index := controller.failures
	if index >= len(deploymentRetryBackoff) {
		index = len(deploymentRetryBackoff) - 1
	}
	controller.failures++
	return deploymentRetryBackoff[index]
}

func runtimeSpecForDeployment(config InstanceConfig, manifest DeploymentManifest) RuntimeSpec {
	return RuntimeSpec{
		Version:     DefaultAgentVersion,
		InstanceID:  config.InstanceID,
		InstallRoot: config.InstallRoot,
		Network:     config.Network,
		Ingress:     config.Ingress,
		Deployments: []DeploymentSpec{manifest.Spec},
		Services:    []ServiceSpec{manifest.Service},
	}
}

func (m *Manager) setControllerRejected(instanceID, serviceName string, reconcileErr error) {
	key := endpointKey(instanceID, serviceName)
	m.controllerMu.Lock()
	manifest, ok := m.manifestCache[key]
	state := m.controllerStates[key]
	hash := state.SpecHash
	if ok {
		state = transitionDeploymentState(state, manifest, hash, deploymentConditionDegraded, "SpecRejected", 0, 0, m.controllerClock.Now())
	} else {
		state.InstanceID = instanceID
		state.ServiceName = serviceName
		state.Conditions = []DeploymentCondition{{Type: deploymentConditionDegraded, Status: true, Reason: "SpecRejected", Generation: state.Generation, LastTransitionTime: m.controllerClock.Now()}}
	}
	m.controllerStates[key] = state
	m.controllerMu.Unlock()
	logf(m.log, "AIFAR deployment manifest rejected instance=%s service=%s error=%v\n", instanceID, serviceName, reconcileErr)
}

func (m *Manager) setControllerReadUnavailable(instanceID, serviceName string) {
	key := endpointKey(instanceID, serviceName)
	currentReplicas, readyReplicas := m.controllerReplicaCounts(key)
	m.controllerMu.Lock()
	manifest, ok := m.manifestCache[key]
	state := m.controllerStates[key]
	if ok {
		state = transitionDeploymentState(state, manifest, state.SpecHash, deploymentConditionDegraded, "AgentUnavailable", currentReplicas, readyReplicas, m.controllerClock.Now())
	} else {
		state.InstanceID = instanceID
		state.ServiceName = serviceName
		state.Conditions = []DeploymentCondition{{Type: deploymentConditionDegraded, Status: true, Reason: "AgentUnavailable", Generation: state.Generation, LastTransitionTime: m.controllerClock.Now()}}
	}
	m.controllerStates[key] = state
	m.controllerMu.Unlock()
	logf(m.log, "AIFAR deployment state temporarily unavailable instance=%s service=%s\n", instanceID, serviceName)
}

func (m *Manager) setControllerPanic(owner *serviceController, generation int64) {
	m.setControllerPanicWithHook(owner, generation, nil)
}

func (m *Manager) setControllerPanicWithHook(owner *serviceController, generation int64, beforeWrite func()) {
	if owner == nil || generation <= 0 {
		return
	}
	key := endpointKey(owner.instanceID, owner.service)
	currentReplicas, readyReplicas := m.controllerReplicaCounts(key)
	m.controllerMu.Lock()
	defer m.controllerMu.Unlock()
	if m.controllers[key] != owner {
		return
	}
	manifest, ok := m.manifestCache[key]
	state, stateOK := m.controllerStates[key]
	if !ok || !stateOK || manifest.Metadata.Generation != generation || state.Generation != generation {
		return
	}
	if beforeWrite != nil {
		beforeWrite()
	}
	state = transitionDeploymentState(state, manifest, state.SpecHash, deploymentConditionDegraded, "ContainerCreateFailed", currentReplicas, readyReplicas, m.controllerClock.Now())
	state.ObservedGeneration = generation
	m.controllerStates[key] = state
}

func (m *Manager) setControllerCondition(manifest DeploymentManifest, hash, conditionType, reason string, observed bool) {
	key := endpointKey(manifest.Metadata.InstanceID, manifest.Metadata.Name)
	currentReplicas, readyReplicas := m.controllerReplicaCounts(key)
	m.controllerMu.Lock()
	state := m.controllerStates[key]
	if state.Generation > manifest.Metadata.Generation {
		m.controllerMu.Unlock()
		return
	}
	state = transitionDeploymentState(state, manifest, hash, conditionType, reason, currentReplicas, readyReplicas, m.controllerClock.Now())
	if observed {
		state.ObservedGeneration = manifest.Metadata.Generation
	}
	m.controllerStates[key] = state
	m.controllerMu.Unlock()
}

func (m *Manager) controllerReplicaCounts(key string) (int, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := m.deployments[key]
	return status.CurrentReplicas, status.ReadyReplicas
}

func (m *Manager) cachedControllerGeneration(instanceID, serviceName string) int64 {
	key := endpointKey(instanceID, serviceName)
	m.controllerMu.Lock()
	defer m.controllerMu.Unlock()
	return m.manifestCache[key].Metadata.Generation
}

func transitionDeploymentState(current DeploymentState, manifest DeploymentManifest, hash, conditionType, reason string, currentReplicas, readyReplicas int, now time.Time) DeploymentState {
	condition := DeploymentCondition{
		Type:               conditionType,
		Status:             true,
		Reason:             reason,
		Generation:         manifest.Metadata.Generation,
		LastTransitionTime: now,
	}
	if len(current.Conditions) == 1 {
		previous := current.Conditions[0]
		if previous.Type == condition.Type && previous.Status == condition.Status && previous.Reason == condition.Reason && previous.Generation == condition.Generation {
			condition.LastTransitionTime = previous.LastTransitionTime
		}
	}
	current.InstanceID = manifest.Metadata.InstanceID
	current.ServiceName = manifest.Metadata.Name
	current.Generation = manifest.Metadata.Generation
	current.SpecHash = hash
	current.DesiredReplicas = manifest.Spec.Replicas
	current.CurrentReplicas = currentReplicas
	current.ReadyReplicas = readyReplicas
	current.Conditions = []DeploymentCondition{condition}
	return current
}

func deploymentFailureReason(err error) string {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "no such image"), strings.Contains(message, "pull access denied"), strings.Contains(message, "manifest unknown"):
		return "ImageMissing"
	case strings.Contains(message, "crashloopbackoff"), strings.Contains(message, "crash loop"):
		return "CrashLoopBackOff"
	case strings.Contains(message, "no space left"), strings.Contains(message, "resource pressure"), strings.Contains(message, "cannot allocate memory"), strings.Contains(message, "oom"):
		return "NodeResourcePressure"
	case strings.Contains(message, "did not become ready"), strings.Contains(message, "readiness"), strings.Contains(message, "health"):
		return "ReadinessFailed"
	case strings.Contains(message, "cannot connect to the docker daemon"), strings.Contains(message, "docker daemon is unavailable"), strings.Contains(message, "aifar agent is unavailable"):
		return "AgentUnavailable"
	case strings.Contains(message, "start stopped"), strings.Contains(message, "restart unhealthy"):
		return "ContainerStartFailed"
	case strings.Contains(message, "create"), strings.Contains(message, "start aifar pod"), strings.Contains(message, "docker run"):
		return "ContainerCreateFailed"
	default:
		return "ContainerCreateFailed"
	}
}

func (m *Manager) removeServiceControllers(instanceID string) {
	prefix := strings.TrimSpace(instanceID) + "/"
	m.controllerMu.Lock()
	for key, controller := range m.controllers {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		controller.stopController()
		delete(m.controllers, key)
		delete(m.controllerStates, key)
		delete(m.manifestCache, key)
	}
	m.controllerMu.Unlock()
}

func (m *Manager) cancelServiceControllerWork(instanceID string) {
	prefix := strings.TrimSpace(instanceID) + "/"
	m.controllerMu.Lock()
	controllers := make([]*serviceController, 0)
	for key, controller := range m.controllers {
		if strings.HasPrefix(key, prefix) {
			controllers = append(controllers, controller)
		}
	}
	m.controllerMu.Unlock()
	for _, controller := range controllers {
		controller.mu.Lock()
		if controller.cancel != nil {
			controller.cancel()
		}
		controller.mu.Unlock()
	}
}
