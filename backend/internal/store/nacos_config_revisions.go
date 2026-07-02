package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const maxNacosConfigRevisions = 3

func (s *Store) SaveNacosConfigRevision(item NacosConfigRevision) (NacosConfigRevision, error) {
	item.NacosInstanceID = strings.TrimSpace(item.NacosInstanceID)
	item.Namespace = strings.TrimSpace(item.Namespace)
	item.Group = strings.TrimSpace(item.Group)
	item.DataID = strings.TrimSpace(item.DataID)
	item.CreatedBy = strings.TrimSpace(item.CreatedBy)
	item.Metadata = strings.TrimSpace(item.Metadata)
	if item.NacosInstanceID == "" || item.Namespace == "" || item.Group == "" || item.DataID == "" {
		return NacosConfigRevision{}, errors.New("nacos instance, namespace, group and data id are required")
	}
	if strings.TrimSpace(item.Content) == "" {
		return NacosConfigRevision{}, errors.New("config content is required")
	}
	if item.ID == "" {
		item.ID = NewID("ncfg")
	}
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.PublishedAt.IsZero() {
		item.PublishedAt = now
	}
	sum := sha256.Sum256([]byte(item.Content))
	item.ContentHash = hex.EncodeToString(sum[:])
	cipher, err := s.encryptSecret(item.Content)
	if err != nil {
		return NacosConfigRevision{}, err
	}
	_, err = s.db.Exec(`insert into nacos_config_revisions(id,nacos_instance_id,namespace,group_name,data_id,content_cipher,content_hash,metadata,created_by,created_at,published_at)
		values(?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.NacosInstanceID, item.Namespace, item.Group, item.DataID, cipher, item.ContentHash, item.Metadata, item.CreatedBy, item.CreatedAt, item.PublishedAt)
	if err != nil {
		return NacosConfigRevision{}, err
	}
	item.Content = ""
	return item, nil
}

func (s *Store) ListNacosConfigRevisions(query NacosConfigRevisionQuery, includeContent bool) ([]NacosConfigRevision, error) {
	clauses := []string{"1=1"}
	args := []any{}
	if value := strings.TrimSpace(query.NacosInstanceID); value != "" {
		clauses = append(clauses, "nacos_instance_id=?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(query.Namespace); value != "" {
		clauses = append(clauses, "namespace=?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(query.Group); value != "" {
		clauses = append(clauses, "group_name=?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(query.DataID); value != "" {
		clauses = append(clauses, "data_id=?")
		args = append(args, value)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	args = append(args, limit)
	rows, err := s.db.Query(`select id,nacos_instance_id,namespace,group_name,data_id,content_cipher,content_hash,coalesce(metadata,''),coalesce(created_by,''),created_at,published_at
		from nacos_config_revisions where `+strings.Join(clauses, " and ")+` order by published_at desc, created_at desc limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NacosConfigRevision{}
	for rows.Next() {
		item, err := s.scanNacosConfigRevision(rows, includeContent)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetNacosConfigRevision(id string, includeContent bool) (NacosConfigRevision, error) {
	rows, err := s.db.Query(`select id,nacos_instance_id,namespace,group_name,data_id,content_cipher,content_hash,coalesce(metadata,''),coalesce(created_by,''),created_at,published_at
		from nacos_config_revisions where id=?`, strings.TrimSpace(id))
	if err != nil {
		return NacosConfigRevision{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return NacosConfigRevision{}, sql.ErrNoRows
	}
	item, err := s.scanNacosConfigRevision(rows, includeContent)
	if err != nil {
		return NacosConfigRevision{}, err
	}
	if err := rows.Err(); err != nil {
		return NacosConfigRevision{}, err
	}
	return item, nil
}

func (s *Store) DeleteOldNacosConfigRevisions(nacosInstanceID, namespace, group, dataID string, keep int) error {
	nacosInstanceID = strings.TrimSpace(nacosInstanceID)
	namespace = strings.TrimSpace(namespace)
	group = strings.TrimSpace(group)
	dataID = strings.TrimSpace(dataID)
	if nacosInstanceID == "" || namespace == "" || group == "" || dataID == "" {
		return nil
	}
	if keep <= 0 {
		keep = maxNacosConfigRevisions
	}
	rows, err := s.db.Query(`select id from nacos_config_revisions
		where nacos_instance_id=? and namespace=? and group_name=? and data_id=?
		order by published_at desc, created_at desc`, nacosInstanceID, namespace, group, dataID)
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
		if _, err := s.db.Exec(`delete from nacos_config_revisions where id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) scanNacosConfigRevision(rows interface {
	Scan(dest ...any) error
}, includeContent bool) (NacosConfigRevision, error) {
	var item NacosConfigRevision
	var cipher string
	if err := rows.Scan(&item.ID, &item.NacosInstanceID, &item.Namespace, &item.Group, &item.DataID, &cipher, &item.ContentHash, &item.Metadata, &item.CreatedBy, &item.CreatedAt, &item.PublishedAt); err != nil {
		return NacosConfigRevision{}, err
	}
	if includeContent {
		content, err := s.decryptSecret(cipher)
		if err != nil {
			return NacosConfigRevision{}, err
		}
		item.Content = content
	}
	return item, nil
}
