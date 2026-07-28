package aifar

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/store"
)

const (
	runtimeDiagnosticLocalMaxArchiveBytes = int64(256 << 20)
	runtimeDiagnosticFilesystemMargin     = int64(64 << 20)
	runtimeDiagnosticManifestMaxBytes     = int64(16 << 20)
)

type runtimeDiagnosticRawManifest struct {
	FormatVersion     string                               `json:"formatVersion"`
	LocalDate         string                               `json:"localDate"`
	Since             string                               `json:"since"`
	Until             string                               `json:"until"`
	ServerTimezone    string                               `json:"serverTimezone"`
	SelectedServices  string                               `json:"selectedServices"`
	SnapshotStartedAt string                               `json:"snapshotStartedAt"`
	Sources           []runtimeDiagnosticRawManifestSource `json:"sources"`
}

type runtimeDiagnosticRawManifestSource struct {
	Service              string `json:"service"`
	SourcePath           string `json:"sourcePath"`
	Device               string `json:"device"`
	Inode                string `json:"inode"`
	CapturedBytes        int64  `json:"capturedBytes"`
	SourceSnapshotSHA256 string `json:"sourceSnapshotSha256"`
	ArchiveEntrySHA256   string `json:"archiveEntrySha256"`
	ActiveSnapshot       int    `json:"activeSnapshot"`
}

type runtimeDiagnosticArchiveEntryDigest struct {
	Size   int64
	SHA256 string
}

type RuntimeDiagnosticArchiveStorage interface {
	Stats(context.Context) (RuntimeDiagnosticStorageStats, error)
	Retention() time.Duration
	Begin(exportID, archiveName string) (RuntimeDiagnosticArchiveSink, error)
	Open(relativePath string) (*os.File, error)
	Remove(relativePath string) error
	RemovePartial(exportID string) error
	Reconcile(context.Context, time.Time) (RuntimeDiagnosticReconcileResult, error)
}

type RuntimeDiagnosticArchiveSink interface {
	io.Writer
	Commit(context.Context, int64) (RuntimeDiagnosticLocalArtifact, error)
	Abort() error
}

type RuntimeDiagnosticStorageStats struct {
	RootAvailableBytes int64 `json:"rootAvailableBytes"`
	ReadyBytes         int64 `json:"readyBytes"`
	ExpiredReadyBytes  int64 `json:"expiredReadyBytes"`
	ReservedBytes      int64 `json:"reservedBytes"`
	QuotaBytes         int64 `json:"quotaBytes"`
	ReservationBytes   int64 `json:"reservationBytes"`
}

type RuntimeDiagnosticLocalArtifact struct {
	RelativePath string
	ArchiveName  string
	Size         int64
	SHA256       string
}

type RuntimeDiagnosticReconcileResult struct {
	RemovedPartials int
	RemovedOrphans  int
	MissingReadyIDs []string
	WarningCodes    []string
}

type runtimeDiagnosticRecordLister interface {
	ListDiagnosticExportsForReconcile() ([]store.DiagnosticExport, error)
}

type localRuntimeDiagnosticArchiveStorage struct {
	root                 string
	quotaBytes           int64
	retention            time.Duration
	records              runtimeDiagnosticRecordLister
	maxArchiveBytes      int64
	maxUncompressedBytes int64
	availableBytes       func(string) (int64, error)
}

type localRuntimeDiagnosticArchiveSink struct {
	storage     *localRuntimeDiagnosticArchiveStorage
	exportID    string
	archiveName string
	partialPath string
	finalPath   string
	file        *os.File
	hasher      hash.Hash
	size        int64
	closed      bool
	committed   bool
}

func NewRuntimeDiagnosticArchiveStorage(root string, quotaBytes int64, retention time.Duration, records runtimeDiagnosticRecordLister) RuntimeDiagnosticArchiveStorage {
	return newRuntimeDiagnosticArchiveStorageWithLimit(root, quotaBytes, retention, records, runtimeDiagnosticLocalMaxArchiveBytes)
}

