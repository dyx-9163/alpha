package minio

import (
	"context"
	"fmt"
	"strings"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type Uninstaller struct {
	remote Remote
}

type UninstallOptions struct {
	RemoveMountedDisks bool
	MountRoots         []string
}

func NewUninstaller(remote Remote) Uninstaller {
	return Uninstaller{remote: remote}
}

func (u Uninstaller) Uninstall(ctx context.Context, server store.Server, version string, apiPort int, options UninstallOptions, log Logger) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("minio version is required for uninstall")
	}
	if apiPort <= 0 {
		apiPort = 9000
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	installRoot := installerkit.InstallRoot(deployDir, "minio")
	legacyInstallRoot := installerkit.LegacyInstallRoot(deployDir, "minio", version)
	script, err := uninstallStandaloneScript(version, installRoot, legacyInstallRoot, apiPort, options)
	if err != nil {
		return err
	}
	_, err = installerkit.Run(ctx, u.remote, server, "sh -s <<'AIFAR_MINIO_UNINSTALL'\n"+script+"\nAIFAR_MINIO_UNINSTALL", log, "minio remote uninstall failed")
	if err != nil {
		return err
	}
	return nil
}
