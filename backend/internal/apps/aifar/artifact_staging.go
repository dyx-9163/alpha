package aifar

import (
	"context"
	"errors"
	"path"
	"regexp"
	"strings"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

var installStageSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type installArtifactStage struct {
	Root          string
	ArchiveRemote string
	AgentRemote   string
	ScriptRemote  string
}

func newInstallArtifactStage(installRoot, releaseID, archiveName, agentName string) (installArtifactStage, error) {
	root := path.Clean(strings.TrimSpace(installRoot))
	if root == "." || root == "/" || !path.IsAbs(root) {
		return installArtifactStage{}, errors.New("AIFAR install staging root is invalid")
	}
	if !installStageSegmentPattern.MatchString(releaseID) {
		return installArtifactStage{}, errors.New("AIFAR install staging release is invalid")
	}
	if !safeInstallStageFileName(archiveName) || (agentName != "" && !safeInstallStageFileName(agentName)) {
		return installArtifactStage{}, errors.New("AIFAR install staging file name is invalid")
	}
	stageRoot := path.Join(root, ".aifar-lifecycle", "install-"+releaseID, "stage")
	stage := installArtifactStage{
		Root:          stageRoot,
		ArchiveRemote: path.Join(stageRoot, archiveName),
		ScriptRemote:  path.Join(stageRoot, "install-aifar.sh"),
	}
	if agentName != "" {
		stage.AgentRemote = path.Join(stageRoot, agentName)
	}
	return stage, nil
}

func safeInstallStageFileName(value string) bool {
	return value != "" && value != "." && value != ".." && path.Base(value) == value && !strings.ContainsAny(value, `/\`)
}

func prepareInstallArtifactStage(ctx context.Context, remote installerkit.Remote, server store.Server, installRoot string, stage installArtifactStage, log Logger) error {
	command := strings.Join([]string{
		"set -eu",
		"umask 077",
		"install_root=" + installerkit.ShellQuote(installRoot),
		"stage_root=" + installerkit.ShellQuote(stage.Root),
		`[ -n "$install_root" ] && [ "$install_root" != "/" ]`,
		`[ -n "$stage_root" ] && [ "$stage_root" != "/" ]`,
		`install_parent="${install_root%/*}"`,
		`[ -n "$install_parent" ] || install_parent="/"`,
		`[ -d "$install_parent" ] && [ ! -L "$install_parent" ] || { echo "AIFAR install staging parent is unsafe" >&2; exit 64; }`,
		`install_parent_real="$(readlink -f "$install_parent")"`,
		`[ "$install_parent_real" = "$install_parent" ] || { echo "AIFAR install staging parent changed" >&2; exit 64; }`,
		`if [ -e "$install_root" ] || [ -L "$install_root" ]; then`,
		`  [ -d "$install_root" ] && [ ! -L "$install_root" ] || { echo "AIFAR install staging root is unsafe" >&2; exit 64; }`,
		`else`,
		`  mkdir -- "$install_root"`,
		`fi`,
		`[ -d "$install_root" ] && [ ! -L "$install_root" ] || { echo "AIFAR install staging root is unsafe" >&2; exit 64; }`,
		`install_real="$(readlink -f "$install_root")"`,
		`[ "$install_real" = "$install_root" ] || { echo "AIFAR install staging root changed" >&2; exit 64; }`,
		`lifecycle_root="$install_root/.aifar-lifecycle"`,
		`operation_root="${stage_root%/stage}"`,
		`[ "$operation_root" != "$stage_root" ] && [ "$stage_root" = "$operation_root/stage" ]`,
		`case "$operation_root" in`,
		`  "$lifecycle_root"/install-*) ;;`,
		`  *) echo "AIFAR install staging operation is unsafe" >&2; exit 64 ;;`,
		`esac`,
		`if [ -e "$lifecycle_root" ] || [ -L "$lifecycle_root" ]; then`,
		`  [ -d "$lifecycle_root" ] && [ ! -L "$lifecycle_root" ] || { echo "AIFAR install staging lifecycle directory is unsafe" >&2; exit 64; }`,
		`else`,
		`  mkdir -- "$lifecycle_root"`,
		`fi`,
		`[ -d "$lifecycle_root" ] && [ ! -L "$lifecycle_root" ] || { echo "AIFAR install staging lifecycle directory is unsafe" >&2; exit 64; }`,
		`lifecycle_real="$(readlink -f "$lifecycle_root")"`,
		`[ "$lifecycle_real" = "$install_real/.aifar-lifecycle" ] || { echo "AIFAR install staging lifecycle directory escaped install root" >&2; exit 64; }`,
		`if [ -e "$operation_root" ] || [ -L "$operation_root" ]; then`,
		`  [ -d "$operation_root" ] && [ ! -L "$operation_root" ] || { echo "AIFAR install staging operation directory is unsafe" >&2; exit 64; }`,
		`else`,
		`  mkdir -- "$operation_root"`,
		`fi`,
		`[ -d "$operation_root" ] && [ ! -L "$operation_root" ] || { echo "AIFAR install staging operation directory is unsafe" >&2; exit 64; }`,
		`operation_real="$(readlink -f "$operation_root")"`,
		`[ "$operation_real" = "$lifecycle_real/${operation_root##*/}" ] || { echo "AIFAR install staging operation directory escaped lifecycle root" >&2; exit 64; }`,
		`if [ -e "$stage_root" ] || [ -L "$stage_root" ]; then`,
		`  [ -d "$stage_root" ] && [ ! -L "$stage_root" ] || { echo "AIFAR install staging path is unsafe" >&2; exit 64; }`,
		`else`,
		`  mkdir -- "$stage_root"`,
		`fi`,
		`[ -d "$stage_root" ] && [ ! -L "$stage_root" ] || { echo "AIFAR install staging path is unsafe" >&2; exit 64; }`,
		`stage_real="$(readlink -f "$stage_root")"`,
		`case "$stage_real" in`,
		`  "$install_real"/.aifar-lifecycle/install-*/stage) ;;`,
		`  *) echo "AIFAR install staging path escaped install root" >&2; exit 64 ;;`,
		"esac",
		`[ "$stage_real" = "$operation_real/stage" ] || { echo "AIFAR install staging path escaped operation root" >&2; exit 64; }`,
		`chmod 0700 "$stage_root"`,
		`printf 'AIFAR_INSTALL_STAGE_READY\n'`,
	}, "\n")
	if _, err := installerkit.Run(ctx, remote, server, command, log, "prepare AIFAR install staging failed"); err != nil {
		return err
	}
	return nil
}

func cleanupInstallArtifactStage(ctx context.Context, remote installerkit.Remote, server store.Server, installRoot string, stage installArtifactStage, log Logger) error {
	command := strings.Join([]string{
		"set -eu",
		"install_root=" + installerkit.ShellQuote(installRoot),
		"stage_root=" + installerkit.ShellQuote(stage.Root),
		`[ -n "$install_root" ] && [ "$install_root" != "/" ]`,
		`[ -n "$stage_root" ] && [ "$stage_root" != "/" ]`,
		`[ ! -L "$stage_root" ] || { echo "AIFAR install staging path is a symlink" >&2; exit 64; }`,
		`install_real="$(readlink -f "$install_root")"`,
		`stage_real="$(readlink -f "$stage_root")"`,
		`case "$stage_real" in`,
		`  "$install_real"/.aifar-lifecycle/install-*/stage) ;;`,
		`  *) echo "AIFAR install staging path escaped install root" >&2; exit 64 ;;`,
		"esac",
		`[ "$stage_real" = "$stage_root" ] || { echo "AIFAR install staging path changed" >&2; exit 64; }`,
		`rm -rf -- "$stage_root"`,
		`printf 'AIFAR_INSTALL_STAGE_CLEANED\n'`,
	}, "\n")
	if _, err := installerkit.Run(ctx, remote, server, command, log, "cleanup AIFAR install staging failed"); err != nil {
		return err
	}
	return nil
}
