package docker

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

func (u Uninstaller) Uninstall(ctx context.Context, server store.Server, version string, log Logger) error {
	return u.UninstallWithLanguage(ctx, server, version, log, "")
}

func (u Uninstaller) UninstallWithLanguage(ctx context.Context, server store.Server, version string, log Logger, lang string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("docker version is required for uninstall")
	}
	deployDir := remoteDeployDir(server.DeployDir)
	installRoot := path.Join(deployDir, "docker", version)
	script, err := uninstallScript(version, installRoot)
	if err != nil {
		return err
	}
	result, err := u.remote.Run(ctx, server, "sh -s <<'AIFAR_DOCKER_UNINSTALL'\n"+script+"\nAIFAR_DOCKER_UNINSTALL")
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
		return fmt.Errorf("docker remote uninstall failed: %w", err)
	}
	return nil
}
