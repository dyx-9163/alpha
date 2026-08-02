package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aifar-deployment/backend/internal/logmask"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db                *sql.DB
	secretKey         []byte
	previousSecretKey []byte
	secretKeyMu       sync.RWMutex
}

// ErrServerCredentialDecryption identifies a failure to decrypt a saved server credential.
var ErrServerCredentialDecryption = errors.New("server credential decryption failed")

func Open(path string) (*Store, error) {
	return OpenWithSecret(path, "")
}

func OpenWithSecret(path, secret string) (*Store, error) {
	return OpenWithSecrets(path, secret, "")
}

func OpenWithSecrets(path, currentSecret, previousSecret string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{
		db:                db,
		secretKey:         deriveSecretKey(currentSecret),
		previousSecretKey: deriveOptionalSecretKey(previousSecret),
	}
	return s, s.migrate()
}

func OpenReadOnlyWithSecret(path, secret string) (*Store, error) {
	return OpenReadOnlyWithSecrets(path, secret, "")
}

func OpenReadOnlyWithSecrets(path, currentSecret, previousSecret string) (*Store, error) {
	dsn, err := sqliteReadOnlyDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`pragma query_only=ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{
		db:                db,
		secretKey:         deriveSecretKey(currentSecret),
		previousSecretKey: deriveOptionalSecretKey(previousSecret),
	}, nil
}

func (s *Store) Close() error {
	defer s.clearSecretKeys()
	return s.db.Close()
}

func (s *Store) Ping() error {
	return s.db.Ping()
}

func (s *Store) BackupDatabase(path string) (int64, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, "", errors.New("backup path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, "", err
	}
	_ = os.Remove(path)
	if _, err := s.db.Exec(`vacuum into ` + sqliteString(path)); err != nil {
		_ = os.Remove(path)
		return 0, "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, "", err
	}
	checksum, err := fileSHA256(path)
	if err != nil {
		return 0, "", err
	}
	return info.Size(), checksum, nil
}

func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func sqliteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func sqliteReadOnlyDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	slashPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	uri := url.URL{Scheme: "file", Path: slashPath}
	query := uri.Query()
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	return uri.String(), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Store) migrate() error {
	schema := []string{
		`create table if not exists users (
			id text primary key, username text not null unique, role text not null,
			token_version integer not null default 1, password_hash text not null, created_at datetime not null
		)`,
		`create table if not exists servers (
			id text primary key, name text not null, host text not null, port integer not null,
			username text not null, auth_type text not null, password text, private_key text,
			tags text, note text, deploy_dir text, docker_host text, status text, last_error text,
			sort_order integer not null default 0, created_at datetime not null, updated_at datetime not null
		)`,
		`create table if not exists tasks (
			id text primary key, type text not null, target text, status text not null,
			created_by text, error text, created_at datetime not null, started_at datetime, finished_at datetime
		)`,
		`create table if not exists task_logs (
			id integer primary key autoincrement, task_id text not null, target text not null default '', level text not null,
			message text not null, created_at datetime not null
		)`,
		`create table if not exists task_targets (
			id integer primary key autoincrement, task_id text not null, target text not null,
			status text not null, error text, created_at datetime not null,
			started_at datetime, finished_at datetime,
			unique(task_id, target)
		)`,
		`create table if not exists task_steps (
			id integer primary key autoincrement, task_id text not null, target text not null,
			name text not null, title text not null, step_order integer not null,
			status text not null, error text, created_at datetime not null,
			started_at datetime, finished_at datetime,
			unique(task_id, target, name)
		)`,
		`create table if not exists audit_logs (
			id integer primary key autoincrement, actor text, action text not null, target text,
			status text not null, message text, created_at datetime not null
		)`,
		`create table if not exists resources (
			id text primary key, app text not null, version text not null, path text not null,
			size integer not null, sha256 text, rpm_count integer not null, created_at datetime not null
		)`,
		`create unique index if not exists resources_app_version_path on resources(app, version, path)`,
		`create table if not exists app_instances (
			id text primary key, app text not null, version text not null, server_id text,
			status text not null, topology text, metadata text, created_at datetime not null, updated_at datetime not null
		)`,
		`create table if not exists app_releases (
			id text primary key, instance_id text not null, app text not null, version text not null,
			release_id text not null, server_id text, status text not null, manifest_json text,
			config_hash text, created_at datetime not null, activated_at datetime
		)`,
		`create unique index if not exists app_releases_instance_release on app_releases(instance_id, release_id)`,
		`create table if not exists aifar_deployments (
			id text primary key, instance_id text not null, service_name text not null,
			desired_replicas integer not null, current_revision text not null, updating_revision text,
			strategy_json text, status text not null, metadata_json text,
			created_at datetime not null, updated_at datetime not null,
			unique(instance_id, service_name)
		)`,
		`create index if not exists aifar_deployments_instance on aifar_deployments(instance_id)`,
		`create table if not exists aifar_replicasets (
			id text primary key, instance_id text not null, service_name text not null,
			revision text not null, image text not null, artifact_hash text,
			desired_pods integer not null, ready_pods integer not null,
			status text not null, metadata_json text,
			created_at datetime not null, updated_at datetime not null,
			unique(instance_id, service_name, revision)
		)`,
		`create index if not exists aifar_replicasets_instance on aifar_replicasets(instance_id)`,
		`create table if not exists aifar_pods (
			id text primary key, instance_id text not null, service_name text not null,
			revision text not null, pod_id text not null, container_name text not null,
			port integer not null, status text not null, ready integer not null, metadata_json text,
			created_at datetime not null, updated_at datetime not null,
			unique(instance_id, service_name, pod_id)
		)`,
		`create index if not exists aifar_pods_instance on aifar_pods(instance_id)`,
		`create table if not exists aifar_service_endpoints (
			id text primary key, instance_id text not null, service_name text not null,
			pod_id text not null, container_name text not null, revision text not null,
			port integer not null, state text not null, ready integer not null, metadata_json text,
			created_at datetime not null, updated_at datetime not null,
			unique(instance_id, service_name, pod_id)
		)`,
		`create index if not exists aifar_service_endpoints_instance on aifar_service_endpoints(instance_id)`,
		`create table if not exists aifar_orchestration_locks (
			id text primary key, instance_id text not null, service_name text not null default '',
			operation text not null, actor text, task_id text, status text not null,
			started_at datetime not null, expires_at datetime not null, released_at datetime,
			created_at datetime not null, updated_at datetime not null
		)`,
		`create index if not exists aifar_orchestration_locks_instance_status on aifar_orchestration_locks(instance_id, status, expires_at)`,
		`create unique index if not exists aifar_orchestration_locks_active_scope on aifar_orchestration_locks(instance_id, service_name) where status='active'`,
		`create table if not exists nacos_config_revisions (
			id text primary key, nacos_instance_id text not null, namespace text not null, group_name text not null,
			data_id text not null, content_cipher text not null, content_hash text not null, metadata text,
			created_by text, created_at datetime not null, published_at datetime not null
		)`,
		`create index if not exists nacos_config_revisions_lookup on nacos_config_revisions(nacos_instance_id, namespace, group_name, data_id, published_at)`,
		`create table if not exists credentials (
			id text primary key, name text not null, kind text not null, username text, endpoint text,
			scope text not null, status text not null, app text, server_id text, app_instance_id text,
			purpose text, tags text, secret_cipher text, secret_fingerprint text,
			current_version integer not null default 0, created_by text,
			created_at datetime not null, updated_at datetime not null
		)`,
		`create index if not exists credentials_kind_status on credentials(kind, status)`,
		`create table if not exists credential_versions (
			id text primary key, credential_id text not null, version integer not null,
			secret_cipher text not null, secret_fingerprint text, created_by text,
			created_at datetime not null, retired_at datetime,
			unique(credential_id, version)
		)`,
		`create table if not exists credential_bindings (
			id text primary key, credential_id text not null, app_instance_id text not null,
			purpose text not null, service_name text, created_at datetime not null,
			unique(credential_id, app_instance_id, purpose)
		)`,
		`create table if not exists storage_items (
			id text primary key, instance_id text not null, kind text not null, name text not null,
			policy text, access_key text, secret_key text, metadata text,
			created_at datetime not null, updated_at datetime not null
		)`,
		`create unique index if not exists storage_items_instance_kind_name on storage_items(instance_id, kind, name)`,
		`create table if not exists settings (
			key text primary key, value text not null, updated_at datetime not null
		)`,
		`create table if not exists collector_runs (
			name text primary key, target text, status text not null,
			last_error text, started_at datetime, finished_at datetime,
			duration_ms integer not null default 0, updated_at datetime not null
		)`,
		`create table if not exists status_snapshots (
			scope text not null, resource_id text not null, server_id text,
			status text not null, payload text not null, last_error text,
			version integer not null default 1, collected_at datetime not null, updated_at datetime not null,
			primary key(scope, resource_id)
		)`,
		`create index if not exists status_snapshots_scope_server on status_snapshots(scope, server_id)`,
		`create table if not exists alerts (
			id text primary key, fingerprint text not null unique, severity text not null, scope text not null,
			resource_id text, server_id text, app text, instance_id text, status text not null,
			title text not null, message text, evidence_json text, required_permission text,
			first_seen_at datetime not null, last_seen_at datetime not null, resolved_at datetime,
			muted_until datetime, acknowledged_by text, acknowledged_at datetime, updated_at datetime not null
		)`,
		`create index if not exists alerts_status_severity_scope on alerts(status, severity, scope)`,
		`create index if not exists alerts_server_app_instance on alerts(server_id, app, instance_id)`,
		`create table if not exists alert_events (
			id integer primary key autoincrement, alert_id text not null, fingerprint text not null,
			event text not null, actor text, message text, created_at datetime not null
		)`,
		`create index if not exists alert_events_alert on alert_events(alert_id, created_at)`,
	}
	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.ensureColumn("resources", "part", `alter table resources add column part text not null default 'backend'`); err != nil {
		return err
	}
	if err := s.ensureColumn("task_logs", "target", `alter table task_logs add column target text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn("servers", "sort_order", `alter table servers add column sort_order integer not null default 0`); err != nil {
		return err
	}
	if err := s.ensureColumn("users", "token_version", `alter table users add column token_version integer not null default 1`); err != nil {
		return err
	}
	if err := s.ensureColumn("alerts", "acknowledged_by", `alter table alerts add column acknowledged_by text`); err != nil {
		return err
	}
	if err := s.ensureColumn("alerts", "acknowledged_at", `alter table alerts add column acknowledged_at datetime`); err != nil {
		return err
	}
	return runStoreMigrations(s.db)
}

