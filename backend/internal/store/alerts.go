package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"aifar-deployment/backend/internal/logmask"
)

const maxAlertEvidenceBytes = 2048

func (s *Store) UpsertAlert(alert Alert) (Alert, string, error) {
	alert = normalizeAlert(alert)
	if alert.Fingerprint == "" {
		return Alert{}, "", sql.ErrNoRows
	}
	now := time.Now()
	if alert.ID == "" {
		alert.ID = NewID("alt")
	}
	if alert.FirstSeenAt.IsZero() {
		alert.FirstSeenAt = now
	}
	if alert.LastSeenAt.IsZero() {
		alert.LastSeenAt = now
	}
	alert.UpdatedAt = now
	current, err := s.GetAlertByFingerprint(alert.Fingerprint)
	if IsNotFound(err) {
		if _, err := s.db.Exec(`insert into alerts(id,fingerprint,severity,scope,resource_id,server_id,app,instance_id,status,title,message,evidence_json,required_permission,first_seen_at,last_seen_at,resolved_at,muted_until,acknowledged_by,acknowledged_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			alert.ID, alert.Fingerprint, alert.Severity, alert.Scope, alert.ResourceID, alert.ServerID, alert.App, alert.InstanceID, alert.Status, alert.Title, alert.Message, alert.EvidenceJSON, alert.RequiredPermission,
			alert.FirstSeenAt, alert.LastSeenAt, nullableTime(alert.ResolvedAt), nullableTime(alert.MutedUntil), alert.AcknowledgedBy, nullableTime(alert.AcknowledgedAt), alert.UpdatedAt); err != nil {
			return Alert{}, "", err
		}
		_, _ = s.AddAlertEvent(AlertEvent{AlertID: alert.ID, Fingerprint: alert.Fingerprint, Event: "created", Actor: "system", Message: alert.Message})
		return alert, "created", nil
	}
	if err != nil {
		return Alert{}, "", err
	}

	action := ""
	visibleChanged := current.Status != "open" ||
		current.Severity != alert.Severity ||
		current.Scope != alert.Scope ||
		current.ResourceID != alert.ResourceID ||
		current.ServerID != alert.ServerID ||
		current.App != alert.App ||
		current.InstanceID != alert.InstanceID ||
		current.Title != alert.Title ||
		current.Message != alert.Message ||
		current.EvidenceJSON != alert.EvidenceJSON ||
		current.RequiredPermission != alert.RequiredPermission
	if visibleChanged {
		action = "updated"
	}
	alert.ID = current.ID
	alert.FirstSeenAt = current.FirstSeenAt
	alert.MutedUntil = current.MutedUntil
	if current.Status == "open" {
		alert.AcknowledgedBy = current.AcknowledgedBy
		alert.AcknowledgedAt = current.AcknowledgedAt
	}
	if _, err := s.db.Exec(`update alerts set severity=?,scope=?,resource_id=?,server_id=?,app=?,instance_id=?,status='open',
		title=?,message=?,evidence_json=?,required_permission=?,last_seen_at=?,resolved_at=null,muted_until=?,acknowledged_by=?,acknowledged_at=?,updated_at=? where id=?`,
		alert.Severity, alert.Scope, alert.ResourceID, alert.ServerID, alert.App, alert.InstanceID, alert.Title, alert.Message, alert.EvidenceJSON, alert.RequiredPermission,
		alert.LastSeenAt, nullableTime(alert.MutedUntil), alert.AcknowledgedBy, nullableTime(alert.AcknowledgedAt), alert.UpdatedAt, alert.ID); err != nil {
		return Alert{}, "", err
	}
	if action != "" {
		_, _ = s.AddAlertEvent(AlertEvent{AlertID: alert.ID, Fingerprint: alert.Fingerprint, Event: action, Actor: "system", Message: alert.Message})
	}
	saved, err := s.GetAlert(alert.ID)
	return saved, action, err
}

func (s *Store) ListAlerts(query AlertQuery) ([]Alert, error) {
	status := strings.ToLower(strings.TrimSpace(query.Status))
	severity := strings.ToLower(strings.TrimSpace(query.Severity))
	scope := strings.TrimSpace(query.Scope)
	clauses := []string{}
	args := []any{}
	if status != "" && status != "all" {
		clauses = append(clauses, "status=?")
		args = append(args, status)
	}
	if severity != "" {
		clauses = append(clauses, "severity=?")
		args = append(args, severity)
	}
	if scope != "" {
		clauses = append(clauses, "scope=?")
		args = append(args, scope)
	}
	stmt := `select id,fingerprint,severity,scope,coalesce(resource_id,''),coalesce(server_id,''),coalesce(app,''),coalesce(instance_id,''),status,title,coalesce(message,''),coalesce(evidence_json,''),coalesce(required_permission,''),first_seen_at,last_seen_at,resolved_at,muted_until,coalesce(acknowledged_by,''),acknowledged_at,updated_at from alerts`
	if len(clauses) > 0 {
		stmt += " where " + strings.Join(clauses, " and ")
	}
	stmt += ` order by case status when 'open' then 0 else 1 end,
		case severity when 'critical' then 0 when 'warning' then 1 else 2 end,
		last_seen_at desc, updated_at desc limit 500`
	rows, err := s.db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, alert)
	}
	return out, rows.Err()
}

func (s *Store) GetAlert(id string) (Alert, error) {
	row := s.db.QueryRow(`select id,fingerprint,severity,scope,coalesce(resource_id,''),coalesce(server_id,''),coalesce(app,''),coalesce(instance_id,''),status,title,coalesce(message,''),coalesce(evidence_json,''),coalesce(required_permission,''),first_seen_at,last_seen_at,resolved_at,muted_until,coalesce(acknowledged_by,''),acknowledged_at,updated_at from alerts where id=?`, strings.TrimSpace(id))
	return scanAlert(row)
}

func (s *Store) GetAlertByFingerprint(fingerprint string) (Alert, error) {
	row := s.db.QueryRow(`select id,fingerprint,severity,scope,coalesce(resource_id,''),coalesce(server_id,''),coalesce(app,''),coalesce(instance_id,''),status,title,coalesce(message,''),coalesce(evidence_json,''),coalesce(required_permission,''),first_seen_at,last_seen_at,resolved_at,muted_until,coalesce(acknowledged_by,''),acknowledged_at,updated_at from alerts where fingerprint=?`, strings.TrimSpace(fingerprint))
	return scanAlert(row)
}

func (s *Store) AcknowledgeAlert(id, actor string) (Alert, error) {
	now := time.Now()
	result, err := s.db.Exec(`update alerts set acknowledged_by=?,acknowledged_at=?,updated_at=? where id=?`, strings.TrimSpace(actor), now, now, strings.TrimSpace(id))
	if err != nil {
		return Alert{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Alert{}, sql.ErrNoRows
	}
	alert, err := s.GetAlert(id)
	if err == nil {
		_, _ = s.AddAlertEvent(AlertEvent{AlertID: alert.ID, Fingerprint: alert.Fingerprint, Event: "acknowledged", Actor: actor})
	}
	return alert, err
}

func (s *Store) MuteAlert(id, actor string, until time.Time) (Alert, error) {
	now := time.Now()
	event := "muted"
	if until.IsZero() || until.Before(now) {
		until = time.Time{}
		event = "unmuted"
	}
	result, err := s.db.Exec(`update alerts set muted_until=?,updated_at=? where id=?`, nullableTime(until), now, strings.TrimSpace(id))
	if err != nil {
		return Alert{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Alert{}, sql.ErrNoRows
	}
	alert, err := s.GetAlert(id)
	if err == nil {
		_, _ = s.AddAlertEvent(AlertEvent{AlertID: alert.ID, Fingerprint: alert.Fingerprint, Event: event, Actor: actor})
	}
	return alert, err
}

func (s *Store) ResolveAlert(id, actor, message string) (Alert, error) {
	now := time.Now()
	message = logmask.Mask(strings.TrimSpace(message))
	result, err := s.db.Exec(`update alerts set status='resolved',resolved_at=?,updated_at=? where id=?`, now, now, strings.TrimSpace(id))
	if err != nil {
		return Alert{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Alert{}, sql.ErrNoRows
	}
	alert, err := s.GetAlert(id)
	if err == nil {
		_, _ = s.AddAlertEvent(AlertEvent{AlertID: alert.ID, Fingerprint: alert.Fingerprint, Event: "resolved", Actor: actor, Message: message})
	}
	return alert, err
}

func (s *Store) ResolveMissingSystemAlerts(activeFingerprints []string) ([]Alert, error) {
	active := uniqueStrings(activeFingerprints)
	stmt := `select id,fingerprint,severity,scope,coalesce(resource_id,''),coalesce(server_id,''),coalesce(app,''),coalesce(instance_id,''),status,title,coalesce(message,''),coalesce(evidence_json,''),coalesce(required_permission,''),first_seen_at,last_seen_at,resolved_at,muted_until,coalesce(acknowledged_by,''),acknowledged_at,updated_at from alerts where status='open' and scope <> 'task'`
	args := []any{}
	if len(active) > 0 {
		placeholders := make([]string, len(active))
		for i, value := range active {
			placeholders[i] = "?"
			args = append(args, value)
		}
		stmt += " and fingerprint not in (" + strings.Join(placeholders, ",") + ")"
	}
	rows, err := s.db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	alerts := []Alert{}
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	for i := range alerts {
		if _, err := s.db.Exec(`update alerts set status='resolved',resolved_at=?,updated_at=? where id=?`, now, now, alerts[i].ID); err != nil {
			return nil, err
		}
		alerts[i].Status = "resolved"
		alerts[i].ResolvedAt = now
		alerts[i].UpdatedAt = now
		_, _ = s.AddAlertEvent(AlertEvent{AlertID: alerts[i].ID, Fingerprint: alerts[i].Fingerprint, Event: "resolved", Actor: "system", Message: "condition recovered"})
	}
	return alerts, nil
}

func (s *Store) AddAlertEvent(event AlertEvent) (AlertEvent, error) {
	event.AlertID = strings.TrimSpace(event.AlertID)
	event.Fingerprint = strings.TrimSpace(event.Fingerprint)
	event.Event = strings.ToLower(strings.TrimSpace(event.Event))
	event.Actor = strings.TrimSpace(event.Actor)
	event.Message = limitString(logmask.Mask(strings.TrimSpace(event.Message)), 1024)
	if event.AlertID == "" || event.Event == "" {
		return AlertEvent{}, sql.ErrNoRows
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	res, err := s.db.Exec(`insert into alert_events(alert_id,fingerprint,event,actor,message,created_at) values(?,?,?,?,?,?)`,
		event.AlertID, event.Fingerprint, event.Event, event.Actor, event.Message, event.CreatedAt)
	if err != nil {
		return AlertEvent{}, err
	}
	event.ID, _ = res.LastInsertId()
	return event, nil
}

func normalizeAlert(alert Alert) Alert {
	alert.Fingerprint = strings.TrimSpace(alert.Fingerprint)
	alert.Severity = normalizeAlertSeverity(alert.Severity)
	alert.Scope = strings.TrimSpace(alert.Scope)
	alert.ResourceID = strings.TrimSpace(alert.ResourceID)
	alert.ServerID = strings.TrimSpace(alert.ServerID)
	alert.App = strings.TrimSpace(alert.App)
	alert.InstanceID = strings.TrimSpace(alert.InstanceID)
	alert.Status = strings.ToLower(strings.TrimSpace(alert.Status))
	if alert.Status == "" {
		alert.Status = "open"
	}
	alert.Title = limitString(logmask.Mask(strings.TrimSpace(alert.Title)), 256)
	alert.Message = limitString(logmask.Mask(strings.TrimSpace(alert.Message)), 2048)
	alert.EvidenceJSON = normalizeAlertEvidence(alert.EvidenceJSON)
	alert.RequiredPermission = strings.TrimSpace(alert.RequiredPermission)
	return alert
}

func normalizeAlertSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "warning", "info":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "warning"
	}
}

func normalizeAlertEvidence(value string) string {
	value = logmask.Mask(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if !json.Valid([]byte(value)) {
		data, _ := json.Marshal(map[string]any{"text": value})
		value = string(data)
	}
	if len(value) <= maxAlertEvidenceBytes {
		return value
	}
	summary := value[:maxAlertEvidenceBytes]
	data, _ := json.Marshal(map[string]any{"truncated": true, "text": summary})
	return string(data)
}

func limitString(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}

type alertScanner interface {
	Scan(dest ...any) error
}

func scanAlert(row alertScanner) (Alert, error) {
	var alert Alert
	var resolvedAt, mutedUntil, acknowledgedAt sql.NullTime
	err := row.Scan(
		&alert.ID,
		&alert.Fingerprint,
		&alert.Severity,
		&alert.Scope,
		&alert.ResourceID,
		&alert.ServerID,
		&alert.App,
		&alert.InstanceID,
		&alert.Status,
		&alert.Title,
		&alert.Message,
		&alert.EvidenceJSON,
		&alert.RequiredPermission,
		&alert.FirstSeenAt,
		&alert.LastSeenAt,
		&resolvedAt,
		&mutedUntil,
		&alert.AcknowledgedBy,
		&acknowledgedAt,
		&alert.UpdatedAt,
	)
	if err != nil {
		return Alert{}, err
	}
	alert.ResolvedAt = nullTime(resolvedAt)
	alert.MutedUntil = nullTime(mutedUntil)
	alert.AcknowledgedAt = nullTime(acknowledgedAt)
	return alert, nil
}
