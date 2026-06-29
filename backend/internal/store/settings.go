package store

import "time"

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
