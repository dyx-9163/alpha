package redis

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
	deployDir := remoteDeployDir(server.DeployDir)
	installRoot := path.Join(deployDir, "redis", version)
	script, err := uninstallStandaloneScript(version, installRoot, port)
	if err != nil {
		return err
	}
	result, err := u.remote.Run(ctx, server, "sh -s <<'AIFAR_REDIS_UNINSTALL'\n"+script+"\nAIFAR_REDIS_UNINSTALL")
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
		return fmt.Errorf("redis remote uninstall failed: %w", err)
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
	deployDir := remoteDeployDir(server.DeployDir)
	installRoot := path.Join(deployDir, "redis", version)
	script, err := uninstallSentinelNodeScript(version, installRoot, redisPort, sentinelPort)
	if err != nil {
		return err
	}
	result, err := u.remote.Run(ctx, server, "sh -s <<'AIFAR_REDIS_SENTINEL_UNINSTALL'\n"+script+"\nAIFAR_REDIS_SENTINEL_UNINSTALL")
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
		return fmt.Errorf("redis sentinel remote uninstall failed: %w", err)
	}
	return nil
}
