package installerkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

func TestRenderTemplateUsesEmbeddedWhenOverrideMissing(t *testing.T) {
	t.Setenv(TemplateDirEnv, t.TempDir())
	out, err := RenderTemplate("mysql", "standalone/install.sh", "test", "hello {{.Name}}", nil, map[string]string{"Name": "embedded"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello embedded" {
		t.Fatalf("unexpected embedded render: %q", out)
	}
}

func TestRenderTemplateUsesConfigOverride(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(root, "mysql", "standalone", "install.sh")
	if err := os.MkdirAll(filepath.Dir(override), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte("override {{ shq .Name }}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(TemplateDirEnv, root)
	out, err := RenderTemplate("mysql", "standalone/install.sh", "test", "embedded {{.Name}}", template.FuncMap{"shq": ShellQuote}, map[string]string{"Name": "a'b"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "override 'a'\"'\"'b'" {
		t.Fatalf("unexpected override render: %q", out)
	}
}

func TestTemplateSourceRejectsUnsafePath(t *testing.T) {
	t.Setenv(TemplateDirEnv, t.TempDir())
	_, err := TemplateSource("mysql", "../install.sh", "embedded")
	if err == nil || !strings.Contains(err.Error(), "invalid installer template path") {
		t.Fatalf("expected unsafe path error, got %v", err)
	}
}
