package aifar

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type stagingRemote struct {
	commands []string
	err      error
}

type shellStagingRemote struct{}

func (shellStagingRemote) Run(ctx context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	stdout, err := cmd.Output()
	result := adapter.CommandResult{Stdout: string(stdout)}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.Stderr = string(exitErr.Stderr)
	}
	return result, err
}

func (shellStagingRemote) UploadFile(context.Context, store.Server, string, string, os.FileMode) error {
	return nil
}

func (r *stagingRemote) Run(_ context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.commands = append(r.commands, command)
	if r.err != nil {
		return adapter.CommandResult{}, r.err
	}
	return adapter.CommandResult{}, nil
}

func (r *stagingRemote) UploadFile(context.Context, store.Server, string, string, os.FileMode) error {
	return nil
}

func TestNewInstallArtifactStageBuildsOperationScopedPaths(t *testing.T) {
	got, err := newInstallArtifactStage(
		"/aifar/apps/admin",
		"runtime-v2-20260831T010203Z",
		"aifar-runtime-v2.tar.gz",
		"aifar-agent-linux-amd64",
	)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := "/aifar/apps/admin/.aifar-lifecycle/install-runtime-v2-20260831T010203Z/stage"
	if got.Root != wantRoot ||
		got.ArchiveRemote != wantRoot+"/aifar-runtime-v2.tar.gz" ||
		got.AgentRemote != wantRoot+"/aifar-agent-linux-amd64" ||
		got.ScriptRemote != wantRoot+"/install-aifar.sh" {
		t.Fatalf("unexpected stage: %+v", got)
	}
}

func TestNewInstallArtifactStageRejectsUnsafeSegments(t *testing.T) {
	tests := []struct {
		name, installRoot, releaseID, archiveName, agentName string
	}{
		{name: "root install directory", installRoot: "/", releaseID: "release-1", archiveName: "bundle.tar.gz"},
		{name: "relative install directory", installRoot: "aifar/apps/admin", releaseID: "release-1", archiveName: "bundle.tar.gz"},
		{name: "release traversal", installRoot: "/aifar/apps/admin", releaseID: "../release", archiveName: "bundle.tar.gz"},
		{name: "archive traversal", installRoot: "/aifar/apps/admin", releaseID: "release-1", archiveName: "../bundle.tar.gz"},
		{name: "agent traversal", installRoot: "/aifar/apps/admin", releaseID: "release-1", archiveName: "bundle.tar.gz", agentName: "../agent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newInstallArtifactStage(tc.installRoot, tc.releaseID, tc.archiveName, tc.agentName); err == nil {
				t.Fatal("expected unsafe stage input to fail")
			}
		})
	}
}

func TestPrepareInstallArtifactStageChecksResolvedContainment(t *testing.T) {
	remote := &stagingRemote{}
	stage, err := newInstallArtifactStage("/aifar/apps/admin", "release-1", "bundle.tar.gz", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallArtifactStage(context.Background(), remote, store.Server{}, "/aifar/apps/admin", stage, nil); err != nil {
		t.Fatal(err)
	}
	command := strings.Join(remote.commands, "\n")
	for _, want := range []string{
		`readlink -f "$install_root"`,
		`readlink -f "$stage_root"`,
		`"$install_real"/.aifar-lifecycle/install-*/stage`,
		`chmod 0700 "$stage_root"`,
		`AIFAR_INSTALL_STAGE_READY`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("prepare command missing %q:\n%s", want, command)
		}
	}
}

