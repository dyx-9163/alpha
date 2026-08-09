package aifar

import (
	"context"
	"errors"
	"sort"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/runtimeagent"
)

func (s Service) RestartRuntime(ctx context.Context, req RuntimeRestartRequest, log Logger, targetLog targetLogger) error {
	control, ok := s.store.(aifarDeploymentControlStore)
	if !ok {
		return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	deployments, err := control.ListAIFARDeployments(req.Instance.ID)
	if err != nil {
		return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_READ_FAILED", err)
	}
	services := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		if deployment.DesiredReplicas > 0 {
			services = append(services, deployment.ServiceName)
		}
	}
	sort.Strings(services)
	if len(services) == 0 {
		return errors.New(i18n.Text(req.Language, "aifar.runtimeMutation.noTargets"))
	}
	current, locks, err := s.acquireServiceOrchestrationLocks(req.Instance.ID, "runtime-restart-all", services, req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		return err
	}
	defer s.releaseOrchestrationLocks(locks)
	req.Instance = current

	plans := make([]deploymentMutationPlan, 0, len(services))
	for _, serviceName := range services {
		plans = append(plans, deploymentMutationPlan{
			ServiceName: serviceName,
			Operation:   "restart",
			Mutate: func(manifest *runtimeagent.DeploymentManifest) error {
				manifest.Spec.RestartGeneration++
				return nil
			},
		})
	}
	_, err = s.mutateDeploymentsFanOut(ctx, current, req.Server, req.Actor, fallbackTaskID(req.TaskID, log), req.Language, defaultRuntimeMutationConcurrency, plans, log, targetLog)
	return err
}
