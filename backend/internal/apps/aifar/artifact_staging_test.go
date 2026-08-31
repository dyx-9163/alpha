package aifar

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type stagingRemote struct {
	commands []string
	err      error
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
