package backuprepo

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"aifar-deployment/backend/internal/store"
)

const (
	archiveName      = "dump.tar"
	partialName      = "dump.tar.partial"
	manifestName     = "backup-manifest.json"
	checksumsName    = "checksums.txt"
	maxManifestSize  = 1 << 20
	maxChecksumsSize = 4 << 10
)

type Repository struct {
	root string
	hook func(point, path string) error
}

type BackupPaths struct {
	Directory      string
	PartialArchive string
	Archive        string
	Manifest       string
	Checksums      string
}

type Verification struct {
	Paths    BackupPaths
	Manifest []byte
	SHA256   string
	Size     int64
}

type backupManifestIdentity struct {
	BackupID string `json:"backupId"`
}

type stableObject struct {
	path string
	info os.FileInfo
	file *os.File
}

type stableBoundaries struct {
	root      stableObject
	directory stableObject
	artifacts []stableObject
}

func (boundaries *stableBoundaries) close() {
	for index := range boundaries.artifacts {
		if boundaries.artifacts[index].file != nil {
			boundaries.artifacts[index].file.Close()
		}
	}
	if boundaries.directory.file != nil {
		boundaries.directory.file.Close()
	}
	if boundaries.root.file != nil {
		boundaries.root.file.Close()
	}
}

func (boundaries *stableBoundaries) closeManagedDirectory() {
	for index := range boundaries.artifacts {
		if boundaries.artifacts[index].file != nil {
			boundaries.artifacts[index].file.Close()
			boundaries.artifacts[index].file = nil
		}
	}
	if boundaries.directory.file != nil {
		boundaries.directory.file.Close()
		boundaries.directory.file = nil
	}
}

func (r *Repository) checkpoint(point, path string) error {
	if r != nil && r.hook != nil {
		return r.hook(point, path)
	}
	return nil
}

func New(root string) (*Repository, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("backup repository root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("canonicalize backup repository root: %w", err)
	}
	abs = filepath.Clean(abs)
	if err := ensureNoSymlinkBoundaries(abs); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create backup repository root: %w", err)
	}
	if err := requireDirectory(abs); err != nil {
		return nil, err
	}
	if err := os.Chmod(abs, 0o700); err != nil && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("secure backup repository root: %w", err)
	}
	return &Repository{root: abs}, nil
}

func (r *Repository) Prepare(backupID string) (BackupPaths, error) {
	if r == nil {
		return BackupPaths{}, errors.New("backup repository is nil")
	}
	if err := validateBackupID(backupID); err != nil {
		return BackupPaths{}, err
	}
	if err := r.validateRoot(); err != nil {
		return BackupPaths{}, err
	}
	paths := r.pathsForID(backupID)
	if err := ensureNoSymlinkBoundaries(paths.Directory); err != nil {
		return BackupPaths{}, err
	}
	if err := os.Mkdir(paths.Directory, 0o700); err != nil {
		return BackupPaths{}, fmt.Errorf("create managed backup directory: %w", err)
	}
	if err := requireDirectory(paths.Directory); err != nil {
		return BackupPaths{}, err
	}
	if err := os.Chmod(paths.Directory, 0o700); err != nil && runtime.GOOS != "windows" {
		return BackupPaths{}, fmt.Errorf("secure managed backup directory: %w", err)
	}
	return paths, nil
}

