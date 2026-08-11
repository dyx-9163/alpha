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

export type RuntimeTrackedTask = {
  id: string
  status?: string
  target?: string
}

export type RuntimeServiceGateContext = {
  canManage: boolean
  permissionReason?: string
  instanceStatus?: string
  agentStatus?: string
  agentError?: string
  agentFeatures?: string[]
  serverAvailable?: boolean
  activeTasks?: RuntimeTrackedTask[]
}

export type RuntimeServiceActionGate = {
  disabled: boolean
  reason: string
  ownerTaskId: string
}

const requiredRuntimeServiceFeatures = [
  'service-manifest-v1',
  'service-generation-v1',
  'per-service-reconcile',
  'per-service-restart',
  'service-conditions-v1'
]

export function buildAifarServiceOptions(
  modules: Array<{ name: string; displayName?: string }> = [],
  discoveredNames: string[] = []
) {
  const seen = new Set<string>()
  const out: Array<{ value: string; label: string }> = []
  for (const module of modules) {
    const name = String(module.name || '').trim()
    if (!name || seen.has(name)) continue
    seen.add(name)
    out.push({ value: name, label: String(module.displayName || name) })
  }
  for (const raw of discoveredNames) {
    const name = String(raw || '').trim()
    if (!name || seen.has(name)) continue
    seen.add(name)
    out.push({ value: name, label: name })
  }
  return out
}

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

export function summarizeRuntimeRestartScope(deployments: AifarRuntimeDeployment[]) {
  return deployments.reduce((scope, deployment) => {
    const replicas = Math.max(0, Number(deployment.desiredReplicas) || 0)
    if (replicas > 0) {
      scope.services += 1
      scope.replicas += replicas
    }
    return scope
  }, { services: 0, replicas: 0 })
}

export function runtimeDeploymentPhaseCounts(deployments: AifarRuntimeDeployment[]) {
  const counts = { available: 0, progressing: 0, degraded: 0, offline: 0, unknown: 0 }
  for (const deployment of deployments) {
    counts[runtimeDeploymentPhase(deployment)] += 1
  }
  return counts
}

export function runtimeServiceActionGate(
  target: AifarRuntimeDeployment,
  deployments: AifarRuntimeDeployment[],
  context: RuntimeServiceGateContext
): RuntimeServiceActionGate {
  if (!context.canManage) return disabledGate(context.permissionReason || 'containers.permissionDisabled')
  if (!deployments.some((deployment) => deployment.instanceId === target.instanceId && deployment.serviceName === target.serviceName)) {
    return disabledGate('containers.runtimeServiceMissingDisabled')
  }
  if (!Number.isFinite(Number(target.generation)) || Number(target.generation) <= 0) {
    return disabledGate('containers.runtimeGenerationUnavailableDisabled')
  }
  const globalReason = runtimeGlobalActionGate(context)
  if (globalReason) return disabledGate(globalReason)
  const targetKey = `${target.instanceId}:${target.serviceName}`
  const owner = (context.activeTasks || []).find((task) => {
    const status = normalize(task.status)
    return normalize(task.target) === targetKey && status !== 'success' && status !== 'failed' && status !== 'cancelled'
  })
  if (owner) {
    return {
      disabled: true,
      reason: 'containers.runtimeServiceTaskDisabled',
      ownerTaskId: owner.id
    }
  }
  return { disabled: false, reason: '', ownerTaskId: '' }
}

export function runtimeGlobalActionGate(context: RuntimeServiceGateContext) {
  if (!context.canManage) return context.permissionReason || 'containers.permissionDisabled'
  if (normalize(context.instanceStatus) === 'maintenance') return 'containers.runtimeInstanceMaintenanceDisabled'
  if (context.serverAvailable === false) return 'containers.runtimeServerUnavailableDisabled'
  if (normalize(context.agentStatus) !== 'running') return 'containers.agentUnavailableDisabled'
  const featureSet = new Set((context.agentFeatures || []).map(normalize).filter(Boolean))
  if (context.agentError || requiredRuntimeServiceFeatures.some((feature) => !featureSet.has(feature))) {
    return 'containers.agentCapabilityDisabled'
  }
  return ''
}

export function reconcileRuntimeServiceTaskOwners(
  owners: Record<string, string>,
  tasks: RuntimeTrackedTask[]
) {
  const activeTaskIds = new Set(tasks
    .filter((task) => !isTerminalTaskStatus(task.status))
    .map((task) => normalize(task.id))
    .filter(Boolean))
  return Object.fromEntries(Object.entries(owners).filter(([, taskId]) => activeTaskIds.has(normalize(taskId))))
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

export function runtimeDeploymentPhase(deployment: AifarRuntimeDeployment): 'available' | 'progressing' | 'degraded' | 'offline' | 'unknown' {
  const activeTypes = new Set((deployment.conditions || [])
    .filter((condition) => condition.status === true)
    .map((condition) => condition.type))
  if (activeTypes.has('Offline')) return 'offline'
  if (activeTypes.has('Degraded')) return 'degraded'
  if (activeTypes.has('Progressing')) return 'progressing'
  if (activeTypes.has('Available')) return 'available'
  return 'unknown'
}

function disabledGate(reason: string): RuntimeServiceActionGate {
  return { disabled: true, reason, ownerTaskId: '' }
}

function normalize(value?: string) {
  return String(value || '').trim()
}

function isTerminalTaskStatus(status?: string) {
  return ['success', 'failed', 'cancelled'].includes(normalize(status))
}
