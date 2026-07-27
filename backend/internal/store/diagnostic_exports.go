package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const diagnosticExportColumns = `id,task_id,instance_id,server_id,status,services_json,since_at,until_at,
	remote_relative_path,archive_name,archive_bytes,uncompressed_bytes,sha256,warning_count,warnings_json,error_text,
	created_by,created_at,ready_at,expires_at,downloaded_at,deleted_at,cleanup_status,cleanup_error,cleanup_attempted_at`

func (s *Store) SaveDiagnosticExport(v DiagnosticExport) (DiagnosticExport, error) {
	v, err := normalizeDiagnosticExport(v)
	if err != nil {
		return DiagnosticExport{}, err
	}
	if v.InstanceID == "" || v.ServerID == "" {
		return DiagnosticExport{}, fmt.Errorf("diagnostic export instance and server are required")
	}
	if v.ID == "" {
		v.ID = NewID("diag")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	_, err = s.db.Exec(`insert into diagnostic_exports(`+diagnosticExportColumns+`)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set
		task_id=excluded.task_id,instance_id=excluded.instance_id,server_id=excluded.server_id,status=excluded.status,
		services_json=excluded.services_json,since_at=excluded.since_at,until_at=excluded.until_at,
		remote_relative_path=excluded.remote_relative_path,archive_name=excluded.archive_name,archive_bytes=excluded.archive_bytes,
		uncompressed_bytes=excluded.uncompressed_bytes,sha256=excluded.sha256,warning_count=excluded.warning_count,
		warnings_json=excluded.warnings_json,error_text=excluded.error_text,created_by=excluded.created_by,
		ready_at=excluded.ready_at,expires_at=excluded.expires_at,downloaded_at=excluded.downloaded_at,
		deleted_at=excluded.deleted_at,cleanup_status=excluded.cleanup_status,cleanup_error=excluded.cleanup_error,
		cleanup_attempted_at=excluded.cleanup_attempted_at`,
		v.ID, v.TaskID, v.InstanceID, v.ServerID, v.Status, v.ServicesJSON, v.SinceAt, v.UntilAt,
		v.RemoteRelativePath, v.ArchiveName, v.ArchiveBytes, v.UncompressedBytes, v.SHA256, v.WarningCount, v.WarningsJSON,
		v.ErrorText, v.CreatedBy, v.CreatedAt, nullableTime(v.ReadyAt), v.ExpiresAt, nullableTime(v.DownloadedAt), nullableTime(v.DeletedAt),
		v.CleanupStatus, v.CleanupError, nullableTime(v.CleanupAttemptedAt))
	if err != nil {
		return DiagnosticExport{}, err
	}
	return v, nil
}

func (s *Store) GetDiagnosticExport(id string) (DiagnosticExport, error) {
	row := s.db.QueryRow(`select `+diagnosticExportColumns+` from diagnostic_exports where id=?`, strings.TrimSpace(id))
	return scanDiagnosticExport(row)
}

func (s *Store) MarkDiagnosticExportDownloaded(id string, downloadedAt time.Time) (bool, error) {
	result, err := s.db.Exec(`update diagnostic_exports set downloaded_at=?
		where id=? and status='ready' and deleted_at is null and expires_at > ?`, downloadedAt, strings.TrimSpace(id), downloadedAt)
	return diagnosticExportWasUpdated(result, err)
}

func (s *Store) MarkDiagnosticExportCleanupPending(id string, attemptedAt time.Time) (bool, error) {
	result, err := s.db.Exec(`update diagnostic_exports
		set status=case when status='ready' then 'expired' else status end,
			cleanup_status='pending',cleanup_error='',cleanup_attempted_at=?
		where id=? and status in ('ready','expired','failed','cancelled')
		and deleted_at is null and cleanup_status <> 'complete'`, attemptedAt, strings.TrimSpace(id))
	return diagnosticExportWasUpdated(result, err)
}

func (s *Store) MarkDiagnosticExportCleanupFailed(id, cleanupError string) (bool, error) {
	result, err := s.db.Exec(`update diagnostic_exports set cleanup_status='failed',cleanup_error=?
		where id=? and status in ('ready','expired','failed','cancelled')
		and deleted_at is null and cleanup_status='pending'`, strings.TrimSpace(cleanupError), strings.TrimSpace(id))
	return diagnosticExportWasUpdated(result, err)
}

func (s *Store) MarkDiagnosticExportDeleted(id string, deletedAt time.Time) (bool, error) {
	result, err := s.db.Exec(`update diagnostic_exports
		set status='deleted',deleted_at=?,cleanup_status='complete',cleanup_error=''
		where id=? and status in ('ready','expired','failed','cancelled')
		and deleted_at is null and cleanup_status='pending'`, deletedAt, strings.TrimSpace(id))
	return diagnosticExportWasUpdated(result, err)
}

func diagnosticExportWasUpdated(result sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (s *Store) ListDiagnosticExports(instanceID string, page, pageSize int) (DiagnosticExportPage, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	instanceID = strings.TrimSpace(instanceID)

	result := DiagnosticExportPage{Items: []DiagnosticExport{}, Page: page, PageSize: pageSize}
	if err := s.db.QueryRow(`select count(*) from diagnostic_exports where instance_id=?`, instanceID).Scan(&result.Total); err != nil {
		return DiagnosticExportPage{}, err
	}
	rows, err := s.db.Query(`select `+diagnosticExportColumns+` from diagnostic_exports
		where instance_id=? order by created_at desc, id desc limit ? offset ?`, instanceID, pageSize, (page-1)*pageSize)
	if err != nil {
		return DiagnosticExportPage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanDiagnosticExport(rows)
		if err != nil {
			return DiagnosticExportPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return DiagnosticExportPage{}, err
	}
	return result, nil
}

func (s *Store) ListDiagnosticExportsDueForCleanup(now time.Time, limit int) ([]DiagnosticExport, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`select `+diagnosticExportColumns+` from diagnostic_exports
		where (status='ready' and expires_at <= ?) or (status='expired' and cleanup_status <> 'complete')
		order by expires_at asc, created_at asc, id asc limit ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DiagnosticExport{}
	for rows.Next() {
		item, err := scanDiagnosticExport(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanDiagnosticExport(rows interface{ Scan(dest ...any) error }) (DiagnosticExport, error) {
	var v DiagnosticExport
	var readyAt, downloadedAt, deletedAt, cleanupAttemptedAt sql.NullTime
	if err := rows.Scan(
		&v.ID, &v.TaskID, &v.InstanceID, &v.ServerID, &v.Status, &v.ServicesJSON, &v.SinceAt, &v.UntilAt,
		&v.RemoteRelativePath, &v.ArchiveName, &v.ArchiveBytes, &v.UncompressedBytes, &v.SHA256, &v.WarningCount, &v.WarningsJSON,
		&v.ErrorText, &v.CreatedBy, &v.CreatedAt, &readyAt, &v.ExpiresAt, &downloadedAt, &deletedAt,
		&v.CleanupStatus, &v.CleanupError, &cleanupAttemptedAt,
	); err != nil {
		return DiagnosticExport{}, err
	}
	var err error
	v.Services, err = decodeDiagnosticExportStrings(v.ServicesJSON)
	if err != nil {
		return DiagnosticExport{}, err
	}
	v.Warnings, err = decodeDiagnosticExportStrings(v.WarningsJSON)
	if err != nil {
		return DiagnosticExport{}, err
	}
	v.ReadyAt = nullTime(readyAt)
	v.DownloadedAt = nullTime(downloadedAt)
	v.DeletedAt = nullTime(deletedAt)
	v.CleanupAttemptedAt = nullTime(cleanupAttemptedAt)
	return v, nil
}

func normalizeDiagnosticExport(v DiagnosticExport) (DiagnosticExport, error) {
	v.ID = strings.TrimSpace(v.ID)
	v.TaskID = strings.TrimSpace(v.TaskID)
	v.InstanceID = strings.TrimSpace(v.InstanceID)
	v.ServerID = strings.TrimSpace(v.ServerID)
	v.Status = strings.ToLower(strings.TrimSpace(v.Status))
	if !isDiagnosticExportStatus(v.Status) {
		return DiagnosticExport{}, fmt.Errorf("unsupported diagnostic export status %q", v.Status)
	}
	v.RemoteRelativePath = strings.TrimSpace(v.RemoteRelativePath)
	v.ArchiveName = strings.TrimSpace(v.ArchiveName)
	v.SHA256 = strings.TrimSpace(v.SHA256)
	v.ErrorText = strings.TrimSpace(v.ErrorText)
	v.CreatedBy = strings.TrimSpace(v.CreatedBy)
	v.CleanupStatus = strings.ToLower(strings.TrimSpace(v.CleanupStatus))
	if v.CleanupStatus == "" {
		v.CleanupStatus = "none"
	}
	if !isDiagnosticExportCleanupStatus(v.CleanupStatus) {
		return DiagnosticExport{}, fmt.Errorf("unsupported diagnostic export cleanup status %q", v.CleanupStatus)
	}
	v.CleanupError = strings.TrimSpace(v.CleanupError)

	services := v.Services
	if len(services) == 0 {
		var err error
		services, err = decodeDiagnosticExportStrings(v.ServicesJSON)
		if err != nil {
			return DiagnosticExport{}, fmt.Errorf("decode diagnostic export services: %w", err)
		}
	}
	v.Services = normalizedDiagnosticExportStrings(services, true)
	servicesJSON, err := json.Marshal(v.Services)
	if err != nil {
		return DiagnosticExport{}, err
	}
	v.ServicesJSON = string(servicesJSON)

	warnings := v.Warnings
	if len(warnings) == 0 {
		var err error
		warnings, err = decodeDiagnosticExportStrings(v.WarningsJSON)
		if err != nil {
			return DiagnosticExport{}, fmt.Errorf("decode diagnostic export warnings: %w", err)
		}
	}
	v.Warnings = normalizedDiagnosticExportStrings(warnings, false)
	warningsJSON, err := json.Marshal(v.Warnings)
	if err != nil {
		return DiagnosticExport{}, err
	}
	v.WarningsJSON = string(warningsJSON)
	v.WarningCount = len(v.Warnings)
	return v, nil
}

func decodeDiagnosticExportStrings(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func normalizedDiagnosticExportStrings(values []string, sortValues bool) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if sortValues {
		sort.Strings(result)
	}
	if len(result) == 0 {
		return []string{}
	}
	return result
}

func isDiagnosticExportStatus(status string) bool {
	switch status {
	case "pending", "building", "ready", "failed", "cancelled", "expired", "deleted":
		return true
	default:
		return false
	}
}

func isDiagnosticExportCleanupStatus(status string) bool {
	switch status {
	case "none", "pending", "failed", "complete":
		return true
	default:
		return false
	}
}
