package aifar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverBundleServicesUsesDirectoryDefinitions(t *testing.T) {
	root := t.TempDir()
	bundle := Bundle{Root: root, AppDir: filepath.Join(root, "services")}
	writeServiceDefinition(t, bundle.AppDir, serviceDefinition{
		Schema: "aifar-runtime-service-v1", Name: "web-vue3", Kind: "web", Port: 8080,
		Required: true, Role: "web", ArtifactExtensions: []string{".zip"}, HealthPath: "/", AffinityPolicy: "round-robin",
	})
	writeServiceDefinition(t, bundle.AppDir, serviceDefinition{
		Schema: "aifar-runtime-service-v1", Name: "custom-report", Kind: "java", ApplicationName: "alpha-custom-report", Port: 38100,
		ArtifactExtensions: []string{".jar"}, HealthPath: "/actuator/health/readiness", AffinityPolicy: "round-robin",
	})
	writeServiceDefinition(t, bundle.AppDir, serviceDefinition{
		Schema: "aifar-runtime-service-v1", Name: "gateway", Kind: "java", ApplicationName: "alpha-gateway", Port: 38000,
		Required: true, Role: "gateway", ArtifactExtensions: []string{".jar"}, HealthPath: "/actuator/health/readiness", AffinityPolicy: "stable",
	})

	definitions, err := discoverBundleServices(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if got := serviceNames(definitions); !reflect.DeepEqual(got, []string{"custom-report", "gateway", "web-vue3"}) {
		t.Fatalf("unexpected directory-driven service order: %#v", got)
	}
	modules := installModuleDefinitions(definitions, "zh")
	if len(modules) != 3 || modules[0].Name != "custom-report" || modules[0].Port != 38100 {
		t.Fatalf("unexpected install modules: %#v", modules)
	}
}

func TestDiscoverBundleServicesRejectsDirectoryNameMismatch(t *testing.T) {
	root := t.TempDir()
	bundle := Bundle{Root: root, AppDir: filepath.Join(root, "services")}
	writeServiceDefinitionInDir(t, bundle.AppDir, "wrong-dir", serviceDefinition{
		Schema: "aifar-runtime-service-v1", Name: "actual-name", Kind: "java", ApplicationName: "alpha-actual", Port: 38101,
	})
	if _, err := discoverBundleServices(bundle); err == nil {
		t.Fatal("expected mismatched service directory and descriptor name to fail")
	}
}

func writeServiceDefinition(t *testing.T, appDir string, definition serviceDefinition) {
	t.Helper()
	writeServiceDefinitionInDir(t, appDir, definition.Name, definition)
}

func writeServiceDefinitionInDir(t *testing.T, appDir, dirName string, definition serviceDefinition) {
	t.Helper()
	dir := filepath.Join(appDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, serviceDefinitionName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
