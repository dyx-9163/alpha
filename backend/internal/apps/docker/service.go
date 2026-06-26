package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dockerinstaller "aifar-deployment/backend/internal/installer/docker"
	"aifar-deployment/backend/internal/store"
)

type Store interface {
	GetServer(id string, includeSecret bool) (store.Server, error)
	SaveServer(v store.Server) (store.Server, error)
	SaveAppInstance(v store.AppInstance) (store.AppInstance, error)
	DeleteAppInstance(id string) error
}

type InstallRequest struct {
	Version   string
	Topology  string
	Language  string
	ServerIDs []string
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
	remote dockerinstaller.Remote
}

type installStepDef struct {
	Name  string
	Title string
}

type targetLogger func(target string) dockerinstaller.Logger

type stepRecorder interface {
	StartTarget(target string)
	FinishTarget(target, status, errText string)
	StartStep(target, name, title string, order int)
	FinishStep(target, name, status, errText string)
}

func NewService(s Store, remote dockerinstaller.Remote) Service {
	return Service{store: s, remote: remote}
}

func (s Service) Install(ctx context.Context, req InstallRequest, resources []store.Resource, log dockerinstaller.Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	bundle, err := dockerinstaller.SelectBundleWithLanguage(resources, req.Version, req.Language)
	if err != nil {
		return err
	}
	if err := dockerinstaller.VerifyBundleWithLanguage(bundle, req.Language); err != nil {
		return err
	}
	log.Info(copy.UsingArchive, bundle.ArchivePath)
	log.Info(copy.UsingRPMs, len(bundle.RPMPaths))
	installer := dockerinstaller.NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	var failures []string
	for idx, serverID := range req.ServerIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if recorder != nil {
			recorder.StartTarget(serverID)
		}
		logForServer := logForTarget(log, targetLog, serverID)
		step := newStepRunner(logForServer, recorder, serverID, copy, idx+1, len(req.ServerIDs))
		var server store.Server
		if err := step(1, copy.LoadServer, func() error {
			var loadErr error
			server, loadErr = s.store.GetServer(serverID, true)
			return loadErr
		}); err != nil {
			msg := fmt.Sprintf("%s: %v", serverID, err)
			failures = append(failures, msg)
			logForServer.Error(copy.LoadFailed, idx+1, len(req.ServerIDs), msg)
			finishTarget(recorder, serverID, "failed", msg)
			continue
		}
		if err := step(2, copy.InstallEngine, func() error {
			return installer.InstallWithLanguage(ctx, server, bundle, logForServer, req.Language)
		}); err != nil {
			msg := fmt.Sprintf("%s: %v", server.Name, err)
			failures = append(failures, msg)
			logForServer.Error(copy.InstallFailed, idx+1, len(req.ServerIDs), msg)
			finishTarget(recorder, serverID, "failed", msg)
			continue
		}
		server.DockerHost = fmt.Sprintf("ssh://%s@%s", server.Username, server.Host)
		server.Status = "available"
		server.LastError = ""
		if err := step(3, copy.UpdateServer, func() error {
			_, err := s.store.SaveServer(server)
			return err
		}); err != nil {
			msg := fmt.Sprintf("%s: %v", server.Name, err)
			failures = append(failures, msg)
			logForServer.Error(copy.UpdateFailed, idx+1, len(req.ServerIDs), msg)
			finishTarget(recorder, serverID, "failed", msg)
			continue
		}
		metadata, _ := json.Marshal(map[string]any{
			"dockerHost":  server.DockerHost,
			"archivePath": bundle.ArchivePath,
			"rpmCount":    len(bundle.RPMPaths),
		})
		var instance store.AppInstance
		if err := step(4, copy.RecordInstance, func() error {
			var saveErr error
			instance, saveErr = s.store.SaveAppInstance(store.AppInstance{App: "docker", Version: bundle.Version, ServerID: serverID, Status: "installed", Topology: req.Topology, Metadata: string(metadata)})
			return saveErr
		}); err != nil {
			msg := fmt.Sprintf("%s: %v", server.Name, err)
			failures = append(failures, msg)
			logForServer.Error(copy.RecordFailed, idx+1, len(req.ServerIDs), msg)
			finishTarget(recorder, serverID, "failed", msg)
			continue
		}
		logForServer.Info(copy.Installed, idx+1, len(req.ServerIDs), instance.ID)
		finishTarget(recorder, serverID, "success", "")
	}
	if len(failures) > 0 {
		return fmt.Errorf(copy.BatchFailed, len(failures), strings.Join(failures, "; "))
	}
	return nil
}

