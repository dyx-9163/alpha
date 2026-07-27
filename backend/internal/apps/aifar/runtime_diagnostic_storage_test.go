package aifar

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func TestLocalDiagnosticStorageRejectsUnsafeIdentity(t *testing.T) {
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(t.TempDir(), 1<<30, 24*time.Hour, nil, 1<<20)
	tests := []struct {
		exportID    string
		archiveName string
	}{
		{exportID: "../diag", archiveName: "archive.tar.gz"},
		{exportID: filepath.Join(t.TempDir(), "diag"), archiveName: "archive.tar.gz"},
		{exportID: "diag/child", archiveName: "archive.tar.gz"},
		{exportID: "diag", archiveName: "../archive.tar.gz"},
		{exportID: "diag", archiveName: filepath.Join(t.TempDir(), "archive.tar.gz")},
		{exportID: "diag", archiveName: "nested/archive.tar.gz"},
		{exportID: "diag", archiveName: "archive.zip"},
	}
	for _, test := range tests {
		if _, err := storage.Begin(test.exportID, test.archiveName); err == nil {
			t.Fatalf("unsafe identity accepted: exportID=%q archiveName=%q", test.exportID, test.archiveName)
		}
	}
	for _, relativePath := range []string{"../archive.tar.gz", "/diag/archive.tar.gz", "diag/../archive.tar.gz", "diag/nested/archive.tar.gz"} {
		if file, err := storage.Open(relativePath); err == nil {
			file.Close()
			t.Fatalf("unsafe relative path accepted: %q", relativePath)
		}
	}
}

func TestLocalDiagnosticStorageRejectsSymlinkRootOrEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available without Windows developer mode")
	}
	outside := t.TempDir()
	rootLink := filepath.Join(t.TempDir(), "diagnostic-exports")
	if err := os.Symlink(outside, rootLink); err != nil {
		t.Skipf("cannot create root symlink: %v", err)
	}
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(rootLink, 1<<30, 24*time.Hour, nil, 1<<20)
	if _, err := storage.Begin("diag-root-link", "archive.tar.gz"); err == nil {
		t.Fatal("symlink root was accepted")
	}

	realRoot := filepath.Join(t.TempDir(), "diagnostic-exports")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	entryLink := filepath.Join(realRoot, "diag-entry-link")
	if err := os.Symlink(outside, entryLink); err != nil {
		t.Skipf("cannot create entry symlink: %v", err)
	}
	storage = newRuntimeDiagnosticArchiveStorageWithLimit(realRoot, 1<<30, 24*time.Hour, nil, 1<<20)
	if _, err := storage.Begin("diag-entry-link", "archive.tar.gz"); err == nil {
		t.Fatal("symlink export directory was accepted")
	}
}

func TestLocalDiagnosticStorageCreates0700RootAnd0600Files(t *testing.T) {
	root := filepath.Join(t.TempDir(), "diagnostic-exports")
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(root, 1<<30, 24*time.Hour, nil, 1<<20)
	sink, err := storage.Begin("diag-permissions", "archive.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Abort()
	if runtime.GOOS == "windows" {
		return
	}
	for path, want := range map[string]os.FileMode{
		root:                                    0o700,
		filepath.Join(root, "diag-permissions"): 0o700,
		filepath.Join(root, "diag-permissions", "archive.tar.gz.partial"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("mode for %s = %o, want %o", filepath.Base(path), got, want)
		}
	}
}

func TestLocalDiagnosticSinkCommitsByAtomicRenameAndSHA256(t *testing.T) {
	root := t.TempDir()
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(root, 1<<30, 24*time.Hour, nil, 1<<20)
	payload := buildDiagnosticArchive(t, "archive", diagnosticArchiveControlEntries("archive"))
	sink, err := storage.Begin("diag-atomic", "archive.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write(payload); err != nil {
		t.Fatal(err)
	}
	finalPath := filepath.Join(root, "diag-atomic", "archive.tar.gz")
	if _, err := os.Stat(finalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final archive existed before commit: %v", err)
	}
	artifact, err := sink.Commit()
	if err != nil {
		t.Fatal(err)
	}
	wantSHA := fmt.Sprintf("%x", sha256.Sum256(payload))
	if artifact.RelativePath != "diag-atomic/archive.tar.gz" || artifact.ArchiveName != "archive.tar.gz" || artifact.Size != int64(len(payload)) || artifact.SHA256 != wantSHA {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("committed archive bytes changed")
	}
	if _, err := os.Stat(finalPath + ".partial"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial remained after commit: %v", err)
	}
}

func TestLocalDiagnosticSinkAbortRemovesPartial(t *testing.T) {
	root := t.TempDir()
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(root, 1<<30, 24*time.Hour, nil, 1<<20)
	sink, err := storage.Begin("diag-abort", "archive.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	if err := sink.Abort(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "diag-abort", "archive.tar.gz.partial"),
		filepath.Join(root, "diag-abort", "archive.tar.gz"),
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("artifact remained after abort: %s err=%v", filepath.Base(path), err)
		}
	}
}

