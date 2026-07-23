import { beforeEach, describe, expect, it, vi } from 'vitest'

const { getCurrentLocaleMock } = vi.hoisted(() => ({ getCurrentLocaleMock: vi.fn() }))

vi.mock('../../i18n', () => ({
  getCurrentLocale: getCurrentLocaleMock
}))

import { messages } from '../../i18n/messages'
import {
  aifarArtifactAccept,
  aifarArtifactHintKey,
  buildAifarArtifactForm,
  formatBytes,
  isAifarArtifactTooLarge
} from './artifacts'
import {
  buildRuntimeServiceOverrides,
  defaultRuntimeConfigState,
  normalizedRuntimeValues,
  optionalRuntimeNumber,
  runtimeConfigNumberText,
  validateRuntimeConfigValues
} from './config'
import {
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel,
  formatDate,
  percentText,
  releaseActivatedAtText,
  releaseKindLabel,
  releaseServicesText,
  releaseStatusLabel,
  runtimeApplyStatusLabel,
  runtimeDeploymentReplicaText,
  runtimeEndpointText,
  runtimeInstanceLabel,
  runtimeNacosStatus
} from './format'
import {
  buildAifarServiceOptions,
  buildRuntimeLogPodOptions,
  buildRuntimeServiceMap,
  filterRuntimeDeploymentsByInstance,
  filterRuntimePodsByInstance,
  filterRuntimeServicesByInstance,
  findRuntimeIngressByInstance,
  findSelectedRuntimeInstance,
  resolveRuntimeAppInstance,
  runtimeDiscoveryTarget,
  runtimeServiceForDeployment
} from './selectors'
import type {
  AifarRuntimeDeployment,
  AifarRuntimeIngress,
  AifarRuntimeInstance,
  AifarRuntimePod,
  AifarRuntimeService,
  RuntimeConfigServiceRow
} from './types'

const t = (key: string) => key

describe('runtime artifact rules', () => {
  beforeEach(() => {
    getCurrentLocaleMock.mockReset()
    getCurrentLocaleMock.mockReturnValue('zh')
  })

  it('directs bundle uploads to the Bundle Packager output', () => {
    expect(messages.zh['apps.aifarUpdateBundleHint']).toContain('AIFARBundlePackager.exe')
    expect(messages.en['apps.aifarUpdateBundleHint']).toContain('AIFARBundlePackager.exe')
    expect(String(messages.zh['apps.aifarUpdateBundleHint'])).not.toContain('export-alpha-jars')
    expect(String(messages.en['apps.aifarUpdateBundleHint'])).not.toContain('export-alpha-jars')
  })

  it.each([
    ['bundle', 'gateway', '.zip', 'apps.aifarUpdateBundleHint'],
    ['single', 'web-vue3', '.zip,.tar,.tgz,.tar.gz', 'apps.aifarUpdateFrontendHint'],
    ['single', 'gateway', '.jar', 'apps.aifarUpdateJarHint']
  ] as const)('returns the accepted files and hint for %s %s', (mode, service, accept, hint) => {
    expect(aifarArtifactAccept(mode, service)).toBe(accept)
    expect(aifarArtifactHintKey(mode, service)).toBe(hint)
  })

  it('builds a localized single-service artifact form', () => {
    const file = new File(['jar-content'], 'gateway.jar', { type: 'application/java-archive' })

    const form = buildAifarArtifactForm('single', 'gateway', file)

    expect(form.get('language')).toBe('zh')
    expect(form.get('service')).toBe('gateway')
    const artifact = form.get('artifact')
    expect(artifact).toBeInstanceOf(File)
    expect((artifact as File).name).toBe('gateway.jar')
    expect((artifact as File).size).toBe(file.size)
    expect((artifact as File).type).toBe('application/java-archive')
    expect(form.get('bundle')).toBeNull()
  })

  it('builds a localized bundle artifact form', () => {
    getCurrentLocaleMock.mockReturnValueOnce('en')
    const file = new File(['bundle-content'], 'release.zip', { type: 'application/zip' })

    const form = buildAifarArtifactForm('bundle', '', file)

    expect(form.get('language')).toBe('en')
    const bundle = form.get('bundle')
    expect(bundle).toBeInstanceOf(File)
    expect((bundle as File).name).toBe('release.zip')
    expect((bundle as File).size).toBe(file.size)
    expect((bundle as File).type).toBe('application/zip')
    expect(form.get('service')).toBeNull()
    expect(form.get('artifact')).toBeNull()
  })

  it('checks upload sizes only when a positive limit exists', () => {
    const file = new File(['12345'], 'artifact.jar')
    expect(isAifarArtifactTooLarge(file, 4)).toBe(true)
    expect(isAifarArtifactTooLarge(file, 5)).toBe(false)
    expect(isAifarArtifactTooLarge(file, 0)).toBe(false)
    expect(isAifarArtifactTooLarge(file)).toBe(false)
  })

  it.each([
    [Number.NaN, '0 B'],
    [-1, '0 B'],
    [0, '0 B'],
    [512, '512 B'],
    [1024, '1.0 KiB'],
    [1536, '1.5 KiB'],
    [1024 ** 3, '1.0 GiB']
  ])('formats %s bytes as %s', (value, expected) => {
    expect(formatBytes(value)).toBe(expected)
  })
})

