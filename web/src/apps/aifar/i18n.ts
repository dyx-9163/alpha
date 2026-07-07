import { resolveAppLocale, type AppLocale } from '../registry/types'
import type {
  AppInstallDialogConfig,
  AppInstallDialogContext,
  AppInstallDialogCopy,
  AppInstallField,
  AppInstallFieldOption,
  AppInstallFieldValues,
  AppInstanceOption
} from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/types'

export type AifarLocale = AppLocale

export const aifarMessages = {
  zh: {
    title: 'AIFAR 服务',
    categoryLabel: '应用服务',
    sourceLabel: 'Runtime v2 离线包',
    description: '基于 resources/aifar/runtime-v2 离线发布包部署 AIFAR 微服务。',
    installTitle: '安装 AIFAR 服务',
    hint: '目标服务器需要先安装 Docker Engine；安装器只连接已部署的 Nacos，业务运行配置请在 Nacos 中维护。',
    version: '版本',
    versionPlaceholder: '选择 runtime-v2 资源包',
    servers: '目标服务器',
    serversPlaceholder: '选择一台 Docker Engine 就绪的服务器',
    noServers: '暂无 Docker Engine 就绪的服务器，请先在应用商店安装 Docker 并执行检测。',
    noDockerReadyServers: '暂无 Docker Engine 就绪的服务器，请先在应用商店安装 Docker 并执行检测。',
    selectedCount: (count: number) => `已选择 ${count} 台服务器`,
    cancel: '取消',
    submit: '开始安装',
    topologySingle: '单服务器',
    timezone: '时区',
    networkName: 'Docker 网络',
    appCPUs: 'CPU 限制',
    appMemoryLimit: '内存限制',
    selectedServices: '安装模块',
    selectedServicesPlaceholder: '选择需要安装的 AIFAR 模块',
    selectedServicesRequired: '至少需要 gateway 和 web-vue3',
    serviceOauth: '认证 oauth',
    servicePermission: '权限 permission',
    serviceSystem: '系统 system',
    serviceFile: '文件 file',
    serviceMessage: '消息 message',
    serviceIm: '即时通讯 im',
    serviceContacts: '通讯录 contacts',
    serviceMeeting: '会议 meeting',
    serviceGateway: '入口 gateway（必选）',
    serviceWeb: '前端 web-vue3（必选）',
    nacosSource: 'Nacos 来源',
    nacosSourceExisting: '选择已部署 Nacos',
    nacosSourceManual: '手动填写 Nacos',
    nacosInstance: '已部署 Nacos',
    nacosInstancePlaceholder: '选择 Nacos 实例',
    noNacosInstances: '暂无可选 Nacos 实例',
    nacosHost: 'Nacos 主机',
    nacosPort: 'Nacos 端口',
    nacosCredential: 'Nacos 凭据',
    nacosCredentialPlaceholder: '可选择凭据中心已有 Nacos 凭据',
    nacosCredentialManual: '手动输入 Nacos 账号',
    nacosUser: 'Nacos 用户',
    nacosPassword: 'Nacos 密码',
    nacosNamespace: 'Nacos 命名空间',
    portInvalid: '端口必须在 1-65535 之间',
    textRequired: '该配置不能为空',
    networkInvalid: 'Docker 网络名不能包含空格'
  },
  en: {
    title: 'AIFAR Service',
    categoryLabel: 'Application',
    sourceLabel: 'Runtime v2 bundle',
    description: 'Deploy AIFAR microservices from the resources/aifar/runtime-v2 offline release bundle.',
    installTitle: 'Install AIFAR Service',
    hint: 'Target server must already have Docker Engine. The installer only connects to deployed Nacos; keep business runtime configuration in Nacos.',
    version: 'Version',
    versionPlaceholder: 'Select runtime-v2 bundle',
    servers: 'Target server',
    serversPlaceholder: 'Select one Docker Engine ready server',
    noServers: 'No Docker Engine ready servers. Install Docker from the app store and run a check first.',
    noDockerReadyServers: 'No Docker Engine ready servers. Install Docker from the app store and run a check first.',
    selectedCount: (count: number) => `${count} server(s) selected`,
    cancel: 'Cancel',
    submit: 'Start install',
    topologySingle: 'Single server',
    timezone: 'Timezone',
    networkName: 'Docker network',
    appCPUs: 'CPU limit',
    appMemoryLimit: 'Memory limit',
    gatewayPort: 'Gateway entry port',
    webPort: 'Web entry port',
    jvmInitialRAMPercentage: 'JVM initial RAM %',
    jvmMaxRAMPercentage: 'JVM max RAM %',
    selectedServices: 'Install modules',
    selectedServicesPlaceholder: 'Select AIFAR modules to install',
    selectedServicesRequired: 'At least gateway and web-vue3 are required',
    serviceOauth: 'Auth oauth',
    servicePermission: 'Permission',
    serviceSystem: 'System',
    serviceFile: 'File',
    serviceMessage: 'Message',
    serviceIm: 'IM',
    serviceContacts: 'Contacts',
    serviceMeeting: 'Meeting',
    serviceGateway: 'Gateway (required)',
    serviceWeb: 'Web Vue3 (required)',
    nacosSource: 'Nacos source',
    nacosSourceExisting: 'Use deployed Nacos',
    nacosSourceManual: 'Enter Nacos manually',
    nacosInstance: 'Deployed Nacos',
    nacosInstancePlaceholder: 'Select a Nacos instance',
    noNacosInstances: 'No selectable Nacos instances',
    nacosHost: 'Nacos host',
    nacosPort: 'Nacos port',
    nacosCredential: 'Nacos credential',
    nacosCredentialPlaceholder: 'Select a Nacos credential from the credential center',
    nacosCredentialManual: 'Enter Nacos account manually',
    nacosUser: 'Nacos user',
    nacosPassword: 'Nacos password',
    nacosNamespace: 'Nacos namespace',
    portInvalid: 'Port must be between 1 and 65535',
    numberInvalid: 'Value is outside the allowed range',
    jvmRangeInvalid: 'JVM initial RAM percentage must be less than or equal to max RAM percentage',
    textRequired: 'This value is required',
    networkInvalid: 'Docker network name must not contain whitespace'
  }
}

