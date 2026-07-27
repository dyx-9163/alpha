package backuprepo

import (
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
	if err := os.Mkdir(paths.Directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
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
	defer func() {
		if retErr != nil {
			if err := os.Remove(paths.PartialArchive); err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove failed partial archive: %w", err))
			}
		}
	}()
	if err := requireDirectory(paths.Directory); err != nil {
		return err
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
	for _, finalPath := range []string{paths.Archive, paths.Manifest, paths.Checksums} {
		if _, err := os.Lstat(finalPath); err == nil {
			return fmt.Errorf("final backup artifact already exists: %q", finalPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	actualSHA, actualSize, err := fileSHA256(paths.PartialArchive)
	if err != nil {
		return err
	}
	if actualSize != expectedSize {
		return fmt.Errorf("backup archive size mismatch: got %d, want %d", actualSize, expectedSize)
	}
	if actualSHA != normalizedSHA {
		return fmt.Errorf("backup archive SHA256 mismatch")
	}
	if err := syncRegularFile(paths.PartialArchive); err != nil {
		return err
	}

	manifestTemp, err := writeSyncedTemp(paths.Directory, ".backup-manifest-", manifest)
	if err != nil {
		return err
	}
	defer os.Remove(manifestTemp)
	checksums := []byte(normalizedSHA + "  " + archiveName + "\n")
	checksumsTemp, err := writeSyncedTemp(paths.Directory, ".checksums-", checksums)
	if err != nil {
		return err
	}
	defer os.Remove(checksumsTemp)

	archivePromoted := false
	manifestPromoted := false
	checksumsPromoted := false
	defer func() {
		if retErr == nil {
			return
		}
		if checksumsPromoted {
			retErr = errors.Join(retErr, removeExactFile(paths.Checksums))
		}
		if manifestPromoted {
			retErr = errors.Join(retErr, removeExactFile(paths.Manifest))
		}
		if archivePromoted {
			retErr = errors.Join(retErr, removeExactFile(paths.Archive))
		}
	}()
	if err := os.Rename(paths.PartialArchive, paths.Archive); err != nil {
		return fmt.Errorf("promote backup archive: %w", err)
	}
	archivePromoted = true
	if err := os.Rename(manifestTemp, paths.Manifest); err != nil {
		return fmt.Errorf("promote backup manifest: %w", err)
	}
	manifestPromoted = true
	if err := os.Rename(checksumsTemp, paths.Checksums); err != nil {
		return fmt.Errorf("promote backup checksums: %w", err)
	}
	checksumsPromoted = true
	if err := syncDirectory(paths.Directory); err != nil {
		return err
	}
	return nil
}

func (r *Repository) Verify(backup store.AppBackup) (Verification, error) {
	if backup.Status != "success" {
		return Verification{}, fmt.Errorf("backup %q is not successful", backup.ID)
	}
	if err := validateBackupID(backup.ID); err != nil {
		return Verification{}, err
	}
	if err := r.validateRoot(); err != nil {
		return Verification{}, err
	}
	paths := r.pathsForID(backup.ID)
	if !samePath(backup.Path, paths.Archive) {
		return Verification{}, fmt.Errorf("backup record path is not the managed archive path")
	}
	if err := requireDirectory(paths.Directory); err != nil {
		return Verification{}, err
	}
	for _, path := range []string{paths.Archive, paths.Manifest, paths.Checksums} {
		if err := requireRegularFile(path); err != nil {
			return Verification{}, err
		}
	}
	manifest, err := readRegularFile(paths.Manifest, maxManifestSize)
	if err != nil {
		return Verification{}, err
	}
	if err := requireManifestID(manifest, backup.ID); err != nil {
		return Verification{}, err
	}
	recordSHA, err := normalizeSHA256(backup.Checksum)
	if err != nil {
		return Verification{}, fmt.Errorf("invalid backup record checksum: %w", err)
	}
	if backup.Size < 0 {
		return Verification{}, errors.New("invalid backup record size")
	}
	actualSHA, actualSize, err := fileSHA256(paths.Archive)
	if err != nil {
		return Verification{}, err
	}
	if actualSize != backup.Size {
		return Verification{}, fmt.Errorf("backup archive size does not match record")
	}
	if actualSHA != recordSHA {
		return Verification{}, fmt.Errorf("backup archive SHA256 does not match record")
	}
	checksums, err := readRegularFile(paths.Checksums, maxChecksumsSize)
	if err != nil {
		return Verification{}, err
	}
	wantChecksums := recordSHA + "  " + archiveName + "\n"
	if string(checksums) != wantChecksums {
		return Verification{}, errors.New("backup checksums file does not match archive record")
	}
	return Verification{Paths: paths, Manifest: manifest, SHA256: actualSHA, Size: actualSize}, nil
}

func (r *Repository) Delete(backup store.AppBackup) error {
	verification, err := r.Verify(backup)
	if err != nil {
		return err
	}
	paths := verification.Paths
	if samePath(paths.Directory, r.root) || !samePath(filepath.Dir(paths.Directory), r.root) {
		return errors.New("refusing to delete repository root or outside path")
	}
	if err := r.validateRoot(); err != nil {
		return err
	}
	if err := requireDirectory(paths.Directory); err != nil {
		return err
	}
	if err := os.RemoveAll(paths.Directory); err != nil {
		return fmt.Errorf("delete managed backup directory: %w", err)
	}
	return syncDirectory(r.root)
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

func syncRegularFile(path string) error {
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("managed backup path %q is not a regular file", path)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return fmt.Errorf("managed backup path %q changed while opening", path)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync backup archive: %w", err)
	}
	return nil
}

func writeSyncedTemp(directory, pattern string, content []byte) (path string, retErr error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		if retErr != nil {
			file.Close()
			os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return "", err
	}
	if _, err := file.Write(content); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, fmt.Errorf("managed metadata file %q is invalid or too large", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("managed metadata file %q changed while opening", path)
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("managed metadata file %q is too large", path)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) || after.Size() != int64(len(content)) {
		return nil, fmt.Errorf("managed metadata file %q changed while reading", path)
	}
	return content, nil
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

func removeExactFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
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
