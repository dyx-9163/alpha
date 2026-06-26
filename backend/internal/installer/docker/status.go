package docker

import (
	"context"
	"fmt"
	"path"
	"strings"

	"aifar-deployment/backend/internal/store"
)

type StatusResult struct {
	Status            string
	Message           string
	DockerVersion     string
	ComposeVersion    string
	InstallRoot       string
	InstallRootExists bool
	UnitExists        bool
}

type Inspector struct {
	remote Remote
}

func NewInspector(remote Remote) Inspector {
	return Inspector{remote: remote}
}

func (i Inspector) Check(ctx context.Context, server store.Server, version string, log Logger) (StatusResult, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return StatusResult{}, fmt.Errorf("docker version is required for status check")
	}
	installRoot := path.Join(remoteDeployDir(server.DeployDir), "docker", version)
	result, err := i.remote.Run(ctx, server, dockerStatusCommand(installRoot))
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
		return StatusResult{Status: "error", InstallRoot: installRoot, Message: err.Error()}, fmt.Errorf("docker status check failed: %w", err)
	}
	status := parseStatusOutput(result.Stdout)
	status.InstallRoot = installRoot
	if status.Status == "" {
		status.Status = "unknown"
	}
	return status, nil
}

func dockerStatusCommand(installRoot string) string {
	return "sh -s <<'AIFAR_DOCKER_STATUS'\n" + `#!/usr/bin/env sh
set -u

INSTALL_ROOT=` + shellQuote(installRoot) + `
STATUS="missing"
DOCKER_VERSION=""
COMPOSE_VERSION=""
UNIT_EXISTS="false"
INSTALL_ROOT_EXISTS="false"

if [ -d "$INSTALL_ROOT" ]; then
  INSTALL_ROOT_EXISTS="true"
fi

if [ -e /etc/systemd/system/docker.service ] || [ -e /etc/systemd/system/containerd.service ]; then
  UNIT_EXISTS="true"
fi

if command -v docker >/dev/null 2>&1; then
  DOCKER_VERSION="$(docker --version 2>/dev/null || true)"
  if docker info >/dev/null 2>&1; then
    STATUS="running"
  elif [ "$STATUS" = "missing" ]; then
    STATUS="stopped"
  fi
fi

if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet docker 2>/dev/null; then
    STATUS="running"
  elif [ "$UNIT_EXISTS" = "true" ] && [ "$STATUS" = "missing" ]; then
    STATUS="stopped"
  fi
fi

if command -v docker >/dev/null 2>&1; then
  COMPOSE_VERSION="$(docker compose version 2>/dev/null || true)"
fi
if [ -z "$COMPOSE_VERSION" ] && command -v docker-compose >/dev/null 2>&1; then
  COMPOSE_VERSION="$(docker-compose version 2>/dev/null || true)"
fi

echo "status=$STATUS"
echo "dockerVersion=$DOCKER_VERSION"
echo "composeVersion=$COMPOSE_VERSION"
echo "unitExists=$UNIT_EXISTS"
echo "installRootExists=$INSTALL_ROOT_EXISTS"
` + "\nAIFAR_DOCKER_STATUS"
}

func parseStatusOutput(output string) StatusResult {
	result := StatusResult{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "status":
			result.Status = strings.TrimSpace(value)
		case "dockerVersion":
			result.DockerVersion = strings.TrimSpace(value)
		case "composeVersion":
			result.ComposeVersion = strings.TrimSpace(value)
		case "unitExists":
			result.UnitExists = strings.EqualFold(strings.TrimSpace(value), "true")
		case "installRootExists":
			result.InstallRootExists = strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	switch result.Status {
	case "running":
		result.Message = "Docker daemon is running"
	case "stopped":
		result.Message = "Docker files or units exist, but the daemon is not running"
	case "missing":
		result.Message = "Docker deployment is not present on target server"
	default:
		result.Message = "Docker status is unknown"
	}
	return result
}
