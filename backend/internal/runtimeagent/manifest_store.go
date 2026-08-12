package runtimeagent

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
)

var (
	manifestStoreWriteMu             sync.Mutex
	errInvalidManifestStateContent   = errors.New("invalid manifest state content")
	errUnsafeManifestFilesystemShape = errors.New("unsafe manifest filesystem shape")
	errManifestFilesystemObservation = errors.New("manifest filesystem observation failed")
)

type ManifestStore struct {
	StateDir          string
	syncFile          func(*os.File) error
	renameFile        func(string, string) error
	syncDirectory     func(string) error
	manifestPathLstat func(string) (os.FileInfo, error)
	removeAll         func(string) error
	renameRetryYield  func()
}

func (s ManifestStore) retireInstance(instanceID string) (string, error) {
	instanceID = strings.TrimSpace(instanceID)
	if err := validateInstanceManifestName(instanceID); err != nil {
		return "", err
	}
	manifestStoreWriteMu.Lock()
	defer manifestStoreWriteMu.Unlock()
	stateDir := s.stateDir()
	livePath := filepath.Join(stateDir, instanceID)
	info, err := os.Lstat(livePath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect live instance state: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("live instance state path is not a directory")
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return "", errors.New("generate instance retirement token")
	}
	retiredPath := filepath.Join(stateDir, fmt.Sprintf(".retired-%s-%x", instanceID, token))
	if err := s.retirementRename(livePath, retiredPath); err != nil {
		return "", fmt.Errorf("retire live instance state: %w", err)
	}
	if err := s.directorySync(stateDir); err != nil {
		rollbackErr := s.retirementRename(retiredPath, livePath)
		if rollbackErr == nil {
			rollbackErr = s.directorySync(stateDir)
		}
		if rollbackErr != nil {
			return "", errors.Join(errors.New("sync retired instance state parent"), errors.New("restore live instance state after retirement failure"))
		}
		return "", errors.New("sync retired instance state parent")
	}
	return retiredPath, nil
}

func (s ManifestStore) retirementRename(oldPath, newPath string) error {
	const maxAttempts = 4
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = s.fileRename(oldPath, newPath)
		if err == nil || !isTransientWindowsRenameError(err) {
			return err
		}
		if attempt+1 < maxAttempts {
			if s.renameRetryYield != nil {
				s.renameRetryYield()
			} else {
				runtime.Gosched()
			}
		}
	}
	return err
}

func isTransientWindowsRenameError(err error) bool {
	return runtime.GOOS == "windows" &&
		(errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(32)))
}

func (s ManifestStore) removeRetiredInstance(path string) error {
	if s.removeAll != nil {
		return s.removeAll(path)
	}
	return os.RemoveAll(path)
}

func isRetiredInstanceStateDirectory(name string) bool {
	return strings.HasPrefix(name, ".retired-")
}

func (s ManifestStore) PutInstance(config InstanceConfig) error {
	config = NormalizeInstanceConfig(config)
	if err := ValidateInstanceConfig(config); err != nil {
		return err
	}
	if err := s.validateInstanceFilesystem(config); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal instance config: %w", err)
	}

	manifestStoreWriteMu.Lock()
	defer manifestStoreWriteMu.Unlock()
	directory := filepath.Join(s.stateDir(), config.InstanceID)
	if err := s.ensureDirectory(directory); err != nil {
		return fmt.Errorf("create instance state directory: %w", err)
	}
	if err := s.atomicWrite(filepath.Join(directory, "instance.json"), append(data, '\n')); err != nil {
		return fmt.Errorf("persist instance config: %w", err)
	}
	return nil
}

func (s ManifestStore) GetInstance(instanceID string) (InstanceConfig, error) {
	var config InstanceConfig
	instanceID = strings.TrimSpace(instanceID)
	if err := validateInstanceManifestName(instanceID); err != nil {
		return config, err
	}
	path := filepath.Join(s.stateDir(), instanceID, "instance.json")
	if err := readManifestJSON(path, &config); err != nil {
		return config, fmt.Errorf("read instance config: %w", err)
	}
	if config.InstanceID != instanceID {
		return InstanceConfig{}, fmt.Errorf("%w: instance config identity does not match state path", errInvalidManifestStateContent)
	}
	if err := ValidateInstanceConfig(config); err != nil {
		return InstanceConfig{}, fmt.Errorf("%w: validate instance config: %v", errInvalidManifestStateContent, err)
	}
	if err := s.validateInstanceFilesystem(config); err != nil {
		return InstanceConfig{}, err
	}
	return config, nil
}

