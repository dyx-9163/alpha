package mysql

import (
	"context"
	"fmt"
	"path"
	"strings"

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
	deployDir := remoteDeployDir(server.DeployDir)
	installRoot := path.Join(deployDir, "mysql", version)
	script, err := uninstallStandaloneScript(version, installRoot, port)
	if err != nil {
		return err
	}
	result, err := u.remote.Run(ctx, server, "sh -s <<'AIFAR_MYSQL_UNINSTALL'\n"+script+"\nAIFAR_MYSQL_UNINSTALL")
	if strings.TrimSpace(result.Stdout) != "" {
		log.Info("%s", strings.TrimSpace(result.Stdout))
	}
	if strings.TrimSpace(result.Stderr) != "" {
		if err != nil {
			log.Error("%s", strings.TrimSpace(result.Stderr))
		} else {
			log.Info("%s", strings.TrimSpace(result.Stderr))
		}
	}
	if err != nil {
		return fmt.Errorf("mysql remote uninstall failed: %w", err)
	}
	return nil
}
