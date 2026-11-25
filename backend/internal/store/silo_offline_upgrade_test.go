package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiloCheckpointFinalizesOnlyOriginalPairAndKeepsObservations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var members []AppInstance
	for _, server := range []string{"one", "two", "other"} {
		member, err := db.SaveAppInstance(AppInstance{App: "minio", Version: "source", ServerID: server, Metadata: `{"lastCheck":"original"}`})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, member)
	}
	checkpoint := SiloOfflineUpgradeCheckpoint{TaskID: "origin", Nodes: []SiloOfflineUpgradeNode{
		{InstanceID: members[0].ID, ServerID: "one"}, {InstanceID: members[1].ID, ServerID: "two"},
	}}
	if err := db.CreateSiloOfflineUpgradeCheckpoint("group", checkpoint); err != nil {
		t.Fatal(err)
	}
	update := func(i int) AppInstanceVersionUpdate {
		return AppInstanceVersionUpdate{Expected: members[i], Version: "target", Metadata: map[string]any{"backup": "original"}}
	}
	if err := db.CompleteSiloOfflineUpgrade("group", "origin", []AppInstanceVersionUpdate{update(0), update(2)}); !errors.Is(err, ErrSiloOfflineUpgradeCheckpointConflict) {
		t.Fatalf("unexpected member was accepted: %v", err)
	}
	saved, err := db.GetSiloOfflineUpgradeCheckpoint("group")
	if err != nil || saved.TaskID != checkpoint.TaskID {
		t.Fatalf("atomic checkpoint was lost: %+v %v", saved, err)
	}
	if err := db.SaveAppInstanceObservation(members[0], AppInstanceObservation{Status: "available", Metadata: map[string]any{"lastCheck": "observed"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteSiloOfflineUpgrade("group", "origin", []AppInstanceVersionUpdate{update(0), update(1)}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAppInstance(members[0].ID)
	if err != nil || got.Version != "target" || !strings.Contains(got.Metadata, `"lastCheck":"observed"`) || !strings.Contains(got.Metadata, `"backup":"original"`) {
		t.Fatalf("missing version or merged metadata: %+v %v", got, err)
	}
}
