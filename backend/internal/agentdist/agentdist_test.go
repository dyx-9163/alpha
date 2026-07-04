package agentdist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindBinaryUsesExplicitEnvironmentOverride(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, "custom-agent")
	if err := os.WriteFile(agent, []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIFAR_AGENT_BINARY", agent)
	if got := FindBinary(); got != agent {
		t.Fatalf("FindBinary() = %q, want %q", got, agent)
	}
}

func TestFindBinaryInvalidEnvironmentOverrideDoesNotFallback(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "aifar-agent-linux-amd64"), []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIFAR_AGENT_BINARY", filepath.Join(root, "missing-agent"))
	t.Chdir(root)
	if got := FindBinary(); got != "" {
		t.Fatalf("FindBinary() = %q, want empty when explicit override is invalid", got)
	}
}

func TestFindBinaryFindsRepoRootBinFromBackendWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	backendDir := filepath.Join(root, "backend")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backendDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(binDir, "aifar-agent-linux-amd64")
	if err := os.WriteFile(agent, []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIFAR_AGENT_BINARY", "")
	t.Chdir(backendDir)
	if got := FindBinary(); got != agent {
		t.Fatalf("FindBinary() = %q, want %q", got, agent)
	}
}
