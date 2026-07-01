package nacos

import (
	"os"
	"path/filepath"
	"testing"

	"aifar-deployment/backend/internal/store"
)

func TestSelectBundleFindsNacosAndJDKArchives(t *testing.T) {
	dir := t.TempDir()
	versionDir := filepath.Join(dir, "nacos", "2.4.3")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nacosPath := filepath.Join(versionDir, "nacos-server-2.4.3.tar.gz")
	jdkX64 := filepath.Join(versionDir, "OpenJDK11U-jdk_x64_linux_hotspot_11.0.29_7.tar.gz")
	jdkArm := filepath.Join(versionDir, "OpenJDK11U-jdk_aarch64_linux_hotspot_11.0.29_7.tar.gz")
	for _, path := range []string{nacosPath, jdkX64, jdkArm} {
		if err := os.WriteFile(path, []byte("archive"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := SelectBundle([]store.Resource{
		{App: "nacos", Part: "backend", Version: "2.4.3", Path: nacosPath},
	}, "2.4.3")
	if err != nil {
		t.Fatalf("SelectBundle returned error: %v", err)
	}
	if bundle.ArchivePath != nacosPath || bundle.JDKX64Path != jdkX64 || bundle.JDKAarch64Path != jdkArm {
		t.Fatalf("unexpected bundle: %#v", bundle)
	}
	if err := VerifyBundle(bundle); err != nil {
		t.Fatalf("VerifyBundle returned error: %v", err)
	}
}
