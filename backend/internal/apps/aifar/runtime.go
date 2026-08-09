package aifar

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"text/template"

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
	ServiceName string
	Operation   string
	Mutate      func(*runtimeagent.DeploymentManifest) error
}

type serviceActionFailure struct {
	service string
	err     error
}

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
	current, locks, err := s.acquireServiceOrchestrationLocks(req.Instance.ID, "runtime-reconcile", services, req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		return err
	}
	defer s.releaseOrchestrationLocks(locks)
	req.Instance = current

	recorder, _ := log.(stepRecorder)
	failures := runServiceFanOut(ctx, req.Instance.ID, services, defaultRuntimeMutationConcurrency, req.Language, log, targetLog, recorder, func(actionCtx context.Context, serviceName string, serviceLog Logger) error {
		script, renderErr := renderRuntimeReconcileScript(runtimeReconcileScriptData{InstanceID: req.Instance.ID, ServiceName: serviceName})
		if renderErr != nil {
			return renderErr
		}
		serviceLog.Info("%s", i18n.Text(req.Language, "aifar.runtimeReconcile.started", serviceName))
		_, runErr := installerkit.Run(actionCtx, s.remote, req.Server, "sh -s <<'AIFAR_RUNTIME_RECONCILE'\n"+script+"\nAIFAR_RUNTIME_RECONCILE", serviceLog, i18n.Text(req.Language, "aifar.runtimeReconcile.failed", serviceName))
		return runErr
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
		result, mutationErr := s.MutateDeployment(actionCtx, DeploymentMutationRequest{
			Instance: instance, Server: server, ServiceName: serviceName,
			ExpectedGeneration: byService[serviceName].Generation,
			Actor:              actor, TaskID: taskID, Operation: plan.Operation, Mutate: plan.Mutate,
		}, serviceLog)
		if mutationErr == nil {
			acceptedMu.Lock()
			accepted[serviceName] = result
			acceptedMu.Unlock()
		}
		return mutationErr
	})
	return accepted, aggregateServiceActionFailures(language, failures)
}

func runServiceFanOut(ctx context.Context, instanceID string, services []string, concurrency int, language string, log Logger, targetLog targetLogger, recorder stepRecorder, action func(context.Context, string, Logger) error) []serviceActionFailure {
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
				err := action(ctx, serviceName, serviceLog)
				status := "success"
				errText := ""
				if err != nil {
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

func aggregateServiceActionFailures(language string, failures []serviceActionFailure) error {
	if len(failures) == 0 {
		return nil
	}
	services := make([]string, 0, len(failures))
	for _, failure := range failures {
		services = append(services, failure.service)
	}
	return fmt.Errorf("%s: %s", i18n.Text(language, "aifar.runtimeMutation.batchFailed"), strings.Join(services, ","))
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
