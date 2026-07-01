package aifar

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
	"aifar-deployment/backend/internal/installer/uploadkit"
	"aifar-deployment/backend/internal/store"
)

//go:embed templates/install.sh templates/uninstall.sh
var templateFS embed.FS

type Logger = installerkit.Logger
type Remote = installerkit.Remote

type Store interface {
	GetServer(id string, includeSecret bool) (store.Server, error)
	ListAppInstances() ([]store.AppInstance, error)
	SaveAppInstance(v store.AppInstance) (store.AppInstance, error)
	DeleteAppInstance(id string) error
}

type InstallRequest struct {
	Version    string
	Topology   string
	Language   string
	ServerID   string
	Parameters map[string]any
}

type DeleteRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
}

type CheckRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
}

type CheckResult struct {
	Status  string
	Message string
	Details map[string]any
}

type Service struct {
	store  Store
	remote Remote
}

type installStepDef struct {
	Name  string
	Title string
}

type targetLogger func(target string) Logger

type stepRecorder interface {
	StartTarget(target string)
	FinishTarget(target, status, errText string)
	StartStep(target, name, title string, order int)
	FinishStep(target, name, status, errText string)
}

func NewService(s Store, remote Remote) Service {
	return Service{store: s, remote: remote}
}

func (s Service) Install(ctx context.Context, req InstallRequest, resources []store.Resource, log Logger, targetLog targetLogger) error {
	copy := copyFor(req.Language)
	target := strings.TrimSpace(req.ServerID)
	if target == "" {
		return errors.New(copy.TargetRequired)
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, installSteps(copy), copy.StepStart, copy.StepDone, copy.StepFailed)

	var server store.Server
	if err := step(1, func() error {
		var loadErr error
		server, loadErr = s.store.GetServer(target, true)
		return loadErr
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	var bundle Bundle
	var archiveLocal string
	options := optionsFromParameters(req.Parameters)
	if err := step(2, func() error {
		var resolveErr error
		options, resolveErr = s.resolveInstallOptions(options)
		if resolveErr != nil {
			return resolveErr
		}
		var bundleErr error
		bundle, bundleErr = SelectBundle(resources, req.Version)
		if bundleErr != nil {
			return bundleErr
		}
		if err := VerifyBundle(bundle); err != nil {
			return err
		}
		if err := options.Validate(); err != nil {
			return err
		}
		if options.InitSQL {
			if _, err := os.Stat(filepath.Join(bundle.SQLDir, "aifar_cloud_nacos.sql")); err != nil {
				return fmt.Errorf("SQL initialization file is required: %w", err)
			}
		}
		var archiveErr error
		archiveLocal, archiveErr = CreateBundleArchive(bundle)
		return archiveErr
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	defer os.Remove(archiveLocal)

	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	workDir := installerkit.WorkDir(deployDir, AppName, bundle.Version, time.Now())
	installRoot := installRootFromDeployDir(deployDir)
	archiveRemote := workDir + "/" + filepath.Base(archiveLocal)
	scriptRemote := workDir + "/install-aifar.sh"

	if err := step(3, func() error {
		logForServer.Info(copy.PrepareWorkDir, workDir)
		if _, err := installerkit.Run(ctx, s.remote, server, "mkdir -p "+installerkit.ShellQuote(workDir), logForServer, copy.RemoteCommandFailed); err != nil {
			return err
		}
		if err := uploadkit.Upload(ctx, s.remote, server, uploadkit.File{
			LocalPath:      archiveLocal,
			RemotePath:     archiveRemote,
			LogMessage:     copy.UploadBundle,
			LogArgs:        []any{bundle.Root},
			FailureMessage: copy.UploadBundleFailed,
		}, logForServer); err != nil {
			return err
		}
		script, err := renderInstallScript(installScriptData{
			InstallRoot:   installRoot,
			WorkDir:       workDir,
			ArchiveRemote: archiveRemote,
			ServiceOrder:  serviceOrderText(),
			Options:       options,
		})
		if err != nil {
			return err
		}
		scriptLocal, err := installerkit.WriteTempScript("aifar-service-install-*.sh", script)
		if err != nil {
			return err
		}
		defer os.Remove(scriptLocal)
		return uploadkit.Upload(ctx, s.remote, server, uploadkit.File{
			LocalPath:      scriptLocal,
			RemotePath:     scriptRemote,
			Mode:           0o755,
			LogMessage:     copy.UploadScript,
			FailureMessage: copy.UploadScriptFailed,
		}, logForServer)
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(4, func() error {
		logForServer.Info(copy.Deploying)
		_, err := installerkit.Run(ctx, s.remote, server, "sh "+installerkit.ShellQuote(scriptRemote), logForServer, copy.RemoteCommandFailed)
		return err
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	var instance store.AppInstance
	if err := step(5, func() error {
		metadata, _ := json.Marshal(map[string]any{
			"installRoot":             installRoot,
			"appDir":                  installRoot + "/" + appBundleDir,
			"sqlDir":                  installRoot + "/" + sqlBundleDir,
			"networkName":             options.NetworkName,
			"endpoint":                fmt.Sprintf("%s:%d", server.Host, options.WebPort),
			"gatewayEndpoint":         fmt.Sprintf("%s:%d", server.Host, options.GatewayPort),
			"nacosEndpoint":           fmt.Sprintf("%s:%d", options.NacosHost, options.NacosWebPort),
			"webPort":                 options.WebPort,
			"gatewayPort":             options.GatewayPort,
			"nacosSource":             options.NacosSource,
			"nacosInstanceId":         options.NacosInstanceID,
			"nacosHost":               options.NacosHost,
			"nacosPort":               options.NacosWebPort,
			"nacosWebPort":            options.NacosWebPort,
			"nacosApiPort":            options.NacosAPIPort,
			"dbSource":                options.DBSource,
			"dbInstanceId":            options.DBInstanceID,
			"dbHost":                  options.DBHost,
			"dbPort":                  options.DBPort,
			"dbNameNacos":             options.DBNameNacos,
			"dbUser":                  options.DBUser,
			"redisSource":             options.RedisSource,
			"redisInstanceId":         options.RedisInstanceID,
			"redisMode":               options.RedisMode,
			"redisHost":               options.RedisHost,
			"redisPort":               options.RedisPort,
			"redisDatabase":           options.RedisDatabase,
			"redisSentinelMasterName": options.RedisSentinelMasterName,
			"redisSentinelNodes":      options.RedisSentinelNodes,
			"redisClusterNodes":       options.RedisClusterNodes,
			"initSql":                 options.InitSQL,
			"services":                serviceOrder,
		})
		var saveErr error
		instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
			App:      AppName,
			Version:  bundle.Version,
			ServerID: target,
			Status:   "installed",
			Topology: defaultTopology,
			Metadata: string(metadata),
		})
		return saveErr
	}); err != nil {
		msg := fmt.Sprintf(copy.RecordFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	logForServer.Info(copy.Installed, instance.ID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) Delete(ctx context.Context, req DeleteRequest, log Logger, targetLog targetLogger) error {
	copy := deleteCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, deleteSteps(copy), copy.StepStart, copy.StepDone, copy.StepFailed)
	metadata := metadataFromInstance(req.Instance)
	networkName := stringFromMetadata(metadata, "networkName", defaultNetworkName)
	installRoot := stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))

	if err := step(1, func() error {
		script, err := renderUninstallScript(uninstallScriptData{
			InstallRoot:  installRoot,
			NetworkName:  networkName,
			ServiceOrder: reverseServiceOrderText(),
		})
		if err != nil {
			return err
		}
		_, err = installerkit.Run(ctx, s.remote, req.Server, "sh -s <<'AIFAR_SERVICE_UNINSTALL'\n"+script+"\nAIFAR_SERVICE_UNINSTALL", logForServer, copy.RemoteCommandFailed)
		return err
	}); err != nil {
		msg := fmt.Sprintf(copy.DeleteFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(2, func() error {
		status, err := NewInspector(s.remote).Check(ctx, req.Server, installRoot, logForServer)
		if err != nil {
			return err
		}
		if status.InstallRootExists {
			return fmt.Errorf("AIFAR service install root still exists: %s", installRoot)
		}
		return nil
	}); err != nil {
		msg := fmt.Sprintf(copy.DeleteFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(3, func() error {
		return s.store.DeleteAppInstance(req.Instance.ID)
	}); err != nil {
		msg := fmt.Sprintf(copy.DeleteFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	logForServer.Info(copy.Deleted, req.Instance.ID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) Check(ctx context.Context, req CheckRequest, log Logger, targetLog targetLogger) (CheckResult, error) {
	copy := checkCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, checkSteps(copy), copy.StepStart, copy.StepDone, copy.StepFailed)
	metadata := metadataFromInstance(req.Instance)
	installRoot := stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
	var status StatusResult
	if err := step(1, func() error {
		var checkErr error
		status, checkErr = NewInspector(s.remote).Check(ctx, req.Server, installRoot, logForServer)
		return checkErr
	}); err != nil {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		_ = s.markInstanceStatus(req.Instance, "error", map[string]any{"message": msg})
		return CheckResult{Status: "error", Message: msg}, err
	}
	details := map[string]any{
		"message":             status.Message,
		"installRoot":         status.InstallRoot,
		"installRootExists":   status.InstallRootExists,
		"totalContainers":     status.TotalContainers,
		"runningContainers":   status.RunningContainers,
		"unhealthyContainers": status.UnhealthyContainers,
		"containers":          status.Containers,
	}
	if err := step(2, func() error {
		return s.markInstanceStatus(req.Instance, status.Status, details)
	}); err != nil {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return CheckResult{Status: "error", Message: msg}, err
	}
	logForServer.Info(copy.Checked, status.Status)
	finishTarget(recorder, target, "success", "")
	return CheckResult{Status: status.Status, Message: status.Message, Details: details}, nil
}

func (s Service) markInstanceStatus(instance store.AppInstance, status string, details map[string]any) error {
	metadata := metadataFromInstance(instance)
	metadata["lastCheck"] = details
	next, _ := json.Marshal(metadata)
	instance.Status = status
	instance.Metadata = string(next)
	_, err := s.store.SaveAppInstance(instance)
	return err
}

type installScriptData struct {
	InstallRoot   string
	WorkDir       string
	ArchiveRemote string
	ServiceOrder  string
	Options       InstallOptions
}

type uninstallScriptData struct {
	InstallRoot  string
	NetworkName  string
	ServiceOrder string
}

func renderInstallScript(data installScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/install.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "install.sh", "aifar-install", string(content), selinux.AddTemplateFuncs(template.FuncMap{
		"quote": shellQuoteAny,
	}), data)
}

func renderUninstallScript(data uninstallScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/uninstall.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "uninstall.sh", "aifar-uninstall", string(content), template.FuncMap{
		"quote": shellQuoteAny,
	}, data)
}

func shellQuoteAny(value any) string {
	return installerkit.ShellQuote(fmt.Sprint(value))
}

func installRootFromDeployDir(deployDir string) string {
	return installerkit.InstallRoot(installerkit.RemoteDeployDir(deployDir), installDirName)
}

func logForTarget(fallback Logger, targetLog targetLogger, target string) Logger {
	if targetLog == nil {
		return fallback
	}
	if log := targetLog(target); log != nil {
		return log
	}
	return fallback
}

func newStepRunner(log Logger, recorder stepRecorder, target string, steps []installStepDef, start, done, failed string) func(stepIndex int, fn func() error) error {
	return func(stepIndex int, fn func() error) error {
		stepName := fmt.Sprintf("step-%d", stepIndex)
		label := stepName
		if stepIndex > 0 && stepIndex <= len(steps) {
			stepName = steps[stepIndex-1].Name
			label = steps[stepIndex-1].Title
		}
		stepTotal := len(steps)
		if recorder != nil {
			recorder.StartStep(target, stepName, label, stepIndex)
		}
		log.Info(start, stepIndex, stepTotal, label)
		if err := fn(); err != nil {
			log.Error(failed, stepIndex, stepTotal, label, err)
			if recorder != nil {
				recorder.FinishStep(target, stepName, "failed", err.Error())
			}
			return err
		}
		log.Info(done, stepIndex, stepTotal, label)
		if recorder != nil {
			recorder.FinishStep(target, stepName, "success", "")
		}
		return nil
	}
}

func finishTarget(recorder stepRecorder, target, status, errText string) {
	if recorder != nil {
		recorder.FinishTarget(target, status, errText)
	}
}

func metadataFromInstance(instance store.AppInstance) map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return metadata
}

func stringFromMetadata(metadata map[string]any, key, fallback string) string {
	if value, ok := metadata[key]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			return text
		}
	}
	return fallback
}

func installSteps(copy Copy) []installStepDef {
	return []installStepDef{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "verify-resource", Title: copy.VerifyResource},
		{Name: "upload-bundle", Title: copy.UploadBundleStep},
		{Name: "deploy-compose", Title: copy.DeployCompose},
		{Name: "record-instance", Title: copy.RecordInstance},
	}
}

func deleteSteps(copy DeleteCopy) []installStepDef {
	return []installStepDef{
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "verify-removed", Title: copy.VerifyRemoved},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func checkSteps(copy CheckCopy) []installStepDef {
	return []installStepDef{
		{Name: "check-runtime", Title: copy.CheckRuntime},
		{Name: "update-instance", Title: copy.UpdateInstance},
	}
}
