package store

import (
	"strings"
	"time"

	"aifar-deployment/backend/internal/logmask"
)

func (s *Store) AddAudit(actor, action, target, status, message string) error {
	target = logmask.Mask(target)
	message = logmask.Mask(message)
	_, err := s.db.Exec(`insert into audit_logs(actor,action,target,status,message,created_at) values(?,?,?,?,?,?)`,
		actor, action, target, status, message, time.Now())
	return err
}

func (s *Store) ListAudit() ([]Audit, error) {
	page, err := s.ListAuditPage(AuditQuery{Page: 1, PageSize: 300})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *Store) ListAuditPage(query AuditQuery) (AuditPage, error) {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 20
	}
	if query.PageSize > 200 {
		query.PageSize = 200
	}
	where, args := auditWhere(query)
	var total int
	if err := s.db.QueryRow(`select count(*) from audit_logs`+where, args...).Scan(&total); err != nil {
		return AuditPage{}, err
	}
	args = append(args, query.PageSize, (query.Page-1)*query.PageSize)
	rows, err := s.db.Query(`select id,actor,action,target,status,message,created_at from audit_logs`+where+` order by created_at desc limit ? offset ?`, args...)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		var a Audit
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Target, &a.Status, &a.Message, &a.CreatedAt); err != nil {
			return AuditPage{}, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, err
	}
	return AuditPage{Items: out, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

func (s *Store) DeleteAuditLogs(ids []int64) (int, error) {
	ids = uniqueInt64s(ids)
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	deleted := 0
	for _, id := range ids {
		res, err := tx.Exec(`delete from audit_logs where id=?`, id)
		if err != nil {
			return 0, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			deleted += int(rows)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *Store) DeleteAuditLogsBefore(cutoff time.Time) (int, error) {
	if cutoff.IsZero() {
		return 0, nil
	}
	return deleteBeforeInBatches(func() (int, error) {
		return s.DeleteAuditLogsBeforeBatch(cutoff, retentionDeleteBatchDefault)
	})
}

func auditWhere(query AuditQuery) (string, []any) {
	where := []string{}
	args := []any{}
	if query.Module != "" {
		where = append(where, `action like ?`)
		args = append(args, query.Module+".%")
	}
	if query.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, query.Status)
	}
	if len(where) == 0 {
		return "", args
	}
	return " where " + strings.Join(where, " and "), args
}
