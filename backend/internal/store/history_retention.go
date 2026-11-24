package store

import (
	"context"
	"time"
)

const historyRetentionBatchSize = 500

func (s *Store) DeleteStatusSnapshotHistoryBefore(ctx context.Context, cutoff time.Time) (int, error) {
	return s.deleteHistoryBefore(ctx, cutoff, `delete from status_snapshot_history where id in (
		select id from status_snapshot_history where created_at < ? order by id limit ?)`)
}

func (s *Store) DeleteAlertEventsBefore(ctx context.Context, cutoff time.Time) (int, error) {
	return s.deleteHistoryBefore(ctx, cutoff, `delete from alert_events where id in (
		select id from alert_events where created_at < ? order by id limit ?)`)
}

func (s *Store) deleteHistoryBefore(ctx context.Context, cutoff time.Time, query string) (int, error) {
	if cutoff.IsZero() {
		return 0, nil
	}
	total := 0
	for {
		result, err := s.db.ExecContext(ctx, query, cutoff, historyRetentionBatchSize)
		if err != nil {
			return total, err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += int(deleted)
		if deleted < historyRetentionBatchSize {
			return total, nil
		}
	}
}
