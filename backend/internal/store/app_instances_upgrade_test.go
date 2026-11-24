package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSaveAppInstancesIfUnchangedRollsBackEveryMemberOnConflict(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	a, err := db.SaveAppInstance(AppInstance{App: "minio", Version: "old"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.SaveAppInstance(AppInstance{App: "minio", Version: "old"})
	if err != nil {
		t.Fatal(err)
	}
	stale := b
	b.Status = "changed"
	if _, err := db.SaveAppInstance(b); err != nil {
		t.Fatal(err)
	}
	a.Version, stale.Version = "new", "new"
	if err := db.SaveAppInstancesIfUnchanged([]AppInstance{a, stale}); !errors.Is(err, ErrAppInstanceConflict) {
		t.Fatalf("upgrade returned %v", err)
	}
	actual, err := db.GetAppInstance(a.ID)
	if err != nil || actual.Version != "old" {
		t.Fatalf("first member update committed despite conflict: %+v, %v", actual, err)
	}
	b, err = db.GetAppInstance(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	b.Version = "new"
	if err := db.SaveAppInstancesIfUnchanged([]AppInstance{a, b}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		actual, err := db.GetAppInstance(id)
		if err != nil || actual.Version != "new" {
			t.Fatalf("member update did not commit: %+v, %v", actual, err)
		}
	}
}