export function resolveAifarLocale(locale?: string): AifarLocale {
  return resolveAppLocale(locale)
}

export function aifarCopy(locale?: string) {
  return aifarMessages[resolveAifarLocale(locale)]
}

export function aifarTopologies(locale?: string): AppTopologyDefinition[] {
  const copy = aifarCopy(locale)
  return [{ name: 'single', label: copy.topologySingle, targetMode: 'single', minTargets: 1, default: true }]
}

export function aifarInstallDialogProps(locale?: string, context?: AppInstallDialogContext): AppInstallDialogConfig {
  const copy = aifarCopy(locale)
  const nacosOptions = nacosInstanceOptions(context)
  const nacosSourceDefault = nacosOptions.length ? 'existing' : 'manual'
  const nacosSelectOptions = nacosOptions.length ? nacosOptions : [{ label: copy.noNacosInstances, value: '', disabled: true }]
  const dialogCopy: AppInstallDialogCopy = {
    title: copy.installTitle,
    hint: copy.hint,
    versionLabel: copy.version,
    versionPlaceholder: copy.versionPlaceholder,
    serversLabel: copy.servers,
    serversPlaceholder: copy.serversPlaceholder,
    noServers: copy.noServers,
    selectedCount: copy.selectedCount,
    cancel: copy.cancel,
    submit: copy.submit
  }
  return {
    targetMode: 'single',
    targetServerFilter: (server, filterContext) => dockerReadyServerIds(filterContext).has(server.id),
    copy: dialogCopy,
    fields: [
      requiredText('timezone', copy.timezone, 'system', copy),
      networkField(copy),
      requiredText('appCPUs', copy.appCPUs, '2.0', copy),
      requiredText('appMemoryLimit', copy.appMemoryLimit, '2GB', copy),
      portField('gatewayPort', labelText(copy, 'gatewayPort', 'Gateway entry port'), 38000, copy),
      portField('webPort', labelText(copy, 'webPort', 'Web entry port'), 8080, copy),
      percentageField('jvmInitialRAMPercentage', labelText(copy, 'jvmInitialRAMPercentage', 'JVM initial RAM %'), 20, copy),
      percentageField('jvmMaxRAMPercentage', labelText(copy, 'jvmMaxRAMPercentage', 'JVM max RAM %'), 70, copy),
      selectedServicesField(copy),
      selectField('nacosSource', copy.nacosSource, [
        { label: copy.nacosSourceExisting, value: 'existing', disabled: nacosOptions.length === 0 },
        { label: copy.nacosSourceManual, value: 'manual' }
      ], nacosSourceDefault, copy),
      {
        ...selectField('nacosInstanceId', copy.nacosInstance, nacosSelectOptions, nacosOptions[0]?.value ?? '', copy, copy.nacosInstancePlaceholder),
        visibleWhen: sourceIs('nacosSource', 'existing')
      },
      {
        ...requiredText('nacosHost', copy.nacosHost, '', copy),
        visibleWhen: sourceIsNot('nacosSource', 'existing')
      },
      {
        ...portField('nacosPort', copy.nacosPort, 8848, copy),
        visibleWhen: sourceIsNot('nacosSource', 'existing')
      },
      selectField('nacosCredentialId', copy.nacosCredential, credentialOptions(context, 'nacos', copy.nacosCredentialManual), '', copy, copy.nacosCredentialPlaceholder, false),
      {
        ...requiredText('nacosUser', copy.nacosUser, 'nacos', copy),
        visibleWhen: (values) => !values.nacosCredentialId
      },
      {
        ...requiredText('nacosPassword', copy.nacosPassword, '', copy),
        type: 'password',
        visibleWhen: (values) => !values.nacosCredentialId
      },
      requiredText('nacosNamespace', copy.nacosNamespace, 'prod', copy)
    ]
  }
}

