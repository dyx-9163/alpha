package maintenance

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store interface {
	BackupDatabase(path string) (int64, string, error)
	DeleteAuditLogsBefore(cutoff time.Time) (int, error)
	DeleteFinishedTasksBefore(cutoff time.Time) (int, error)
}

type RetentionConfig struct {
	AuditRetentionDays int
	TaskRetentionDays  int
}

type RetentionPlan struct {
	AuditCutoff time.Time
	TaskCutoff  time.Time
}

type DatabaseBackup struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"createdAt"`
}

type DatabaseBackupVerification struct {
	Backup         DatabaseBackup `json:"backup"`
	IntegrityCheck string         `json:"integrityCheck"`
	RequiredTables []string       `json:"requiredTables"`
	MissingTables  []string       `json:"missingTables"`
	VerifiedAt     time.Time      `json:"verifiedAt"`
	OK             bool           `json:"ok"`
}

type Service struct {
	store Store
	cfg   RetentionConfig
}

func NewService(store Store, cfg RetentionConfig) Service {
	return Service{store: store, cfg: cfg}
}

func (s Service) Plan(now time.Time) RetentionPlan {
	if now.IsZero() {
		now = time.Now()
	}
	plan := RetentionPlan{}
	if s.cfg.AuditRetentionDays > 0 {
		plan.AuditCutoff = now.AddDate(0, 0, -s.cfg.AuditRetentionDays)
	}
	if s.cfg.TaskRetentionDays > 0 {
		plan.TaskCutoff = now.AddDate(0, 0, -s.cfg.TaskRetentionDays)
	}
	return plan
}

func (s Service) CleanupAudit(plan RetentionPlan) (int, error) {
	if plan.AuditCutoff.IsZero() {
		return 0, nil
	}
	return s.store.DeleteAuditLogsBefore(plan.AuditCutoff)
}

func (s Service) CleanupTasks(plan RetentionPlan) (int, error) {
	if plan.TaskCutoff.IsZero() {
		return 0, nil
	}
	return s.store.DeleteFinishedTasksBefore(plan.TaskCutoff)
}

func (s Service) BackupDatabase(dir string, now time.Time) (DatabaseBackup, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return DatabaseBackup{}, fmt.Errorf("database backup directory is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	name := fmt.Sprintf("aifar-control-plane-%s-%d.db", now.Format("20060102-150405"), now.UnixNano())
	path := filepath.Join(dir, name)
	size, checksum, err := s.store.BackupDatabase(path)
	if err != nil {
		return DatabaseBackup{}, err
	}
	return DatabaseBackup{Name: name, Path: path, Size: size, SHA256: checksum, CreatedAt: now}, nil
}

func (s Service) ListDatabaseBackups(dir string) ([]DatabaseBackup, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("database backup directory is required")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "aifar-control-plane-*.db"))
	if err != nil {
		return nil, err
	}
	out := make([]DatabaseBackup, 0, len(matches))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			continue
		}
		checksum, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		out = append(out, DatabaseBackup{
			Name:      filepath.Base(path),
			Path:      path,
			Size:      info.Size(),
			SHA256:    checksum,
			CreatedAt: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s Service) GetDatabaseBackup(dir, name string) (DatabaseBackup, error) {
	name, err := validateBackupName(name)
	if err != nil {
		return DatabaseBackup{}, err
	}
	path, err := safeBackupPath(dir, name)
	if err != nil {
		return DatabaseBackup{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return DatabaseBackup{}, err
	}
	if info.IsDir() {
		return DatabaseBackup{}, fmt.Errorf("backup is not a file: %s", name)
	}
	checksum, err := fileSHA256(path)
	if err != nil {
		return DatabaseBackup{}, err
	}
	return DatabaseBackup{
		Name:      name,
		Path:      path,
		Size:      info.Size(),
		SHA256:    checksum,
		CreatedAt: info.ModTime(),
	}, nil
}

func (s Service) VerifyDatabaseBackup(dir, name string) (DatabaseBackupVerification, error) {
	backup, err := s.GetDatabaseBackup(dir, name)
	if err != nil {
		return DatabaseBackupVerification{}, err
	}
	db, err := sql.Open("sqlite", backup.Path)
	if err != nil {
		return DatabaseBackupVerification{}, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow(`pragma integrity_check`).Scan(&integrity); err != nil {
		return DatabaseBackupVerification{}, err
	}
	required := []string{"users", "servers", "tasks", "task_logs", "task_targets", "task_steps", "audit_logs", "resources", "app_instances", "storage_items", "settings"}
	rows, err := db.Query(`select name from sqlite_master where type='table'`)
	if err != nil {
		return DatabaseBackupVerification{}, err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return DatabaseBackupVerification{}, err
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		return DatabaseBackupVerification{}, err
	}
	missing := []string{}
	for _, table := range required {
		if !found[table] {
			missing = append(missing, table)
		}
	}
	return DatabaseBackupVerification{
		Backup:         backup,
		IntegrityCheck: integrity,
		RequiredTables: required,
		MissingTables:  missing,
		VerifiedAt:     time.Now(),
		OK:             integrity == "ok" && len(missing) == 0,
	}, nil
}

func (s Service) DeleteDatabaseBackups(dir string, names []string) (int, []string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return 0, nil, fmt.Errorf("database backup directory is required")
	}
	deletedNames := []string{}
	seen := map[string]bool{}
	for _, raw := range names {
		name, err := validateBackupName(raw)
		if err != nil {
			return 0, deletedNames, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		path, err := safeBackupPath(dir, name)
		if err != nil {
			return 0, deletedNames, err
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, deletedNames, err
		}
		deletedNames = append(deletedNames, name)
	}
	return len(deletedNames), deletedNames, nil
}

func validateBackupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("backup name is required")
	}
	if filepath.Base(name) != name || strings.Contains(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, ":") {
		return "", fmt.Errorf("invalid backup name: %s", name)
	}
	if !strings.HasPrefix(name, "aifar-control-plane-") || !strings.HasSuffix(name, ".db") {
		return "", fmt.Errorf("invalid backup name: %s", name)
	}
	return name, nil
}

func safeBackupPath(dir, name string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(absDir, name)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("backup path escapes backup directory")
	}
	return absPath, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