func (s *Store) ensureColumn(table, column, stmt string) error {
	rows, err := s.db.Query(`pragma table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(stmt)
	return err
}

func (s *Store) BootstrapUser(username, password string) error {
	var count int
	if err := s.db.QueryRow(`select count(*) from users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`insert into users(id, username, role, token_version, password_hash, created_at) values(?,?,?,?,?,?)`,
		NewID("usr"), username, "owner", 1, string(hash), time.Now())
	return err
}

func (s *Store) CreateUser(username, password, role string) (UserSummary, error) {
	username = strings.TrimSpace(username)
	role = strings.TrimSpace(role)
	if username == "" || password == "" || role == "" {
		return UserSummary{}, errors.New("username, password and role are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return UserSummary{}, err
	}
	user := UserSummary{
		ID:           NewID("usr"),
		Username:     username,
		Role:         role,
		TokenVersion: 1,
		CreatedAt:    time.Now(),
	}
	_, err = s.db.Exec(`insert into users(id, username, role, token_version, password_hash, created_at) values(?,?,?,?,?,?)`,
		user.ID, user.Username, user.Role, user.TokenVersion, string(hash), user.CreatedAt)
	if err != nil {
		return UserSummary{}, err
	}
	return user, nil
}

func (s *Store) ResetUserPassword(username, password string) error {
	username = strings.TrimSpace(username)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`update users set password_hash=?, token_version=coalesce(token_version, 1)+1 where username=?`, string(hash), username)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows > 0 {
		return nil
	}
	_, err = s.db.Exec(`insert into users(id, username, role, token_version, password_hash, created_at) values(?,?,?,?,?,?)`,
		NewID("usr"), username, "owner", 1, string(hash), time.Now())
	return err
}

