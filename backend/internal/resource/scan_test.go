package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestScanResources(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "docker", "24.0.9")
	rpmDir := filepath.Join(appDir, "rpms")
	if err := os.MkdirAll(rpmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "docker.tar"), []byte("bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rpmDir, "tar.rpm"), []byte("rpm"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected one resource, got %d", len(out))
	}
	if out[0].App != "docker" || out[0].Part != "backend" || out[0].Version != "24.0.9" || out[0].RPMCount != 1 {
		t.Fatalf("unexpected resource: %+v", out[0])
	}
}

func TestScanSplitFrontendBackendResources(t *testing.T) {
	root := t.TempDir()
	frontendDir := filepath.Join(root, "mysql-frontend", "8.0.36")
	backendDir := filepath.Join(root, "mysql-backend", "8.0.36")
	if err := os.MkdirAll(frontendDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backendDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontendDir, "mysql-ui.tar.gz"), []byte("frontend"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backendDir, "mysql-installer.tar.gz"), []byte("backend"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected two resources, got %d", len(out))
	}
	parts := map[string]bool{}
	for _, res := range out {
		if res.App != "mysql" || res.Version != "8.0.36" {
			t.Fatalf("unexpected resource: %+v", res)
		}
		parts[res.Part] = true
	}
	if !parts["frontend"] || !parts["backend"] {
		t.Fatalf("expected frontend and backend parts, got %+v", parts)
	}
}

func TestScanUsesVersionManifestChecksum(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "docker", "24.0.9")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("bundle")
	sum := sha256.Sum256(content)
	archive := filepath.Join(appDir, "docker.tar")
	if err := os.WriteFile(archive, content, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"files":{"docker.tar":{"sha256":"%s","part":"backend"}}}`, hex.EncodeToString(sum[:]))
	if err := os.WriteFile(filepath.Join(appDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected one resource, got %d", len(out))
	}
	if out[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("expected manifest sha256 to be applied, got %+v", out[0])
	}
}
