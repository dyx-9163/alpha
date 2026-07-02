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
    sourceLabel: 'Docker Compose 离线包',
    description: '基于 resources/aifar/docker-apps 离线包部署 AIFAR 微服务。',
    installTitle: '安装 AIFAR 服务',
    hint: '目标服务器需要先安装 Docker Engine 和 Docker Compose；安装器只连接已部署的 Nacos，业务运行配置请在 Nacos 中维护。',
    version: '版本',
    versionPlaceholder: '选择 docker-apps 资源包',
    servers: '目标服务器',
    serversPlaceholder: '选择一台已安装 Docker Engine 和 Docker Compose 的服务器',
    noServers: '暂无已安装 Docker Engine 和 Docker Compose 的服务器，请先在应用商店安装 Docker 并执行检测。',
    noDockerReadyServers: '暂无已安装 Docker Engine 和 Docker Compose 的服务器，请先在应用商店安装 Docker 并执行检测。',
    selectedCount: (count: number) => `已选择 ${count} 台服务器`,
    cancel: '取消',
    submit: '开始安装',
    topologySingle: '单服务器',
    timezone: '时区',
    networkName: 'Docker 网络',
    appCPUs: 'CPU 限制',
    appMemoryLimit: '内存限制',
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
    sourceLabel: 'Docker Compose bundle',
    description: 'Deploy AIFAR microservices from the resources/aifar/docker-apps offline bundle.',
    installTitle: 'Install AIFAR Service',
    hint: 'Target server must already have Docker Engine and Docker Compose. The installer only connects to deployed Nacos; keep business runtime configuration in Nacos.',
    version: 'Version',
    versionPlaceholder: 'Select docker-apps bundle',
    servers: 'Target server',
    serversPlaceholder: 'Select one Docker Engine + Docker Compose ready server',
    noServers: 'No Docker Engine + Docker Compose ready servers. Install Docker from the app store and run a check first.',
    noDockerReadyServers: 'No Docker Engine + Docker Compose ready servers. Install Docker from the app store and run a check first.',
    selectedCount: (count: number) => `${count} server(s) selected`,
    cancel: 'Cancel',
    submit: 'Start install',
    topologySingle: 'Single server',
    timezone: 'Timezone',
    networkName: 'Docker network',
    appCPUs: 'CPU limit',
    appMemoryLimit: 'Memory limit',
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
        ...requiredText('nacosPassword', copy.nacosPassword, 'oversea.nacos', copy),
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
  return String(lastCheck.dockerVersion ?? '').trim() !== '' && String(lastCheck.composeVersion ?? '').trim() !== ''
}

function metadataRecord(value: unknown) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function statusReady(value: unknown) {
  return ['installed', 'running', 'available', 'ok', 'success'].includes(String(value ?? '').trim().toLowerCase())
}
