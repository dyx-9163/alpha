package aifar

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

const runtimeControlPlaneRepairCode = "AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED"

func (s Service) ScaleService(ctx context.Context, req ScaleRequest, log Logger, targetLog targetLogger) error {
	return s.ScaleServices(ctx, ScaleServicesRequest{
		Instance:        req.Instance,
		Server:          req.Server,
		Language:        req.Language,
		Actor:           req.Actor,
		TaskID:          req.TaskID,
		DesiredReplicas: map[string]int{req.ServiceName: req.Replicas},
		Reason:          req.Reason,
	}, log, targetLog)
}

func (s Service) ScaleServices(ctx context.Context, req ScaleServicesRequest, log Logger, targetLog targetLogger) error {
	desiredTargets, services, err := normalizeScaleTargets(req.DesiredReplicas)
	if err != nil {
		return err
	}
	metadata := metadataFromInstance(req.Instance)
	if err := ensureK8sLikeMetadata(metadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support service scale; reinstall with k8s-like orchestration first"}); err != nil {
		return err
	}
	plans := make([]deploymentMutationPlan, 0, len(services))
	for _, serviceName := range services {
		replicas := desiredTargets[serviceName]
		plans = append(plans, deploymentMutationPlan{
			ServiceName: serviceName,
			Operation:   "scale",
			Validate: func(instance store.AppInstance, _ store.AIFARDeployment) error {
				freshMetadata := metadataFromInstance(instance)
				if err := ensureK8sLikeMetadata(freshMetadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support service scale; reinstall with k8s-like orchestration first"}); err != nil {
					return err
				}
				if !serviceInList(serviceName, servicesFromMetadata(freshMetadata)) {
					return fmt.Errorf("AIFAR service %s is not installed", serviceName)
				}
				return nil
			},
			Mutate: func(manifest *runtimeagent.DeploymentManifest) error {
				manifest.Spec.Replicas = replicas
				return nil
			},
			Project: func(projectCtx context.Context, lock store.AIFAROrchestrationLock, accepted store.AIFARDeployment) error {
				_, err := s.updateAcceptedDeploymentMetadata(projectCtx, lock, accepted, "AIFAR_RUNTIME_CONTROL_METADATA_WRITE_FAILED", func(nextMetadata map[string]any) error {
					desired := desiredReplicasFromMetadata(nextMetadata)
					desired[serviceName] = accepted.DesiredReplicas
					nextMetadata["desiredReplicas"] = desired
					return nil
				})
				return err
			},
		})
	}
	_, mutationErr := s.mutateDeploymentsFanOut(ctx, req.Instance, req.Server, req.Actor, fallbackTaskID(req.TaskID, log), req.Language, defaultRuntimeMutationConcurrency, plans, log, targetLog)
	return mutationErr
}

func normalizeScaleTargets(requested map[string]int) (map[string]int, []string, error) {
	if len(requested) == 0 {
		return nil, nil, errors.New("at least one AIFAR service scale target is required")
	}
	desired := make(map[string]int, len(requested))
	for rawService, replicas := range requested {
		service := cleanAIFARServiceName(rawService)
		if service == "" || !isAIFARService(service) {
			return nil, nil, fmt.Errorf("unsupported AIFAR service for scale: %s", strings.TrimSpace(rawService))
		}
		if replicas < 0 {
			return nil, nil, errors.New("AIFAR service replicas must be greater than or equal to 0")
		}
		desired[service] = replicas
	}
	services := make([]string, 0, len(desired))
	for _, service := range serviceOrder {
		if _, ok := desired[service]; ok {
			services = append(services, service)
		}
	}
	if len(services) != len(desired) {
		return nil, nil, errors.New("unsupported AIFAR service scale target")
	}
	return desired, services, nil
}

func serviceInList(service string, services []string) bool {
	for _, item := range services {
		if item == service {
			return true
		}
	}
	return false
}
