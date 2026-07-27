export type AifarRuntimeInstance = {
  id: string
  version?: string
  status?: string
  orchestrationModel?: string
  legacy?: boolean
  installRoot?: string
  endpoint?: string
  gatewayEndpoint?: string
  runtimeConfig?: RuntimeConfigState
}

export type RuntimeConfigValues = {
  appCPUs?: string
  appMemoryLimit?: string
  jvmInitialRAMPercentage?: number
  jvmMaxRAMPercentage?: number
}

export type RuntimeConfigState = {
  configVersion?: number
  updatedAt?: string
  updatedBy?: string
  global?: RuntimeConfigValues
  services?: Record<string, RuntimeConfigValues>
  nacosEphemeral?: boolean
  appliedVersion?: number
  lastAppliedAt?: string
  lastApplyStatus?: string
  lastApplyError?: string
}

export type RuntimeConfigFormValues = Required<RuntimeConfigValues> & {
  nacosEphemeral: boolean
}

export type RuntimeConfigServiceRow = {
  serviceName: string
  appCPUs: string
  appMemoryLimit: string
  jvmInitialRAMPercentage: string
  jvmMaxRAMPercentage: string
}

export type AifarRuntimeAgent = {
  status?: string
  version?: string
  mode?: string
  error?: string
  listeners?: number[]
  features?: string[]
}

export type AifarRuntimeService = {
  instanceId: string
  serviceName: string
  appName?: string
  proxyName?: string
  desiredReplicas?: number
  readyReplicas?: number
  activeEndpoints?: number
  endpointCount?: number
  readyEndpointCount?: number
  image?: string
  status?: string
  rolloutStatus?: string
  nacosRegistered?: boolean
  nacosReady?: boolean
  lastNacosError?: string
  lastError?: string
  cpuPercent?: number
  memoryPercent?: number
  failureReason?: string
}

export type AifarRuntimeDeployment = {
  instanceId: string
  deploymentName?: string
  serviceName: string
  appName?: string
  desiredReplicas?: number
  currentReplicas?: number
  readyReplicas?: number
  updatedReplicas?: number
  availableReplicas?: number
  podRevision?: string
  updatingPodRevision?: string
  image?: string
  status?: string
  updatedAt?: string
  failureReason?: string
}

export type AifarRuntimePod = {
  instanceId: string
  serviceName: string
  podId?: string
  containerName: string
  revision?: string
  image?: string
  port?: number
  status?: string
  ready?: boolean
  cpuPercent?: number
  memoryPercent?: number
  memoryUsage?: string
}

export type AifarRuntimeIngress = {
  instanceId: string
  container?: string
  status?: string
  gatewayPort?: number
  webPort?: number
  gatewayRoute?: string
  webRoute?: string
  error?: string
}

export type AifarRuntimeLogPod = {
  instanceId: string
  serviceName: string
  podId?: string
  containerName: string
  revision?: string
  status?: string
  ready?: boolean
  logs?: string[]
  lineCount?: number
  collectionError?: string
}

export type AifarRuntimeLogsResponse = {
  serverId?: string
  instanceId?: string
  service?: string
  services?: string[]
  podsFilter?: string[]
  tail?: number
  batchSize?: number
  mode?: string
  pods?: AifarRuntimeLogPod[]
  warnings?: string[]
}

export type RuntimeLogRow = {
  id: string
  time: string
  timestamp: number
  sequence: number
  serviceName: string
  pod: string
  level: string
  message: string
  errorContext: boolean
}

export type RuntimeEntryRoute = {
  name: string
  route: string
  port: string
  status: string
}

export type AifarRuntimeResponse = {
  serverId?: string
  runtimeStatus?: string
  agent?: AifarRuntimeAgent
  instances?: AifarRuntimeInstance[]
  deployments?: AifarRuntimeDeployment[]
  services?: AifarRuntimeService[]
  pods?: AifarRuntimePod[]
  ingress?: AifarRuntimeIngress[]
  warnings?: string[]
}

export type AifarRelease = {
  id?: string
  instanceId: string
  releaseId: string
  kind?: string
  status?: string
  manifestStatus?: string
  version?: string
  serverId?: string
  configHash?: string
  createdAt?: string
  activatedAt?: string
  changedServices?: string[]
  rollbackAvailable?: boolean
  manifest?: Record<string, any>
}

export type AifarReleaseListResponse = {
  items?: AifarRelease[]
}

export type RuntimeDiagnosticRequest = {
  instanceId: string
  sinceAt: string
  untilAt: string
  services: string[]
}

export type RuntimeDiagnosticEstimate = {
  services: Array<{ service: string; candidateFiles: number; candidateScanBytes: number }>
  logSource: 'host-mounted'
  candidateFiles: number
  candidateScanBytes: number
  estimatedSecondsMin: number
  estimatedSecondsMax: number
  maxFileScanBytes: number
  maxTotalScanBytes: number
  maxFilteredBytes: number
  maxArchiveBytes: number
  timeoutSeconds: number
  serverTimezone: string
  localAvailableBytes: number
  localReadyBytes: number
  localReservedBytes: number
  localQuotaBytes: number
  expiresAt: string
  allowed: boolean
  blockReason?: string
  warnings?: string[]
}

export type RuntimeDiagnosticExport = {
  id: string
  taskId?: string
  instanceId: string
  serverId: string
  status: 'pending' | 'building' | 'ready' | 'failed' | 'cancelled' | 'expired' | 'deleted'
  storageKind: 'local' | 'remote'
  services: string[]
  sinceAt: string
  untilAt: string
  archiveName?: string
  archiveBytes: number
  uncompressedBytes: number
  sha256?: string
  warningCount: number
  warnings?: string[]
  error?: string
  createdAt: string
  readyAt?: string
  expiresAt: string
  downloadedAt?: string
  deletedAt?: string
  cleanupStatus: 'none' | 'pending' | 'failed' | 'complete'
  cleanupError?: string
}

export type RuntimeDiagnosticExportPage = {
  items: RuntimeDiagnosticExport[]
  total: number
  page: number
  pageSize: number
}