func (r *Repository) Commit(paths BackupPaths, manifest []byte, expectedSHA256 string, expectedSize int64) (retErr error) {
	backupID, err := r.validatePaths(paths)
	if err != nil {
		return err
	}
	if int64(len(manifest)) > maxManifestSize {
		return fmt.Errorf("backup manifest exceeds %d bytes", maxManifestSize)
	}
	if err := requireManifestID(manifest, backupID); err != nil {
		return err
	}
	normalizedSHA, err := normalizeSHA256(expectedSHA256)
	if err != nil {
		return err
	}
	if expectedSize < 0 {
		return errors.New("expected backup archive size must not be negative")
	}

	boundaries, err := r.openBoundaries(paths.Directory)
	if err != nil {
		return err
	}
	defer boundaries.close()
	partial, err := openStableRegular(paths.PartialArchive, os.O_RDWR)
	if err != nil {
		return err
	}
	partialBound := true
	defer func() {
		if retErr != nil && partialBound {
			retErr = errors.Join(retErr, r.removeOwned(paths.PartialArchive, partial.info, "commit.rollback-partial"))
		}
	}()
	defer func() {
		if partial.file != nil {
			partial.file.Close()
		}
	}()
	for _, finalPath := range []string{paths.Archive, paths.Manifest, paths.Checksums} {
		if err := requireAbsent(finalPath); err != nil {
			return err
		}
	}
	actualSHA, actualSize, err := hashStableFile(partial)
	if err != nil {
		return err
	}
	if actualSize != expectedSize {
		return fmt.Errorf("backup archive size mismatch: got %d, want %d", actualSize, expectedSize)
	}
	if actualSHA != normalizedSHA {
		return errors.New("backup archive SHA256 mismatch")
	}
	if err := partial.file.Sync(); err != nil {
		return fmt.Errorf("sync backup archive: %w", err)
	}
	if err := partial.file.Close(); err != nil {
		return err
	}
	partial.file = nil
	if err := r.checkpoint("commit.after-partial-hash", paths.PartialArchive); err != nil {
		return err
	}
	if err := boundaries.recheck(); err != nil {
		return err
	}
	partialAfter, err := openStableRegular(paths.PartialArchive, os.O_RDWR)
	if err != nil {
		return err
	}
	defer partialAfter.file.Close()
	if !os.SameFile(partial.info, partialAfter.info) {
		return errors.New("backup partial changed identity after checksum verification")
	}
	actualSHA, actualSize, err = hashStableFile(partialAfter)
	if err != nil || actualSHA != normalizedSHA || actualSize != expectedSize {
		if err != nil {
			return err
		}
		return errors.New("backup partial changed after checksum verification")
	}
	if err := partialAfter.file.Close(); err != nil {
		return err
	}

	manifestTemp, err := writeSyncedTemp(paths.Directory, ".backup-manifest-", manifest)
	if err != nil {
		return err
	}
	checksums := []byte(normalizedSHA + "  " + archiveName + "\n")
	checksumsTemp, err := writeSyncedTemp(paths.Directory, ".checksums-", checksums)
	if err != nil {
		_ = r.removeOwned(manifestTemp.path, manifestTemp.info, "commit.rollback-manifest-temp")
		return err
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, r.removeOwned(checksumsTemp.path, checksumsTemp.info, "commit.rollback-checksums-temp"))
			retErr = errors.Join(retErr, r.removeOwned(manifestTemp.path, manifestTemp.info, "commit.rollback-manifest-temp"))
		}
	}()

	promoted := make([]stableObject, 0, 3)
	defer func() {
		if retErr == nil {
			return
		}
		for index := len(promoted) - 1; index >= 0; index-- {
			retErr = errors.Join(retErr, r.removeOwned(promoted[index].path, promoted[index].info, "commit.rollback-final"))
		}
	}()
	archive, err := r.promote(boundaries, partial, paths.Archive, "archive")
	if archive.info != nil {
		promoted = append(promoted, archive)
	}
	if err != nil {
		return err
	}
	partialBound = false
	archiveSHA, archiveSize, err := fileSHA256(paths.Archive)
	if err != nil || archiveSHA != normalizedSHA || archiveSize != expectedSize {
		if err != nil {
			return err
		}
		return errors.New("promoted backup archive failed checksum verification")
	}
	if err := recheckPathIdentity(archive); err != nil {
		return err
	}
	manifestFinal, err := r.promote(boundaries, manifestTemp, paths.Manifest, "manifest")
	if manifestFinal.info != nil {
		promoted = append(promoted, manifestFinal)
	}
	if err != nil {
		return err
	}
	checksumsFinal, err := r.promote(boundaries, checksumsTemp, paths.Checksums, "checksums")
	if checksumsFinal.info != nil {
		promoted = append(promoted, checksumsFinal)
	}
	if err != nil {
		return err
	}
	if err := r.checkpoint("commit.before-final-verify", paths.Directory); err != nil {
		return err
	}
	if err := boundaries.recheck(); err != nil {
		return err
	}
	for _, object := range promoted {
		if err := recheckPathIdentity(object); err != nil {
			return err
		}
	}
	archiveSHA, archiveSize, err = fileSHA256(paths.Archive)
	if err != nil || archiveSHA != normalizedSHA || archiveSize != expectedSize {
		if err != nil {
			return err
		}
		return errors.New("published backup archive failed final verification")
	}
	if err := syncDirectory(paths.Directory); err != nil {
		return err
	}
	return nil
}

