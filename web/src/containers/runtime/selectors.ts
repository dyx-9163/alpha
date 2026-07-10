import type {
  AifarRuntimeDeployment,
  AifarRuntimeIngress,
  AifarRuntimeInstance,
  AifarRuntimePod,
  AifarRuntimeService
} from './types'

export type RuntimeAppInstance = {
  id: string
  app: string
  serverId: string
  version?: string
  status: string
  metadata?: string
}

export const aifarServiceNames = [
  'oauth',
  'permission',
  'system',
  'file',
  'message',
  'im',
  'contacts',
  'meeting',
  'gateway',
  'web-vue3'
]

export const aifarServiceOptions = aifarServiceNames.map((name) => ({ value: name, label: name }))

export function findSelectedRuntimeInstance(instances: AifarRuntimeInstance[], selectedId: string) {
  const current = instances.find((instance) => instance.id === selectedId)
  return current ?? instances[0] ?? null
}

export function resolveRuntimeAppInstance(
  runtimeInstance: AifarRuntimeInstance | null,
  appInstances: RuntimeAppInstance[],
  selectedServerId: string
) {
  if (!runtimeInstance) return null
  return appInstances.find((instance) => instance.id === runtimeInstance.id) ?? {
    id: runtimeInstance.id,
    app: 'aifar',
    serverId: selectedServerId,
    version: runtimeInstance.version,
    status: runtimeInstance.status || 'installed',
    metadata: ''
  }
}

export function filterRuntimeServicesByInstance(services: AifarRuntimeService[], instanceId?: string) {
  return services.filter((item) => item.instanceId === instanceId)
}

export function filterRuntimeDeploymentsByInstance(deployments: AifarRuntimeDeployment[], instanceId?: string) {
  return deployments.filter((item) => item.instanceId === instanceId)
}

export function filterRuntimePodsByInstance(pods: AifarRuntimePod[], instanceId?: string) {
  return pods.filter((item) => item.instanceId === instanceId)
}

export function findRuntimeIngressByInstance(ingress: AifarRuntimeIngress[], instanceId?: string) {
  return ingress.find((item) => item.instanceId === instanceId) ?? null
}

export function buildRuntimeServiceMap(services: AifarRuntimeService[]) {
  const out = new Map<string, AifarRuntimeService>()
  for (const service of services) {
    if (service.serviceName) out.set(service.serviceName, service)
  }
  return out
}

export function runtimeDiscoveryTarget(row: AifarRuntimeService) {
  return row.proxyName || row.appName || row.serviceName || '-'
}

export function runtimeServiceForDeployment(row: AifarRuntimeDeployment, services: Map<string, AifarRuntimeService>) {
  const existing = services.get(row.serviceName)
  if (existing) {
    return existing
  }
  return {
    instanceId: row.instanceId,
    serviceName: row.serviceName,
    appName: row.appName || row.deploymentName || row.serviceName,
    desiredReplicas: row.desiredReplicas,
    readyReplicas: row.readyReplicas,
    image: row.image,
    status: row.status,
    rolloutStatus: row.status,
    failureReason: row.failureReason
  }
}

export function buildRuntimeLogPodOptions(pods: AifarRuntimePod[], serviceFilter: string[]) {
  const services = new Set(serviceFilter)
  return pods
    .filter((pod) => !services.size || services.has(pod.serviceName))
    .map((pod) => ({
      value: pod.containerName,
      label: `${pod.serviceName} / ${pod.containerName}`,
      serviceName: pod.serviceName,
      status: pod.status || 'unknown'
    }))
}
