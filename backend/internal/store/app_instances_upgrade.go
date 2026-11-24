package store

import (
	"database/sql"
	"errors"
	"time"
)

var ErrAppInstanceConflict = errors.New("app instance changed concurrently")

type AppInstanceVersionUpdate struct {
	Expected AppInstance
	Version  string
	Metadata map[string]any
}

// SaveAppInstanceVersionsIfUnchanged commits verified versions as one
// transaction and preserves runtime observations made during the upgrade.
func (s *Store) SaveAppInstanceVersionsIfUnchanged(updates []AppInstanceVersionUpdate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, update := range updates {
		current, err := appInstanceMetadataAtRevision(tx, update.Expected)
		if err != nil {
			return err
		}
		metadata, err := mergeAppInstanceMetadata(current, update.Metadata)
		if err != nil {
			return err
		}
		updatedAt := time.Now().UTC()
		if updatedAt.Sub(update.Expected.UpdatedAt) < time.Millisecond {
			updatedAt = update.Expected.UpdatedAt.Add(time.Millisecond)
		}
		if _, err = tx.Exec(`update app_instances set version=?,metadata=?,updated_at=? where id=?`,
			update.Version, metadata, updatedAt, update.Expected.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveAppInstancesIfUnchanged commits verified member changes as one transaction.
func (s *Store) SaveAppInstancesIfUnchanged(instances []AppInstance) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, instance := range instances {
		var current time.Time
		err := tx.QueryRow("select updated_at from app_instances where id=?", instance.ID).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAppInstanceConflict
		}
		if err != nil {
			return err
		}
		if !current.Equal(instance.UpdatedAt) {
			return ErrAppInstanceConflict
		}
		updatedAt := time.Now().UTC()
		if updatedAt.Sub(current) < time.Millisecond {
			updatedAt = current.Add(time.Millisecond)
		}
		result, err := tx.Exec(`update app_instances
			set version=?,server_id=?,status=?,topology=?,metadata=?,updated_at=?
			where id=?`,
			instance.Version, instance.ServerID, instance.Status, instance.Topology,
			instance.Metadata, updatedAt, instance.ID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrAppInstanceConflict
		}
	}
	return tx.Commit()
}
