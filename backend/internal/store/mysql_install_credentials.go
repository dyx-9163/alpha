package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrMySQLInstallAdminCredentialBinding = errors.New("MySQL install admin credential binding failed")

type MySQLInstallAdminCredential struct {
	Instance   AppInstance
	Credential Credential
	Generated  bool
}

func (s *Store) MarkMySQLInstallInstancesFailed(items []AppInstance) error {
	if err := validateMySQLInstallInstanceSet(items); err != nil {
		return ErrMySQLInstallAdminCredentialBinding
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ErrMySQLInstallAdminCredentialBinding
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	now := time.Now()
	for _, item := range items {
		if item.ID == "" || seen[item.ID] || item.App != "mysql" || item.Status != "failed" || strings.TrimSpace(item.Metadata) == "" {
			return ErrMySQLInstallAdminCredentialBinding
		}
		seen[item.ID] = true
		var current AppInstance
		if err := tx.QueryRow(`select id,app,version,server_id,status,topology,metadata,created_at,updated_at from app_instances where id=?`, item.ID).
			Scan(&current.ID, &current.App, &current.Version, &current.ServerID, &current.Status, &current.Topology, &current.Metadata, &current.CreatedAt, &current.UpdatedAt); err != nil {
			return ErrMySQLInstallAdminCredentialBinding
		}
		if current.App != "mysql" || current.Version != item.Version || current.ServerID != item.ServerID || current.Topology != item.Topology ||
			mysqlInstallClusterID(current.Metadata) != mysqlInstallClusterID(item.Metadata) || !current.CreatedAt.Equal(item.CreatedAt) {
			return ErrMySQLInstallAdminCredentialBinding
		}
		result, err := tx.Exec(`update app_instances set status='failed',metadata=?,updated_at=? where id=?`, item.Metadata, now, item.ID)
		if err != nil {
			return ErrMySQLInstallAdminCredentialBinding
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrMySQLInstallAdminCredentialBinding
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrMySQLInstallAdminCredentialBinding
	}
	return nil
}

func (s *Store) SaveMySQLInstallAdminCredentials(items []MySQLInstallAdminCredential) error {
	instances := make([]AppInstance, 0, len(items))
	for _, item := range items {
		instances = append(instances, item.Instance)
	}
	if err := validateMySQLInstallInstanceSet(instances); err != nil {
		return ErrMySQLInstallAdminCredentialBinding
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ErrMySQLInstallAdminCredentialBinding
	}
	defer tx.Rollback()
	now := time.Now()
	seenInstances := map[string]bool{}
	validatedSelected := map[string]bool{}
	for _, item := range items {
		instanceID := strings.TrimSpace(item.Instance.ID)
		if instanceID == "" || seenInstances[instanceID] {
			return ErrMySQLInstallAdminCredentialBinding
		}
		seenInstances[instanceID] = true
		var app, version, serverID, topology, currentMetadata string
		var createdAt time.Time
		if err := tx.QueryRow(`select app,version,server_id,topology,metadata,created_at from app_instances where id=?`, instanceID).Scan(&app, &version, &serverID, &topology, &currentMetadata, &createdAt); err != nil ||
			app != "mysql" || version != item.Instance.Version || serverID != item.Instance.ServerID || topology != item.Instance.Topology ||
			mysqlInstallClusterID(currentMetadata) != mysqlInstallClusterID(item.Instance.Metadata) || !createdAt.Equal(item.Instance.CreatedAt) {
			return ErrMySQLInstallAdminCredentialBinding
		}
		var existing int
		if err := tx.QueryRow(`select count(*) from credential_bindings cb join credentials c on c.id=cb.credential_id where cb.app_instance_id=? and cb.purpose='admin' and c.status='active'`, instanceID).Scan(&existing); err != nil || existing != 0 {
			return ErrMySQLInstallAdminCredentialBinding
		}
		credentialID := strings.TrimSpace(item.Credential.ID)
		if item.Generated {
			credentialID, err = s.insertGeneratedMySQLInstallCredentialTx(tx, item.Credential, instanceID, now)
			if err != nil {
				return ErrMySQLInstallAdminCredentialBinding
			}
		} else if credentialID == "" {
			return ErrMySQLInstallAdminCredentialBinding
		} else if !validatedSelected[credentialID] {
			if err := s.validateSelectedMySQLInstallCredentialTx(tx, item.Credential); err != nil {
				return ErrMySQLInstallAdminCredentialBinding
			}
			validatedSelected[credentialID] = true
		}
		if _, err := tx.Exec(`insert into credential_bindings(id,credential_id,app_instance_id,purpose,service_name,created_at) values(?,?,?,?,?,?)`,
			NewID("cbind"), credentialID, instanceID, "admin", "mysql", now); err != nil {
			return ErrMySQLInstallAdminCredentialBinding
		}
		metadata, _ := json.Marshal(map[string]any{"app": "mysql", "serverId": item.Instance.ServerID})
		lifecycle := "retain"
		if item.Generated {
			lifecycle = "delete-with-resource"
		}
		if _, err := tx.Exec(`insert into credential_references(id,credential_id,resource_type,resource_id,purpose,generated,lifecycle_policy,metadata,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?)`,
			NewID("cref"), credentialID, "app-instance", instanceID, "admin", boolInt(item.Generated), lifecycle, string(metadata), now, now); err != nil {
			return ErrMySQLInstallAdminCredentialBinding
		}
	}
	for instanceID := range seenInstances {
		credential, cipher, err := readUniqueActiveMySQLAdminCredentialTx(tx, instanceID)
		if err != nil || strings.TrimSpace(credential.Username) == "" || strings.TrimSpace(cipher) == "" {
			return ErrMySQLInstallAdminCredentialBinding
		}
		secret, err := s.decodeCredentialSecret(cipher)
		if err != nil || strings.TrimSpace(secret["password"]) == "" {
			return ErrMySQLInstallAdminCredentialBinding
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrMySQLInstallAdminCredentialBinding
	}
	return nil
}

func validateMySQLInstallInstanceSet(items []AppInstance) error {
	if len(items) == 0 {
		return ErrMySQLInstallAdminCredentialBinding
	}
	topology := strings.TrimSpace(items[0].Topology)
	if (topology == "standalone" && len(items) != 1) || (topology == "innodb-cluster" && len(items) < 3) {
		return ErrMySQLInstallAdminCredentialBinding
	}
	if topology != "standalone" && topology != "innodb-cluster" {
		return ErrMySQLInstallAdminCredentialBinding
	}
	clusterID := ""
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" || seen[item.ID] || item.App != "mysql" || item.Topology != topology {
			return ErrMySQLInstallAdminCredentialBinding
		}
		seen[item.ID] = true
		if topology != "innodb-cluster" {
			continue
		}
		candidate := mysqlInstallClusterID(item.Metadata)
		if candidate == "" || (clusterID != "" && candidate != clusterID) {
			return ErrMySQLInstallAdminCredentialBinding
		}
		clusterID = candidate
	}
	return nil
}

func mysqlInstallClusterID(metadata string) string {
	var value map[string]any
	if json.Unmarshal([]byte(metadata), &value) != nil {
		return ""
	}
	clusterID, _ := value["clusterId"].(string)
	return strings.TrimSpace(clusterID)
}

func (s *Store) insertGeneratedMySQLInstallCredentialTx(tx *sql.Tx, credential Credential, instanceID string, now time.Time) (string, error) {
	credential = normalizeCredential(credential)
	password := strings.TrimSpace(credential.Secret["password"])
	if credential.Name == "" || !strings.EqualFold(credential.Kind, "mysql") || strings.TrimSpace(credential.Username) == "" || password == "" {
		return "", ErrMySQLInstallAdminCredentialBinding
	}
	credential.ID = NewID("cred")
	credential.Status = "active"
	credential.Scope = "app-instance"
	credential.App = "mysql"
	credential.AppInstanceID = instanceID
	credential.Purpose = "admin"
	credential.CurrentVersion = 1
	credential.CreatedAt, credential.UpdatedAt = now, now
	cipher, fingerprint, err := s.encodeCredentialSecret(map[string]string{"password": password})
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`insert into credentials(id,name,kind,username,endpoint,scope,status,app,server_id,app_instance_id,purpose,tags,secret_cipher,secret_fingerprint,current_version,created_by,created_at,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		credential.ID, credential.Name, "mysql", credential.Username, credential.Endpoint, credential.Scope, credential.Status, credential.App, credential.ServerID,
		credential.AppInstanceID, credential.Purpose, credential.Tags, cipher, fingerprint, credential.CurrentVersion, credential.CreatedBy, now, now); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`insert into credential_versions(id,credential_id,version,secret_cipher,secret_fingerprint,created_by,created_at) values(?,?,?,?,?,?,?)`,
		NewID("cver"), credential.ID, 1, cipher, fingerprint, credential.CreatedBy, now); err != nil {
		return "", err
	}
	return credential.ID, nil
}

func (s *Store) validateSelectedMySQLInstallCredentialTx(tx *sql.Tx, expected Credential) error {
	var kind, username, status, cipher string
	var currentVersion int
	credentialID := strings.TrimSpace(expected.ID)
	if credentialID == "" || expected.CurrentVersion <= 0 || strings.TrimSpace(expected.Username) == "" || expected.Secret["password"] == "" {
		return ErrMySQLInstallAdminCredentialBinding
	}
	if err := tx.QueryRow(`select kind,coalesce(username,''),status,coalesce(secret_cipher,''),current_version from credentials where id=?`, credentialID).Scan(&kind, &username, &status, &cipher, &currentVersion); err != nil {
		return err
	}
	if !strings.EqualFold(kind, "mysql") || status != "active" || currentVersion != expected.CurrentVersion || username != expected.Username || strings.TrimSpace(cipher) == "" {
		return ErrMySQLInstallAdminCredentialBinding
	}
	secret, err := s.decodeCredentialSecret(cipher)
	if err != nil || secret["password"] != expected.Secret["password"] {
		return ErrMySQLInstallAdminCredentialBinding
	}
	return nil
}

func readUniqueActiveMySQLAdminCredentialTx(tx *sql.Tx, instanceID string) (Credential, string, error) {
	rows, err := tx.Query(`select c.id,c.kind,coalesce(c.username,''),c.status,coalesce(c.secret_cipher,'') from credential_bindings cb join credentials c on c.id=cb.credential_id where cb.app_instance_id=? and cb.purpose='admin' and c.status='active' order by c.id`, instanceID)
	if err != nil {
		return Credential{}, "", err
	}
	defer rows.Close()
	var items []Credential
	var ciphers []string
	for rows.Next() {
		var credential Credential
		var cipher string
		if err := rows.Scan(&credential.ID, &credential.Kind, &credential.Username, &credential.Status, &cipher); err != nil {
			return Credential{}, "", err
		}
		items, ciphers = append(items, credential), append(ciphers, cipher)
	}
	if err := rows.Err(); err != nil || len(items) != 1 || !strings.EqualFold(items[0].Kind, "mysql") {
		return Credential{}, "", ErrMySQLInstallAdminCredentialBinding
	}
	return items[0], ciphers[0], nil
}
