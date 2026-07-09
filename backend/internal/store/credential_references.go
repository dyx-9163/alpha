package store

import (
	"database/sql"
	"strings"
	"time"
)

func (s *Store) SaveCredentialReference(ref CredentialReference) (CredentialReference, error) {
	ref = normalizeCredentialReference(ref)
	if ref.CredentialID == "" || ref.ResourceType == "" || ref.ResourceID == "" {
		return CredentialReference{}, sql.ErrNoRows
	}
	now := time.Now()
	if ref.ID == "" {
		ref.ID = NewID("cref")
		ref.CreatedAt = now
	}
	if ref.CreatedAt.IsZero() {
		ref.CreatedAt = now
	}
	ref.UpdatedAt = now
	_, err := s.db.Exec(`insert into credential_references(id,credential_id,resource_type,resource_id,purpose,generated,lifecycle_policy,metadata,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?)
		on conflict(credential_id,resource_type,resource_id,purpose) do update set
		generated=excluded.generated,lifecycle_policy=excluded.lifecycle_policy,metadata=excluded.metadata,updated_at=excluded.updated_at`,
		ref.ID, ref.CredentialID, ref.ResourceType, ref.ResourceID, ref.Purpose, boolInt(ref.Generated), ref.LifecyclePolicy, ref.Metadata, ref.CreatedAt, ref.UpdatedAt)
	return ref, err
}

func (s *Store) ListCredentialReferences(credentialID, resourceType, resourceID string) ([]CredentialReference, error) {
	args := []any{}
	query := `select id,credential_id,resource_type,resource_id,purpose,generated,lifecycle_policy,coalesce(metadata,'{}'),created_at,updated_at from credential_references`
	where := []string{}
	if strings.TrimSpace(credentialID) != "" {
		where = append(where, "credential_id=?")
		args = append(args, strings.TrimSpace(credentialID))
	}
	if resourceType = normalizeCredentialReferenceType(resourceType); resourceType != "" {
		where = append(where, "resource_type=?")
		args = append(args, resourceType)
	}
	if strings.TrimSpace(resourceID) != "" {
		where = append(where, "resource_id=?")
		args = append(args, strings.TrimSpace(resourceID))
	}
	if len(where) > 0 {
		query += " where " + strings.Join(where, " and ")
	}
	query += " order by updated_at desc"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialReference{}
	for rows.Next() {
		ref, err := scanCredentialReference(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCredentialReferencesForResource(resourceType, resourceID string) (int, error) {
	resourceType = normalizeCredentialReferenceType(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	if resourceType == "" || resourceID == "" {
		return 0, nil
	}
	res, err := s.db.Exec(`delete from credential_references where resource_type=? and resource_id=?`, resourceType, resourceID)
	if err != nil {
		return 0, err
	}
	rows, err := res.RowsAffected()
	return int(rows), err
}

func normalizeCredentialReference(ref CredentialReference) CredentialReference {
	ref.ID = strings.TrimSpace(ref.ID)
	ref.CredentialID = strings.TrimSpace(ref.CredentialID)
	ref.ResourceType = normalizeCredentialReferenceType(ref.ResourceType)
	ref.ResourceID = strings.TrimSpace(ref.ResourceID)
	ref.Purpose = strings.TrimSpace(ref.Purpose)
	ref.LifecyclePolicy = normalizeCredentialReferenceLifecycle(ref.LifecyclePolicy)
	ref.Metadata = strings.TrimSpace(ref.Metadata)
	if ref.Metadata == "" {
		ref.Metadata = "{}"
	}
	return ref
}

func normalizeCredentialReferenceType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func normalizeCredentialReferenceLifecycle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "retain", "delete-with-resource", "rotate-with-resource":
		return value
	default:
		return "retain"
	}
}

func scanCredentialReference(scanner interface {
	Scan(dest ...any) error
}) (CredentialReference, error) {
	var ref CredentialReference
	var generated int
	err := scanner.Scan(&ref.ID, &ref.CredentialID, &ref.ResourceType, &ref.ResourceID, &ref.Purpose, &generated, &ref.LifecyclePolicy, &ref.Metadata, &ref.CreatedAt, &ref.UpdatedAt)
	ref.Generated = generated != 0
	return ref, err
}
