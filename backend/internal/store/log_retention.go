package store

import (
	"fmt"
	"strings"
	"time"
)

const retentionDeleteBatchDefault = 500

func (s *Store) DeleteAuditLogsBeforeBatch(cutoff time.Time, limit int) (int, error) {
	return s.deleteRowsBeforeBatch("audit_logs", "created_at", cutoff, limit)
}

func (s *Store) DeleteStatusSnapshotHistoryBeforeBatch(cutoff time.Time, limit int) (int, error) {
	return s.deleteRowsBeforeBatch("status_snapshot_history", "created_at", cutoff, limit)
}

func (s *Store) DeleteAlertEventsBeforeBatch(cutoff time.Time, limit int) (int, error) {
	return s.deleteRowsBeforeBatch("alert_events", "created_at", cutoff, limit)
}

func (s *Store) DeleteFinishedTasksBeforeBatch(cutoff time.Time, limit int) (int, error) {
	if cutoff.IsZero() {
		return 0, nil
	}
	limit = normalizeRetentionDeleteBatchLimit(limit)
	rows, err := s.db.Query(`select id from tasks where finished_at is not null and finished_at < ? and status in ('success','failed','cancelled','timeout') order by finished_at, id limit ?`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	return s.DeleteTasks(ids)
}

func (s *Store) deleteRowsBeforeBatch(table, timestampColumn string, cutoff time.Time, limit int) (int, error) {
	if cutoff.IsZero() {
		return 0, nil
	}
	limit = normalizeRetentionDeleteBatchLimit(limit)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	selectSQL := fmt.Sprintf(`select id from %s where %s < ? order by id limit ?`, table, timestampColumn)
	rows, err := tx.Query(selectSQL, cutoff, limit)
	if err != nil {
		return 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 0, nil
	}

	args := make([]any, 0, len(ids))
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, "?")
	}
	deleteSQL := fmt.Sprintf(`delete from %s where id in (%s)`, table, strings.Join(placeholders, ","))
	res, err := tx.Exec(deleteSQL, args...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	deleted, _ := res.RowsAffected()
	return int(deleted), nil
}

func normalizeRetentionDeleteBatchLimit(limit int) int {
	if limit <= 0 || limit > retentionDeleteBatchDefault {
		return retentionDeleteBatchDefault
	}
	return limit
}

func deleteBeforeInBatches(batch func() (int, error)) (int, error) {
	total := 0
	for {
		deleted, err := batch()
		if err != nil {
			return total, err
		}
		if deleted == 0 {
			return total, nil
		}
		total += deleted
		if deleted < retentionDeleteBatchDefault {
			return total, nil
		}
	}
}