func newRuntimeDiagnosticArchiveStorageWithLimit(root string, quotaBytes int64, retention time.Duration, records runtimeDiagnosticRecordLister, maxArchiveBytes int64) *localRuntimeDiagnosticArchiveStorage {
	if absolute, err := filepath.Abs(strings.TrimSpace(root)); err == nil {
		root = absolute
	}
	return &localRuntimeDiagnosticArchiveStorage{
		root: filepath.Clean(root), quotaBytes: quotaBytes, retention: retention, records: records, maxArchiveBytes: maxArchiveBytes,
		maxUncompressedBytes: runtimeDiagnosticMaxUncompressed, availableBytes: runtimeDiagnosticAvailableBytes,
	}
}

func (s *localRuntimeDiagnosticArchiveStorage) Retention() time.Duration {
	if s == nil || s.retention <= 0 {
		return runtimeDiagnosticRetention
	}
	return s.retention
}

func (s *localRuntimeDiagnosticArchiveStorage) Stats(ctx context.Context) (RuntimeDiagnosticStorageStats, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeDiagnosticStorageStats{}, err
	}
	if err := s.ensureRoot(); err != nil {
		return RuntimeDiagnosticStorageStats{}, err
	}
	available, err := s.availableBytes(s.root)
	if err != nil {
		return RuntimeDiagnosticStorageStats{}, err
	}
	result := RuntimeDiagnosticStorageStats{
		RootAvailableBytes: available,
		QuotaBytes:         s.quotaBytes,
		ReservationBytes:   runtimeDiagnosticLocalMaxArchiveBytes,
	}
	if s.records == nil {
		return result, nil
	}
	records, err := s.records.ListDiagnosticExportsForReconcile()
	if err != nil {
		return RuntimeDiagnosticStorageStats{}, err
	}
	now := time.Now()
	for _, record := range records {
		if record.StorageKind != "local" || !record.DeletedAt.IsZero() {
			continue
		}
		result.ReservedBytes += record.ReservedBytes
		if record.ArchiveBytes > 0 {
			result.ReadyBytes += record.ArchiveBytes
			if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now) {
				result.ExpiredReadyBytes += record.ArchiveBytes
			}
		}
	}
	return result, nil
}