describe('runtime configuration rules', () => {
  it('provides the frozen runtime defaults', () => {
    expect(defaultRuntimeConfigState()).toEqual({
      configVersion: 1,
      appliedVersion: 1,
      lastApplyStatus: 'applied',
      global: {
        appCPUs: '2.0',
        appMemoryLimit: '2GB',
        jvmInitialRAMPercentage: 20,
        jvmMaxRAMPercentage: 70
      },
      nacosEphemeral: true,
      services: {}
    })
  })

  it('trims string values and supplies defaults only for missing values', () => {
    expect(normalizedRuntimeValues({ appCPUs: ' 1.5 ', appMemoryLimit: ' 768MiB ' })).toEqual({
      appCPUs: '1.5',
      appMemoryLimit: '768MiB',
      jvmInitialRAMPercentage: 20,
      jvmMaxRAMPercentage: 70
    })
  })

  it('preserves explicit zero JVM percentages so validation can reject them', () => {
    const values = normalizedRuntimeValues({
      appCPUs: '2',
      appMemoryLimit: '2GB',
      jvmInitialRAMPercentage: 0,
      jvmMaxRAMPercentage: 0
    })

    expect(values.jvmInitialRAMPercentage).toBe(0)
    expect(values.jvmMaxRAMPercentage).toBe(0)
    expect(validateRuntimeConfigValues(values, t)).toBe('containers.runtimeConfigJvmInvalid')
  })

  it.each([
    [undefined, ''],
    [0, ''],
    [-1, ''],
    [12.5, '12.5']
  ])('formats optional config number %s', (value, expected) => {
    expect(runtimeConfigNumberText(value)).toBe(expected)
  })

  it('accepts a valid runtime configuration', () => {
    expect(validateRuntimeConfigValues({
      appCPUs: '2.5',
      appMemoryLimit: '2048MiB',
      jvmInitialRAMPercentage: 20,
      jvmMaxRAMPercentage: 70
    }, t)).toBe('')
  })

  it.each(['', '0', '-1', '1e2', 'two'])('rejects invalid CPU value %s', (appCPUs) => {
    expect(validateRuntimeConfigValues({
      appCPUs,
      appMemoryLimit: '2GB',
      jvmInitialRAMPercentage: 20,
      jvmMaxRAMPercentage: 70
    }, t)).toBe('containers.runtimeConfigCpuInvalid')
  })

  it.each(['', '0GB', '-1GB', '1.5GB', 'lots'])('rejects invalid memory value %s', (appMemoryLimit) => {
    expect(validateRuntimeConfigValues({
      appCPUs: '2',
      appMemoryLimit,
      jvmInitialRAMPercentage: 20,
      jvmMaxRAMPercentage: 70
    }, t)).toBe('containers.runtimeConfigMemoryInvalid')
  })

  it.each([
    [Number.NaN, 70, 'containers.runtimeConfigJvmInvalid'],
    [0, 70, 'containers.runtimeConfigJvmInvalid'],
    [20, 0, 'containers.runtimeConfigJvmInvalid'],
    [20, 91, 'containers.runtimeConfigJvmInvalid'],
    [71, 70, 'containers.runtimeConfigJvmOrderInvalid']
  ])('rejects invalid JVM percentages %s / %s', (initial, max, expected) => {
    expect(validateRuntimeConfigValues({
      appCPUs: '2',
      appMemoryLimit: '2GB',
      jvmInitialRAMPercentage: initial,
      jvmMaxRAMPercentage: max
    }, t)).toBe(expected)
  })

  it('builds trimmed service overrides and ignores JVM fields for web-vue3', () => {
    const rows: RuntimeConfigServiceRow[] = [
      {
        serviceName: 'gateway',
        appCPUs: ' 1.0 ',
        appMemoryLimit: '',
        jvmInitialRAMPercentage: ' 25 ',
        jvmMaxRAMPercentage: ''
      },
      {
        serviceName: 'web-vue3',
        appCPUs: '',
        appMemoryLimit: ' 512MiB ',
        jvmInitialRAMPercentage: 'invalid-but-ignored',
        jvmMaxRAMPercentage: 'invalid-but-ignored'
      },
      {
        serviceName: 'oauth',
        appCPUs: '',
        appMemoryLimit: '',
        jvmInitialRAMPercentage: '',
        jvmMaxRAMPercentage: ''
      }
    ]
    const global = {
      appCPUs: '2.0',
      appMemoryLimit: '2GB',
      jvmInitialRAMPercentage: 20,
      jvmMaxRAMPercentage: 70,
      nacosEphemeral: true
    }

    expect(buildRuntimeServiceOverrides(rows, global, t)).toEqual({
      gateway: { appCPUs: '1.0', jvmInitialRAMPercentage: 25 },
      'web-vue3': { appMemoryLimit: '512MiB' }
    })
  })

  it('names the service whose optional JVM override is invalid', () => {
    const rows: RuntimeConfigServiceRow[] = [{
      serviceName: 'gateway',
      appCPUs: '',
      appMemoryLimit: '',
      jvmInitialRAMPercentage: 'not-a-number',
      jvmMaxRAMPercentage: ''
    }]
    const global = {
      appCPUs: '2.0',
      appMemoryLimit: '2GB',
      jvmInitialRAMPercentage: 20,
      jvmMaxRAMPercentage: 70,
      nacosEphemeral: true
    }

    expect(() => buildRuntimeServiceOverrides(rows, global, t))
      .toThrow('gateway: containers.runtimeConfigJvmInvalid')
  })

  it.each([
    ['', undefined],
    ['  ', undefined],
    ['12.5', 12.5],
    ['invalid', Number.NaN]
  ])('parses optional number %j', (value, expected) => {
    const actual = optionalRuntimeNumber(value)
    if (Number.isNaN(expected)) expect(actual).toBeNaN()
    else expect(actual).toBe(expected)
  })
})

