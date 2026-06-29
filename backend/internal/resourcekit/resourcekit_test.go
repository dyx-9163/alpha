package resourcekit

import (
	"os"
	"path/filepath"
	"testing"

	"aifar-deployment/backend/internal/store"
)

func TestSelectChoosesLatestMatchingResource(t *testing.T) {
	resources := []store.Resource{
		{App: "redis", Part: "backend", Version: "7.0.0", Path: filepath.Join("resources", "redis-7.0.0.tar.gz")},
		{App: "redis", Part: "backend", Version: "7.2.14", Path: filepath.Join("resources", "redis-7.2.14.tar.gz")},
		{App: "redis", Part: "backend", Version: "7.2.14", Path: filepath.Join("resources", "redis-7.2.14.tar.gz.sha256sum")},
		{App: "mysql", Part: "backend", Version: "8.0.36", Path: filepath.Join("resources", "mysql-8.0.36.tar.gz")},
	}

	selected, ok := Select(resources, SelectOptions{
		App:            "redis",
		Part:           "backend",
		Version:        "latest",
		SkipSignatures: true,
		Match: func(baseLower string, _ store.Resource) bool {
			return baseLower[:5] == "redis"
		},
	})
	if !ok {
		t.Fatal("expected resource to be selected")
	}
	if selected.Version != "7.2.14" {
		t.Fatalf("expected latest redis version, got %s", selected.Version)
	}
}

func TestListRPMsSortsRPMFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"z.rpm", "ignore.txt", "a.rpm"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rpms := ListRPMs(dir)
	if len(rpms) != 2 {
		t.Fatalf("expected two rpm files, got %d", len(rpms))
	}
	if filepath.Base(rpms[0]) != "a.rpm" || filepath.Base(rpms[1]) != "z.rpm" {
		t.Fatalf("expected sorted rpm files, got %#v", rpms)
	}
}

func TestVerifySHA256RejectsMismatch(t *testing.T) {
	file := filepath.Join(t.TempDir(), "resource.tar.gz")
	if err := os.WriteFile(file, []byte("resource"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := VerifySHA256(file, "deadbeef", "resource archive")
	if err == nil {
		t.Fatal("expected sha256 mismatch")
	}
}