func (s *localRuntimeDiagnosticArchiveStorage) Begin(exportID, archiveName string) (RuntimeDiagnosticArchiveSink, error) {
	if !validRuntimeDiagnosticStorageComponent(exportID) {
		return nil, fmt.Errorf("invalid diagnostic export identity")
	}
	if !validRuntimeDiagnosticArchiveName(archiveName) {
		return nil, fmt.Errorf("invalid diagnostic archive name")
	}
	if s.maxArchiveBytes <= 0 {
		return nil, fmt.Errorf("diagnostic archive size limit must be positive")
	}
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	available, err := s.availableBytes(s.root)
	if err != nil {
		return nil, err
	}
	if available < runtimeDiagnosticLocalMaxArchiveBytes+runtimeDiagnosticFilesystemMargin {
		return nil, fmt.Errorf("insufficient diagnostic archive filesystem headroom")
	}
	exportDir := filepath.Join(s.root, exportID)
	if err := ensureRuntimeDiagnosticDirectory(exportDir); err != nil {
		return nil, err
	}
	finalPath := filepath.Join(exportDir, archiveName)
	if err := rejectExistingRuntimeDiagnosticEntry(finalPath); err != nil {
		return nil, err
	}
	partialPath := finalPath + ".partial"
	file, err := os.OpenFile(partialPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(partialPath, 0o600); err != nil {
		file.Close()
		os.Remove(partialPath)
		return nil, err
	}
	return &localRuntimeDiagnosticArchiveSink{
		storage: s, exportID: exportID, archiveName: archiveName, partialPath: partialPath, finalPath: finalPath,
		file: file, hasher: sha256.New(),
	}, nil
}

func (s *localRuntimeDiagnosticArchiveStorage) Open(relativePath string) (*os.File, error) {
	exportID, archiveName, err := parseRuntimeDiagnosticRelativePath(relativePath)
	if err != nil {
		return nil, err
	}
	if err := s.ensureRoot(); err != nil {
		return nil, err
	}
	exportDir := filepath.Join(s.root, exportID)
	if err := requireRuntimeDiagnosticDirectory(exportDir); err != nil {
		return nil, err
	}
	archivePath := filepath.Join(exportDir, archiveName)
	info, err := os.Lstat(archivePath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("diagnostic archive is not a regular file")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !os.SameFile(info, openedInfo) {
		file.Close()
		return nil, fmt.Errorf("diagnostic archive changed while opening")
	}
	return file, nil
}

func (s *localRuntimeDiagnosticArchiveStorage) Remove(relativePath string) error {
	exportID, archiveName, err := parseRuntimeDiagnosticRelativePath(relativePath)
	if err != nil {
		return err
	}
	if err := s.ensureRoot(); err != nil {
		return err
	}
	exportDir := filepath.Join(s.root, exportID)
	if err := requireRuntimeDiagnosticDirectory(exportDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	archivePath := filepath.Join(exportDir, archiveName)
	info, err := os.Lstat(archivePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("diagnostic archive is not a regular file")
	}
	if err := os.Remove(archivePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := runtimeDiagnosticSyncDirectory(exportDir); err != nil {
		return err
	}
	if err := os.Remove(exportDir); err != nil && !errors.Is(err, os.ErrNotExist) && !isRuntimeDiagnosticDirectoryNotEmpty(err) {
		return err
	}
	return runtimeDiagnosticSyncDirectory(s.root)
}

func (s *localRuntimeDiagnosticArchiveStorage) RemovePartial(exportID string) error {
	if !validRuntimeDiagnosticStorageComponent(exportID) {
		return fmt.Errorf("invalid diagnostic export identity")
	}
	if err := s.ensureRoot(); err != nil {
		return err
	}
	exportDir := filepath.Join(s.root, exportID)
	if err := requireRuntimeDiagnosticDirectory(exportDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".partial") || !validRuntimeDiagnosticArchiveName(strings.TrimSuffix(name, ".partial")) {
			continue
		}
		entryPath := filepath.Join(exportDir, name)
		info, err := os.Lstat(entryPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("diagnostic archive partial is not a regular file")
		}
		if err := os.Remove(entryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(exportDir); err != nil && !errors.Is(err, os.ErrNotExist) && !isRuntimeDiagnosticDirectoryNotEmpty(err) {
		return err
	}
	return runtimeDiagnosticSyncDirectory(s.root)
}

func (s *localRuntimeDiagnosticArchiveStorage) Reconcile(ctx context.Context, now time.Time) (RuntimeDiagnosticReconcileResult, error) {
	result := RuntimeDiagnosticReconcileResult{MissingReadyIDs: []string{}, WarningCodes: []string{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.ensureRoot(); err != nil {
		return result, err
	}
	referenced := map[string]struct{}{}
	warnings := map[string]struct{}{}
	if s.records != nil {
		records, err := s.records.ListDiagnosticExportsForReconcile()
		if err != nil {
			return result, err
		}
		for _, record := range records {
			if record.StorageKind != "local" || !record.DeletedAt.IsZero() || strings.TrimSpace(record.StorageRelativePath) == "" {
				continue
			}
			exportID, archiveName, err := parseRuntimeDiagnosticRelativePath(record.StorageRelativePath)
			if err != nil {
				warnings["diagnostic-storage-record-invalid"] = struct{}{}
				if record.Status == "ready" {
					result.MissingReadyIDs = append(result.MissingReadyIDs, record.ID)
				}
				continue
			}
			relativePath := path.Join(exportID, archiveName)
			referenced[relativePath] = struct{}{}
			if record.Status != "ready" {
				continue
			}
			exportDir := filepath.Join(s.root, exportID)
			if err := requireRuntimeDiagnosticDirectory(exportDir); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					result.MissingReadyIDs = append(result.MissingReadyIDs, record.ID)
				} else {
					warnings["diagnostic-storage-symlink-refused"] = struct{}{}
				}
				continue
			}
			info, err := os.Lstat(filepath.Join(exportDir, archiveName))
			if errors.Is(err, os.ErrNotExist) {
				result.MissingReadyIDs = append(result.MissingReadyIDs, record.ID)
				continue
			}
			if err != nil {
				warnings["diagnostic-storage-read-failed"] = struct{}{}
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				warnings["diagnostic-storage-symlink-refused"] = struct{}{}
			}
		}
	}

	rootEntries, err := os.ReadDir(s.root)
	if err != nil {
		return result, err
	}
	partialCutoff := now.Add(-time.Hour)
	orphanCutoff := now.Add(-s.retention)
	for _, rootEntry := range rootEntries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		exportID := rootEntry.Name()
		exportDir := filepath.Join(s.root, exportID)
		rootInfo, err := os.Lstat(exportDir)
		if err != nil {
			warnings["diagnostic-storage-read-failed"] = struct{}{}
			continue
		}
		if rootInfo.Mode()&os.ModeSymlink != 0 {
			warnings["diagnostic-storage-symlink-refused"] = struct{}{}
			continue
		}
		if !rootInfo.IsDir() || !validRuntimeDiagnosticStorageComponent(exportID) {
			warnings["diagnostic-storage-entry-invalid"] = struct{}{}
			continue
		}
		entries, err := os.ReadDir(exportDir)
		if err != nil {
			warnings["diagnostic-storage-read-failed"] = struct{}{}
			continue
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			entryPath := filepath.Join(exportDir, entry.Name())
			info, err := os.Lstat(entryPath)
			if err != nil {
				warnings["diagnostic-storage-read-failed"] = struct{}{}
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				warnings["diagnostic-storage-symlink-refused"] = struct{}{}
				continue
			}
			if !info.Mode().IsRegular() {
				warnings["diagnostic-storage-entry-invalid"] = struct{}{}
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".partial") {
				archiveName := strings.TrimSuffix(name, ".partial")
				if !validRuntimeDiagnosticArchiveName(archiveName) {
					warnings["diagnostic-storage-entry-invalid"] = struct{}{}
					continue
				}
				if !info.ModTime().After(partialCutoff) {
					if err := os.Remove(entryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
						warnings["diagnostic-storage-remove-failed"] = struct{}{}
					} else {
						result.RemovedPartials++
					}
				}
				continue
			}
			if !validRuntimeDiagnosticArchiveName(name) {
				warnings["diagnostic-storage-entry-invalid"] = struct{}{}
				continue
			}
			relativePath := path.Join(exportID, name)
			if _, exists := referenced[relativePath]; exists || info.ModTime().After(orphanCutoff) {
				continue
			}
			if err := validateRuntimeDiagnosticTarGzip(ctx, entryPath, strings.TrimSuffix(name, ".tar.gz"), -1, s.maxUncompressedBytes); err != nil {
				warnings["diagnostic-storage-orphan-invalid"] = struct{}{}
				continue
			}
			if err := os.Remove(entryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				warnings["diagnostic-storage-remove-failed"] = struct{}{}
			} else {
				result.RemovedOrphans++
			}
		}
		if err := os.Remove(exportDir); err != nil && !errors.Is(err, os.ErrNotExist) && !isRuntimeDiagnosticDirectoryNotEmpty(err) {
			warnings["diagnostic-storage-remove-failed"] = struct{}{}
		}
	}
	sort.Strings(result.MissingReadyIDs)
	for warning := range warnings {
		result.WarningCodes = append(result.WarningCodes, warning)
	}
	sort.Strings(result.WarningCodes)
	return result, nil
}

func (s *localRuntimeDiagnosticArchiveStorage) ensureRoot() error {
	if strings.TrimSpace(s.root) == "" || s.root == "." {
		return fmt.Errorf("diagnostic archive root is required")
	}
	info, err := os.Lstat(s.root)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("diagnostic archive root is not a directory")
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(s.root, 0o700); err != nil {
			return err
		}
	default:
		return err
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return err
	}
	return nil
}

func ensureRuntimeDiagnosticDirectory(directory string) error {
	info, err := os.Lstat(directory)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("diagnostic export directory is unsafe")
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(directory, 0o700); err != nil {
			return err
		}
	default:
		return err
	}
	return os.Chmod(directory, 0o700)
}

func requireRuntimeDiagnosticDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("diagnostic export directory is unsafe")
	}
	return nil
}