func TestLocalDiagnosticStorageRejectsArchiveAbove256MiB(t *testing.T) {
	root := t.TempDir()
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(root, 1<<30, 24*time.Hour, nil, 32)
	sink, err := storage.Begin("diag-limit", "archive.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write(bytes.Repeat([]byte("x"), 33)); err == nil {
		t.Fatal("archive write above configured limit succeeded")
	}
	if err := sink.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "diag-limit", "archive.tar.gz.partial")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized partial remained: %v", err)
	}
}

func TestLocalDiagnosticStorageStatsIncludeReadyExpiredAndReservedBytes(t *testing.T) {
	now := time.Now()
	records := staticRuntimeDiagnosticRecordLister{
		{ID: "ready", StorageKind: "local", Status: "ready", ArchiveBytes: 100, ExpiresAt: now.Add(time.Hour)},
		{ID: "expired", StorageKind: "local", Status: "ready", ArchiveBytes: 50, ExpiresAt: now.Add(-time.Hour)},
		{ID: "building", StorageKind: "local", Status: "building", ReservedBytes: 20, ExpiresAt: now.Add(time.Hour)},
	}
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(t.TempDir(), 1000, 24*time.Hour, records, 1<<20)
	storage.availableBytes = func(string) (int64, error) { return 999, nil }
	stats, err := storage.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := RuntimeDiagnosticStorageStats{
		RootAvailableBytes: 999,
		ReadyBytes:         150,
		ExpiredReadyBytes:  50,
		ReservedBytes:      20,
		QuotaBytes:         1000,
		ReservationBytes:   256 << 20,
	}
	if stats != want {
		t.Fatalf("storage stats = %+v, want %+v", stats, want)
	}
}

func TestLocalDiagnosticStorageRejectsInsufficientFilesystemHeadroom(t *testing.T) {
	root := t.TempDir()
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(root, 1<<30, 24*time.Hour, nil, 1<<20)
	storage.availableBytes = func(string) (int64, error) {
		return runtimeDiagnosticLocalMaxArchiveBytes + runtimeDiagnosticFilesystemMargin - 1, nil
	}
	if _, err := storage.Begin("diag-disk-full", "archive.tar.gz"); err == nil {
		t.Fatal("storage began an archive without reservation and filesystem safety headroom")
	}
	if _, err := os.Stat(filepath.Join(root, "diag-disk-full")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capacity rejection created an export directory: %v", err)
	}
}

