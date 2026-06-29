package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db        *sql.DB
	secretKey []byte
}

func Open(path string) (*Store, error) {
	return OpenWithSecret(path, "")
}

func OpenWithSecret(path, secret string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, secretKey: deriveSecretKey(secret)}
	return s, s.migrate()
}

func (s *Store) Close() error { return s.db.Close() }

func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func (s *Store) migrate() error {
	schema := []string{
		`create table if not exists users (
			id text primary key, username text not null unique, role text not null,
			password_hash text not null, created_at datetime not null
		)`,
		`create table if not exists servers (
			id text primary key, name text not null, host text not null, port integer not null,
			username text not null, auth_type text not null, password text, private_key text,
			tags text, note text, deploy_dir text, docker_host text, status text, last_error text,
			created_at datetime not null, updated_at datetime not null
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
		`create table if not exists storage_items (
			id text primary key, instance_id text not null, kind text not null, name text not null,
			policy text, access_key text, secret_key text, metadata text,
			created_at datetime not null, updated_at datetime not null
		)`,
		`create unique index if not exists storage_items_instance_kind_name on storage_items(instance_id, kind, name)`,
		`create table if not exists settings (
			key text primary key, value text not null, updated_at datetime not null
		)`,
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
	return nil
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
	_, err = s.db.Exec(`insert into users(id, username, role, password_hash, created_at) values(?,?,?,?,?)`,
		NewID("usr"), username, "owner", string(hash), time.Now())
	return err
}

func (s *Store) ResetUserPassword(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`update users set password_hash=? where username=?`, string(hash), username)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows > 0 {
		return nil
	}
	_, err = s.db.Exec(`insert into users(id, username, role, password_hash, created_at) values(?,?,?,?,?)`,
		NewID("usr"), username, "owner", string(hash), time.Now())
	return err
}

func (s *Store) ListUsers() ([]UserSummary, error) {
	rows, err := s.db.Query(`select id, username, role, created_at from users order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserSummary{}
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountRows(table string) (int, error) {
	switch table {
	case "users", "servers", "tasks", "task_logs", "task_targets", "task_steps", "audit_logs", "resources", "app_instances", "storage_items", "settings":
	default:
		return 0, fmt.Errorf("unsupported table %q", table)
	}
	var count int
	err := s.db.QueryRow(`select count(*) from ` + table).Scan(&count)
	return count, err
}

func (s *Store) UserByUsername(username string) (User, error) {
	var u User
	err := s.db.QueryRow(`select id, username, role, password_hash, created_at from users where username = ?`, username).
		Scan(&u.ID, &u.Username, &u.Role, &u.PasswordHash, &u.CreatedAt)
	return u, err
}

func (s *Store) ListServers() ([]Server, error) {
	rows, err := s.db.Query(`select id,name,host,port,username,auth_type,tags,note,deploy_dir,docker_host,status,last_error,created_at,updated_at from servers order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		var v Server
		if err := rows.Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.AuthType, &v.Tags, &v.Note, &v.DeployDir, &v.DockerHost, &v.Status, &v.LastError, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetServer(id string, includeSecret bool) (Server, error) {
	var v Server
	err := s.db.QueryRow(`select id,name,host,port,username,auth_type,password,private_key,tags,note,deploy_dir,docker_host,status,last_error,created_at,updated_at from servers where id=?`, id).
		Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.AuthType, &v.Password, &v.PrivateKey, &v.Tags, &v.Note, &v.DeployDir, &v.DockerHost, &v.Status, &v.LastError, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return v, err
	}
	if !includeSecret {
		v.Password, v.PrivateKey = "", ""
		return v, nil
	}
	if v.Password, err = s.decryptSecret(v.Password); err != nil {
		return Server{}, err
	}
	if v.PrivateKey, err = s.decryptSecret(v.PrivateKey); err != nil {
		return Server{}, err
	}
	return v, err
}

func (s *Store) SaveServer(v Server) (Server, error) {
	now := time.Now()
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
	v.UpdatedAt = now
	stored := v
	var err error
	if stored.Password, err = s.encryptSecret(stored.Password); err != nil {
		return Server{}, err
	}
	if stored.PrivateKey, err = s.encryptSecret(stored.PrivateKey); err != nil {
		return Server{}, err
	}
	_, err = s.db.Exec(`insert into servers(id,name,host,port,username,auth_type,password,private_key,tags,note,deploy_dir,docker_host,status,last_error,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set name=excluded.name,host=excluded.host,port=excluded.port,username=excluded.username,auth_type=excluded.auth_type,
		password=coalesce(nullif(excluded.password,''),servers.password),private_key=coalesce(nullif(excluded.private_key,''),servers.private_key),
		tags=excluded.tags,note=excluded.note,deploy_dir=excluded.deploy_dir,docker_host=excluded.docker_host,status=excluded.status,last_error=excluded.last_error,updated_at=excluded.updated_at`,
		stored.ID, stored.Name, stored.Host, stored.Port, stored.Username, stored.AuthType, stored.Password, stored.PrivateKey, stored.Tags, stored.Note, stored.DeployDir, stored.DockerHost, stored.Status, stored.LastError, stored.CreatedAt, stored.UpdatedAt)
	return v, err
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
	_, err := s.db.Exec(`insert into tasks(id,type,target,status,created_by,error,created_at,started_at,finished_at) values(?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Type, t.Target, t.Status, t.CreatedBy, t.Error, t.CreatedAt, nullableTime(t.StartedAt), nullableTime(t.FinishedAt))
	return t, err
}

func (s *Store) UpdateTaskStatus(id, status, errText string) error {
	now := time.Now()
	switch status {
	case "running":
		_, err := s.db.Exec(`update tasks set status=?, started_at=? where id=?`, status, now, id)
		return err
	case "success", "failed", "cancelled", "timeout":
		_, err := s.db.Exec(`update tasks set status=?, error=?, finished_at=? where id=?`, status, errText, now, id)
		return err
	default:
		_, err := s.db.Exec(`update tasks set status=?, error=? where id=?`, status, errText, id)
		return err
	}
}

func (s *Store) AddTaskLog(taskID, level, message string) (TaskLog, error) {
	return s.AddTaskTargetLog(taskID, "", level, message)
}

func (s *Store) AddTaskTargetLog(taskID, target, level, message string) (TaskLog, error) {
	now := time.Now()
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
	err := s.db.QueryRow(`select id,type,target,status,created_by,error,created_at,started_at,finished_at from tasks where id=?`, id).
		Scan(&t.ID, &t.Type, &t.Target, &t.Status, &t.CreatedBy, &t.Error, &t.CreatedAt, &startedAt, &finishedAt)
	if err != nil {
		return t, nil, err
	}
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
	rows, err := s.db.Query(`select id,type,target,status,created_by,error,created_at,started_at,finished_at from tasks order by created_at desc limit 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		var startedAt, finishedAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.Type, &t.Target, &t.Status, &t.CreatedBy, &t.Error, &t.CreatedAt, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
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

func (s *Store) ClearTaskLogs(taskID string) error {
	_, err := s.db.Exec(`delete from task_logs where task_id=?`, taskID)
	return err
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

func (s *Store) AddAudit(actor, action, target, status, message string) error {
	_, err := s.db.Exec(`insert into audit_logs(actor,action,target,status,message,created_at) values(?,?,?,?,?,?)`,
		actor, action, target, status, message, time.Now())
	return err
}

func (s *Store) ListAudit() ([]Audit, error) {
	page, err := s.ListAuditPage(AuditQuery{Page: 1, PageSize: 300})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *Store) ListAuditPage(query AuditQuery) (AuditPage, error) {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 20
	}
	if query.PageSize > 200 {
		query.PageSize = 200
	}
	where, args := auditWhere(query)
	var total int
	if err := s.db.QueryRow(`select count(*) from audit_logs`+where, args...).Scan(&total); err != nil {
		return AuditPage{}, err
	}
	args = append(args, query.PageSize, (query.Page-1)*query.PageSize)
	rows, err := s.db.Query(`select id,actor,action,target,status,message,created_at from audit_logs`+where+` order by created_at desc limit ? offset ?`, args...)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		var a Audit
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Target, &a.Status, &a.Message, &a.CreatedAt); err != nil {
			return AuditPage{}, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, err
	}
	return AuditPage{Items: out, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

func (s *Store) DeleteAuditLogs(ids []int64) (int, error) {
	ids = uniqueInt64s(ids)
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
		res, err := tx.Exec(`delete from audit_logs where id=?`, id)
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

func (s *Store) SaveAppInstance(v AppInstance) (AppInstance, error) {
	now := time.Now()
	if v.ID == "" {
		v.ID = NewID("app")
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	_, err := s.db.Exec(`insert into app_instances(id,app,version,server_id,status,topology,metadata,created_at,updated_at) values(?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set version=excluded.version,server_id=excluded.server_id,status=excluded.status,topology=excluded.topology,metadata=excluded.metadata,updated_at=excluded.updated_at`,
		v.ID, v.App, v.Version, v.ServerID, v.Status, v.Topology, v.Metadata, v.CreatedAt, v.UpdatedAt)
	return v, err
}

func (s *Store) ListAppInstances() ([]AppInstance, error) {
	rows, err := s.db.Query(`select id,app,version,server_id,status,topology,metadata,created_at,updated_at from app_instances order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppInstance{}
	for rows.Next() {
		var v AppInstance
		if err := rows.Scan(&v.ID, &v.App, &v.Version, &v.ServerID, &v.Status, &v.Topology, &v.Metadata, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetAppInstance(id string) (AppInstance, error) {
	var v AppInstance
	err := s.db.QueryRow(`select id,app,version,server_id,status,topology,metadata,created_at,updated_at from app_instances where id=?`, id).
		Scan(&v.ID, &v.App, &v.Version, &v.ServerID, &v.Status, &v.Topology, &v.Metadata, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func (s *Store) DeleteAppInstance(id string) error {
	res, err := s.db.Exec(`delete from app_instances where id=?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListStorageItems(instanceID, kind string) ([]StorageItem, error) {
	rows, err := s.db.Query(`select id,instance_id,kind,name,coalesce(policy,''),coalesce(access_key,''),coalesce(secret_key,''),coalesce(metadata,''),created_at,updated_at
		from storage_items where instance_id=? and kind=? order by created_at desc`, instanceID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StorageItem{}
	for rows.Next() {
		var item StorageItem
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.Kind, &item.Name, &item.Policy, &item.AccessKey, &item.SecretKey, &item.Metadata, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.SecretKey = ""
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SaveStorageItem(item StorageItem) (StorageItem, error) {
	now := time.Now()
	if item.ID == "" {
		item.ID = NewID("obj")
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	stored := item
	var err error
	if stored.SecretKey, err = s.encryptSecret(stored.SecretKey); err != nil {
		return StorageItem{}, err
	}
	_, err = s.db.Exec(`insert into storage_items(id,instance_id,kind,name,policy,access_key,secret_key,metadata,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?)
		on conflict(instance_id,kind,name) do update set
		policy=excluded.policy,access_key=excluded.access_key,
		secret_key=coalesce(nullif(excluded.secret_key,''),storage_items.secret_key),
		metadata=excluded.metadata,updated_at=excluded.updated_at`,
		stored.ID, stored.InstanceID, stored.Kind, stored.Name, stored.Policy, stored.AccessKey, stored.SecretKey, stored.Metadata, stored.CreatedAt, stored.UpdatedAt)
	item.SecretKey = ""
	return item, err
}

func (s *Store) DeleteStorageItem(instanceID, kind, id string) error {
	res, err := s.db.Exec(`delete from storage_items where instance_id=? and kind=? and id=?`, instanceID, kind, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetSetting(key, fallback string) string {
	var value string
	if err := s.db.QueryRow(`select value from settings where key=?`, key).Scan(&value); err != nil {
		return fallback
	}
	return value
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`insert into settings(key,value,updated_at) values(?,?,?)
		on conflict(key) do update set value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now())
	return err
}

func auditWhere(query AuditQuery) (string, []any) {
	where := []string{}
	args := []any{}
	if query.Module != "" {
		where = append(where, `action like ?`)
		args = append(args, query.Module+".%")
	}
	if query.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, query.Status)
	}
	if len(where) == 0 {
		return "", args
	}
	return " where " + strings.Join(where, " and "), args
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