func (s ManifestStore) Put(manifest DeploymentManifest) (DeploymentAcceptance, error) {
	manifest = NormalizeDeploymentManifest(manifest)
	if err := validateInstanceManifestName(manifest.Metadata.InstanceID); err != nil {
		return DeploymentAcceptance{}, err
	}
	if err := validateServiceManifestName(manifest.Metadata.Name); err != nil {
		return DeploymentAcceptance{}, err
	}

	manifestStoreWriteMu.Lock()
	defer manifestStoreWriteMu.Unlock()
	config, err := s.GetInstance(manifest.Metadata.InstanceID)
	if err != nil {
		return DeploymentAcceptance{}, err
	}
	if err := ValidateDeploymentManifest(config, manifest); err != nil {
		return DeploymentAcceptance{}, err
	}
	if err := s.validateDeploymentFilesystem(config, manifest); err != nil {
		return DeploymentAcceptance{}, err
	}
	hash, err := DeploymentManifestSpecHash(manifest)
	if err != nil {
		return DeploymentAcceptance{}, fmt.Errorf("hash deployment manifest: %w", err)
	}
	acceptance := DeploymentAcceptance{Accepted: true, Generation: manifest.Metadata.Generation, SpecHash: hash}

	directory := filepath.Join(s.stateDir(), manifest.Metadata.InstanceID, "deployments")
	if err := s.ensureDirectory(directory); err != nil {
		return DeploymentAcceptance{}, fmt.Errorf("create deployment state directory: %w", err)
	}
	path := filepath.Join(directory, manifest.Metadata.Name+".json")
	var current DeploymentManifest
	if err := readManifestJSON(path, &current); err == nil {
		if err := ValidateDeploymentManifest(config, current); err != nil {
			return DeploymentAcceptance{}, fmt.Errorf("validate persisted deployment manifest: %w", err)
		}
		currentHash, hashErr := DeploymentManifestSpecHash(current)
		if hashErr != nil {
			return DeploymentAcceptance{}, fmt.Errorf("hash persisted deployment manifest: %w", hashErr)
		}
		switch {
		case manifest.Metadata.Generation < current.Metadata.Generation:
			return DeploymentAcceptance{Generation: current.Metadata.Generation, SpecHash: currentHash}, fmt.Errorf("%w: current generation is %d", ErrStaleDeploymentGeneration, current.Metadata.Generation)
		case manifest.Metadata.Generation == current.Metadata.Generation && hash != currentHash:
			return DeploymentAcceptance{Generation: current.Metadata.Generation, SpecHash: currentHash}, fmt.Errorf("%w: generation %d already has another spec hash", ErrDeploymentGenerationConflict, current.Metadata.Generation)
		case manifest.Metadata.Generation == current.Metadata.Generation:
			if err := s.directorySync(directory); err != nil {
				return DeploymentAcceptance{}, fmt.Errorf("resync idempotent deployment directory: %w", err)
			}
			return DeploymentAcceptance{Accepted: true, Generation: current.Metadata.Generation, SpecHash: currentHash}, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return DeploymentAcceptance{}, fmt.Errorf("read persisted deployment manifest: %w", err)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return DeploymentAcceptance{}, fmt.Errorf("marshal deployment manifest: %w", err)
	}
	if err := s.atomicWrite(path, append(data, '\n')); err != nil {
		return DeploymentAcceptance{}, fmt.Errorf("persist deployment manifest: %w", err)
	}
	return acceptance, nil
}

func (s ManifestStore) Get(instanceID, serviceName string) (DeploymentManifest, error) {
	var manifest DeploymentManifest
	instanceID = strings.TrimSpace(instanceID)
	serviceName = normalizeServiceManifestName(serviceName)
	if err := validateInstanceManifestName(instanceID); err != nil {
		return manifest, err
	}
	if err := validateServiceManifestName(serviceName); err != nil {
		return manifest, err
	}
	config, err := s.GetInstance(instanceID)
	if err != nil {
		return manifest, err
	}
	path := filepath.Join(s.stateDir(), instanceID, "deployments", serviceName+".json")
	if err := readManifestJSON(path, &manifest); err != nil {
		return DeploymentManifest{}, fmt.Errorf("read deployment manifest: %w", err)
	}
	if manifest.Metadata.Name != serviceName {
		return DeploymentManifest{}, fmt.Errorf("%w: deployment manifest identity does not match state path", errInvalidManifestStateContent)
	}
	if err := ValidateDeploymentManifest(config, manifest); err != nil {
		return DeploymentManifest{}, fmt.Errorf("%w: validate deployment manifest: %v", errInvalidManifestStateContent, err)
	}
	if err := s.validateDeploymentFilesystem(config, manifest); err != nil {
		return DeploymentManifest{}, err
	}
	return manifest, nil
}

// List returns every valid manifest in service-name order. If one or more
// files are invalid, the valid peers are returned together with a joined
// error so startup recovery can isolate the rejected services.
func (s ManifestStore) List(instanceID string) ([]DeploymentManifest, error) {
	instanceID = strings.TrimSpace(instanceID)
	if err := validateInstanceManifestName(instanceID); err != nil {
		return nil, err
	}
	if _, err := s.GetInstance(instanceID); err != nil {
		return nil, err
	}
	directory := filepath.Join(s.stateDir(), instanceID, "deployments")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []DeploymentManifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list deployment manifests: %w", err)
	}
	manifests := make([]DeploymentManifest, 0, len(entries))
	var readErrors []error
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		serviceName := strings.TrimSuffix(entry.Name(), ".json")
		if err := validateServiceManifestName(serviceName); err != nil {
			readErrors = append(readErrors, fmt.Errorf("invalid deployment state filename %q", entry.Name()))
			continue
		}
		manifest, getErr := s.Get(instanceID, serviceName)
		if getErr != nil {
			readErrors = append(readErrors, fmt.Errorf("load deployment %s: %w", serviceName, getErr))
			continue
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Metadata.Name < manifests[j].Metadata.Name })
	return manifests, errors.Join(readErrors...)
}

