package backuprepo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func TestNewRejectsEmptyRoot(t *testing.T) {
	for _, root := range []string{"", " ", "\t"} {
		t.Run(root, func(t *testing.T) {
			if _, err := New(root); err == nil {
				t.Fatalf("New(%q) succeeded", root)
			}
		})
	}
}

func TestNewRejectsSymlinkedRootBoundary(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "repository")
	requireSymlink(t, target, link)
	if _, err := New(link); err == nil {
		t.Fatal("New accepted a symlinked repository root")
	}
}

func TestPrepareRejectsUnmanagedBackupIDs(t *testing.T) {
	repo, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(string(filepath.Separator), "absolute-backup")
	ids := []string{"", ".", "..", "../escape", "child/escape", `child\escape`, abs}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			if _, err := repo.Prepare(id); err == nil {
				t.Fatalf("Prepare(%q) succeeded", id)
			}
		})
	}
}

func TestPrepareCreatesOnlyManagedDirectoryAndPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mysql-backups")
	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := repo.Prepare("backup_mysql_20260727_test")
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(root, "backup_mysql_20260727_test")
	if paths.Directory != wantDirectory ||
		paths.PartialArchive != filepath.Join(wantDirectory, "dump.tar.partial") ||
		paths.Archive != filepath.Join(wantDirectory, "dump.tar") ||
		paths.Manifest != filepath.Join(wantDirectory, "backup-manifest.json") ||
		paths.Checksums != filepath.Join(wantDirectory, "checksums.txt") {
		t.Fatalf("Prepare paths = %+v", paths)
	}
	entries, err := os.ReadDir(paths.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Prepare created files: %+v", entries)
	}
	if _, err := os.Lstat(paths.PartialArchive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Prepare must not create partial archive: %v", err)
	}
}

func TestPrepareRejectsSymlinkedBackupDirectory(t *testing.T) {
	root := t.TempDir()
	repo, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	requireSymlink(t, target, filepath.Join(root, "backup-link"))
	if _, err := repo.Prepare("backup-link"); err == nil {
		t.Fatal("Prepare accepted a symlinked backup directory")
	}
}

func TestCommitPromotesVerifiedPartialToExactLayout(t *testing.T) {
	repo, paths := prepareRepository(t, "backup-success")
	content := []byte("mysql dump tar payload")
	writePartial(t, paths, content)
	manifest := []byte(`{"backupId":"backup-success","app":"mysql"}`)
	digest := digestBytes(content)

	if err := repo.Commit(paths, manifest, digest, int64(len(content))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.PartialArchive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial still exists after commit: %v", err)
	}
	entries, err := os.ReadDir(paths.Directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	want := []string{"backup-manifest.json", "checksums.txt", "dump.tar"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("layout = %v, want %v", names, want)
	}
	gotManifest, err := os.ReadFile(paths.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotManifest) != string(manifest) {
		t.Fatalf("manifest = %q", gotManifest)
	}
	gotChecksums, err := os.ReadFile(paths.Checksums)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotChecksums) != digest+"  dump.tar\n" {
		t.Fatalf("checksums = %q", gotChecksums)
	}
}

func TestCommitRejectsInvalidPartialAndLeavesNoFinalArchive(t *testing.T) {
	content := []byte("archive")
	tests := []struct {
		name         string
		makePartial  func(t *testing.T, paths BackupPaths)
		expectedHash string
		expectedSize int64
	}{
		{
			name: "checksum mismatch",
			makePartial: func(t *testing.T, paths BackupPaths) {
				writePartial(t, paths, content)
			},
			expectedHash: strings.Repeat("0", 64), expectedSize: int64(len(content)),
		},
		{
			name: "size mismatch",
			makePartial: func(t *testing.T, paths BackupPaths) {
				writePartial(t, paths, content)
			},
			expectedHash: digestBytes(content), expectedSize: int64(len(content) + 1),
		},
		{
			name: "non regular archive",
			makePartial: func(t *testing.T, paths BackupPaths) {
				if err := os.Mkdir(paths.PartialArchive, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			expectedHash: digestBytes(content), expectedSize: int64(len(content)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, paths := prepareRepository(t, "backup-failed")
			tt.makePartial(t, paths)
			err := repo.Commit(paths, []byte(`{"backupId":"backup-failed"}`), tt.expectedHash, tt.expectedSize)
			if err == nil {
				t.Fatal("Commit succeeded")
			}
			if _, err := os.Lstat(paths.Archive); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("final archive exists after failed commit: %v", err)
			}
			if _, err := os.Lstat(paths.PartialArchive); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial remains after failed commit: %v", err)
			}
		})
	}
}

