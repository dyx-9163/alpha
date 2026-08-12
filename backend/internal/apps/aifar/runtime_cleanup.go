package aifar

import (
	"context"
	"errors"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
)

func (s Service) CleanupRuntimeStalePods(ctx context.Context, req RuntimeCleanupRequest, log Logger, targetLog targetLogger) error {
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(
		logForServer,
		recorder,
		target,
		runtimeCleanupSteps(),
		"AIFAR runtime cleanup step %d/%d started: %s",
		"AIFAR runtime cleanup step %d/%d completed: %s",
		"AIFAR runtime cleanup step %d/%d failed: %s: %v",
	)

	current, lock, err := s.acquireOrchestrationLock(req.Instance.ID, "runtime-cleanup", "", req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	defer s.releaseOrchestrationLock(lock)

	var installRoot string
	var existingContainers []string
	if err := step(1, func() error {
		metadata := metadataFromInstance(current)
		if err := ensureServiceControllerMetadata(metadata); err != nil {
			return err
		}
		installRoot = stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
		if strings.TrimSpace(installRoot) == "" {
			return errors.New("AIFAR install root is missing")
		}
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(2, func() error {
		result, runErr := installerkit.Run(ctx, s.remote, req.Server, runtimePodContainerNamesCommand(installRoot), logForServer, "AIFAR runtime stale Pod scan failed")
		if runErr != nil {
			return runErr
		}
		existingContainers = parseRuntimePodContainerNames(result.Stdout)
		logForServer.Info("AIFAR runtime scan found %d existing Pod containers", len(existingContainers))
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(3, func() error {
		cleanupStore, ok := s.store.(aifarRuntimeCleanupStore)
		if !ok {
			return errors.New("AIFAR runtime cleanup store is not available")
		}
		pods, err := cleanupStore.PruneAIFARPodRecords(current.ID, existingContainers)
		if err != nil {
			return err
		}
		endpoints, err := cleanupStore.PruneAIFARServiceEndpointRecords(current.ID, existingContainers)
		if err != nil {
			return err
		}
		saved, err := s.store.GetAppInstance(current.ID)
		if err != nil {
			return err
		}
		metadata := metadataFromInstance(saved)
		metadata["lastRuntimeCleanup"] = map[string]any{
			"kind":             "stale-pods",
			"podRecords":       pods,
			"endpointRecords":  endpoints,
			"existingPods":     len(existingContainers),
			"cleanedAt":        time.Now().UTC().Format(time.RFC3339),
			"reason":           strings.TrimSpace(req.Reason),
			"cleanedBy":        strings.TrimSpace(req.Actor),
			"installRoot":      installRoot,
			"orchestrationKey": "runtime-cleanup",
		}
		delete(metadata, "orchestrationLock")
		if err := saveMetadata(s.store, saved, metadata); err != nil {
			return err
		}
		logForServer.Info("AIFAR runtime stale cleanup removed %d Pod records and %d endpoint records", pods, endpoints)
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) UninstallRuntimeAgent(ctx context.Context, req RuntimeAgentUninstallRequest, log Logger, targetLog targetLogger) error {
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(
		logForServer,
		recorder,
		target,
		runtimeAgentUninstallSteps(),
		"AIFAR agent uninstall step %d/%d started: %s",
		"AIFAR agent uninstall step %d/%d completed: %s",
		"AIFAR agent uninstall step %d/%d failed: %s: %v",
	)

	current, lock, err := s.acquireOrchestrationLock(req.Instance.ID, "runtime-agent-uninstall", "", req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	defer s.releaseOrchestrationLock(lock)

	var installRoot string
	var specPath string
	if err := step(1, func() error {
		metadata := metadataFromInstance(current)
		if err := ensureServiceControllerMetadata(metadata); err != nil {
			return err
		}
		installRoot = stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
		if strings.TrimSpace(installRoot) == "" {
			return errors.New("AIFAR install root is missing")
		}
		specPath = stringFromMetadata(metadata, "runtimeSpecPath", runtimeSpecPath(installRoot))
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(2, func() error {
		_, runErr := installerkit.Run(ctx, s.remote, req.Server, runtimeAgentUninstallCommand(installRoot, specPath), logForServer, "AIFAR agent uninstall failed")
		return runErr
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(3, func() error {
		saved, err := s.store.GetAppInstance(current.ID)
		if err != nil {
			return err
		}
		metadata := metadataFromInstance(saved)
		metadata["agentUninstalledAt"] = time.Now().UTC().Format(time.RFC3339)
		metadata["agentUninstalledBy"] = strings.TrimSpace(req.Actor)
		metadata["agentUninstallReason"] = strings.TrimSpace(req.Reason)
		delete(metadata, "orchestrationLock")
		return saveMetadata(s.store, saved, metadata)
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	finishTarget(recorder, target, "success", "")
	return nil
}

func runtimeCleanupSteps() []installStepDef {
	return []installStepDef{
		{Name: "validate-runtime-cleanup", Title: "validate AIFAR runtime cleanup"},
		{Name: "scan-pod-containers", Title: "scan existing AIFAR Pod containers"},
		{Name: "prune-control-plane", Title: "prune stale AIFAR Pod control-plane records"},
	}
}

func runtimeAgentUninstallSteps() []installStepDef {
	return []installStepDef{
		{Name: "validate-agent-uninstall", Title: "validate AIFAR agent uninstall"},
		{Name: "remove-agent-runtime", Title: "deregister Nacos proxies and remove aifar-agent"},
		{Name: "record-agent-uninstall", Title: "record AIFAR agent uninstall"},
	}
}

func runtimePodContainerNamesCommand(installRoot string) string {
	return "sh -s <<'AIFAR_RUNTIME_POD_SCAN'\n" + `#!/usr/bin/env sh
set -u
INSTALL_ROOT=` + installerkit.ShellQuote(installRoot) + `
command -v docker >/dev/null 2>&1 || { echo "docker command is required" >&2; exit 1; }
docker ps -a \
  --filter "label=aifar.app=aifar" \
  --filter "label=aifar.install-root=$INSTALL_ROOT" \
  --filter "label=aifar.component=pod" \
  --format '{{.Names}}'
` + "\nAIFAR_RUNTIME_POD_SCAN"
}

func parseRuntimePodContainerNames(output string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, line := range strings.Split(output, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func runtimeAgentUninstallCommand(installRoot, specPath string) string {
	return "sh -s <<'AIFAR_AGENT_UNINSTALL'\n" + `#!/usr/bin/env sh
set -eu
INSTALL_ROOT=` + installerkit.ShellQuote(installRoot) + `
SPEC_PATH=` + installerkit.ShellQuote(specPath) + `
INSTANCE_ID=admin
INSTANCE_STATE="/var/lib/aifar-agent/instances/$INSTANCE_ID"

if command -v aifar-agent >/dev/null 2>&1; then
  if [ -f "$SPEC_PATH" ]; then
    aifar-agent deregister-nacos --spec "$SPEC_PATH" >/dev/null 2>&1 || true
  fi
  aifar-agent deregister-nacos --state-dir /var/lib/aifar-agent/instances >/dev/null 2>&1 || true
  aifar-agent remove-instance --instance "$INSTANCE_ID" >/dev/null 2>&1
elif [ -e "$INSTANCE_STATE" ]; then
  echo "aifar-agent is required to retire existing Runtime state" >&2
  exit 1
fi
[ ! -e "$INSTANCE_STATE" ] || { echo "AIFAR Runtime state retirement was not durable" >&2; exit 1; }

if command -v systemctl >/dev/null 2>&1; then
  systemctl stop aifar-agent >/dev/null 2>&1 || true
  systemctl disable aifar-agent >/dev/null 2>&1 || true
fi

rm -f /etc/systemd/system/aifar-agent.service
rm -f /usr/local/bin/aifar-agent

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
echo "aifar-agent uninstalled for $INSTALL_ROOT"
` + "\nAIFAR_AGENT_UNINSTALL"
}
