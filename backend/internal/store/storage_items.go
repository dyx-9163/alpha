package store

import (
	"database/sql"
	"time"
)

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
