package store

import (
	"database/sql"
	"fmt"
	"time"
)

type storeMigration struct {
	Version int
	Name    string
	Up      func(*sql.Tx) error
}

var storeMigrations = []storeMigration{
	{
		Version: 2026070901,
		Name:    "operation locks",
		Up: func(tx *sql.Tx) error {
			return execMigrationStatements(tx,
				`create table if not exists operation_locks (
					id text primary key,
					scope text not null,
					resource_id text not null,
					operation text not null,
					owner_task_id text,
					owner text,
					status text not null,
					expires_at datetime not null,
					heartbeat_at datetime not null,
					released_at datetime,
					metadata text not null default '{}',
					created_at datetime not null,
					updated_at datetime not null
				)`,
				`create index if not exists operation_locks_scope_status on operation_locks(scope, resource_id, status, expires_at)`,
				`create index if not exists operation_locks_task on operation_locks(owner_task_id)`,
				`create unique index if not exists operation_locks_active_scope on operation_locks(scope, resource_id, operation) where status='active'`,
			)
		},
	},
	{
		Version: 2026070902,
		Name:    "task leases",
		Up: func(tx *sql.Tx) error {
			for _, column := range []struct {
				Name string
				DDL  string
			}{
				{Name: "lease_owner", DDL: `alter table tasks add column lease_owner text not null default ''`},
				{Name: "lease_expires_at", DDL: `alter table tasks add column lease_expires_at datetime`},
				{Name: "attempt", DDL: `alter table tasks add column attempt integer not null default 0`},
				{Name: "idempotency_key", DDL: `alter table tasks add column idempotency_key text not null default ''`},
				{Name: "correlation_id", DDL: `alter table tasks add column correlation_id text not null default ''`},
			} {
				if err := ensureColumnTx(tx, "tasks", column.Name, column.DDL); err != nil {
					return err
				}
			}
			return execMigrationStatements(tx,
				`create index if not exists tasks_status_lease on tasks(status, lease_expires_at)`,
				`create index if not exists tasks_idempotency on tasks(idempotency_key) where idempotency_key <> ''`,
				`create index if not exists tasks_correlation on tasks(correlation_id) where correlation_id <> ''`,
			)
		},
	},
	{
		Version: 2026070903,
		Name:    "credential references",
		Up: func(tx *sql.Tx) error {
			return execMigrationStatements(tx,
				`create table if not exists credential_references (
					id text primary key,
					credential_id text not null,
					resource_type text not null,
					resource_id text not null,
					purpose text not null default '',
					generated integer not null default 0,
					lifecycle_policy text not null default 'retain',
					metadata text not null default '{}',
					created_at datetime not null,
					updated_at datetime not null,
					unique(credential_id, resource_type, resource_id, purpose)
				)`,
				`create index if not exists credential_references_resource on credential_references(resource_type, resource_id)`,
				`create index if not exists credential_references_credential on credential_references(credential_id)`,
			)
		},
	},
	{
		Version: 2026070904,
		Name:    "application clusters",
		Up: func(tx *sql.Tx) error {
			return execMigrationStatements(tx,
				`create table if not exists app_clusters (
					id text primary key,
					app text not null,
					name text not null,
					topology text not null,
					status text not null,
					metadata text not null default '{}',
					created_at datetime not null,
					updated_at datetime not null,
					unique(app, name)
				)`,
				`create index if not exists app_clusters_app_status on app_clusters(app, status)`,
				`create table if not exists app_cluster_members (
					id text primary key,
					cluster_id text not null,
					instance_id text not null,
					server_id text not null default '',
					role text not null default '',
					status text not null,
					metadata text not null default '{}',
					created_at datetime not null,
					updated_at datetime not null,
					unique(cluster_id, instance_id)
				)`,
				`create index if not exists app_cluster_members_cluster on app_cluster_members(cluster_id, role, status)`,
				`create index if not exists app_cluster_members_instance on app_cluster_members(instance_id)`,
			)
		},
	},
	{
		Version: 2026070905,
		Name:    "release artifacts backups status history",
		Up: func(tx *sql.Tx) error {
			return execMigrationStatements(tx,
				`create table if not exists app_release_artifacts (
					id text primary key,
					instance_id text not null,
					release_id text not null,
					app text not null,
					service_name text not null default '',
					artifact_type text not null,
					name text not null,
					version text not null default '',
					checksum text not null default '',
					size integer not null default 0,
					path text not null default '',
					metadata text not null default '{}',
					created_at datetime not null
				)`,
				`create index if not exists app_release_artifacts_release on app_release_artifacts(instance_id, release_id)`,
				`create index if not exists app_release_artifacts_service on app_release_artifacts(instance_id, service_name)`,
				`create table if not exists app_release_snapshots (
					id text primary key,
					instance_id text not null,
					release_id text not null,
					app text not null,
					snapshot_kind text not null,
					status text not null,
					payload_json text not null default '{}',
					checksum text not null default '',
					metadata text not null default '{}',
					created_at datetime not null,
					restored_at datetime,
					unique(instance_id, release_id, snapshot_kind)
				)`,
				`create index if not exists app_release_snapshots_release on app_release_snapshots(instance_id, release_id)`,
				`create table if not exists app_backups (
					id text primary key,
					app text not null,
					instance_id text not null default '',
					server_id text not null default '',
					backup_type text not null,
					status text not null,
					path text not null default '',
					checksum text not null default '',
					size integer not null default 0,
					task_id text not null default '',
					metadata text not null default '{}',
					created_at datetime not null,
					completed_at datetime
				)`,
				`create index if not exists app_backups_instance on app_backups(instance_id, backup_type, created_at)`,
				`create index if not exists app_backups_task on app_backups(task_id) where task_id <> ''`,
				`create table if not exists status_snapshot_history (
					id integer primary key autoincrement,
					scope text not null,
					resource_id text not null,
					server_id text not null default '',
					status text not null,
					payload text not null,
					last_error text not null default '',
					version integer not null,
					collected_at datetime not null,
					created_at datetime not null
				)`,
				`create index if not exists status_snapshot_history_lookup on status_snapshot_history(scope, resource_id, created_at)`,
			)
		},
	},
	{
		Version: 2026072701,
		Name:    "diagnostic exports",
		Up: func(tx *sql.Tx) error {
			return execMigrationStatements(tx,
				`create table if not exists diagnostic_exports (
					id text primary key,
					task_id text not null default '',
					instance_id text not null,
					server_id text not null,
					status text not null,
					services_json text not null default '[]',
					since_at datetime not null,
					until_at datetime not null,
					remote_relative_path text not null default '',
					archive_name text not null default '',
					archive_bytes integer not null default 0,
					uncompressed_bytes integer not null default 0,
					sha256 text not null default '',
					warning_count integer not null default 0,
					warnings_json text not null default '[]',
					error_text text not null default '',
					created_by text not null default '',
					created_at datetime not null,
					ready_at datetime,
					expires_at datetime not null,
					downloaded_at datetime,
					deleted_at datetime,
					cleanup_status text not null default 'none',
					cleanup_error text not null default '',
					cleanup_attempted_at datetime
				)`,
				`create index if not exists diagnostic_exports_instance_created on diagnostic_exports(instance_id, created_at)`,
				`create index if not exists diagnostic_exports_status_expires on diagnostic_exports(status, expires_at)`,
				`create index if not exists diagnostic_exports_task on diagnostic_exports(task_id)`,
			)
		},
	},
	{
		Version: 2026072702,
		Name:    "diagnostic export local storage",
		Up: func(tx *sql.Tx) error {
			for _, column := range []struct {
				Name string
				DDL  string
			}{
				{Name: "storage_kind", DDL: `alter table diagnostic_exports add column storage_kind text not null default 'remote'`},
				{Name: "storage_relative_path", DDL: `alter table diagnostic_exports add column storage_relative_path text not null default ''`},
				{Name: "reserved_bytes", DDL: `alter table diagnostic_exports add column reserved_bytes integer not null default 0`},
			} {
				if err := ensureColumnTx(tx, "diagnostic_exports", column.Name, column.DDL); err != nil {
					return err
				}
			}
			return execMigrationStatements(tx,
				`create index if not exists diagnostic_exports_storage_kind_status on diagnostic_exports(storage_kind, status, expires_at)`,
			)
		},
	},
	{
		Version: 2026072801,
		Name:    "diagnostic export local date",
		Up: func(tx *sql.Tx) error {
			return ensureColumnTx(tx, "diagnostic_exports", "local_date", `alter table diagnostic_exports add column local_date text not null default ''`)
		},
	},
	{
		Version: 2026073001,
		Name:    "mysql cluster authoritative topology",
		Up:      backfillLegacyMySQLClusterTopologies,
	},
}

func runStoreMigrations(db *sql.DB) error {
	if _, err := db.Exec(`create table if not exists schema_migrations (
		version integer primary key,
		name text not null,
		applied_at datetime not null
	)`); err != nil {
		return err
	}
	for _, migration := range storeMigrations {
		if err := runStoreMigration(db, migration); err != nil {
			return err
		}
	}
	return nil
}

func runStoreMigration(db *sql.DB, migration storeMigration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`select count(*) from schema_migrations where version=?`, migration.Version).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return tx.Commit()
	}
	if err := migration.Up(tx); err != nil {
		return fmt.Errorf("apply migration %d %s: %w", migration.Version, migration.Name, err)
	}
	if _, err := tx.Exec(`insert into schema_migrations(version,name,applied_at) values(?,?,?)`, migration.Version, migration.Name, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func execMigrationStatements(tx *sql.Tx, stmts ...string) error {
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumnTx(tx *sql.Tx, table, column, stmt string) error {
	rows, err := tx.Query(`pragma table_info(` + table + `)`)
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
	_, err = tx.Exec(stmt)
	return err
}