export function aifarDeployDisabledReason(locale?: string, context?: AppInstallDialogContext) {
  const copy = aifarCopy(locale)
  return dockerReadyServerIds(context).size > 0 ? '' : copy.noDockerReadyServers
}

function requiredText(name: string, label: string, defaultValue: string, copy: ReturnType<typeof aifarCopy>) {
  return {
    name,
    label,
    type: 'text' as const,
    defaultValue,
    required: true,
    validate: (value: unknown) => String(value ?? '').trim() ? undefined : copy.textRequired
  }
}

function networkField(copy: ReturnType<typeof aifarCopy>): AppInstallField {
  return {
    ...requiredText('networkName', copy.networkName, 'aifar-network', copy),
    validate: (value: unknown) => {
      const text = String(value ?? '').trim()
      if (!text) return copy.textRequired
      return /\s/.test(text) ? copy.networkInvalid : undefined
    }
  }
}

const defaultAifarServices = [
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

function selectedServicesField(copy: ReturnType<typeof aifarCopy>): AppInstallField {
  return {
    name: 'selectedServices',
    label: copy.selectedServices,
    type: 'select',
    multiple: true,
    required: true,
    defaultValue: defaultAifarServices,
    placeholder: copy.selectedServicesPlaceholder,
    options: [
      { label: copy.serviceOauth, value: 'oauth' },
      { label: copy.servicePermission, value: 'permission' },
      { label: copy.serviceSystem, value: 'system' },
      { label: copy.serviceFile, value: 'file' },
      { label: copy.serviceMessage, value: 'message' },
      { label: copy.serviceIm, value: 'im' },
      { label: copy.serviceContacts, value: 'contacts' },
      { label: copy.serviceMeeting, value: 'meeting' },
      { label: copy.serviceGateway, value: 'gateway', disabled: true },
      { label: copy.serviceWeb, value: 'web-vue3', disabled: true }
    ],
    validate: (value: unknown) => {
      const selected = new Set(stringArray(value))
      return selected.has('gateway') && selected.has('web-vue3') ? undefined : copy.selectedServicesRequired
    }
  }
}

function portField(name: string, label: string, defaultValue: number, copy: ReturnType<typeof aifarCopy>, min = 1, max = 65535) {
  return {
    name,
    label,
    type: 'number' as const,
    defaultValue,
    required: true,
    min,
    max,
    step: 1,
    validate: (value: unknown) => {
      const port = Number(value)
      return Number.isInteger(port) && port >= min && port <= max ? undefined : copy.portInvalid
    }
  }
}

function percentageField(name: string, label: string, defaultValue: number, copy: ReturnType<typeof aifarCopy>) {
  return {
    name,
    label,
    type: 'number' as const,
    defaultValue,
    required: true,
    min: 1,
    max: 90,
    step: 1,
    validate: (value: unknown, values: AppInstallFieldValues) => {
      const current = Number(value)
      if (!Number.isFinite(current) || current < 1 || current > 90) {
        return labelText(copy, 'numberInvalid', 'Value is outside the allowed range')
      }
      const initial = Number(values.jvmInitialRAMPercentage ?? 20)
      const max = Number(values.jvmMaxRAMPercentage ?? 70)
      if (Number.isFinite(initial) && Number.isFinite(max) && initial > max) {
        return labelText(copy, 'jvmRangeInvalid', 'JVM initial RAM percentage must be less than or equal to max RAM percentage')
      }
      return undefined
    }
  }
}

function labelText(copy: ReturnType<typeof aifarCopy>, key: string, fallback: string) {
  const value = (copy as Record<string, unknown>)[key]
  return typeof value === 'string' && value.trim() ? value : fallback
}

function selectField(
  name: string,
  label: string,
  options: AppInstallFieldOption[],
  defaultValue: string | number | boolean,
  copy: ReturnType<typeof aifarCopy>,
  placeholder?: string,
  required = true
): AppInstallField {
  return {
    name,
    label,
    type: 'select',
    options,
    defaultValue,
    placeholder,
    required,
    validate: required ? (value) => String(value ?? '').trim() ? undefined : copy.textRequired : undefined
  }
}

function sourceIs(name: string, value: string | number | boolean) {
  return (values: AppInstallFieldValues) => values[name] === value
}

function sourceIsNot(name: string, value: string | number | boolean) {
  return (values: AppInstallFieldValues) => values[name] !== value
}

function stringArray(value: unknown) {
  if (Array.isArray(value)) {
    return value.map((item) => String(item ?? '').trim()).filter(Boolean)
  }
  const text = String(value ?? '').trim()
  return text ? [text] : []
}

function credentialOptions(context: AppInstallDialogContext | undefined, kind: string, manualLabel: string): AppInstallFieldOption[] {
  return [
    { label: manualLabel, value: '' },
    ...(context?.credentials ?? [])
      .filter((credential) => credential.kind === kind && credential.status !== 'retired')
      .map((credential) => ({
        label: [credential.name, credential.username, credential.endpoint].filter(Boolean).join(' / '),
        value: credential.id
      }))
  ]
}

function nacosInstanceOptions(context?: AppInstallDialogContext): AppInstallFieldOption[] {
  return (context?.instances ?? [])
    .filter((instance) => instance.app === 'nacos')
    .map((instance) => ({
      label: dependencyLabel(instance, context, 'Nacos'),
      value: instance.id
    }))
}

function dependencyLabel(instance: AppInstanceOption, context: AppInstallDialogContext | undefined, prefix: string, preferredEndpoint?: string) {
  const metadata = parseMetadata(instance.metadata)
  const topology = String(instance.topology || metadata.topology || '').trim()
  const endpoint = String(preferredEndpoint || metadata.endpoint || metadata.clusterEndpoint || metadata.currentMasterEndpoint || '').trim()
  const server = (context?.servers ?? []).find((item) => item.id === instance.serverId)
  const serverText = server ? `${server.name || server.id} (${server.host})` : instance.serverId
  const parts = [prefix]
  if (topology) {
    parts.push(topology)
  }
  if (endpoint) {
    parts.push(endpoint)
  } else if (serverText) {
    parts.push(serverText)
  }
  return parts.join(' / ')
}

function parseMetadata(value?: string) {
  if (!value) {
    return {} as Record<string, unknown>
  }
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' ? parsed as Record<string, unknown> : {}
  } catch {
    return {}
  }
}

function dockerReadyServerIds(context?: AppInstallDialogContext) {
  const out = new Set<string>()
  for (const instance of context?.instances ?? []) {
    if (isDockerReadyInstance(instance)) {
      out.add(instance.serverId || '')
    }
  }
  out.delete('')
  return out
}

function isDockerReadyInstance(instance: AppInstanceOption) {
  if (instance.app !== 'docker' || !instance.serverId) {
    return false
  }
  if (!statusReady(instance.status)) {
    return false
  }
  const metadata = parseMetadata(instance.metadata)
  const lastCheck = metadataRecord(metadata.lastCheck)
  if (!lastCheck) {
    return true
  }
  const checkedStatus = String(lastCheck.status ?? instance.status ?? '').trim()
  if (checkedStatus && !statusReady(checkedStatus)) {
    return false
  }
  return String(lastCheck.dockerVersion ?? '').trim() !== ''
}

function metadataRecord(value: unknown) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function statusReady(value: unknown) {
  return ['installed', 'running', 'available', 'ok', 'success'].includes(String(value ?? '').trim().toLowerCase())
}
