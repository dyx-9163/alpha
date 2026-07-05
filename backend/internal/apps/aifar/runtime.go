package aifar

import (
	"context"
	"errors"
	"strings"

	"aifar-deployment/backend/internal/installer/installerkit"
)

func (s Service) ReconcileRuntime(ctx context.Context, req RuntimeReconcileRequest, log Logger, targetLog targetLogger) error {
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	current, err := s.acquireOrchestrationLock(req.Instance.ID, "runtime-reconcile", "", req.Actor)
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	defer s.releaseOrchestrationLock(req.Instance.ID, "runtime-reconcile")

	metadata := metadataFromInstance(current)
	if err := ensureK8sLikeMetadata(metadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support runtime reconcile; reinstall with k8s-like orchestration first"}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	installRoot := stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
	if strings.TrimSpace(installRoot) == "" {
		err := errors.New("AIFAR install root is missing")
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	specPath := stringFromMetadata(metadata, "runtimeSpecPath", runtimeSpecPath(installRoot))
	command := "command -v aifar-agent >/dev/null 2>&1 || { echo 'aifar-agent is not installed' >&2; exit 1; }; " +
		"test -f " + installerkit.ShellQuote(specPath) + " || { echo 'AIFAR runtime spec is missing' >&2; exit 1; }; " +
		"aifar-agent reconcile-runtime --spec " + installerkit.ShellQuote(specPath)
	logForServer.Info("reconciling AIFAR runtime for instance %s", current.ID)
	if _, err := installerkit.Run(ctx, s.remote, req.Server, command, logForServer, "AIFAR runtime reconcile failed"); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	logForServer.Info("AIFAR runtime reconciled for instance %s", current.ID)
	finishTarget(recorder, target, "success", "")
	return nil
}