func (s Service) Delete(ctx context.Context, req DeleteRequest, log dockerinstaller.Logger, targetLog targetLogger) error {
	copy := DeleteCopyFor(req.Language)
	server := req.Server
	target := req.Instance.ServerID
	if target == "" {
		target = server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newDeleteStepRunner(logForServer, recorder, target, copy)
	uninstaller := dockerinstaller.NewUninstaller(s.remote)
	if err := step(1, "remove-remote", copy.RemoveRemote, func() error {
		return uninstaller.UninstallWithLanguage(ctx, server, req.Instance.Version, logForServer, req.Language)
	}); err != nil {
		msg := fmt.Sprintf("%s: %v", server.Name, err)
		logForServer.Error(copy.DeleteFailed, msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	if err := step(2, "verify-removed", copy.VerifyRemoved, func() error {
		inspector := dockerinstaller.NewInspector(s.remote)
		status, checkErr := inspector.Check(ctx, server, req.Instance.Version, logForServer)
		if checkErr != nil {
			return checkErr
		}
		if status.Status != "missing" {
			return fmt.Errorf("docker removal verification failed: status=%s, installRootExists=%v, unitExists=%v", status.Status, status.InstallRootExists, status.UnitExists)
		}
		return nil
	}); err != nil {
		msg := fmt.Sprintf("%s: %v", server.Name, err)
		logForServer.Error(copy.DeleteFailed, msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	server.DockerHost = ""
	if err := step(3, "update-server", copy.UpdateServer, func() error {
		_, err := s.store.SaveServer(server)
		return err
	}); err != nil {
		msg := fmt.Sprintf("%s: %v", server.Name, err)
		logForServer.Error(copy.DeleteFailed, msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	if err := step(4, "delete-instance", copy.DeleteInstance, func() error {
		return s.store.DeleteAppInstance(req.Instance.ID)
	}); err != nil {
		msg := fmt.Sprintf("%s: %v", server.Name, err)
		logForServer.Error(copy.DeleteFailed, msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	logForServer.Info(copy.Deleted, req.Instance.ID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) Check(ctx context.Context, req CheckRequest, log dockerinstaller.Logger, targetLog targetLogger) (CheckResult, error) {
	copy := CheckCopyFor(req.Language)
	server := req.Server
	target := req.Instance.ServerID
	if target == "" {
		target = server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newCheckStepRunner(logForServer, recorder, target, copy)
	var status dockerinstaller.StatusResult
	if err := step(1, "check-runtime", copy.CheckRuntime, func() error {
		var checkErr error
		inspector := dockerinstaller.NewInspector(s.remote)
		status, checkErr = inspector.Check(ctx, server, req.Instance.Version, logForServer)
		return checkErr
	}); err != nil {
		msg := fmt.Sprintf("%s: %v", server.Name, err)
		logForServer.Error(copy.CheckFailed, msg)
		finishTarget(recorder, target, "failed", msg)
		_ = s.markInstanceStatus(req.Instance, "error", map[string]any{"message": msg})
		return CheckResult{Status: "error", Message: msg}, err
	}
	details := map[string]any{
		"message":           status.Message,
		"dockerVersion":     status.DockerVersion,
		"composeVersion":    status.ComposeVersion,
		"installRoot":       status.InstallRoot,
		"installRootExists": status.InstallRootExists,
		"unitExists":        status.UnitExists,
	}
	if err := step(2, "update-instance", copy.UpdateInstance, func() error {
		return s.markInstanceStatus(req.Instance, status.Status, details)
	}); err != nil {
		msg := fmt.Sprintf("%s: %v", server.Name, err)
		logForServer.Error(copy.CheckFailed, msg)
		finishTarget(recorder, target, "failed", msg)
		return CheckResult{Status: "error", Message: msg}, err
	}
	logForServer.Info(copy.Checked, status.Status)
	finishTarget(recorder, target, "success", "")
	return CheckResult{Status: status.Status, Message: status.Message, Details: details}, nil
}

func logForTarget(fallback dockerinstaller.Logger, targetLog targetLogger, target string) dockerinstaller.Logger {
	if targetLog == nil {
		return fallback
	}
	if log := targetLog(target); log != nil {
		return log
	}
	return fallback
}

func dockerInstallSteps(copy Copy) []installStepDef {
	return []installStepDef{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "install-engine", Title: copy.InstallEngine},
		{Name: "update-server", Title: copy.UpdateServer},
		{Name: "record-instance", Title: copy.RecordInstance},
	}
}

func dockerDeleteSteps(copy DeleteCopy) []installStepDef {
	return []installStepDef{
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "verify-removed", Title: copy.VerifyRemoved},
		{Name: "update-server", Title: copy.UpdateServer},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func dockerCheckSteps(copy CheckCopy) []installStepDef {
	return []installStepDef{
		{Name: "check-runtime", Title: copy.CheckRuntime},
		{Name: "update-instance", Title: copy.UpdateInstance},
	}
}

func newStepRunner(log dockerinstaller.Logger, recorder stepRecorder, target string, copy Copy, targetIndex, targetTotal int) func(stepIndex int, label string, fn func() error) error {
	steps := dockerInstallSteps(copy)
	return func(stepIndex int, label string, fn func() error) error {
		stepName := fmt.Sprintf("step-%d", stepIndex)
		if stepIndex > 0 && stepIndex <= len(steps) {
			stepName = steps[stepIndex-1].Name
		}
		stepTotal := len(steps)
		if recorder != nil {
			recorder.StartStep(target, stepName, label, stepIndex)
		}
		log.Info(copy.StepStart, targetIndex, targetTotal, stepIndex, stepTotal, label)
		if err := fn(); err != nil {
			log.Error(copy.StepFailed, targetIndex, targetTotal, stepIndex, stepTotal, label, err)
			if recorder != nil {
				recorder.FinishStep(target, stepName, "failed", err.Error())
			}
			return err
		}
		log.Info(copy.StepDone, targetIndex, targetTotal, stepIndex, stepTotal, label)
		if recorder != nil {
			recorder.FinishStep(target, stepName, "success", "")
		}
		return nil
	}
}

func newDeleteStepRunner(log dockerinstaller.Logger, recorder stepRecorder, target string, copy DeleteCopy) func(stepIndex int, stepName, label string, fn func() error) error {
	steps := dockerDeleteSteps(copy)
	return func(stepIndex int, stepName, label string, fn func() error) error {
		stepTotal := len(steps)
		if recorder != nil {
			recorder.StartStep(target, stepName, label, stepIndex)
		}
		log.Info(copy.StepStart, stepIndex, stepTotal, label)
		if err := fn(); err != nil {
			log.Error(copy.StepFailed, stepIndex, stepTotal, label, err)
			if recorder != nil {
				recorder.FinishStep(target, stepName, "failed", err.Error())
			}
			return err
		}
		log.Info(copy.StepDone, stepIndex, stepTotal, label)
		if recorder != nil {
			recorder.FinishStep(target, stepName, "success", "")
		}
		return nil
	}
}

func newCheckStepRunner(log dockerinstaller.Logger, recorder stepRecorder, target string, copy CheckCopy) func(stepIndex int, stepName, label string, fn func() error) error {
	steps := dockerCheckSteps(copy)
	return func(stepIndex int, stepName, label string, fn func() error) error {
		stepTotal := len(steps)
		if recorder != nil {
			recorder.StartStep(target, stepName, label, stepIndex)
		}
		log.Info(copy.StepStart, stepIndex, stepTotal, label)
		if err := fn(); err != nil {
			log.Error(copy.StepFailed, stepIndex, stepTotal, label, err)
			if recorder != nil {
				recorder.FinishStep(target, stepName, "failed", err.Error())
			}
			return err
		}
		log.Info(copy.StepDone, stepIndex, stepTotal, label)
		if recorder != nil {
			recorder.FinishStep(target, stepName, "success", "")
		}
		return nil
	}
}

func (s Service) markInstanceStatus(instance store.AppInstance, status string, details map[string]any) error {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	metadata["lastCheck"] = details
	next, _ := json.Marshal(metadata)
	instance.Status = status
	instance.Metadata = string(next)
	_, err := s.store.SaveAppInstance(instance)
	return err
}

func finishTarget(recorder stepRecorder, target, status, errText string) {
	if recorder == nil {
		return
	}
	recorder.FinishTarget(target, status, errText)
}