func (s ManifestStore) stateDir() string {
	if strings.TrimSpace(s.StateDir) == "" {
		return DefaultStateDir
	}
	return filepath.Clean(s.StateDir)
}

func (s ManifestStore) validateInstanceFilesystem(config InstanceConfig) error {
	if err := validateManifestPathComponents(config.InstallRoot, s.pathLstat()); err != nil {
		return fmt.Errorf("validate instance installRoot filesystem path: %w", err)
	}
	return nil
}

func (s ManifestStore) validateDeploymentFilesystem(config InstanceConfig, manifest DeploymentManifest) error {
	if err := s.validateInstanceFilesystem(config); err != nil {
		return err
	}
	for index, envFile := range manifest.Spec.EnvFiles {
		if err := validateManifestPathComponents(envFile, s.pathLstat()); err != nil {
			return fmt.Errorf("validate deployment envFiles[%d] filesystem path: %w", index, err)
		}
	}
	for index, volume := range manifest.Spec.Volumes {
		if err := validateManifestPathComponents(volume.Source, s.pathLstat()); err != nil {
			return fmt.Errorf("validate deployment volumes[%d] source filesystem path: %w", index, err)
		}
	}
	return nil
}

func (s ManifestStore) pathLstat() func(string) (os.FileInfo, error) {
	if s.manifestPathLstat != nil {
		return s.manifestPathLstat
	}
	return os.Lstat
}

func validateManifestPathComponents(value string, lstat func(string) (os.FileInfo, error)) error {
	components := []string{"/"}
	current := "/"
	for _, component := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if component == "" {
			continue
		}
		current = path.Join(current, component)
		components = append(components, current)
	}
	for index, component := range components {
		info, err := lstat(component)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: inspect existing path component: %w", errManifestFilesystemObservation, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: existing path component must not be a symbolic link", errUnsafeManifestFilesystemShape)
		}
		if index < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("%w: existing parent path component must be a directory", errUnsafeManifestFilesystemShape)
		}
	}
	return nil
}

func (s ManifestStore) ensureDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("state path exists and is not a directory")
		}
		if err := s.directorySync(filepath.Dir(directory)); err != nil {
			return fmt.Errorf("sync existing state directory parent: %w", err)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(directory)
	if parent == directory {
		return errors.New("state directory root does not exist")
	}
	if err := s.ensureDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err = os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("created state path is not a directory")
	}
	if err := s.directorySync(parent); err != nil {
		return fmt.Errorf("sync parent state directory: %w", err)
	}
	return nil
}

func (s ManifestStore) atomicWrite(path string, data []byte) (returnErr error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary state file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary state file: %w", err)
	}
	if err := s.fileSync(temporary); err != nil {
		return fmt.Errorf("sync temporary state file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary state file: %w", err)
	}
	if err := s.fileRename(temporaryPath, path); err != nil {
		return fmt.Errorf("rename temporary state file: %w", err)
	}
	if err := s.directorySync(directory); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}

func (s ManifestStore) fileSync(file *os.File) error {
	if s.syncFile != nil {
		return s.syncFile(file)
	}
	return file.Sync()
}

func (s ManifestStore) fileRename(oldPath, newPath string) error {
	if s.renameFile != nil {
		return s.renameFile(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

func (s ManifestStore) directorySync(path string) error {
	if s.syncDirectory != nil {
		return s.syncDirectory(path)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func readManifestJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: open state file: %w", errManifestFilesystemObservation, err)
	}
	defer file.Close()
	reader := &manifestStateReader{file: file}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if reader.err != nil {
			return fmt.Errorf("%w: read state file: %w", errManifestFilesystemObservation, reader.err)
		}
		return fmt.Errorf("%w: decode state file: %v", errInvalidManifestStateContent, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if reader.err != nil {
			return fmt.Errorf("%w: read trailing state data: %w", errManifestFilesystemObservation, reader.err)
		}
		if err == nil {
			return fmt.Errorf("%w: state file contains multiple JSON values", errInvalidManifestStateContent)
		}
		return fmt.Errorf("%w: decode trailing state data: %v", errInvalidManifestStateContent, err)
	}
	return nil
}

type manifestStateReader struct {
	file *os.File
	err  error
}

func (reader *manifestStateReader) Read(buffer []byte) (int, error) {
	count, err := reader.file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		reader.err = err
	}
	return count, err
}
