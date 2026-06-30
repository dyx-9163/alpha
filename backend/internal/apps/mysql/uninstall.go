package mysql

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

func (u Uninstaller) Uninstall(ctx context.Context, server store.Server, version string, port int, log Logger) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("mysql version is required for uninstall")
	}
	if port <= 0 {
		port = 3306
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	installRoot := installerkit.InstallRoot(deployDir, "mysql")
	legacyInstallRoot := installerkit.LegacyInstallRoot(deployDir, "mysql", version)
	script, err := uninstallStandaloneScript(version, installRoot, legacyInstallRoot, port)
	if err != nil {
		return err
	}
	_, err = installerkit.Run(ctx, u.remote, server, "sh -s <<'AIFAR_MYSQL_UNINSTALL'\n"+script+"\nAIFAR_MYSQL_UNINSTALL", log, "mysql remote uninstall failed")
	if err != nil {
		return err
	}
	return nil
}