func (r *Repository) Verify(backup store.AppBackup) (Verification, error) {
	verification, boundaries, err := r.verifyAnchored(backup)
	defer func() {
		if boundaries != nil {
			boundaries.close()
		}
	}()
	return verification, err
}

func (r *Repository) verifyAnchored(backup store.AppBackup) (Verification, *stableBoundaries, error) {
	if backup.Status != "success" {
		return Verification{}, nil, fmt.Errorf("backup %q is not successful", backup.ID)
	}
	if err := validateBackupID(backup.ID); err != nil {
		return Verification{}, nil, err
	}
	paths := r.pathsForID(backup.ID)
	if !samePath(backup.Path, paths.Archive) {
		return Verification{}, nil, errors.New("backup record path is not the managed archive path")
	}
	boundaries, err := r.openBoundaries(paths.Directory)
	if err != nil {
		return Verification{}, nil, err
	}
	fail := func(err error) (Verification, *stableBoundaries, error) {
		return Verification{}, boundaries, err
	}
	archive, err := openStableRegular(paths.Archive, os.O_RDONLY)
	if err != nil {
		return fail(err)
	}
	boundaries.artifacts = append(boundaries.artifacts, archive)
	recordSHA, err := normalizeSHA256(backup.Checksum)
	if err != nil {
		return fail(fmt.Errorf("invalid backup record checksum: %w", err))
	}
	if backup.Size < 0 {
		return fail(errors.New("invalid backup record size"))
	}
	actualSHA, actualSize, err := hashStableFile(archive)
	if err != nil {
		return fail(err)
	}
	if err := r.checkpoint("verify.after-archive", paths.Archive); err != nil {
		return fail(err)
	}
	if actualSize != backup.Size || actualSHA != recordSHA {
		return fail(errors.New("backup archive does not match record size and SHA256"))
	}
	manifestObject, err := openStableRegular(paths.Manifest, os.O_RDONLY)
	if err != nil {
		return fail(err)
	}
	boundaries.artifacts = append(boundaries.artifacts, manifestObject)
	manifest, err := readStableFile(manifestObject, maxManifestSize)
	if err != nil {
		return fail(err)
	}
	if err := requireManifestID(manifest, backup.ID); err != nil {
		return fail(err)
	}
	checksumsObject, err := openStableRegular(paths.Checksums, os.O_RDONLY)
	if err != nil {
		return fail(err)
	}
	boundaries.artifacts = append(boundaries.artifacts, checksumsObject)
	checksums, err := readStableFile(checksumsObject, maxChecksumsSize)
	if err != nil {
		return fail(err)
	}
	if string(checksums) != recordSHA+"  "+archiveName+"\n" {
		return fail(errors.New("backup checksums file does not match archive record"))
	}
	if err := boundaries.recheck(); err != nil {
		return fail(err)
	}
	return Verification{Paths: paths, Manifest: manifest, SHA256: actualSHA, Size: actualSize}, boundaries, nil
}