func TestCommitRejectsForgedPathsAndManifestID(t *testing.T) {
	t.Run("paths outside repository", func(t *testing.T) {
		repo, paths := prepareRepository(t, "backup-forged")
		outside := filepath.Join(t.TempDir(), "dump.tar.partial")
		if err := os.WriteFile(outside, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths.PartialArchive = outside
		if err := repo.Commit(paths, []byte(`{"backupId":"backup-forged"}`), digestBytes([]byte("archive")), 7); err == nil {
			t.Fatal("Commit accepted an outside partial path")
		}
	})
	t.Run("manifest id mismatch", func(t *testing.T) {
		repo, paths := prepareRepository(t, "backup-one")
		writePartial(t, paths, []byte("archive"))
		if err := repo.Commit(paths, []byte(`{"backupId":"backup-two"}`), digestBytes([]byte("archive")), 7); err == nil {
			t.Fatal("Commit accepted a mismatched manifest backup ID")
		}
		if _, err := os.Lstat(paths.Archive); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("final archive exists after manifest rejection: %v", err)
		}
	})
}

func TestVerifyRequiresRecordManifestArchiveAndChecksumsAgreement(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, paths BackupPaths, backup *store.AppBackup)
	}{
		{
			name: "record path outside root",
			mutate: func(t *testing.T, paths BackupPaths, backup *store.AppBackup) {
				backup.Path = filepath.Join(t.TempDir(), "dump.tar")
			},
		},
		{
			name: "unsupported extension",
			mutate: func(t *testing.T, paths BackupPaths, backup *store.AppBackup) {
				backup.Path = filepath.Join(paths.Directory, "dump.zip")
			},
		},
		{
			name: "manifest record id mismatch",
			mutate: func(t *testing.T, paths BackupPaths, backup *store.AppBackup) {
				if err := os.WriteFile(paths.Manifest, []byte(`{"backupId":"another-backup"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "archive checksum mismatch",
			mutate: func(t *testing.T, paths BackupPaths, backup *store.AppBackup) {
				if err := os.WriteFile(paths.Archive, []byte("tampered payloa"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "archive size mismatch",
			mutate: func(t *testing.T, paths BackupPaths, backup *store.AppBackup) {
				backup.Size++
			},
		},
		{
			name: "checksums mismatch",
			mutate: func(t *testing.T, paths BackupPaths, backup *store.AppBackup) {
				if err := os.WriteFile(paths.Checksums, []byte(strings.Repeat("0", 64)+"  dump.tar\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "non regular archive",
			mutate: func(t *testing.T, paths BackupPaths, backup *store.AppBackup) {
				if err := os.Remove(paths.Archive); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(paths.Archive, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, paths, backup := committedBackup(t, "backup-verify")
			tt.mutate(t, paths, &backup)
			if _, err := repo.Verify(backup); err == nil {
				t.Fatal("Verify succeeded")
			}
		})
	}
}

func TestVerifyReturnsValidatedArtifact(t *testing.T) {
	repo, paths, backup := committedBackup(t, "backup-verified")
	verification, err := repo.Verify(backup)
	if err != nil {
		t.Fatal(err)
	}
	if verification.Paths != paths || verification.SHA256 != backup.Checksum || verification.Size != backup.Size {
		t.Fatalf("verification = %+v", verification)
	}
	if !strings.Contains(string(verification.Manifest), `"backupId":"backup-verified"`) {
		t.Fatalf("manifest = %q", verification.Manifest)
	}
}

func TestVerifyRejectsSymlinkedArchive(t *testing.T) {
	repo, paths, backup := committedBackup(t, "backup-symlink")
	if err := os.Remove(paths.Archive); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.tar")
	if err := os.WriteFile(target, []byte("archive payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, target, paths.Archive)
	if _, err := repo.Verify(backup); err == nil {
		t.Fatal("Verify accepted symlinked archive")
	}
}

func TestDeleteRemovesOnlyVerifiedManagedBackupDirectory(t *testing.T) {
	repo, paths, backup := committedBackup(t, "backup-delete")
	if err := repo.Delete(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup directory remains: %v", err)
	}
	if info, err := os.Stat(repo.root); err != nil || !info.IsDir() {
		t.Fatalf("repository root was removed: info=%v err=%v", info, err)
	}
}

func TestDeleteRejectsUnverifiedOrOutsideTargets(t *testing.T) {
	t.Run("outside record path", func(t *testing.T) {
		repo, paths, backup := committedBackup(t, "backup-delete-outside")
		backup.Path = filepath.Join(t.TempDir(), "dump.tar")
		if err := repo.Delete(backup); err == nil {
			t.Fatal("Delete accepted outside record path")
		}
		if _, err := os.Stat(paths.Archive); err != nil {
			t.Fatalf("managed archive changed: %v", err)
		}
	})
	t.Run("repository root", func(t *testing.T) {
		repo, _, backup := committedBackup(t, "backup-delete-root")
		backup.ID = "."
		backup.Path = repo.root
		if err := repo.Delete(backup); err == nil {
			t.Fatal("Delete accepted repository root")
		}
		if _, err := os.Stat(repo.root); err != nil {
			t.Fatalf("repository root changed: %v", err)
		}
	})
}

func TestRetentionCandidatesKeepLatestSuccessfulBackup(t *testing.T) {
	repo, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	backups := []store.AppBackup{
		{ID: "failed-newest", Status: "failed", CreatedAt: base.Add(5 * time.Hour)},
		{ID: "success-oldest", Status: "success", CreatedAt: base.Add(time.Hour)},
		{ID: "deleted", Status: "deleted", CreatedAt: base.Add(4 * time.Hour)},
		{ID: "success-newest", Status: "success", CreatedAt: base.Add(3 * time.Hour)},
		{ID: "success-middle", Status: "success", CreatedAt: base.Add(2 * time.Hour)},
	}

	got := repo.RetentionCandidates(backups, 1)
	if len(got) != 2 || got[0].ID != "success-middle" || got[1].ID != "success-oldest" {
		t.Fatalf("candidates = %+v", got)
	}
	got = repo.RetentionCandidates(backups, 0)
	if len(got) != 2 || got[0].ID != "success-middle" || got[1].ID != "success-oldest" {
		t.Fatalf("keepLast=0 candidates = %+v", got)
	}
	if backups[0].ID != "failed-newest" || backups[1].ID != "success-oldest" {
		t.Fatalf("input order mutated: %+v", backups)
	}
}

func prepareRepository(t *testing.T, backupID string) (*Repository, BackupPaths) {
	t.Helper()
	repo, err := New(filepath.Join(t.TempDir(), "mysql-backups"))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := repo.Prepare(backupID)
	if err != nil {
		t.Fatal(err)
	}
	return repo, paths
}

func committedBackup(t *testing.T, backupID string) (*Repository, BackupPaths, store.AppBackup) {
	t.Helper()
	repo, paths := prepareRepository(t, backupID)
	content := []byte("archive payload")
	writePartial(t, paths, content)
	digest := digestBytes(content)
	manifest := []byte(`{"backupId":"` + backupID + `","app":"mysql"}`)
	if err := repo.Commit(paths, manifest, digest, int64(len(content))); err != nil {
		t.Fatal(err)
	}
	return repo, paths, store.AppBackup{
		ID: backupID, App: "mysql", BackupType: "logical-full", Status: "success",
		Path: paths.Archive, Checksum: digest, Size: int64(len(content)), CreatedAt: time.Now().UTC(),
	}
}

func writePartial(t *testing.T, paths BackupPaths, content []byte) {
	t.Helper()
	if err := os.WriteFile(paths.PartialArchive, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func requireSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		lower := strings.ToLower(err.Error())
		if runtime.GOOS == "windows" && (errors.Is(err, os.ErrPermission) || strings.Contains(lower, "privilege")) {
			t.Skipf("Windows denied symlink creation: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
}
