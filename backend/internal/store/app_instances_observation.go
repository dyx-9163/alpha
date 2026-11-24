package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"
)

type AppInstanceObservation struct {
	Status              string
	DefaultTopology     string
	Metadata            map[string]any
	ClearInstallFailure bool
}

// SaveAppInstanceObservation merges runtime fields without advancing the
// instance revision. A check started before a durable change is obsolete.
func (s *Store) SaveAppInstanceObservation(instance AppInstance, observation AppInstanceObservation) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := appInstanceMetadataAtRevision(tx, instance)
	if errors.Is(err, ErrAppInstanceConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	fields := observation.Metadata
	if observation.ClearInstallFailure {
		fields = map[string]any{"installFailed": nil, "failedAt": nil, "taskId": nil, "error": nil}
		maps.Copy(fields, observation.Metadata)
	}
	metadata, err := mergeAppInstanceMetadata(current, fields)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`update app_instances
		set status=coalesce(nullif(?,''),status), topology=coalesce(nullif(topology,''),?), metadata=?
		where id=?`, observation.Status, observation.DefaultTopology, metadata, instance.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func appInstanceMetadataAtRevision(tx *sql.Tx, expected AppInstance) (string, error) {
	var metadata string
	var updatedAt time.Time
	err := tx.QueryRow(`select metadata,updated_at from app_instances
		where id=? and app=? and version=? and server_id=?`,
		expected.ID, expected.App, expected.Version, expected.ServerID).Scan(&metadata, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAppInstanceConflict
	}
	if err != nil {
		return "", err
	}
	if !updatedAt.Equal(expected.UpdatedAt) {
		return "", ErrAppInstanceConflict
	}
	return metadata, nil
}

func mergeAppInstanceMetadata(current string, fields map[string]any) (string, error) {
	metadata := map[string]json.RawMessage{}
	if strings.TrimSpace(current) != "" {
		if err := json.Unmarshal([]byte(current), &metadata); err != nil {
			return "", err
		}
	}
	if metadata == nil {
		metadata = map[string]json.RawMessage{}
	}
	for key, value := range fields {
		if value == nil {
			delete(metadata, key)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		metadata[key] = encoded
	}
	encoded, err := json.Marshal(metadata)
	return string(encoded), err
}
