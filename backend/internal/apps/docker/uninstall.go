package docker

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

func NewUninstaller(remote Remote) Uninstaller {
	return Uninstaller{remote: remote}
}

func (u Uninstaller) Uninstall(ctx context.Context, server store.Server, version string, log Logger) error {
	return u.UninstallWithLanguage(ctx, server, version, log, "")
}

func (u Uninstaller) UninstallWithLanguage(ctx context.Context, server store.Server, version string, log Logger, lang string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("docker version is required for uninstall")
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	installRoot := installerkit.InstallRoot(deployDir, "docker")
	script, err := uninstallScript(version, installRoot)
	if err != nil {
		return err
	}
	_, err = installerkit.Run(ctx, u.remote, server, "sh -s <<'AIFAR_DOCKER_UNINSTALL'\n"+script+"\nAIFAR_DOCKER_UNINSTALL", log, "docker remote uninstall failed")
	if err != nil {
		return err
	}
	return nil
}
