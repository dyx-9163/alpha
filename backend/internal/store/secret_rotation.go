package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type secretRotationTarget struct {
	table  string
	column string
}

var credentialSecretRotationTargets = []secretRotationTarget{
	{table: "servers", column: "password"},
	{table: "servers", column: "private_key"},
	{table: "credentials", column: "secret_cipher"},
	{table: "credential_versions", column: "secret_cipher"},
	{table: "storage_items", column: "secret_key"},
	{table: "nacos_config_revisions", column: "content_cipher"},
}

// ValidateCredentialSecrets verifies that every encrypted value in a known
// credential column can be decrypted with the configured current or previous
// secret. It never mutates the database or the Store's key state.
func (s *Store) ValidateCredentialSecrets() error {
	current, previous := s.secretKeys()
	defer zeroSecretKey(current)
	defer zeroSecretKey(previous)

	for _, target := range credentialSecretRotationTargets {
		if err := validateSecretColumn(s.db, target, current, previous); err != nil {
			return err
		}
	}
	return nil
}

// RotateCredentialSecrets atomically re-encrypts every known encrypted store
// column with the current credential secret. The previous secret remains
// available when rotation fails and is removed from this Store after commit.
// Legacy plaintext values are encrypted during rotation; empty values remain
// empty.
func (s *Store) RotateCredentialSecrets() (int, error) {
	current, previous := s.secretKeys()
	defer zeroSecretKey(current)
	defer zeroSecretKey(previous)
	if len(previous) == 0 {
		return 0, errors.New("previous credential secret is not configured")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rotated := 0
	for _, target := range credentialSecretRotationTargets {
		count, err := rotateSecretColumnTx(tx, target, current, previous)
		if err != nil {
			return 0, err
		}
		rotated += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}

	s.clearPreviousSecretKey()
	return rotated, nil
}

func validateSecretColumn(db *sql.DB, target secretRotationTarget, current, previous []byte) error {
	query := fmt.Sprintf("select id, coalesce(%s,'') from %s", target.column, target.table)
	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("read %s.%s for credential secret validation: %w", target.table, target.column, err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, value string
		if err := rows.Scan(&id, &value); err != nil {
			return fmt.Errorf("scan %s.%s for credential secret validation: %w", target.table, target.column, err)
		}
		if strings.TrimSpace(value) == "" || !strings.HasPrefix(value, encryptedSecretPrefix) {
			continue
		}
		if _, err := decryptSecretWithKeys(value, current, previous); err != nil {
			return fmt.Errorf("validate %s.%s row %s: encrypted value cannot be decrypted with configured credential secrets", target.table, target.column, id)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s.%s for credential secret validation: %w", target.table, target.column, err)
	}
	return nil
}

func rotateSecretColumnTx(tx *sql.Tx, target secretRotationTarget, current, previous []byte) (int, error) {
	query := fmt.Sprintf("select id, coalesce(%s,'') from %s", target.column, target.table)
	rows, err := tx.Query(query)
	if err != nil {
		return 0, fmt.Errorf("read %s.%s for credential secret rotation: %w", target.table, target.column, err)
	}
	type secretRow struct {
		id    string
		value string
	}
	values := []secretRow{}
	for rows.Next() {
		var value secretRow
		if err := rows.Scan(&value.id, &value.value); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan %s.%s for credential secret rotation: %w", target.table, target.column, err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("read %s.%s for credential secret rotation: %w", target.table, target.column, err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close %s.%s credential secret rows: %w", target.table, target.column, err)
	}

	update := fmt.Sprintf("update %s set %s=? where id=?", target.table, target.column)
	rotated := 0
	for _, value := range values {
		if strings.TrimSpace(value.value) == "" {
			continue
		}
		plain, err := decryptSecretWithKeys(value.value, current, previous)
		if err != nil {
			return 0, fmt.Errorf("decrypt %s.%s row %s for credential secret rotation: %w", target.table, target.column, value.id, err)
		}
		cipher, err := encryptPlaintextSecret(plain, current)
		if err != nil {
			return 0, fmt.Errorf("encrypt %s.%s row %s for credential secret rotation: %w", target.table, target.column, value.id, err)
		}
		if _, err := tx.Exec(update, cipher, value.id); err != nil {
			return 0, fmt.Errorf("update %s.%s row %s for credential secret rotation: %w", target.table, target.column, value.id, err)
		}
		rotated++
	}
	return rotated, nil
}

func zeroSecretKey(key []byte) {
	for index := range key {
		key[index] = 0
	}
}