func (r *Repository) Delete(backup store.AppBackup) (retErr error) {
	verification, boundaries, err := r.verifyAnchored(backup)
	defer func() {
		if boundaries != nil {
			boundaries.close()
		}
	}()
	if err != nil {
		return err
	}
	paths := verification.Paths
	if samePath(paths.Directory, r.root) || !samePath(filepath.Dir(paths.Directory), r.root) {
		return errors.New("refusing to delete repository root or outside path")
	}
	if err := boundaries.recheck(); err != nil {
		return err
	}
	boundaries.closeManagedDirectory()
	if err := r.checkpoint("delete.after-verify", paths.Directory); err != nil {
		return err
	}
	if err := recheckStableObject(boundaries.root); err != nil {
		return err
	}
	if err := recheckPathAsIdentity(paths.Directory, boundaries.directory.info); err != nil {
		return err
	}
	quarantine, err := uniqueSiblingPath(r.root, ".delete-"+backup.ID+"-")
	if err != nil {
		return err
	}
	if err := r.checkpoint("delete.before-quarantine", quarantine); err != nil {
		return err
	}
	if err := recheckStableObject(boundaries.root); err != nil {
		return err
	}
	if err := recheckPathAsIdentity(paths.Directory, boundaries.directory.info); err != nil {
		return err
	}
	if err := atomicRenameNoReplace(paths.Directory, quarantine); err != nil {
		return fmt.Errorf("quarantine managed backup directory: %w", err)
	}
	quarantined := true
	defer func() {
		if retErr != nil && quarantined {
			retErr = errors.Join(retErr, restoreQuarantined(quarantine, paths.Directory, boundaries.directory.info))
		}
	}()
	if err := r.checkpoint("delete.after-quarantine", quarantine); err != nil {
		return err
	}
	if err := recheckPathAsIdentity(quarantine, boundaries.directory.info); err != nil {
		return err
	}
	if err := recheckStableObject(boundaries.root); err != nil {
		return err
	}
	boundaries.close()
	boundaries = nil
	quarantined = false
	if err := os.RemoveAll(quarantine); err != nil {
		return fmt.Errorf("delete quarantined managed backup directory: %w", err)
	}
	return syncDirectory(r.root)
}

func restoreQuarantined(quarantine, original string, expected os.FileInfo) error {
	current, err := os.Lstat(quarantine)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	owned := os.SameFile(expected, current) && current.Mode()&os.ModeSymlink == 0 && current.IsDir()
	if err := atomicRenameNoReplace(quarantine, original); err != nil {
		if owned {
			return fmt.Errorf("restore verified backup directory from quarantine: %w", err)
		}
		return fmt.Errorf("preserve replacement quarantine without overwriting original: %w", err)
	}
	if !owned {
		return errors.New("quarantine identity changed; replacement restored without deletion")
	}
	return nil
}

func (r *Repository) RetentionCandidates(backups []store.AppBackup, keepLast int) []store.AppBackup {
	keep := keepLast
	if keep < 1 {
		keep = 1
	}
	successful := make([]store.AppBackup, 0, len(backups))
	for _, backup := range backups {
		if backup.Status == "success" {
			successful = append(successful, backup)
		}
	}
	sort.SliceStable(successful, func(i, j int) bool {
		left := successful[i].CreatedAt
		right := successful[j].CreatedAt
		if left.Equal(right) {
			return successful[i].ID > successful[j].ID
		}
		return left.After(right)
	})
	if len(successful) <= keep {
		return []store.AppBackup{}
	}
	return append([]store.AppBackup(nil), successful[keep:]...)
}

func (r *Repository) validateRoot() error {
	if r == nil || strings.TrimSpace(r.root) == "" {
		return errors.New("backup repository is not initialized")
	}
	if !filepath.IsAbs(r.root) || filepath.Clean(r.root) != r.root {
		return errors.New("backup repository root is not canonical")
	}
	if err := ensureNoSymlinkBoundaries(r.root); err != nil {
		return err
	}
	return requireDirectory(r.root)
}

func (r *Repository) validatePaths(paths BackupPaths) (string, error) {
	if err := r.validateRoot(); err != nil {
		return "", err
	}
	backupID := filepath.Base(paths.Directory)
	if err := validateBackupID(backupID); err != nil {
		return "", err
	}
	expected := r.pathsForID(backupID)
	if !sameBackupPaths(paths, expected) {
		return "", errors.New("backup paths do not match managed repository layout")
	}
	if err := ensureNoSymlinkBoundaries(paths.Directory); err != nil {
		return "", err
	}
	return backupID, nil
}

func (r *Repository) pathsForID(backupID string) BackupPaths {
	directory := filepath.Join(r.root, backupID)
	return BackupPaths{
		Directory: directory, PartialArchive: filepath.Join(directory, partialName),
		Archive: filepath.Join(directory, archiveName), Manifest: filepath.Join(directory, manifestName),
		Checksums: filepath.Join(directory, checksumsName),
	}
}

