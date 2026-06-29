package maintenance

import "time"

type Store interface {
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