describe('runtime format rules', () => {
  it.each([
    ['ready', 'running'],
    ['ACTIVE', 'unknown'],
    ['rolling', 'pending'],
    ['stale', 'degraded'],
    ['offline', 'degraded'],
    ['failed', 'failed'],
    ['no-endpoints', 'failed'],
    ['', 'unknown'],
    [undefined, 'unknown']
  ])('maps status %s to %s', (status, expected) => {
    expect(aifarRuntimeStatusKind(status)).toBe(expected)
  })

  it('uses translated labels and falls back to raw values', () => {
    const translate = (key: string) => ({
      'containers.runtimeStatus.ready': 'Ready',
      'containers.releaseKind.update': 'Update',
      'containers.releaseStatus.success': 'Success',
      'containers.runtimeApplyStatus.applied': 'Applied',
      'common.unknown': 'Unknown'
    }[key] ?? key)

    expect(aifarRuntimeStatusLabel('ready', translate)).toBe('Ready')
    expect(releaseKindLabel('update', translate)).toBe('Update')
    expect(releaseStatusLabel('success', translate)).toBe('Success')
    expect(runtimeApplyStatusLabel('applied', translate)).toBe('Applied')
    expect(aifarRuntimeStatusLabel('custom', translate)).toBe('custom')
    expect(aifarRuntimeStatusLabel(undefined, translate)).toBe('Unknown')
  })

  it('formats release service lists and dates', () => {
    expect(releaseServicesText({ instanceId: 'instance-1', releaseId: 'release-1', changedServices: ['gateway', 'oauth'] }))
      .toBe('gateway, oauth')
    expect(releaseServicesText({ instanceId: 'instance-1', releaseId: 'release-1' })).toBe('-')
    expect(formatDate()).toBe('-')
    expect(formatDate('not-a-date')).toBe('not-a-date')
    expect(formatDate('2026-07-10T12:00:00Z')).toBe(new Date('2026-07-10T12:00:00Z').toLocaleString())
    expect(releaseActivatedAtText({ instanceId: 'instance-1', releaseId: 'release-failed', status: 'failed', activatedAt: '0001-01-01T00:00:00Z' })).toBe('-')
    expect(releaseActivatedAtText({ instanceId: 'instance-1', releaseId: 'release-pending', status: 'pending', createdAt: '2026-07-10T12:00:00Z' })).toBe('-')
    expect(releaseActivatedAtText({ instanceId: 'instance-1', releaseId: 'release-success', status: 'success', activatedAt: '2026-07-10T12:00:00Z' })).toBe(new Date('2026-07-10T12:00:00Z').toLocaleString())
  })

  it('formats ready and total service endpoints using the documented fallbacks', () => {
    expect(runtimeEndpointText(service({ readyEndpointCount: 2, endpointCount: 3, activeEndpoints: 9 }))).toBe('2 / 3')
    expect(runtimeEndpointText(service({ activeEndpoints: 4 }))).toBe('4 / 4')
    expect(runtimeEndpointText(service({ readyReplicas: 1 }))).toBe('1 / 1')
    expect(runtimeEndpointText(service({ readyEndpointCount: Number.NaN, endpointCount: Number.NaN }))).toBe('0 / 0')
  })

  it('formats deployment replica progress and an independent updated count', () => {
    expect(runtimeDeploymentReplicaText(deployment({ readyReplicas: 2, desiredReplicas: 3, updatedReplicas: 1 })))
      .toBe('2 / 3 (1)')
    expect(runtimeDeploymentReplicaText(deployment({ availableReplicas: 2, desiredReplicas: 2, updatedReplicas: 2 })))
      .toBe('2 / 2')
  })

  it('derives Nacos readiness in priority order', () => {
    expect(runtimeNacosStatus(service({ nacosReady: true, lastNacosError: 'old' }))).toBe('ready')
    expect(runtimeNacosStatus(service({ lastNacosError: 'failed' }))).toBe('failed')
    expect(runtimeNacosStatus(service({ nacosRegistered: true }))).toBe('running')
    expect(runtimeNacosStatus(service({ status: 'offline' }))).toBe('offline')
    expect(runtimeNacosStatus(service())).toBe('unknown')
  })

  it.each([
    [undefined, '-'],
    [0, '-'],
    [Number.NaN, '-'],
    [12.34, '12.3%']
  ])('formats percent %s', (value, expected) => {
    expect(percentText(value)).toBe(expected)
  })

  it('formats a runtime instance label with fallbacks', () => {
    const translate = (key: string) => key === 'common.unknown' ? 'Unknown' : key
    expect(runtimeInstanceLabel({
      id: 'instance-1',
      version: '2.1.0',
      orchestrationModel: 'agent-runtime-v2',
      installRoot: '/aifar/apps'
    }, translate)).toBe('2.1.0 / agent-runtime-v2 / /aifar/apps')
    expect(runtimeInstanceLabel({ id: 'instance-2' }, translate)).toBe('aifar / Unknown / instance-2')
  })
})

