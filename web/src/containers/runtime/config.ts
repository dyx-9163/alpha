import type {
  RuntimeConfigFormValues,
  RuntimeConfigServiceRow,
  RuntimeConfigState,
  RuntimeConfigValues
} from './types'
import type { RuntimeTranslate } from './format'

export function defaultRuntimeConfigState(): RuntimeConfigState {
  return {
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
  }
}

export function normalizedRuntimeValues(values?: RuntimeConfigValues): Required<RuntimeConfigValues> {
  return {
    appCPUs: String(values?.appCPUs || '2.0').trim(),
    appMemoryLimit: String(values?.appMemoryLimit || '2GB').trim(),
    jvmInitialRAMPercentage: Number(values?.jvmInitialRAMPercentage ?? 20),
    jvmMaxRAMPercentage: Number(values?.jvmMaxRAMPercentage ?? 70)
  }
}

export function runtimeConfigNumberText(value?: number) {
  if (value === undefined || value === null || Number(value) <= 0) {
    return ''
  }
  return String(value)
}

export function validateRuntimeConfigValues(values: Required<RuntimeConfigValues>, t: RuntimeTranslate) {
  const cpuPattern = /^[0-9]+(\.[0-9]+)?$/
  const memoryPattern = /^[1-9][0-9]*(b|k|m|g|kb|mb|gb|kib|mib|gib)?$/i
  if (!cpuPattern.test(values.appCPUs) || Number(values.appCPUs) <= 0) return t('containers.runtimeConfigCpuInvalid')
  if (!memoryPattern.test(values.appMemoryLimit)) return t('containers.runtimeConfigMemoryInvalid')
  if (!Number.isFinite(values.jvmInitialRAMPercentage) || !Number.isFinite(values.jvmMaxRAMPercentage)) return t('containers.runtimeConfigJvmInvalid')
  if (values.jvmInitialRAMPercentage <= 0 || values.jvmMaxRAMPercentage <= 0 || values.jvmMaxRAMPercentage > 90) return t('containers.runtimeConfigJvmInvalid')
  if (values.jvmInitialRAMPercentage > values.jvmMaxRAMPercentage) return t('containers.runtimeConfigJvmOrderInvalid')
  return ''
}

export function buildRuntimeServiceOverrides(
  rows: RuntimeConfigServiceRow[],
  globalValues: RuntimeConfigFormValues,
  t: RuntimeTranslate
) {
  const services: Record<string, RuntimeConfigValues> = {}
  for (const row of rows) {
    const values: RuntimeConfigValues = {}
    if (row.appCPUs.trim()) values.appCPUs = row.appCPUs.trim()
    if (row.appMemoryLimit.trim()) values.appMemoryLimit = row.appMemoryLimit.trim()
    if (row.serviceName !== 'web-vue3') {
      const initial = optionalRuntimeNumber(row.jvmInitialRAMPercentage)
      const max = optionalRuntimeNumber(row.jvmMaxRAMPercentage)
      if (initial !== undefined) {
        if (!Number.isFinite(initial)) throw new Error(`${row.serviceName}: ${t('containers.runtimeConfigJvmInvalid')}`)
        values.jvmInitialRAMPercentage = initial
      }
      if (max !== undefined) {
        if (!Number.isFinite(max)) throw new Error(`${row.serviceName}: ${t('containers.runtimeConfigJvmInvalid')}`)
        values.jvmMaxRAMPercentage = max
      }
    }
    if (Object.keys(values).length) {
      const effective = normalizedRuntimeValues({ ...globalValues, ...values })
      const err = validateRuntimeConfigValues(effective, t)
      if (err) {
        throw new Error(`${row.serviceName}: ${err}`)
      }
      services[row.serviceName] = values
    }
  }
  return services
}

export function optionalRuntimeNumber(value: string) {
  const text = String(value || '').trim()
  if (!text) return undefined
  const n = Number(text)
  return Number.isFinite(n) ? n : Number.NaN
}
