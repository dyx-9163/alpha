package aifar

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

const defaultRuntimeMutationConcurrency = 4

type runtimeReconcileScriptData struct {
	InstanceID  string
	ServiceName string
}

type deploymentMutationPlan struct {
	ServiceName      string
	Operation        string
	LockAlreadyHeld  bool
	LockID           string
	ExpectedRevision string
	Validate         func(store.AppInstance, store.AIFARDeployment) error
	Prepare          func(context.Context, store.AppInstance, store.AIFARDeployment, Logger) error
	Mutate           func(*runtimeagent.DeploymentManifest) error
	Project          func(context.Context, store.AIFAROrchestrationLock, store.AIFARDeployment) error
}

var errServiceActionSkipped = errors.New("service action skipped")

type serviceActionSkippedError struct{ message string }

func (e serviceActionSkippedError) Error() string { return e.message }
func (serviceActionSkippedError) Unwrap() error   { return errServiceActionSkipped }

type serviceActionFailure struct {
	service string
	err     error
}

type serviceActionAggregateError struct {
	message string
	errors  []error
}

func (e serviceActionAggregateError) Error() string   { return e.message }
func (e serviceActionAggregateError) Unwrap() []error { return e.errors }

func (s Service) ReconcileRuntime(ctx context.Context, req RuntimeReconcileRequest, log Logger, targetLog targetLogger) error {
	control, ok := s.store.(aifarDeploymentControlStore)
	if !ok {
		return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	deployments, err := control.ListAIFARDeployments(req.Instance.ID)
	if err != nil {
		return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_READ_FAILED", err)
	}
	services := make([]string, 0, len(deployments))
	selected := cleanAIFARServiceName(req.ServiceName)
	for _, deployment := range deployments {
		if selected == "" || deployment.ServiceName == selected {
			services = append(services, deployment.ServiceName)
		}
	}
	sort.Strings(services)
	if len(services) == 0 {
		return errors.New(i18n.Text(req.Language, "aifar.runtimeMutation.noTargets"))
	}
	if selected != "" && (len(services) != 1 || services[0] != selected) {
		return errors.New(i18n.Text(req.Language, "aifar.runtimeMutation.noTargets"))
	}
	recorder, _ := log.(stepRecorder)
	failures := runServiceFanOut(ctx, req.Instance.ID, services, defaultRuntimeMutationConcurrency, req.Language, log, targetLog, recorder, func(actionCtx context.Context, serviceName string, serviceLog Logger) error {
		return s.withServiceOrchestrationLock(actionCtx, req.Instance.ID, "runtime-reconcile", serviceName, req.Actor, fallbackTaskID(req.TaskID, log), func(lockedCtx context.Context, _ store.AppInstance, _ store.AIFAROrchestrationLock) error {
			if _, err := loadDeploymentForMutation(control, req.Instance.ID, serviceName); err != nil {
				return err
			}
			script, renderErr := renderRuntimeReconcileScript(runtimeReconcileScriptData{InstanceID: req.Instance.ID, ServiceName: serviceName})
			if renderErr != nil {
				return renderErr
			}
			serviceLog.Info("%s", i18n.Text(req.Language, "aifar.runtimeReconcile.started", serviceName))
			_, runErr := installerkit.Run(lockedCtx, s.remote, req.Server, "sh -s <<'AIFAR_RUNTIME_RECONCILE'\n"+script+"\nAIFAR_RUNTIME_RECONCILE", serviceLog, i18n.Text(req.Language, "aifar.runtimeReconcile.failed", serviceName))
			return runErr
		})
	})
	return aggregateServiceActionFailures(req.Language, failures)
}

func (s Service) mutateDeploymentsFanOut(ctx context.Context, instance store.AppInstance, server store.Server, actor, taskID, language string, concurrency int, plans []deploymentMutationPlan, log Logger, targetLog targetLogger) (map[string]store.AIFARDeployment, error) {
	control, ok := s.store.(aifarDeploymentControlStore)
	if !ok {
		return nil, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	current, err := control.ListAIFARDeployments(instance.ID)
	if err != nil {
		return nil, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_READ_FAILED", err)
	}
	byService := make(map[string]store.AIFARDeployment, len(current))
	for _, deployment := range current {
		byService[deployment.ServiceName] = deployment
	}
	planByService := make(map[string]deploymentMutationPlan, len(plans))
	services := make([]string, 0, len(plans))
	for _, plan := range plans {
		name := cleanAIFARServiceName(plan.ServiceName)
		if !aifarServiceSupported(name) {
			return nil, errors.New(i18n.Text(language, "aifar.runtimeMutation.invalidTarget", plan.ServiceName))
		}
		if _, exists := planByService[name]; exists {
			return nil, errors.New(i18n.Text(language, "aifar.runtimeMutation.invalidTarget", name))
		}
		if _, exists := byService[name]; !exists {
			return nil, errors.New(i18n.Text(language, "aifar.runtimeMutation.noTargets"))
		}
		plan.ServiceName = name
		planByService[name] = plan
		services = append(services, name)
	}
	sort.Strings(services)
	if len(services) == 0 {
		return map[string]store.AIFARDeployment{}, nil
	}

	recorder, _ := log.(stepRecorder)
	accepted := make(map[string]store.AIFARDeployment, len(services))
	var acceptedMu sync.Mutex
	failures := runServiceFanOut(ctx, instance.ID, services, concurrency, language, log, targetLog, recorder, func(actionCtx context.Context, serviceName string, serviceLog Logger) error {
		plan := planByService[serviceName]
		mutate := func(lockedCtx context.Context, freshInstance store.AppInstance, lock store.AIFAROrchestrationLock) error {
			deployment, loadErr := loadDeploymentForMutation(control, freshInstance.ID, serviceName)
			if loadErr != nil {
				return loadErr
			}
			if strings.TrimSpace(plan.ExpectedRevision) != "" && deployment.CurrentRevision != plan.ExpectedRevision {
				return deploymentError(deploymentGenerationConflictCode, deploymentGenerationConflictCode, "aifar.deploymentControl.generationConflict")
			}
			if plan.Validate != nil {
				if validateErr := plan.Validate(freshInstance, deployment); validateErr != nil {
					return validateErr
				}
			}
			if plan.Prepare != nil {
				if prepareErr := plan.Prepare(lockedCtx, freshInstance, deployment, serviceLog); prepareErr != nil {
					return prepareErr
				}
				if err := lockedCtx.Err(); err != nil {
					return err
				}
			}
			result, mutationErr := s.MutateDeployment(lockedCtx, DeploymentMutationRequest{
				Instance: freshInstance, Server: server, ServiceName: serviceName,
				ExpectedGeneration: deployment.Generation,
				Actor:              actor, TaskID: taskID, Operation: plan.Operation, LockID: lock.ID, Mutate: plan.Mutate,
			}, serviceLog)
			if mutationErr != nil {
				return mutationErr
			}
			if err := s.ensureAIFAROrchestrationLockOwnership(lockedCtx, lock); err != nil {
				return err
			}
			if err := ensureAcceptedDeploymentIsCurrent(control, result); err != nil {
				return err
			}
			if plan.Project != nil {
				if projectErr := plan.Project(lockedCtx, lock, result); projectErr != nil {
					return projectErr
				}
			}
			acceptedMu.Lock()
			accepted[serviceName] = result
			acceptedMu.Unlock()
			return nil
		}
		var mutationErr error
		if plan.LockAlreadyHeld {
			freshInstance, getErr := s.store.GetAppInstance(instance.ID)
			if getErr != nil {
				return getErr
			}
			mutationErr = mutate(actionCtx, freshInstance, store.AIFAROrchestrationLock{
				ID: plan.LockID, InstanceID: instance.ID, ServiceName: serviceName, Operation: plan.Operation,
			})
		} else {
			mutationErr = s.withServiceOrchestrationLock(actionCtx, instance.ID, plan.Operation, serviceName, actor, taskID, mutate)
		}
		return mutationErr
	})
	return accepted, aggregateServiceActionFailures(language, failures)
}

func (s Service) withServiceOrchestrationLock(ctx context.Context, instanceID, operation, serviceName, actor, taskID string, action func(context.Context, store.AppInstance, store.AIFAROrchestrationLock) error) error {
	_, lock, err := s.acquireOrchestrationLock(instanceID, operation, serviceName, actor, taskID)
	if err != nil {
		return err
	}
	defer s.releaseOrchestrationLock(lock)
	lockedCtx, stopHeartbeat := s.startAIFAROrchestrationLockHeartbeat(ctx, lock)
	defer stopHeartbeat()
	freshInstance, err := s.store.GetAppInstance(instanceID)
	if err != nil {
		return err
	}
	return action(lockedCtx, freshInstance, lock)
}

func (s Service) ensureAIFAROrchestrationLockOwnership(ctx context.Context, lock store.AIFAROrchestrationLock) error {
	if err := ctx.Err(); err != nil {
		return repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
	}
	if strings.TrimSpace(lock.ID) == "" {
		return nil
	}
	lockStore, ok := s.store.(aifarOrchestrationLockStore)
	if !ok {
		return repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_FENCE_UNAVAILABLE", nil)
	}
	renewed, err := lockStore.RenewAIFAROrchestrationLock(lock.ID, time.Now().UTC().Add(orchestrationLockTTL))
	if err != nil || !renewed {
		return repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
	}
	return nil
}

func ensureAcceptedDeploymentIsCurrent(control aifarDeploymentControlStore, accepted store.AIFARDeployment) error {
	current, err := loadDeploymentForMutation(control, accepted.InstanceID, accepted.ServiceName)
	if err != nil {
		return err
	}
	acceptedCurrent := strings.EqualFold(current.Status, "Accepted") || current.ObservedGeneration >= current.Generation && runtimeObservedDeploymentStatus(current.Status)
	if current.Generation != accepted.Generation || current.CurrentRevision != accepted.CurrentRevision || current.SpecJSON != accepted.SpecJSON || !acceptedCurrent {
		return repairRequired("AIFAR_RUNTIME_ACCEPTED_PROJECTION_OBSOLETE", store.ErrAIFARDeploymentGenerationConflict)
	}
	return nil
}

func (s Service) updateAcceptedDeploymentMetadata(ctx context.Context, lock store.AIFAROrchestrationLock, accepted store.AIFARDeployment, repairReason string, mutate func(map[string]any) error) (store.AppInstance, error) {
	control, ok := s.store.(aifarDeploymentControlStore)
	if !ok {
		return store.AppInstance{}, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	return s.updateAppInstanceMetadata(accepted.InstanceID, repairReason, func(metadata map[string]any) error {
		if err := s.ensureAIFAROrchestrationLockOwnership(ctx, lock); err != nil {
			return err
		}
		if err := ensureAcceptedDeploymentIsCurrent(control, accepted); err != nil {
			return err
		}
		return mutate(metadata)
	})
}

func (s Service) updateAppInstanceMetadataWithLock(ctx context.Context, lock store.AIFAROrchestrationLock, instanceID, repairReason string, mutate func(map[string]any) error) (store.AppInstance, error) {
	return s.updateAppInstanceMetadata(instanceID, repairReason, func(metadata map[string]any) error {
		if err := s.ensureAIFAROrchestrationLockOwnership(ctx, lock); err != nil {
			return err
		}
		return mutate(metadata)
	})
}

func runtimeObservedDeploymentStatus(status string) bool {
	switch status {
	case "Progressing", "Available", "Degraded", "Offline":
		return true
	default:
		return false
	}
}

func runServiceFanOut(ctx context.Context, instanceID string, services []string, concurrency int, language string, log Logger, targetLog targetLogger, recorder stepRecorder, action func(context.Context, string, Logger) error) []serviceActionFailure {
	if len(services) == 0 {
		return nil
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(services) {
		concurrency = len(services)
	}
	jobs := make(chan string)
	failures := make(chan serviceActionFailure, len(services))
	var wg sync.WaitGroup
	for workerIndex := 0; workerIndex < concurrency; workerIndex++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for serviceName := range jobs {
				target := instanceID + ":" + serviceName
				serviceLog := logForTarget(log, targetLog, target)
				if recorder != nil {
					recorder.StartTarget(target)
					recorder.StartStep(target, "accept-service-intent", i18n.Text(language, "aifar.runtimeMutation.stepAccept", serviceName), 1)
				}
				err := callServiceActionSafely(ctx, serviceName, serviceLog, language, action)
				status := "success"
				errText := ""
				if errors.Is(err, errServiceActionSkipped) {
					serviceLog.Info("%s", err.Error())
				} else if err != nil {
					status = "failed"
					errText = err.Error()
					if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
						status = "cancelled"
					}
					failures <- serviceActionFailure{service: serviceName, err: err}
				}
				if recorder != nil {
					recorder.FinishStep(target, "accept-service-intent", status, errText)
					recorder.FinishTarget(target, status, errText)
				}
			}
		}()
	}
	for _, serviceName := range services {
		jobs <- serviceName
	}
	close(jobs)
	wg.Wait()
	close(failures)
	out := make([]serviceActionFailure, 0, len(failures))
	for failure := range failures {
		out = append(out, failure)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].service < out[j].service })
	return out
}

func callServiceActionSafely(ctx context.Context, serviceName string, serviceLog Logger, language string, action func(context.Context, string, Logger) error) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New(i18n.Text(language, "aifar.runtimeMutation.panicked"))
		}
	}()
	return action(ctx, serviceName, serviceLog)
}

func aggregateServiceActionFailures(language string, failures []serviceActionFailure) error {
	if len(failures) == 0 {
		return nil
	}
	services := make([]string, 0, len(failures))
	for _, failure := range failures {
		services = append(services, failure.service)
	}
	causes := make([]error, 0, len(failures))
	for _, failure := range failures {
		causes = append(causes, failure.err)
	}
	return serviceActionAggregateError{
		message: fmt.Sprintf("%s: %s", i18n.Text(language, "aifar.runtimeMutation.batchFailed"), strings.Join(services, ",")),
		errors:  causes,
	}
}

func renderRuntimeReconcileScript(data runtimeReconcileScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/runtime-reconcile.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "runtime-reconcile.sh", "aifar-runtime-reconcile", string(content), selinux.AddTemplateFuncs(template.FuncMap{
		"quote": shellQuoteAny,
	}), data)
}