func rejectExistingRuntimeDiagnosticEntry(entry string) error {
	info, err := os.Lstat(entry)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("diagnostic archive entry is a symlink")
	}
	return fmt.Errorf("diagnostic archive entry already exists")
}

func validRuntimeDiagnosticStorageComponent(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || filepath.IsAbs(value) || filepath.Base(value) != value || strings.ContainsAny(value, `/\\`) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validRuntimeDiagnosticArchiveName(value string) bool {
	return validRuntimeDiagnosticStorageComponent(value) && strings.HasSuffix(value, ".tar.gz") && len(strings.TrimSuffix(value, ".tar.gz")) > 0
}

func parseRuntimeDiagnosticRelativePath(relativePath string) (string, string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" || path.IsAbs(relativePath) || filepath.IsAbs(relativePath) || strings.Contains(relativePath, `\`) || path.Clean(relativePath) != relativePath {
		return "", "", fmt.Errorf("invalid diagnostic archive relative path")
	}
	parts := strings.Split(relativePath, "/")
	if len(parts) != 2 || !validRuntimeDiagnosticStorageComponent(parts[0]) || !validRuntimeDiagnosticArchiveName(parts[1]) {
		return "", "", fmt.Errorf("invalid diagnostic archive relative path")
	}
	return parts[0], parts[1], nil
}

