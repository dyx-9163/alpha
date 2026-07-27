package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxCredentialVersions = 3

var (
	ErrBoundCredentialNotFound      = errors.New("bound credential not found")
	ErrBoundCredentialSecretMissing = errors.New("bound credential secret is missing")
	ErrBoundCredentialAmbiguous     = errors.New("bound credential is ambiguous")
)

func (s *Store) ListCredentials(query CredentialQuery) ([]Credential, error) {
	clauses := []string{"1=1"}
	args := []any{}
	if kind := normalizeCredentialKind(query.Kind); kind != "" {
		clauses = append(clauses, "kind=?")
		args = append(args, kind)
	}
	if status := normalizeCredentialStatus(query.Status); status != "" {
		clauses = append(clauses, "status=?")
		args = append(args, status)
	}
	if q := strings.TrimSpace(query.Q); q != "" {
		clauses = append(clauses, "(name like ? or username like ? or endpoint like ? or app like ? or tags like ?)")
		like := "%" + q + "%"
		args = append(args, like, like, like, like, like)
	}
	rows, err := s.db.Query(`select id,name,kind,coalesce(username,''),coalesce(endpoint,''),scope,status,
		coalesce(app,''),coalesce(server_id,''),coalesce(
			(select ai.id from app_instances ai where ai.id=nullif(credentials.app_instance_id,'')),
			(select cb.app_instance_id from credential_bindings cb join app_instances ai on ai.id=cb.app_instance_id where cb.credential_id=credentials.id order by cb.created_at desc limit 1),
			''
		),coalesce(purpose,''),coalesce(tags,''),
		coalesce(secret_cipher,''),current_version,coalesce(created_by,''),created_at,updated_at
		from credentials where `+strings.Join(clauses, " and ")+` order by updated_at desc`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		item, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetCredential(id string, includeSecret bool) (Credential, error) {
	var item Credential
	var cipher string
	err := s.db.QueryRow(`select id,name,kind,coalesce(username,''),coalesce(endpoint,''),scope,status,
		coalesce(app,''),coalesce(server_id,''),coalesce(
			(select ai.id from app_instances ai where ai.id=nullif(credentials.app_instance_id,'')),
			(select cb.app_instance_id from credential_bindings cb join app_instances ai on ai.id=cb.app_instance_id where cb.credential_id=credentials.id order by cb.created_at desc limit 1),
			''
		),coalesce(purpose,''),coalesce(tags,''),
		coalesce(secret_cipher,''),current_version,coalesce(created_by,''),created_at,updated_at
		from credentials where id=?`, strings.TrimSpace(id)).
		Scan(&item.ID, &item.Name, &item.Kind, &item.Username, &item.Endpoint, &item.Scope, &item.Status,
			&item.App, &item.ServerID, &item.AppInstanceID, &item.Purpose, &item.Tags, &cipher, &item.CurrentVersion, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Credential{}, err
	}
	item.HasSecret = strings.TrimSpace(cipher) != ""
	item.SecretPreview = credentialSecretPreview(item)
	if includeSecret && item.HasSecret {
		secret, err := s.decodeCredentialSecret(cipher)
		if err != nil {
			return Credential{}, err
		}
		item.Secret = secret
	}
	return item, nil
}

func (s *Store) GetBoundCredential(appInstanceID, purpose string, includeSecret bool) (Credential, error) {
	rows, err := s.db.Query(`select c.id,c.name,c.kind,coalesce(c.username,''),coalesce(c.endpoint,''),c.scope,c.status,
		coalesce(c.app,''),coalesce(c.server_id,''),cb.app_instance_id,coalesce(c.purpose,''),coalesce(c.tags,''),
		coalesce(c.secret_cipher,''),c.current_version,coalesce(c.created_by,''),c.created_at,c.updated_at
		from credential_bindings cb join credentials c on c.id=cb.credential_id
		where cb.app_instance_id=? and cb.purpose=? and c.status='active'
		order by cb.created_at asc, cb.id asc, c.id asc`, strings.TrimSpace(appInstanceID), strings.TrimSpace(purpose))
	if err != nil {
		return Credential{}, err
	}
	defer rows.Close()
	items := []Credential{}
	ciphers := []string{}
	for rows.Next() {
		item, cipher, err := scanCredentialWithCipher(rows)
		if err != nil {
			return Credential{}, err
		}
		items = append(items, item)
		ciphers = append(ciphers, cipher)
	}
	if err := rows.Err(); err != nil {
		return Credential{}, err
	}
	if len(items) == 0 {
		return Credential{}, ErrBoundCredentialNotFound
	}
	if len(items) > 1 {
		return Credential{}, ErrBoundCredentialAmbiguous
	}
	if strings.TrimSpace(ciphers[0]) == "" {
		return Credential{}, ErrBoundCredentialSecretMissing
	}
	if includeSecret {
		secret, err := s.decodeCredentialSecret(ciphers[0])
		if err != nil {
			return Credential{}, err
		}
		items[0].Secret = secret
	}
	return items[0], nil
}

func (s *Store) SaveCredential(item Credential) (Credential, error) {
	item = normalizeCredential(item)
	if item.Name == "" || item.Kind == "" {
		return Credential{}, errors.New("credential name and kind are required")
	}
	now := time.Now()
	isNew := strings.TrimSpace(item.ID) == ""
	if isNew {
		item.ID = NewID("cred")
		item.CreatedAt = now
		item.CurrentVersion = 0
	} else {
		current, err := s.GetCredential(item.ID, false)
		if err != nil {
			return Credential{}, err
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = current.CreatedAt
		}
		if item.CurrentVersion <= 0 {
			item.CurrentVersion = current.CurrentVersion
		}
	}
	item.UpdatedAt = now
	secretCipher := ""
	secretFingerprint := ""
	hasSecretUpdate := len(item.Secret) > 0
	if hasSecretUpdate {
		cipher, fingerprint, err := s.encodeCredentialSecret(item.Secret)
		if err != nil {
			return Credential{}, err
		}
		secretCipher = cipher
		secretFingerprint = fingerprint
		item.CurrentVersion++
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Credential{}, err
	}
	defer tx.Rollback()
	if isNew {
		_, err = tx.Exec(`insert into credentials(id,name,kind,username,endpoint,scope,status,app,server_id,app_instance_id,purpose,tags,secret_cipher,secret_fingerprint,current_version,created_by,created_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			item.ID, item.Name, item.Kind, item.Username, item.Endpoint, item.Scope, item.Status, item.App, item.ServerID, item.AppInstanceID,
			item.Purpose, item.Tags, secretCipher, secretFingerprint, item.CurrentVersion, item.CreatedBy, item.CreatedAt, item.UpdatedAt)
	} else if hasSecretUpdate {
		_, err = tx.Exec(`update credentials set name=?,kind=?,username=?,endpoint=?,scope=?,status=?,app=?,server_id=?,app_instance_id=?,purpose=?,tags=?,
			secret_cipher=?,secret_fingerprint=?,current_version=?,updated_at=? where id=?`,
			item.Name, item.Kind, item.Username, item.Endpoint, item.Scope, item.Status, item.App, item.ServerID, item.AppInstanceID,
			item.Purpose, item.Tags, secretCipher, secretFingerprint, item.CurrentVersion, item.UpdatedAt, item.ID)
	} else {
		_, err = tx.Exec(`update credentials set name=?,kind=?,username=?,endpoint=?,scope=?,status=?,app=?,server_id=?,app_instance_id=?,purpose=?,tags=?,updated_at=? where id=?`,
			item.Name, item.Kind, item.Username, item.Endpoint, item.Scope, item.Status, item.App, item.ServerID, item.AppInstanceID,
			item.Purpose, item.Tags, item.UpdatedAt, item.ID)
	}
	if err != nil {
		return Credential{}, err
	}
	if hasSecretUpdate {
		if _, err := tx.Exec(`insert into credential_versions(id,credential_id,version,secret_cipher,secret_fingerprint,created_by,created_at) values(?,?,?,?,?,?,?)`,
			NewID("cver"), item.ID, item.CurrentVersion, secretCipher, secretFingerprint, item.CreatedBy, now); err != nil {
			return Credential{}, err
		}
		if err := pruneCredentialVersionsTx(tx, item.ID, maxCredentialVersions); err != nil {
			return Credential{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Credential{}, err
	}
	item.HasSecret = hasSecretUpdate || item.CurrentVersion > 0
	item.SecretPreview = credentialSecretPreview(item)
	item.Secret = nil
	return item, nil
}

func (s *Store) DeleteCredential(id string) error {
	id = strings.TrimSpace(id)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := cleanupStaleCredentialReferencesTx(tx); err != nil {
		return err
	}
	var bindings int
	if err := tx.QueryRow(`select count(*) from credential_bindings cb join app_instances ai on ai.id=cb.app_instance_id where cb.credential_id=?`, id).Scan(&bindings); err != nil {
		return err
	}
	if bindings > 0 {
		return fmt.Errorf("credential is bound to %d app instance(s)", bindings)
	}
	var references int
	if err := tx.QueryRow(`select count(*) from credential_references where credential_id=?`, id).Scan(&references); err != nil {
		return err
	}
	if references > 0 {
		return fmt.Errorf("credential is referenced by %d resource(s)", references)
	}
	if _, err := tx.Exec(`delete from credential_versions where credential_id=?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`delete from credentials where id=?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) BindCredential(binding CredentialBinding) (CredentialBinding, error) {
	binding.CredentialID = strings.TrimSpace(binding.CredentialID)
	binding.AppInstanceID = strings.TrimSpace(binding.AppInstanceID)
	binding.Purpose = strings.TrimSpace(binding.Purpose)
	binding.ServiceName = strings.TrimSpace(binding.ServiceName)
	if binding.CredentialID == "" || binding.AppInstanceID == "" || binding.Purpose == "" {
		return CredentialBinding{}, errors.New("credential id, app instance id and purpose are required")
	}
	if binding.ID == "" {
		binding.ID = NewID("cbind")
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`insert into credential_bindings(id,credential_id,app_instance_id,purpose,service_name,created_at)
		values(?,?,?,?,?,?)
		on conflict(credential_id,app_instance_id,purpose) do update set service_name=excluded.service_name`,
		binding.ID, binding.CredentialID, binding.AppInstanceID, binding.Purpose, binding.ServiceName, binding.CreatedAt)
	return binding, err
}

func (s *Store) CredentialBindings(credentialID string) ([]CredentialBinding, error) {
	rows, err := s.db.Query(`select cb.id,cb.credential_id,cb.app_instance_id,cb.purpose,coalesce(cb.service_name,''),cb.created_at
		from credential_bindings cb join app_instances ai on ai.id=cb.app_instance_id
		where cb.credential_id=? order by cb.created_at desc`, strings.TrimSpace(credentialID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialBinding{}
	for rows.Next() {
		var item CredentialBinding
		if err := rows.Scan(&item.ID, &item.CredentialID, &item.AppInstanceID, &item.Purpose, &item.ServiceName, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func cleanupStaleCredentialReferencesTx(tx *sql.Tx) error {
	if _, err := tx.Exec(`delete from credential_bindings
		where not exists (select 1 from app_instances ai where ai.id=credential_bindings.app_instance_id)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from credential_references
		where resource_type='app-instance'
		and not exists (select 1 from app_instances ai where ai.id=credential_references.resource_id)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`update credentials set app_instance_id=''
		where coalesce(app_instance_id,'') <> ''
		and not exists (select 1 from app_instances ai where ai.id=credentials.app_instance_id)`); err != nil {
		return err
	}
	return nil
}

func scanCredential(rows interface {
	Scan(dest ...any) error
}) (Credential, error) {
	item, _, err := scanCredentialWithCipher(rows)
	return item, err
}

func scanCredentialWithCipher(rows interface {
	Scan(dest ...any) error
}) (Credential, string, error) {
	var item Credential
	var cipher string
	err := rows.Scan(&item.ID, &item.Name, &item.Kind, &item.Username, &item.Endpoint, &item.Scope, &item.Status,
		&item.App, &item.ServerID, &item.AppInstanceID, &item.Purpose, &item.Tags, &cipher, &item.CurrentVersion, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Credential{}, "", err
	}
	item.HasSecret = strings.TrimSpace(cipher) != ""
	item.SecretPreview = credentialSecretPreview(item)
	return item, cipher, nil
}

func (s *Store) encodeCredentialSecret(secret map[string]string) (string, string, error) {
	normalized := normalizeSecretMap(secret)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", "", err
	}
	cipher, err := s.encryptSecret(string(raw))
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return cipher, hex.EncodeToString(sum[:]), nil
}

func (s *Store) decodeCredentialSecret(cipher string) (map[string]string, error) {
	raw, err := s.decryptSecret(cipher)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return normalizeSecretMap(out), nil
}

func pruneCredentialVersionsTx(tx *sql.Tx, credentialID string, keep int) error {
	rows, err := tx.Query(`select id from credential_versions where credential_id=? order by version desc`, credentialID)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) <= keep {
		return nil
	}
	for _, id := range ids[keep:] {
		if _, err := tx.Exec(`delete from credential_versions where id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

func normalizeCredential(item Credential) Credential {
	item.ID = strings.TrimSpace(item.ID)
	item.Name = strings.TrimSpace(item.Name)
	item.Kind = normalizeCredentialKind(item.Kind)
	item.Username = strings.TrimSpace(item.Username)
	item.Endpoint = strings.TrimSpace(item.Endpoint)
	item.Scope = normalizeCredentialScope(item.Scope)
	item.Status = normalizeCredentialStatusWithDefault(item.Status)
	item.App = strings.TrimSpace(item.App)
	item.ServerID = strings.TrimSpace(item.ServerID)
	item.AppInstanceID = strings.TrimSpace(item.AppInstanceID)
	item.Purpose = strings.TrimSpace(item.Purpose)
	item.Tags = strings.TrimSpace(item.Tags)
	item.CreatedBy = strings.TrimSpace(item.CreatedBy)
	item.Secret = normalizeSecretMap(item.Secret)
	return item
}

func normalizeCredentialKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

func normalizeCredentialScope(scope string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case "server", "app", "app_instance", "app-instance":
		return strings.ReplaceAll(scope, "_", "-")
	default:
		return "global"
	}
}

func normalizeCredentialStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "active", "retired", "invalid":
		return status
	default:
		return ""
	}
}

func normalizeCredentialStatusWithDefault(status string) string {
	if normalized := normalizeCredentialStatus(status); normalized != "" {
		return normalized
	}
	return "active"
}

func normalizeSecretMap(secret map[string]string) map[string]string {
	if len(secret) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range secret {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func credentialSecretPreview(item Credential) string {
	if strings.TrimSpace(item.Username) == "" {
		return "******"
	}
	return maskCredentialValue(item.Username)
}

func maskCredentialValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 2 {
		return string(runes[:1]) + "***"
	}
	if len(runes) <= 5 {
		return string(runes[:2]) + "***"
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-2:])
}