describe('runtime selectors', () => {
  const instances: AifarRuntimeInstance[] = [
    { id: 'instance-1', version: '1.0', status: 'ready' },
    { id: 'instance-2', version: '2.0', status: 'degraded' }
  ]
  const services = [
    service({ instanceId: 'instance-1', serviceName: 'gateway', appName: 'gateway-app', proxyName: 'gateway-proxy' }),
    service({ instanceId: 'instance-2', serviceName: 'oauth', appName: 'oauth-app' }),
    service({ instanceId: 'instance-1', serviceName: 'system', appName: 'system-old' }),
    service({ instanceId: 'instance-1', serviceName: 'system', appName: 'system-new' })
  ]
  const deployments = [
    deployment({ instanceId: 'instance-1', serviceName: 'gateway' }),
    deployment({ instanceId: 'instance-2', serviceName: 'oauth' })
  ]
  const pods: AifarRuntimePod[] = [
    { instanceId: 'instance-1', serviceName: 'gateway', containerName: 'gateway-1', status: 'running' },
    { instanceId: 'instance-1', serviceName: 'oauth', containerName: 'oauth-1' },
    { instanceId: 'instance-2', serviceName: 'gateway', containerName: 'gateway-2' }
  ]
  const ingress: AifarRuntimeIngress[] = [
    { instanceId: 'instance-1', status: 'ready' },
    { instanceId: 'instance-2', status: 'failed' }
  ]

  it('builds service options from backend-discovered modules and runtime fallbacks', () => {
    expect(buildAifarServiceOptions(
      [{ name: 'gateway', displayName: 'Gateway (required)' }, { name: 'custom' }],
      ['gateway', 'runtime-only']
    )).toEqual([
      { value: 'gateway', label: 'Gateway (required)' },
      { value: 'custom', label: 'custom' },
      { value: 'runtime-only', label: 'runtime-only' }
    ])
  })

  it('selects an instance by id and otherwise falls back to the first item', () => {
    expect(findSelectedRuntimeInstance(instances, 'instance-2')).toBe(instances[1])
    expect(findSelectedRuntimeInstance(instances, 'missing')).toBe(instances[0])
    expect(findSelectedRuntimeInstance([], 'missing')).toBeNull()
  })

  it('resolves the matching app instance or builds the frozen fallback', () => {
    const appInstance = { id: 'instance-1', app: 'aifar', serverId: 'server-9', version: '1.0', status: 'installed' }
    expect(resolveRuntimeAppInstance(instances[0], [appInstance], 'server-1')).toBe(appInstance)
    expect(resolveRuntimeAppInstance(instances[1], [], 'server-1')).toEqual({
      id: 'instance-2',
      app: 'aifar',
      serverId: 'server-1',
      version: '2.0',
      status: 'degraded',
      metadata: ''
    })
    expect(resolveRuntimeAppInstance(null, [appInstance], 'server-1')).toBeNull()
  })

  it('filters services, deployments, pods, and ingress by instance', () => {
    expect(filterRuntimeServicesByInstance(services, 'instance-1')).toEqual([services[0], services[2], services[3]])
    expect(filterRuntimeDeploymentsByInstance(deployments, 'instance-2')).toEqual([deployments[1]])
    expect(filterRuntimePodsByInstance(pods, 'instance-1')).toEqual([pods[0], pods[1]])
    expect(findRuntimeIngressByInstance(ingress, 'instance-2')).toBe(ingress[1])
    expect(findRuntimeIngressByInstance(ingress, 'missing')).toBeNull()
  })

  it('maps services by name using the last duplicate and resolves discovery targets', () => {
    const map = buildRuntimeServiceMap(services)
    expect(map.get('system')).toBe(services[3])
    expect(runtimeDiscoveryTarget(services[0])).toBe('gateway-proxy')
    expect(runtimeDiscoveryTarget(service({ proxyName: '', appName: 'oauth-app', serviceName: 'oauth' }))).toBe('oauth-app')
    expect(runtimeDiscoveryTarget(service({ proxyName: '', appName: '', serviceName: 'oauth' }))).toBe('oauth')
    expect(runtimeDiscoveryTarget(service({ proxyName: '', appName: '', serviceName: '' }))).toBe('-')
  })

  it('uses the service map and creates a deployment fallback when absent', () => {
    const map = buildRuntimeServiceMap(services)
    expect(runtimeServiceForDeployment(deployments[0], map)).toBe(services[0])
    const missing = deployment({
      instanceId: 'instance-1',
      serviceName: 'file',
      deploymentName: 'file-deployment',
      desiredReplicas: 3,
      readyReplicas: 2,
      image: 'file:v1',
      status: 'rolling',
      failureReason: 'one pod pending'
    })
    expect(runtimeServiceForDeployment(missing, map)).toEqual({
      instanceId: 'instance-1',
      serviceName: 'file',
      appName: 'file-deployment',
      desiredReplicas: 3,
      readyReplicas: 2,
      image: 'file:v1',
      status: 'rolling',
      rolloutStatus: 'rolling',
      failureReason: 'one pod pending'
    })
  })

  it('builds log pod options in pod order and filters by selected services', () => {
    expect(buildRuntimeLogPodOptions(pods, ['gateway'])).toEqual([
      { value: 'gateway-1', label: 'gateway / gateway-1', serviceName: 'gateway', status: 'running' },
      { value: 'gateway-2', label: 'gateway / gateway-2', serviceName: 'gateway', status: 'unknown' }
    ])
    expect(buildRuntimeLogPodOptions(pods, [])).toHaveLength(3)
  })
})

function service(overrides: Partial<AifarRuntimeService> = {}): AifarRuntimeService {
  return {
    instanceId: 'instance-1',
    serviceName: 'gateway',
    ...overrides
  }
}

function deployment(overrides: Partial<AifarRuntimeDeployment> = {}): AifarRuntimeDeployment {
  return {
    instanceId: 'instance-1',
    serviceName: 'gateway',
    ...overrides
  }
}
