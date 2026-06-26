package minio

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

func (u Uninstaller) Uninstall(ctx context.Context, server store.Server, version string, apiPort int, log Logger) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("minio version is required for uninstall")
	}
	if apiPort <= 0 {
		apiPort = 9000
	}
	deployDir := remoteDeployDir(server.DeployDir)
	installRoot := path.Join(deployDir, "minio", version)
	script, err := uninstallStandaloneScript(version, installRoot, apiPort)
	if err != nil {
		return err
	}
	result, err := u.remote.Run(ctx, server, "sh -s <<'AIFAR_MINIO_UNINSTALL'\n"+script+"\nAIFAR_MINIO_UNINSTALL")
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
		return fmt.Errorf("minio remote uninstall failed: %w", err)
	}
	return nil
}