func (s *Store) SetUserRole(username, role string) error {
	username = strings.TrimSpace(username)
	role = strings.TrimSpace(role)
	if username == "" || role == "" {
		return errors.New("username and role are required")
	}
	res, err := s.db.Exec(`update users set role=?, token_version=coalesce(token_version, 1)+1 where username=?`, role, username)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CountUsersByRole(role string) (int, error) {
	var count int
	err := s.db.QueryRow(`select count(*) from users where lower(role)=lower(?)`, strings.TrimSpace(role)).Scan(&count)
	return count, err
}

func (s *Store) ListUsers() ([]UserSummary, error) {
	rows, err := s.db.Query(`select id, username, role, coalesce(token_version,1), created_at from users order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserSummary{}
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.TokenVersion, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountRows(table string) (int, error) {
	switch table {
	case "schema_migrations", "users", "servers", "tasks", "task_logs", "task_targets", "task_steps", "audit_logs", "resources", "app_instances", "app_releases", "app_release_artifacts", "app_release_snapshots", "app_backups", "app_clusters", "app_cluster_members", "operation_locks", "aifar_orchestration_locks", "nacos_config_revisions", "credentials", "credential_versions", "credential_bindings", "credential_references", "storage_items", "settings", "collector_runs", "status_snapshots", "status_snapshot_history", "alerts", "alert_events":
	default:
		return 0, fmt.Errorf("unsupported table %q", table)
	}
	var count int
	err := s.db.QueryRow(`select count(*) from ` + table).Scan(&count)
	return count, err
}

func (s *Store) UserByUsername(username string) (User, error) {
	var u User
	err := s.db.QueryRow(`select id, username, role, coalesce(token_version,1), password_hash, created_at from users where username = ?`, username).
		Scan(&u.ID, &u.Username, &u.Role, &u.TokenVersion, &u.PasswordHash, &u.CreatedAt)
	return u, err
}

func (s *Store) UserByID(id string) (User, error) {
	var u User
	err := s.db.QueryRow(`select id, username, role, coalesce(token_version,1), password_hash, created_at from users where id = ?`, id).
		Scan(&u.ID, &u.Username, &u.Role, &u.TokenVersion, &u.PasswordHash, &u.CreatedAt)
	return u, err
}

func (s *Store) ListServers() ([]Server, error) {
	rows, err := s.db.Query(`select id,name,host,port,username,auth_type,tags,note,deploy_dir,docker_host,status,last_error,coalesce(sort_order,0),created_at,updated_at from servers order by case when sort_order > 0 then 0 else 1 end, sort_order asc, created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		var v Server
		if err := rows.Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.AuthType, &v.Tags, &v.Note, &v.DeployDir, &v.DockerHost, &v.Status, &v.LastError, &v.SortOrder, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetServer(id string, includeSecret bool) (Server, error) {
	var v Server
	err := s.db.QueryRow(`select id,name,host,port,username,auth_type,password,private_key,tags,note,deploy_dir,docker_host,status,last_error,coalesce(sort_order,0),created_at,updated_at from servers where id=?`, id).
		Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.AuthType, &v.Password, &v.PrivateKey, &v.Tags, &v.Note, &v.DeployDir, &v.DockerHost, &v.Status, &v.LastError, &v.SortOrder, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return v, err
	}
	if !includeSecret {
		v.Password, v.PrivateKey = "", ""
		return v, nil
	}
	if v.Password, err = s.decryptSecret(v.Password); err != nil {
		return Server{}, fmt.Errorf("%w: %v", ErrServerCredentialDecryption, err)
	}
	if v.PrivateKey, err = s.decryptSecret(v.PrivateKey); err != nil {
		return Server{}, fmt.Errorf("%w: %v", ErrServerCredentialDecryption, err)
	}
	return v, err
}

func (s *Store) SaveServer(v Server) (Server, error) {
	now := time.Now()
	isNew := strings.TrimSpace(v.ID) == ""
	if v.ID == "" {
		v.ID = NewID("srv")
		v.CreatedAt = now
	}
	if v.Port == 0 {
		v.Port = 22
	}
	if v.AuthType == "" {
		v.AuthType = "password"
	}
	if v.DeployDir == "" {
		v.DeployDir = "/aifar/apps"
	}
	if v.Status == "" {
		v.Status = "unknown"
	}
	if v.SortOrder <= 0 {
		if isNew {
			sortOrder, err := s.nextServerSortOrder()
			if err != nil {
				return Server{}, err
			}
			v.SortOrder = sortOrder
		} else {
			var currentOrder int
			err := s.db.QueryRow(`select coalesce(sort_order,0) from servers where id=?`, v.ID).Scan(&currentOrder)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return Server{}, err
			}
			if err == nil {
				v.SortOrder = currentOrder
			} else {
				sortOrder, nextErr := s.nextServerSortOrder()
				if nextErr != nil {
					return Server{}, nextErr
				}
				v.SortOrder = sortOrder
			}
		}
	}
	v.LastError = logmask.Mask(v.LastError)
	v.UpdatedAt = now
	stored := v
	var err error
	if stored.Password, err = s.encryptSecret(stored.Password); err != nil {
		return Server{}, err
	}
	if stored.PrivateKey, err = s.encryptSecret(stored.PrivateKey); err != nil {
		return Server{}, err
	}
	_, err = s.db.Exec(`insert into servers(id,name,host,port,username,auth_type,password,private_key,tags,note,deploy_dir,docker_host,status,last_error,sort_order,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set name=excluded.name,host=excluded.host,port=excluded.port,username=excluded.username,auth_type=excluded.auth_type,
		password=coalesce(nullif(excluded.password,''),servers.password),private_key=coalesce(nullif(excluded.private_key,''),servers.private_key),
		tags=excluded.tags,note=excluded.note,deploy_dir=excluded.deploy_dir,docker_host=excluded.docker_host,status=excluded.status,last_error=excluded.last_error,
		sort_order=case when excluded.sort_order > 0 then excluded.sort_order else servers.sort_order end,updated_at=excluded.updated_at`,
		stored.ID, stored.Name, stored.Host, stored.Port, stored.Username, stored.AuthType, stored.Password, stored.PrivateKey, stored.Tags, stored.Note, stored.DeployDir, stored.DockerHost, stored.Status, stored.LastError, stored.SortOrder, stored.CreatedAt, stored.UpdatedAt)
	return v, err
}

func (s *Store) nextServerSortOrder() (int, error) {
	var sortOrder int
	err := s.db.QueryRow(`select coalesce(max(sort_order),0) + 1 from servers`).Scan(&sortOrder)
	return sortOrder, err
}

func (s *Store) ReorderServers(ids []string) error {
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`select id from servers order by case when sort_order > 0 then 0 else 1 end, sort_order asc, created_at desc`)
	if err != nil {
		return err
	}
	var existing []string
	exists := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing = append(existing, id)
		exists[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	ordered := make([]string, 0, len(existing))
	seen := map[string]bool{}
	for _, id := range ids {
		if !exists[id] {
			return sql.ErrNoRows
		}
		ordered = append(ordered, id)
		seen[id] = true
	}
	for _, id := range existing {
		if !seen[id] {
			ordered = append(ordered, id)
		}
	}

	now := time.Now()
	for idx, id := range ordered {
		if _, err := tx.Exec(`update servers set sort_order=?, updated_at=? where id=?`, idx+1, now, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteServer(id string) error {
	res, err := s.db.Exec(`delete from servers where id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CreateTask(t Task) (Task, error) {
	if t.ID == "" {
		t.ID = NewID("tsk")
	}
	if t.Status == "" {
		t.Status = "pending"
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.Target = logmask.Mask(t.Target)
	t.Error = logmask.Mask(t.Error)
	_, err := s.db.Exec(`insert into tasks(id,type,target,status,created_by,error,lease_owner,lease_expires_at,attempt,idempotency_key,correlation_id,created_at,started_at,finished_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Type, t.Target, t.Status, t.CreatedBy, t.Error, t.LeaseOwner, nullableTime(t.LeaseExpiresAt), t.Attempt, t.IdempotencyKey, t.CorrelationID, t.CreatedAt, nullableTime(t.StartedAt), nullableTime(t.FinishedAt))
	return t, err
}

func (s *Store) UpdateTaskStatus(id, status, errText string) error {
	now := time.Now()
	errText = logmask.Mask(errText)
	switch status {
	case "running":
		_, err := s.db.Exec(`update tasks set status=?, started_at=? where id=?`, status, now, id)
		return err
	case "success", "failed", "cancelled", "timeout":
		_, err := s.db.Exec(`update tasks set status=?, error=?, lease_owner='', lease_expires_at=null, finished_at=? where id=?`, status, errText, now, id)
		return err
	default:
		_, err := s.db.Exec(`update tasks set status=?, error=? where id=?`, status, errText, id)
		return err
	}
}

func (s *Store) RecoverInterruptedTasks(errText string) ([]Task, error) {
	now := time.Now()
	errText = logmask.Mask(strings.TrimSpace(errText))
	if errText == "" {
		errText = "task interrupted by aifar-server restart"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`select id,type,target,status,created_by,coalesce(error,''),coalesce(lease_owner,''),lease_expires_at,coalesce(attempt,0),coalesce(idempotency_key,''),coalesce(correlation_id,''),created_at,started_at,finished_at from tasks where status in ('pending','running') order by created_at`)
	if err != nil {
		return nil, err
	}
	tasks := []Task{}
	for rows.Next() {
		var t Task
		var leaseExpiresAt, startedAt, finishedAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.Type, &t.Target, &t.Status, &t.CreatedBy, &t.Error, &t.LeaseOwner, &leaseExpiresAt, &t.Attempt, &t.IdempotencyKey, &t.CorrelationID, &t.CreatedAt, &startedAt, &finishedAt); err != nil {
			rows.Close()
			return nil, err
		}
		t.LeaseExpiresAt = nullTime(leaseExpiresAt)
		t.StartedAt = nullTime(startedAt)
		t.FinishedAt = nullTime(finishedAt)
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	for idx := range tasks {
		taskID := tasks[idx].ID
		if _, err := tx.Exec(`update tasks set status='failed', error=?, lease_owner='', lease_expires_at=null, finished_at=? where id=?`, errText, now, taskID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`insert into task_logs(task_id,target,level,message,created_at) values(?,?,?,?,?)`, taskID, "", "error", errText, now); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`update task_targets set status='failed', error=?, finished_at=? where task_id=? and status in ('pending','running')`, errText, now, taskID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`update task_steps set status='failed', error=?, finished_at=? where task_id=? and status in ('pending','running')`, errText, now, taskID); err != nil {
			return nil, err
		}
		tasks[idx].Status = "failed"
		tasks[idx].Error = errText
		tasks[idx].FinishedAt = now
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Store) AddTaskLog(taskID, level, message string) (TaskLog, error) {
	return s.AddTaskTargetLog(taskID, "", level, message)
}

func (s *Store) AddTaskTargetLog(taskID, target, level, message string) (TaskLog, error) {
	now := time.Now()
	target = logmask.Mask(target)
	message = logmask.Mask(message)
	res, err := s.db.Exec(`insert into task_logs(task_id,target,level,message,created_at) values(?,?,?,?,?)`, taskID, target, level, message, now)
	if err != nil {
		return TaskLog{}, err
	}
	id, _ := res.LastInsertId()
	return TaskLog{ID: id, TaskID: taskID, Target: target, Level: level, Message: message, CreatedAt: now}, nil
}

func (s *Store) UpsertTaskTarget(taskID, target, status, errText string) error {
	if status == "" {
		status = "pending"
	}
	target = logmask.Mask(target)
	errText = logmask.Mask(errText)
	now := time.Now()
	var startedAt, finishedAt any
	switch status {
	case "running":
		startedAt = now
	case "success", "failed", "cancelled", "timeout":
		finishedAt = now
	}
	_, err := s.db.Exec(`insert into task_targets(task_id,target,status,error,created_at,started_at,finished_at) values(?,?,?,?,?,?,?)
		on conflict(task_id,target) do update set status=excluded.status,error=excluded.error,
		started_at=coalesce(task_targets.started_at, excluded.started_at),finished_at=excluded.finished_at`,
		taskID, target, status, errText, now, startedAt, finishedAt)
	return err
}

func (s *Store) UpsertTaskStep(taskID, target, name, title string, order int, status, errText string) error {
	if status == "" {
		status = "pending"
	}
	target = logmask.Mask(target)
	errText = logmask.Mask(errText)
	now := time.Now()
	var startedAt, finishedAt any
	switch status {
	case "running":
		startedAt = now
	case "success", "failed", "cancelled", "timeout":
		finishedAt = now
	}
	_, err := s.db.Exec(`insert into task_steps(task_id,target,name,title,step_order,status,error,created_at,started_at,finished_at) values(?,?,?,?,?,?,?,?,?,?)
		on conflict(task_id,target,name) do update set
		title=case when excluded.title <> '' then excluded.title else task_steps.title end,
		step_order=case when excluded.step_order > 0 then excluded.step_order else task_steps.step_order end,
		status=excluded.status,error=excluded.error,
		started_at=coalesce(task_steps.started_at, excluded.started_at),finished_at=excluded.finished_at`,
		taskID, target, name, title, order, status, errText, now, startedAt, finishedAt)
	return err
}

func (s *Store) GetTask(id string) (Task, []TaskLog, error) {
	var t Task
	var startedAt, finishedAt sql.NullTime
	var leaseExpiresAt sql.NullTime
	err := s.db.QueryRow(`select id,type,target,status,created_by,error,coalesce(lease_owner,''),lease_expires_at,coalesce(attempt,0),coalesce(idempotency_key,''),coalesce(correlation_id,''),created_at,started_at,finished_at from tasks where id=?`, id).
		Scan(&t.ID, &t.Type, &t.Target, &t.Status, &t.CreatedBy, &t.Error, &t.LeaseOwner, &leaseExpiresAt, &t.Attempt, &t.IdempotencyKey, &t.CorrelationID, &t.CreatedAt, &startedAt, &finishedAt)
	if err != nil {
		return t, nil, err
	}
	t.LeaseExpiresAt = nullTime(leaseExpiresAt)
	t.StartedAt = nullTime(startedAt)
	t.FinishedAt = nullTime(finishedAt)
	logs, err := s.TaskLogs(id)
	return t, logs, err
}

func (s *Store) ListTaskTargets(taskID string) ([]TaskTarget, error) {
	rows, err := s.db.Query(`select id,task_id,target,status,coalesce(error,''),created_at,started_at,finished_at from task_targets where task_id=? order by id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskTarget{}
	for rows.Next() {
		var target TaskTarget
		var startedAt, finishedAt sql.NullTime
		if err := rows.Scan(&target.ID, &target.TaskID, &target.Target, &target.Status, &target.Error, &target.CreatedAt, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
		target.StartedAt = nullTime(startedAt)
		target.FinishedAt = nullTime(finishedAt)
		out = append(out, target)
	}
	return out, rows.Err()
}

func (s *Store) ListTaskSteps(taskID string) ([]TaskStep, error) {
	rows, err := s.db.Query(`select id,task_id,target,name,title,step_order,status,coalesce(error,''),created_at,started_at,finished_at from task_steps where task_id=? order by target,step_order,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskStep{}
	for rows.Next() {
		var step TaskStep
		var startedAt, finishedAt sql.NullTime
		if err := rows.Scan(&step.ID, &step.TaskID, &step.Target, &step.Name, &step.Title, &step.Order, &step.Status, &step.Error, &step.CreatedAt, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
		step.StartedAt = nullTime(startedAt)
		step.FinishedAt = nullTime(finishedAt)
		out = append(out, step)
	}
	return out, rows.Err()
}

func (s *Store) ListTasks() ([]Task, error) {
	rows, err := s.db.Query(`select id,type,target,status,created_by,error,coalesce(lease_owner,''),lease_expires_at,coalesce(attempt,0),coalesce(idempotency_key,''),coalesce(correlation_id,''),created_at,started_at,finished_at from tasks order by created_at desc limit 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		var leaseExpiresAt, startedAt, finishedAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.Type, &t.Target, &t.Status, &t.CreatedBy, &t.Error, &t.LeaseOwner, &leaseExpiresAt, &t.Attempt, &t.IdempotencyKey, &t.CorrelationID, &t.CreatedAt, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
		t.LeaseExpiresAt = nullTime(leaseExpiresAt)
		t.StartedAt = nullTime(startedAt)
		t.FinishedAt = nullTime(finishedAt)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TaskLogs(taskID string) ([]TaskLog, error) {
	rows, err := s.db.Query(`select id,task_id,coalesce(target,''),level,message,created_at from task_logs where task_id=? order by id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskLog{}
	for rows.Next() {
		var l TaskLog
		if err := rows.Scan(&l.ID, &l.TaskID, &l.Target, &l.Level, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTask(id string) error {
	deleted, err := s.DeleteTasks([]string{id})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteTasks(ids []string) (int, error) {
	ids = uniqueStrings(ids)
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
		if _, err := tx.Exec(`delete from task_logs where task_id=?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`delete from task_steps where task_id=?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`delete from task_targets where task_id=?`, id); err != nil {
			return 0, err
		}
		res, err := tx.Exec(`delete from tasks where id=?`, id)
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

func (s *Store) DeleteFinishedTasksBefore(cutoff time.Time) (int, error) {
	if cutoff.IsZero() {
		return 0, nil
	}
	rows, err := s.db.Query(`select id from tasks where finished_at is not null and finished_at < ? and status in ('success','failed','cancelled','timeout')`, cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return s.DeleteTasks(ids)
}

func (s *Store) ClearTaskLogs(taskID string) error {
	_, err := s.db.Exec(`delete from task_logs where task_id=?`, taskID)
	return err
}

func (s *Store) ClearTaskLogsForTasks(ids []string) (int, error) {
	ids = uniqueStrings(ids)
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
		res, err := tx.Exec(`delete from task_logs where task_id=?`, id)
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

func (s *Store) DeleteTaskLog(id int64) error {
	res, err := s.db.Exec(`delete from task_logs where id=?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpsertResource(r Resource) error {
	if r.ID == "" {
		r.ID = NewID("res")
	}
	if r.Part == "" {
		r.Part = "backend"
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`insert into resources(id,app,part,version,path,size,sha256,rpm_count,created_at) values(?,?,?,?,?,?,?,?,?)
		on conflict(app,version,path) do update set part=excluded.part,size=excluded.size,sha256=excluded.sha256,rpm_count=excluded.rpm_count`,
		r.ID, r.App, r.Part, r.Version, r.Path, r.Size, r.SHA256, r.RPMCount, r.CreatedAt)
	return err
}

func (s *Store) ReplaceResources(resources []Resource) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`delete from resources`); err != nil {
		return err
	}
	now := time.Now()
	for _, r := range resources {
		if r.ID == "" {
			r.ID = NewID("res")
		}
		if r.Part == "" {
			r.Part = "backend"
		}
		if r.CreatedAt.IsZero() {
			r.CreatedAt = now
		}
		if _, err := tx.Exec(`insert into resources(id,app,part,version,path,size,sha256,rpm_count,created_at) values(?,?,?,?,?,?,?,?,?)`,
			r.ID, r.App, r.Part, r.Version, r.Path, r.Size, r.SHA256, r.RPMCount, r.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListResources() ([]Resource, error) {
	rows, err := s.db.Query(`select id,app,coalesce(part,'backend'),version,path,size,sha256,rpm_count,created_at from resources order by app,version,part,path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Resource{}
	for rows.Next() {
		var r Resource
		if err := rows.Scan(&r.ID, &r.App, &r.Part, &r.Version, &r.Path, &r.Size, &r.SHA256, &r.RPMCount, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func uniqueInt64s(values []int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, value := range values {
		if value == 0 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullTime(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
