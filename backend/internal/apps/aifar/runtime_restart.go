package aifar

import (
	"context"
	"errors"
	"strings"
	"text/template"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
)

type runtimeRestartScriptData struct {
	InstallRoot string
	SpecPath    string
}

func (s Service) RestartRuntime(ctx context.Context, req RuntimeRestartRequest, log Logger, targetLog targetLogger) error {
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	steps := []struct {
		name  string
		title string
	}{
		{"load-instance", i18n.Text(req.Language, "aifar.runtimeRestart.stepLoadInstance")},
		{"preflight-runtime", i18n.Text(req.Language, "aifar.runtimeRestart.stepPreflight")},
		{"rolling-restart", i18n.Text(req.Language, "aifar.runtimeRestart.stepRollingRestart")},
		{"verify-runtime", i18n.Text(req.Language, "aifar.runtimeRestart.stepVerify")},
	}
	activeStep := ""
	startStep := func(index int) {
		activeStep = steps[index].name
		if recorder != nil {
			recorder.StartStep(target, activeStep, steps[index].title, index+1)
		}
	}
	finishStep := func(status, errText string) {
		if recorder != nil && activeStep != "" {
			recorder.FinishStep(target, activeStep, status, errText)
		}
		activeStep = ""
	}
	fail := func(err error) error {
		status := "failed"
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status = "cancelled"
		}
		finishStep(status, err.Error())
		finishTarget(recorder, target, status, err.Error())
		return err
	}

	startStep(0)
	current, err := s.acquireOrchestrationLock(req.Instance.ID, "runtime-restart-all", "", req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		return fail(err)
	}
	defer s.releaseOrchestrationLock(req.Instance.ID, "runtime-restart-all", "")
	finishStep("success", "")

	startStep(1)
	metadata := metadataFromInstance(current)
	if err := ensureK8sLikeMetadata(metadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support runtime restart; reinstall with k8s-like orchestration first"}); err != nil {
		return fail(err)
	}
	installRoot := stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
	if strings.TrimSpace(installRoot) == "" {
		err := errors.New("AIFAR install root is missing")
		return fail(err)
	}
	if err := s.ensureRuntimeAgent(ctx, req.Server, "", req.Language, logForServer); err != nil {
		return fail(err)
	}
	specPath := stringFromMetadata(metadata, "runtimeSpecPath", runtimeSpecPath(installRoot))
	script, err := renderRuntimeRestartScript(runtimeRestartScriptData{InstallRoot: installRoot, SpecPath: specPath})
	if err != nil {
		return fail(err)
	}
	finishStep("success", "")

	startStep(2)
	logForServer.Info("restarting enabled AIFAR runtime services for instance %s", current.ID)
	if _, err := installerkit.Run(ctx, s.remote, req.Server, "sh -s <<'AIFAR_RUNTIME_RESTART'\n"+script+"\nAIFAR_RUNTIME_RESTART", logForServer, "AIFAR runtime restart failed"); err != nil {
		return fail(err)
	}
	finishStep("success", "")

	startStep(3)
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	finishStep("success", "")
	logForServer.Info("enabled AIFAR runtime services restarted for instance %s", current.ID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func renderRuntimeRestartScript(data runtimeRestartScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/runtime-restart.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "runtime-restart.sh", "aifar-runtime-restart", string(content), selinux.AddTemplateFuncs(template.FuncMap{
		"quote": shellQuoteAny,
	}), data)
}