func TestPrepareInstallArtifactStageCreatesAndValidatesOneComponentAtATime(t *testing.T) {
	remote := &stagingRemote{}
	stage, err := newInstallArtifactStage("/aifar/apps/admin", "release-1", "bundle.tar.gz", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallArtifactStage(context.Background(), remote, store.Server{}, "/aifar/apps/admin", stage, nil); err != nil {
		t.Fatal(err)
	}
	command := strings.Join(remote.commands, "\n")
	if strings.Contains(command, `mkdir -p "$install_root" "$stage_root"`) {
		t.Fatalf("prepare recursively creates unchecked descendants:\n%s", command)
	}
	wants := []string{
		`root_real="$(readlink -f "/")"`,
		`mkdir -- "$next"`,
		`next_real="$(readlink -f "$next")"`,
		`install_real="$(readlink -f "$install_root")"`,
		`mkdir -- "$lifecycle_root"`,
		`lifecycle_real="$(readlink -f "$lifecycle_root")"`,
		`mkdir -- "$operation_root"`,
		`operation_real="$(readlink -f "$operation_root")"`,
		`mkdir -- "$stage_root"`,
		`stage_real="$(readlink -f "$stage_root")"`,
	}
	last := -1
	for _, want := range wants {
		index := strings.Index(command, want)
		if index < 0 || index <= last {
			t.Fatalf("prepare command does not validate components in order at %q:\n%s", want, command)
		}
		last = index
	}
}

func TestPrepareInstallArtifactStageWalksInstallRootFromValidatedRoot(t *testing.T) {
	remote := &stagingRemote{}
	stage, err := newInstallArtifactStage("/aifar/apps/admin", "release-1", "bundle.tar.gz", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallArtifactStage(context.Background(), remote, store.Server{}, "/aifar/apps/admin", stage, nil); err != nil {
		t.Fatal(err)
	}
	command := strings.Join(remote.commands, "\n")
	for _, want := range []string{
		`remaining="${install_root#/}"`,
		`next="$current/$component"`,
		`if [ -e "$next" ] || [ -L "$next" ]; then`,
		`mkdir -- "$next"`,
		`next_real="$(readlink -f "$next")"`,
		`[ "$next_real" = "$next" ]`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("install-root walk missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(command, `install_parent="${install_root%/*}"`) {
		t.Fatalf("prepare still requires the immediate install-root parent to exist:\n%s", command)
	}
}

func TestPrepareInstallArtifactStageCreatesMissingInstallRootHierarchy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell semantics; the cross-platform root-walk contract remains active")
	}
	parent := t.TempDir()
	installRoot := filepath.ToSlash(filepath.Join(parent, "aifar", "apps", "admin"))
	stage, err := newInstallArtifactStage(installRoot, "release-1", "bundle.tar.gz", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallArtifactStage(context.Background(), shellStagingRemote{}, store.Server{}, installRoot, stage, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.FromSlash(stage.Root))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("missing install hierarchy was not safely created: info=%v err=%v", info, err)
	}
}

func TestPrepareInstallArtifactStageRejectsSymlinkAncestorWithoutCreatingOutsideInstallRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX symlink and shell semantics; the cross-platform root-walk contract remains active")
	}
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(parent, "aifar")); err != nil {
		t.Fatal(err)
	}
	installRoot := filepath.ToSlash(filepath.Join(parent, "aifar", "apps", "admin"))
	stage, err := newInstallArtifactStage(installRoot, "release-1", "bundle.tar.gz", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallArtifactStage(context.Background(), shellStagingRemote{}, store.Server{}, installRoot, stage, nil); err == nil {
		t.Fatal("expected install-root symlink ancestor to be rejected")
	}
	escapedInstallRoot := filepath.Join(outside, "apps", "admin")
	if _, err := os.Lstat(escapedInstallRoot); !os.IsNotExist(err) {
		t.Fatalf("prepare created an install hierarchy through a symlink ancestor: %s (err=%v)", escapedInstallRoot, err)
	}
}

func TestPrepareInstallArtifactStageRejectsParentSymlinksWithoutCreatingOutsideInstallRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX symlink and shell semantics; the cross-platform command-order test remains active")
	}
	for _, tc := range []struct {
		name         string
		linkRelative string
		escapedParts []string
	}{
		{name: "lifecycle", linkRelative: ".aifar-lifecycle", escapedParts: []string{"install-release-1", "stage"}},
		{name: "operation", linkRelative: ".aifar-lifecycle/install-release-1", escapedParts: []string{"stage"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			installRoot := filepath.ToSlash(filepath.Join(parent, "admin"))
			outside := filepath.Join(parent, "outside")
			link := filepath.Join(filepath.FromSlash(installRoot), filepath.FromSlash(tc.linkRelative))
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(outside, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			stage, err := newInstallArtifactStage(installRoot, "release-1", "bundle.tar.gz", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := prepareInstallArtifactStage(context.Background(), shellStagingRemote{}, store.Server{}, installRoot, stage, nil); err == nil {
				t.Fatalf("expected %s symlink to be rejected", tc.name)
			}
			escapedStage := filepath.Join(append([]string{outside}, tc.escapedParts...)...)
			if _, err := os.Lstat(escapedStage); !os.IsNotExist(err) {
				t.Fatalf("prepare created an outside descendant through the %s symlink: %s (err=%v)", tc.name, escapedStage, err)
			}
		})
	}
}

func TestCleanupInstallArtifactStageCanOnlyRemoveExactContainedStage(t *testing.T) {
	remote := &stagingRemote{}
	stage, _ := newInstallArtifactStage("/aifar/apps/admin", "release-1", "bundle.tar.gz", "")
	if err := cleanupInstallArtifactStage(context.Background(), remote, store.Server{}, "/aifar/apps/admin", stage, nil); err != nil {
		t.Fatal(err)
	}
	command := strings.Join(remote.commands, "\n")
	if !strings.Contains(command, `rm -rf -- "$stage_root"`) || strings.Contains(command, `rm -rf -- "$install_root"`) {
		t.Fatalf("unsafe cleanup command:\n%s", command)
	}
}

func TestInstallArtifactStageCommandsPropagateRunErrors(t *testing.T) {
	remote := &stagingRemote{err: errors.New("boom")}
	stage, err := newInstallArtifactStage("/aifar/apps/admin", "release-1", "bundle.tar.gz", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallArtifactStage(context.Background(), remote, store.Server{}, "/aifar/apps/admin", stage, nil); err == nil {
		t.Fatal("expected prepare error")
	}
	if err := cleanupInstallArtifactStage(context.Background(), remote, store.Server{}, "/aifar/apps/admin", stage, nil); err == nil {
		t.Fatal("expected cleanup error")
	}
}