func TestLocalDiagnosticSinkRejectsInvalidOrUnsafeTarGzip(t *testing.T) {
	valid := diagnosticArchiveControlEntries("archive")
	tests := []struct {
		name    string
		payload func(*testing.T) []byte
	}{
		{name: "invalid gzip", payload: func(*testing.T) []byte { return []byte("not-gzip") }},
		{name: "absolute entry", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", append(valid, diagnosticArchiveEntry{Name: "/absolute.log", Body: "x"}))
		}},
		{name: "parent traversal", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", append(valid, diagnosticArchiveEntry{Name: "archive/../escape.log", Body: "x"}))
		}},
		{name: "symlink entry", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", append(valid, diagnosticArchiveEntry{Name: "archive/link", Typeflag: tar.TypeSymlink, Linkname: "README.txt"}))
		}},
		{name: "hardlink entry", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", append(valid, diagnosticArchiveEntry{Name: "archive/link", Typeflag: tar.TypeLink, Linkname: "archive/README.txt"}))
		}},
		{name: "device entry", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", append(valid, diagnosticArchiveEntry{Name: "archive/device", Typeflag: tar.TypeChar}))
		}},
		{name: "multiple roots", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", append(valid, diagnosticArchiveEntry{Name: "other/file.log", Body: "x"}))
		}},
		{name: "missing readme", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", []diagnosticArchiveEntry{{Name: "archive/manifest.json", Body: "{}"}, {Name: "archive/collection-errors.txt"}})
		}},
		{name: "missing manifest", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", []diagnosticArchiveEntry{{Name: "archive/README.txt"}, {Name: "archive/collection-errors.txt"}})
		}},
		{name: "missing collection errors", payload: func(t *testing.T) []byte {
			return buildDiagnosticArchive(t, "archive", []diagnosticArchiveEntry{{Name: "archive/README.txt"}, {Name: "archive/manifest.json", Body: "{}"}})
		}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			storage := newRuntimeDiagnosticArchiveStorageWithLimit(root, 1<<30, 24*time.Hour, nil, 1<<20)
			exportID := fmt.Sprintf("diag-invalid-%d", index)
			sink, err := storage.Begin(exportID, "archive.tar.gz")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sink.Write(test.payload(t)); err != nil {
				t.Fatal(err)
			}
			if _, err := sink.Commit(); err == nil {
				t.Fatal("unsafe archive committed")
			}
			for _, name := range []string{"archive.tar.gz.partial", "archive.tar.gz"} {
				if _, err := os.Stat(filepath.Join(root, exportID, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unsafe archive artifact remained: %s err=%v", name, err)
				}
			}
		})
	}
}

