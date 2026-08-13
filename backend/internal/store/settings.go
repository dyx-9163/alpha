package store

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Store) GetSetting(key, fallback string) string {
	var value string
	if err := s.db.QueryRow(`select value from settings where key=?`, key).Scan(&value); err != nil {
		return fallback
	}
	return value
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`insert into settings(key,value,updated_at) values(?,?,?)
		on conflict(key) do update set value=excluded.value, updated_at=excluded.updated_at`, normalizeSettingKey(key), value, time.Now())
	return err
}

func (s *Store) SetSettings(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now()
	for key, value := range values {
		normalizedKey := normalizeSettingKey(key)
		if normalizedKey == "" {
			return fmt.Errorf("invalid setting key")
		}
		if _, err := tx.Exec(`insert into settings(key,value,updated_at) values(?,?,?)
			on conflict(key) do update set value=excluded.value, updated_at=excluded.updated_at`, normalizedKey, value, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeploymentConcurrency(fallback int) int {
	value := s.GetSetting("deploymentConcurrency", fmt.Sprintf("%d", fallback))
	return NormalizeDeploymentConcurrency(value, fallback)
}

func (s *Store) LogRetentionDays(fallback int) int {
	value := s.GetSetting("logRetentionDays", fmt.Sprintf("%d", fallback))
	return NormalizeLogRetentionDays(value, fallback)
}

func NormalizeDeploymentConcurrency(value string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		n = fallback
	}
	if n < 1 {
		return 1
	}
	if n > 20 {
		return 20
	}
	return n
}

func NormalizeLogRetentionDays(value string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		n = fallback
	}
	if n < 1 {
		return 1
	}
	if n > 3650 {
		return 3650
	}
	return n
}

func normalizeSettingKey(key string) string {
	return strings.TrimSpace(key)
}