func (s *localRuntimeDiagnosticArchiveSink) Write(payload []byte) (int, error) {
	if s.closed || s.committed {
		return 0, fmt.Errorf("diagnostic archive sink is closed")
	}
	if int64(len(payload)) > s.storage.maxArchiveBytes-s.size {
		return 0, fmt.Errorf("diagnostic archive exceeds size limit")
	}
	written, err := io.MultiWriter(s.file, s.hasher).Write(payload)
	s.size += int64(written)
	return written, err
}

func (s *localRuntimeDiagnosticArchiveSink) Commit(ctx context.Context, expectedUncompressedBytes int64) (RuntimeDiagnosticLocalArtifact, error) {
	if s.committed {
		return RuntimeDiagnosticLocalArtifact{}, fmt.Errorf("diagnostic archive sink is already committed")
	}
	if s.closed {
		return RuntimeDiagnosticLocalArtifact{}, fmt.Errorf("diagnostic archive sink is closed")
	}
	if err := s.file.Sync(); err != nil {
		s.Abort()
		return RuntimeDiagnosticLocalArtifact{}, err
	}
	if err := s.file.Close(); err != nil {
		s.closed = true
		s.Abort()
		return RuntimeDiagnosticLocalArtifact{}, err
	}
	s.closed = true
	if err := validateRuntimeDiagnosticTarGzip(ctx, s.partialPath, strings.TrimSuffix(s.archiveName, ".tar.gz"), expectedUncompressedBytes, s.storage.maxUncompressedBytes); err != nil {
		s.Abort()
		return RuntimeDiagnosticLocalArtifact{}, err
	}
	if err := rejectExistingRuntimeDiagnosticEntry(s.finalPath); err != nil {
		s.Abort()
		return RuntimeDiagnosticLocalArtifact{}, err
	}
	if err := os.Rename(s.partialPath, s.finalPath); err != nil {
		s.Abort()
		return RuntimeDiagnosticLocalArtifact{}, err
	}
	if err := os.Chmod(s.finalPath, 0o600); err != nil {
		os.Remove(s.finalPath)
		return RuntimeDiagnosticLocalArtifact{}, err
	}
	if err := runtimeDiagnosticSyncDirectory(filepath.Dir(s.finalPath)); err != nil {
		os.Remove(s.finalPath)
		return RuntimeDiagnosticLocalArtifact{}, err
	}
	s.committed = true
	return RuntimeDiagnosticLocalArtifact{
		RelativePath: path.Join(s.exportID, s.archiveName), ArchiveName: s.archiveName,
		Size: s.size, SHA256: hex.EncodeToString(s.hasher.Sum(nil)),
	}, nil
}