func validateBackupID(backupID string) error {
	if backupID == "" || strings.TrimSpace(backupID) != backupID || filepath.IsAbs(backupID) || filepath.Clean(backupID) != backupID || backupID == "." || backupID == ".." {
		return fmt.Errorf("invalid backup ID %q", backupID)
	}
	for i, char := range backupID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || ((char == '_' || char == '-') && i > 0) {
			continue
		}
		return fmt.Errorf("invalid backup ID %q", backupID)
	}
	return nil
}

func ensureNoSymlinkBoundaries(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)
	volume := filepath.VolumeName(abs)
	rest := strings.TrimPrefix(abs, volume)
	rest = strings.TrimLeft(rest, string(filepath.Separator))
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	if rest == "" {
		return rejectSymlink(current)
	}
	for _, component := range strings.Split(rest, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup repository path boundary %q is a symlink", current)
		}
	}
	return nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup repository path boundary %q is a symlink", path)
	}
	return nil
}

func requireDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("managed backup path %q is not a real directory", path)
	}
	return nil
}

func requireRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("managed backup path %q is not a regular file", path)
	}
	return nil
}

func requireManifestID(manifest []byte, backupID string) error {
	var identity backupManifestIdentity
	if err := json.Unmarshal(manifest, &identity); err != nil {
		return fmt.Errorf("decode backup manifest: %w", err)
	}
	if identity.BackupID == "" || identity.BackupID != backupID {
		return errors.New("backup manifest ID does not match managed backup ID")
	}
	return nil
}

