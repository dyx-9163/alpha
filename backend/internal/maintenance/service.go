package maintenance

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
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
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"createdAt"`
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
	return DatabaseBackup{Path: path, Size: size, SHA256: checksum, CreatedAt: now}, nil
}
