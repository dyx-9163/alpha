package redis

import (
	"context"
	"fmt"
	"path"
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
	return u.UninstallWithLanguage(ctx, server, version, port, log, "")
}

func (u Uninstaller) UninstallWithLanguage(ctx context.Context, server store.Server, version string, port int, log Logger, lang string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("redis version is required for uninstall")
	}
	if port <= 0 {
		port = 6379
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	installRoot := path.Join(deployDir, "redis", version)
	script, err := uninstallStandaloneScript(version, installRoot, port)
	if err != nil {
		return err
	}
	_, err = installerkit.Run(ctx, u.remote, server, "sh -s <<'AIFAR_REDIS_UNINSTALL'\n"+script+"\nAIFAR_REDIS_UNINSTALL", log, "redis remote uninstall failed")
	if err != nil {
		return err
	}
	return nil
}

func (u Uninstaller) UninstallSentinelWithLanguage(ctx context.Context, server store.Server, version string, redisPort, sentinelPort int, log Logger, lang string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("redis version is required for uninstall")
	}
	if redisPort <= 0 {
		redisPort = 6379
	}
	if sentinelPort <= 0 {
		sentinelPort = 26379
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	installRoot := path.Join(deployDir, "redis", version)
	script, err := uninstallSentinelNodeScript(version, installRoot, redisPort, sentinelPort)
	if err != nil {
		return err
	}
	_, err = installerkit.Run(ctx, u.remote, server, "sh -s <<'AIFAR_REDIS_SENTINEL_UNINSTALL'\n"+script+"\nAIFAR_REDIS_SENTINEL_UNINSTALL", log, "redis sentinel remote uninstall failed")
	if err != nil {
		return err
	}
	return nil
}