func normalizeSHA256(value string) (string, error) {
	if len(value) != 64 || strings.ToLower(value) != value {
		return "", errors.New("SHA256 must be 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("SHA256 must be 64 lowercase hexadecimal characters")
	}
	return value, nil
}

func (r *Repository) openBoundaries(directory string) (*stableBoundaries, error) {
	root, err := openStableDirectory(r.root)
	if err != nil {
		return nil, err
	}
	managed, err := openStableDirectory(directory)
	if err != nil {
		root.file.Close()
		return nil, err
	}
	boundaries := &stableBoundaries{root: root, directory: managed}
	if err := boundaries.recheck(); err != nil {
		boundaries.close()
		return nil, err
	}
	return boundaries, nil
}

func openStableDirectory(path string) (stableObject, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return stableObject{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return stableObject{}, fmt.Errorf("managed backup path %q is not a real directory", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return stableObject{}, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return stableObject{}, err
	}
	if !opened.IsDir() || !os.SameFile(before, opened) {
		file.Close()
		return stableObject{}, fmt.Errorf("managed backup directory %q changed while opening", path)
	}
	object := stableObject{path: path, info: opened, file: file}
	if err := recheckPathIdentity(object); err != nil {
		file.Close()
		return stableObject{}, err
	}
	return object, nil
}

func openStableRegular(path string, flag int) (stableObject, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return stableObject{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return stableObject{}, fmt.Errorf("managed backup path %q is not a regular file", path)
	}
	file, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return stableObject{}, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return stableObject{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		file.Close()
		return stableObject{}, fmt.Errorf("managed backup file %q changed while opening", path)
	}
	object := stableObject{path: path, info: opened, file: file}
	if err := recheckPathIdentity(object); err != nil {
		file.Close()
		return stableObject{}, err
	}
	return object, nil
}

func (boundaries *stableBoundaries) recheck() error {
	if boundaries == nil {
		return errors.New("managed backup boundaries are not open")
	}
	if err := recheckStableObject(boundaries.root); err != nil {
		return err
	}
	if err := recheckStableObject(boundaries.directory); err != nil {
		return err
	}
	for _, artifact := range boundaries.artifacts {
		if err := recheckStableObject(artifact); err != nil {
			return err
		}
	}
	return nil
}

func recheckStableObject(object stableObject) error {
	if object.file == nil || object.info == nil {
		return fmt.Errorf("managed object %q is not anchored", object.path)
	}
	opened, err := object.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(object.info, opened) {
		return fmt.Errorf("managed object %q changed identity", object.path)
	}
	return recheckPathAsIdentity(object.path, object.info)
}

func recheckPathIdentity(object stableObject) error {
	return recheckPathAsIdentity(object.path, object.info)
}

func recheckPathAsIdentity(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, current) {
		return fmt.Errorf("managed path %q changed identity", path)
	}
	return nil
}

func requireAbsent(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("final backup artifact already exists: %q", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeSyncedTemp(directory, pattern string, content []byte) (object stableObject, retErr error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return stableObject{}, err
	}
	path := file.Name()
	created, err := file.Stat()
	if err != nil {
		file.Close()
		return stableObject{}, err
	}
	defer func() {
		if retErr != nil {
			file.Close()
			retErr = errors.Join(retErr, removeOwnedPath(path, created))
		}
	}()
	if err := file.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return stableObject{}, err
	}
	if _, err := file.Write(content); err != nil {
		return stableObject{}, err
	}
	if err := file.Sync(); err != nil {
		return stableObject{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return stableObject{}, err
	}
	if err := file.Close(); err != nil {
		return stableObject{}, err
	}
	return stableObject{path: path, info: info}, nil
}

func readStableFile(object stableObject, limit int64) ([]byte, error) {
	if object.info.Size() > limit {
		return nil, fmt.Errorf("managed metadata file %q is too large", object.path)
	}
	if _, err := object.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	content, err := io.ReadAll(io.LimitReader(object.file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("managed metadata file %q is too large", object.path)
	}
	after, err := object.file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(object.info, after) || after.Size() != int64(len(content)) {
		return nil, fmt.Errorf("managed metadata file %q changed while reading", object.path)
	}
	return content, nil
}

func (r *Repository) promote(boundaries *stableBoundaries, source stableObject, destination, kind string) (stableObject, error) {
	if err := r.checkpoint("commit.before-"+kind+"-promote", source.path); err != nil {
		return stableObject{}, err
	}
	if err := boundaries.recheck(); err != nil {
		return stableObject{}, err
	}
	if err := recheckPathAsIdentity(source.path, source.info); err != nil {
		return stableObject{}, err
	}
	if err := atomicRenameNoReplace(source.path, destination); err != nil {
		return stableObject{}, fmt.Errorf("promote backup %s without replacement: %w", kind, err)
	}
	current, err := os.Lstat(destination)
	if err != nil {
		return stableObject{}, err
	}
	if !current.Mode().IsRegular() || !os.SameFile(source.info, current) {
		restoreErr := atomicRenameNoReplace(destination, source.path)
		return stableObject{}, errors.Join(fmt.Errorf("promoted backup %s changed source identity", kind), restoreErr)
	}
	promoted := stableObject{path: destination, info: current}
	if err := r.checkpoint("commit.after-"+kind+"-promote", destination); err != nil {
		return promoted, err
	}
	if err := boundaries.recheck(); err != nil {
		return promoted, err
	}
	if err := recheckPathIdentity(promoted); err != nil {
		return promoted, err
	}
	return promoted, nil
}

func (r *Repository) removeOwned(path string, expected os.FileInfo, point string) error {
	if err := r.checkpoint(point, path); err != nil {
		return err
	}
	return removeOwnedPath(path, expected)
}

func removeOwnedPath(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	quarantine, err := uniqueSiblingPath(filepath.Dir(path), ".cleanup-")
	if err != nil {
		return err
	}
	if err := atomicRenameNoReplace(path, quarantine); err != nil {
		return err
	}
	quarantined, err := os.Lstat(quarantine)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, quarantined) {
		restoreErr := atomicRenameNoReplace(quarantine, path)
		return errors.Join(fmt.Errorf("refusing to remove replacement at %q", path), restoreErr)
	}
	if current.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed cleanup target %q became a symlink", path)
	}
	if quarantined.IsDir() {
		return os.RemoveAll(quarantine)
	}
	return os.Remove(quarantine)
}

func uniqueSiblingPath(directory, prefix string) (string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return "", err
		}
		path := filepath.Join(directory, prefix+hex.EncodeToString(bytes))
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not allocate unique managed quarantine path")
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync backup directory: %w", err)
	}
	return nil
}

func sameBackupPaths(left, right BackupPaths) bool {
	return samePath(left.Directory, right.Directory) && samePath(left.PartialArchive, right.PartialArchive) &&
		samePath(left.Archive, right.Archive) && samePath(left.Manifest, right.Manifest) && samePath(left.Checksums, right.Checksums)
}

func samePath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
