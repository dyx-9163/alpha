package store

import (
	"path/filepath"
	"testing"
)

func TestSetSettingsRollsBackAllKeysWhenOneEntryIsInvalid(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.SetSetting("language", "zh"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSettings(map[string]string{
		"language": "en",
		"":         "invalid",
	}); err == nil {
		t.Fatal("expected invalid key to reject the whole settings update")
	}
	if got := db.GetSetting("language", ""); got != "zh" {
		t.Fatalf("language=%q, want original value zh after rollback", got)
	}
	if got := db.GetSetting("", "missing"); got != "missing" {
		t.Fatalf("empty setting key was inserted: %q", got)
	}
}
