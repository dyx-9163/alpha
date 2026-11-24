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

func TestSaveAppInstanceVersionsIfUnchangedKeepsObservationsAndRollsBackOnEdit(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mysql, err := db.SaveAppInstance(AppInstance{App: "mysql", Version: "8.0.36", Status: "installed", Metadata: `{"endpoint":"mysql:3306"}`})
	if err != nil {
		t.Fatal(err)
	}
	router, err := db.SaveAppInstance(AppInstance{App: "mysql-router", Version: "8.0.36"})
	if err != nil {
		t.Fatal(err)
	}
	updates := []AppInstanceVersionUpdate{
		{Expected: mysql, Version: "8.4.13", Metadata: map[string]any{"mysqlShellVersion": "26.10.0"}},
		{Expected: router, Version: "8.4.13", Metadata: map[string]any{"routerVersion": "26.10.0"}},
	}
	if err := db.SaveAppInstanceObservation(mysql, AppInstanceObservation{Status: "running", Metadata: map[string]any{"role": "primary"}}); err != nil {
		t.Fatal(err)
	}
	router.ServerID = "changed-server"
	router, err = db.SaveAppInstance(router)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAppInstanceVersionsIfUnchanged(updates); !errors.Is(err, ErrAppInstanceConflict) {
		t.Fatalf("expected conflict on durable edit, got %v", err)
	}
	current, err := db.GetAppInstance(mysql.ID)
	if err != nil || current.Version != "8.0.36" {
		t.Fatalf("earlier member update was not rolled back: %+v, %v", current, err)
	}
	updates[1].Expected = router
	if err := db.SaveAppInstanceVersionsIfUnchanged(updates); err != nil {
		t.Fatal(err)
	}
	current, err = db.GetAppInstance(mysql.ID)
	if err != nil || current.Version != "8.4.13" || current.Status != "running" ||
		current.Metadata != `{"endpoint":"mysql:3306","mysqlShellVersion":"26.10.0","role":"primary"}` {
		t.Fatalf("upgrade or running observation was lost: %+v, %v", current, err)
	}
}