func TestDiagnosticReconcileRemovesOnlyStaleValidatedArtifacts(t *testing.T) {
	now := time.Now().UTC()
	root := t.TempDir()
	records := staticRuntimeDiagnosticRecordLister{
		{ID: "diag-referenced", StorageKind: "local", StorageRelativePath: "diag-referenced/referenced.tar.gz", Status: "ready", ArchiveBytes: 1, ExpiresAt: now.Add(time.Hour)},
		{ID: "diag-missing", StorageKind: "local", StorageRelativePath: "diag-missing/missing.tar.gz", Status: "ready", ArchiveBytes: 1, ExpiresAt: now.Add(time.Hour)},
	}
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(root, 1<<30, time.Hour, records, 1<<20)
	storage.availableBytes = func(string) (int64, error) { return 1 << 40, nil }

	commitDiagnosticArchiveForTest(t, storage, "diag-referenced", "referenced.tar.gz")
	referencedPath := filepath.Join(root, "diag-referenced", "referenced.tar.gz")
	if err := os.Chtimes(referencedPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	commitDiagnosticArchiveForTest(t, storage, "diag-orphan", "orphan.tar.gz")
	orphanPath := filepath.Join(root, "diag-orphan", "orphan.tar.gz")
	if err := os.Chtimes(orphanPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	stalePartial := filepath.Join(root, "diag-stale", "stale.tar.gz.partial")
	if err := os.Mkdir(filepath.Dir(stalePartial), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stalePartial, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stalePartial, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	youngPartial := filepath.Join(root, "diag-young", "young.tar.gz.partial")
	if err := os.Mkdir(filepath.Dir(youngPartial), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(youngPartial, []byte("young"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(youngPartial, now.Add(-30*time.Minute), now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	invalidOrphan := filepath.Join(root, "diag-invalid-orphan", "invalid.tar.gz")
	if err := os.Mkdir(filepath.Dir(invalidOrphan), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalidOrphan, []byte("not-gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(invalidOrphan, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	symlinkCreated := false
	symlinkPath := filepath.Join(root, "diag-symlink")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(t.TempDir(), symlinkPath); err == nil {
			symlinkCreated = true
		}
	}

	result, err := storage.Reconcile(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedPartials != 1 || result.RemovedOrphans != 1 {
		t.Fatalf("unexpected reconcile counts: %+v", result)
	}
	if strings.Join(result.MissingReadyIDs, ",") != "diag-missing" {
		t.Fatalf("unexpected missing ready IDs: %v", result.MissingReadyIDs)
	}
	if symlinkCreated && !containsDiagnosticWarning(result.WarningCodes, "diagnostic-storage-symlink-refused") {
		t.Fatalf("missing symlink warning: %v", result.WarningCodes)
	}
	if !containsDiagnosticWarning(result.WarningCodes, "diagnostic-storage-orphan-invalid") {
		t.Fatalf("missing invalid orphan warning: %v", result.WarningCodes)
	}
	if strings.Contains(strings.Join(result.WarningCodes, ","), root) {
		t.Fatalf("warning leaked absolute root: %v", result.WarningCodes)
	}
	for _, kept := range []string{referencedPath, youngPartial, invalidOrphan} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("reconcile removed protected artifact %s: %v", filepath.Base(kept), err)
		}
	}
	if symlinkCreated {
		if _, err := os.Lstat(symlinkPath); err != nil {
			t.Fatalf("reconcile removed symlink entry: %v", err)
		}
	}
	for _, removed := range []string{stalePartial, orphanPath} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reconcile retained stale artifact %s: %v", filepath.Base(removed), err)
		}
	}
}

func TestLocalDiagnosticStorageRemoveIsValidatedAndIdempotent(t *testing.T) {
	root := t.TempDir()
	storage := newRuntimeDiagnosticArchiveStorageWithLimit(root, 1<<30, time.Hour, nil, 1<<20)
	storage.availableBytes = func(string) (int64, error) { return 1 << 40, nil }
	artifact := commitDiagnosticArchiveForTest(t, storage, "diag-remove", "remove.tar.gz")
	if err := storage.Remove(artifact.RelativePath); err != nil {
		t.Fatal(err)
	}
	if err := storage.Remove(artifact.RelativePath); err != nil {
		t.Fatalf("idempotent remove failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(artifact.RelativePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive remained after remove: %v", err)
	}
	for _, unsafe := range []string{"../outside.tar.gz", "/diag/archive.tar.gz", "diag/../archive.tar.gz"} {
		if err := storage.Remove(unsafe); err == nil {
			t.Fatalf("unsafe remove path accepted: %q", unsafe)
		}
	}

	partialDir := filepath.Join(root, "diag-partial")
	if err := os.Mkdir(partialDir, 0o700); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(partialDir, "partial.tar.gz.partial")
	if err := os.WriteFile(partialPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.RemovePartial("diag-partial"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial remained after removal: %v", err)
	}
	if err := storage.RemovePartial("../diag-partial"); err == nil {
		t.Fatal("unsafe partial identity accepted")
	}
}

type diagnosticArchiveEntry struct {
	Name     string
	Body     string
	Typeflag byte
	Linkname string
}

func diagnosticArchiveControlEntries(top string) []diagnosticArchiveEntry {
	return []diagnosticArchiveEntry{
		{Name: top + "/README.txt", Body: "diagnostic export"},
		{Name: top + "/manifest.json", Body: "{}"},
		{Name: top + "/collection-errors.txt"},
	}
}

func buildDiagnosticArchive(t *testing.T, _ string, entries []diagnosticArchiveEntry) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		typeflag := entry.Typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name: entry.Name, Mode: 0o600, Typeflag: typeflag, Linkname: entry.Linkname,
			ModTime: time.Unix(1, 0).UTC(),
		}
		if typeflag == tar.TypeReg || typeflag == tar.TypeRegA {
			header.Size = int64(len(entry.Body))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := io.Copy(tarWriter, strings.NewReader(entry.Body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func commitDiagnosticArchiveForTest(t *testing.T, storage RuntimeDiagnosticArchiveStorage, exportID, archiveName string) RuntimeDiagnosticLocalArtifact {
	t.Helper()
	top := strings.TrimSuffix(archiveName, ".tar.gz")
	sink, err := storage.Begin(exportID, archiveName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write(buildDiagnosticArchive(t, top, diagnosticArchiveControlEntries(top))); err != nil {
		sink.Abort()
		t.Fatal(err)
	}
	artifact, err := sink.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func containsDiagnosticWarning(warnings []string, expected string) bool {
	for _, warning := range warnings {
		if warning == expected {
			return true
		}
	}
	return false
}

type staticRuntimeDiagnosticRecordLister []store.DiagnosticExport

func (records staticRuntimeDiagnosticRecordLister) ListDiagnosticExportsForReconcile() ([]store.DiagnosticExport, error) {
	return append([]store.DiagnosticExport(nil), records...), nil
}