func (s *localRuntimeDiagnosticArchiveSink) Abort() error {
	if s.committed {
		return nil
	}
	var closeErr error
	if !s.closed && s.file != nil {
		closeErr = s.file.Close()
		s.closed = true
	}
	removeErr := os.Remove(s.partialPath)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	directoryErr := os.Remove(filepath.Dir(s.partialPath))
	if errors.Is(directoryErr, os.ErrNotExist) || isRuntimeDiagnosticDirectoryNotEmpty(directoryErr) {
		directoryErr = nil
	}
	return errors.Join(closeErr, removeErr, directoryErr)
}

func validateRuntimeDiagnosticTarGzip(ctx context.Context, archivePath, expectedTop string, expectedUncompressedBytes, maxUncompressedBytes int64) error {
	if !validRuntimeDiagnosticStorageComponent(expectedTop) {
		return fmt.Errorf("invalid diagnostic archive root")
	}
	if ctx == nil {
		return fmt.Errorf("diagnostic archive validation context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maxUncompressedBytes <= 0 || expectedUncompressedBytes < -1 || expectedUncompressedBytes > maxUncompressedBytes {
		return fmt.Errorf("invalid diagnostic archive uncompressed size")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("invalid diagnostic gzip: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	required := map[string]bool{
		path.Join(expectedTop, "README.txt"):            false,
		path.Join(expectedTop, "manifest.json"):         false,
		path.Join(expectedTop, "collection-errors.txt"): false,
	}
	manifestName := path.Join(expectedTop, "manifest.json")
	servicesPrefix := path.Join(expectedTop, "services") + "/"
	seenEntries := make(map[string]struct{})
	serviceEntries := make(map[string]runtimeDiagnosticArchiveEntryDigest)
	var manifestContent bytes.Buffer
	var regularBytes int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid diagnostic tar: %w", err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		clean := path.Clean(name)
		if name == "" || path.IsAbs(name) || strings.Contains(name, `\`) || clean != name || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe diagnostic tar entry")
		}
		parts := strings.Split(clean, "/")
		if len(parts) == 0 || parts[0] != expectedTop {
			return fmt.Errorf("diagnostic tar has unexpected top-level directory")
		}
		if _, exists := seenEntries[clean]; exists {
			return fmt.Errorf("diagnostic tar contains duplicate entry")
		}
		seenEntries[clean] = struct{}{}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxUncompressedBytes-regularBytes {
				return fmt.Errorf("diagnostic tar exceeds uncompressed size limit")
			}
			regularBytes += header.Size
			if _, ok := required[clean]; ok {
				required[clean] = true
			}
			entryReader := &runtimeDiagnosticContextReader{ctx: ctx, reader: tarReader}
			switch {
			case clean == manifestName:
				if header.Size > runtimeDiagnosticManifestMaxBytes {
					return fmt.Errorf("diagnostic manifest exceeds size limit")
				}
				if _, err := io.Copy(&manifestContent, entryReader); err != nil {
					return fmt.Errorf("read diagnostic manifest: %w", err)
				}
			case strings.HasPrefix(clean, servicesPrefix):
				hasher := sha256.New()
				if _, err := io.Copy(hasher, entryReader); err != nil {
					return fmt.Errorf("read diagnostic service entry: %w", err)
				}
				serviceEntries[clean] = runtimeDiagnosticArchiveEntryDigest{Size: header.Size, SHA256: hex.EncodeToString(hasher.Sum(nil))}
			default:
				if _, err := io.Copy(io.Discard, entryReader); err != nil {
					return fmt.Errorf("read diagnostic tar entry: %w", err)
				}
			}
		case tar.TypeDir:
		default:
			return fmt.Errorf("diagnostic tar contains unsupported entry type")
		}
		if header.Typeflag == tar.TypeDir {
			if _, err := io.Copy(io.Discard, &runtimeDiagnosticContextReader{ctx: ctx, reader: tarReader}); err != nil {
				return fmt.Errorf("read diagnostic tar entry: %w", err)
			}
		}
	}
	if expectedUncompressedBytes >= 0 && regularBytes != expectedUncompressedBytes {
		return fmt.Errorf("diagnostic tar uncompressed size mismatch")
	}
	for name, present := range required {
		if !present {
			return fmt.Errorf("diagnostic tar is missing required entry %s", path.Base(name))
		}
	}
	if err := validateRuntimeDiagnosticRawManifest(expectedTop, manifestContent.Bytes(), serviceEntries); err != nil {
		return err
	}
	return nil
}

func validateRuntimeDiagnosticRawManifest(expectedTop string, content []byte, serviceEntries map[string]runtimeDiagnosticArchiveEntryDigest) error {
	var envelope struct {
		FormatVersion string `json:"formatVersion"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		return fmt.Errorf("invalid diagnostic manifest: %w", err)
	}
	if envelope.FormatVersion != "AIFAR_DIAGNOSTIC_RAW_SNAPSHOT_V1" {
		return nil
	}
	var manifest runtimeDiagnosticRawManifest
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("invalid diagnostic manifest: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("invalid diagnostic manifest trailing data")
	}
	matched := make(map[string]struct{}, len(manifest.Sources))
	for _, source := range manifest.Sources {
		if !runtimeDiagnosticNamePattern.MatchString(source.Service) || source.SourcePath == "" || path.IsAbs(source.SourcePath) || strings.Contains(source.SourcePath, `\`) || path.Clean(source.SourcePath) != source.SourcePath || strings.HasPrefix(source.SourcePath, "../") {
			return fmt.Errorf("invalid diagnostic raw manifest source path")
		}
		entryName := path.Join(expectedTop, "services", source.Service, source.SourcePath)
		if _, duplicate := matched[entryName]; duplicate {
			return fmt.Errorf("duplicate diagnostic raw manifest source")
		}
		matched[entryName] = struct{}{}
		entry, ok := serviceEntries[entryName]
		if !ok {
			return fmt.Errorf("diagnostic raw manifest source is missing from archive")
		}
		if source.CapturedBytes < 0 || source.CapturedBytes != entry.Size || source.ActiveSnapshot < 0 || source.ActiveSnapshot > 1 || !validRuntimeDiagnosticSHA256(source.SourceSnapshotSHA256) || !validRuntimeDiagnosticSHA256(source.ArchiveEntrySHA256) || source.SourceSnapshotSHA256 != entry.SHA256 || source.ArchiveEntrySHA256 != entry.SHA256 {
			return fmt.Errorf("diagnostic raw manifest source integrity mismatch")
		}
	}
	if len(matched) != len(serviceEntries) {
		return fmt.Errorf("diagnostic raw manifest does not cover service entries")
	}
	return nil
}

func validRuntimeDiagnosticSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func isRuntimeDiagnosticDirectoryNotEmpty(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "directory not empty") || strings.Contains(strings.ToLower(err.Error()), "not empty")
}
