import { resolveAppLocale, type AppLocale } from '../registry/types'
import { targetModeResolver, topologySelectField } from '../registry/topology'
import type {
  AppInstallDialogConfig,
  AppInstallDialogContext,
  AppInstallDialogCopy,
  AppInstallField,
  AppInstallFieldOption,
  AppInstallFieldValues,
  AppInstanceOption,
  ServerOption
} from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/types'

export type NacosLocale = AppLocale

export const nacosMessages = {
  zh: {
    title: 'Nacos',
    categoryLabel: 'DevOps',
    sourceLabel: 'resources/nacos 离线包',
    description: '基于 resources/nacos 离线包安装 Nacos，支持单体和 3 节点 Cluster 模式。',
    installTitle: '安装 Nacos',
    hint: '单体和集群模式默认都可以使用本地存储；需要外部持久化时再选择已部署 MySQL 或手动填写 MySQL。',
    version: '版本',
    versionPlaceholder: '选择版本',
    servers: '目标服务器',
    serversPlaceholder: '选择服务器',
    noServers: '暂无服务器，请先在服务器工作台添加目标主机。',
    selectedCount: (count: number) => `已选择 ${count} 台服务器`,
    cancel: '取消',
    submit: '开始安装',
    topology: '拓扑',
    standalone: '单体',
    cluster: 'Cluster 3 节点',
    clusterServers: 'Nacos 节点',
    clusterServersPlaceholder: '选择 3 台 Nacos 节点',
    clusterServersRequired: 'Nacos Cluster 模式必须选择 3 台服务器。',
    port: 'Nacos 端口',
    jvmXms: 'JVM Xms',
    jvmXmx: 'JVM Xmx',
    jvmXmn: 'JVM Xmn',
    nacosCredential: 'Nacos 凭据',
    nacosCredentialPlaceholder: '可选择凭据中心已有 Nacos 凭据',
    nacosCredentialManual: '手动输入 Nacos 账号',
    nacosUser: 'Nacos 用户',
    nacosPassword: 'Nacos 密码',
    dbSource: '存储来源',
    dbSourceLocal: '使用本地存储',
    dbSourceExisting: '选择已部署 MySQL',
    dbSourceManual: '手动填写 MySQL',
    dbInstance: '已部署 MySQL',
    dbInstancePlaceholder: '选择 MySQL 或 MySQL Router 实例',
    noDbInstances: '暂无可选 MySQL 实例',
    dbHost: '数据库主机',
    dbHostPlaceholder: '例如 192.168.74.132',
    dbPort: '数据库端口',
    dbName: '数据库名',
    dbCredential: 'MySQL 凭据',
    dbCredentialPlaceholder: '可选择凭据中心已有 MySQL 凭据',
    dbCredentialManual: '手动输入数据库账号',
    dbUser: '数据库用户',
    dbPassword: '数据库密码',
    initDatabase: '初始化 Nacos SQL',
    portInvalid: '端口必须在 1-65535 之间',
    textRequired: '该配置不能为空'
  },
  en: {
    title: 'Nacos',
    categoryLabel: 'DevOps',
    sourceLabel: 'Offline resources/nacos package',
    description: 'Install Nacos standalone or three-node cluster mode from the offline Nacos package.',
    installTitle: 'Install Nacos',
    hint: 'Standalone and cluster mode can use local storage by default. MySQL is optional when you want external persistence.',
    version: 'Version',
    versionPlaceholder: 'Select version',
    servers: 'Target server',
    serversPlaceholder: 'Select server',
    noServers: 'No servers yet. Add target hosts in the server workbench first.',
    selectedCount: (count: number) => `${count} server(s) selected`,
    cancel: 'Cancel',
    submit: 'Start install',
    topology: 'Topology',
    standalone: 'Standalone',
    cluster: 'Cluster 3 nodes',
    clusterServers: 'Nacos nodes',
    clusterServersPlaceholder: 'Select exactly 3 Nacos nodes',
    clusterServersRequired: 'Nacos cluster mode requires exactly 3 servers.',
    port: 'Nacos port',
    jvmXms: 'JVM Xms',
    jvmXmx: 'JVM Xmx',
    jvmXmn: 'JVM Xmn',
    nacosCredential: 'Nacos credential',
    nacosCredentialPlaceholder: 'Select a Nacos credential from the credential center',
    nacosCredentialManual: 'Enter Nacos account manually',
    nacosUser: 'Nacos user',
    nacosPassword: 'Nacos password',
    dbSource: 'Storage source',
    dbSourceLocal: 'Use local storage',
    dbSourceExisting: 'Use deployed MySQL',
    dbSourceManual: 'Enter MySQL manually',
    dbInstance: 'Deployed MySQL',
    dbInstancePlaceholder: 'Select a MySQL or MySQL Router instance',
    noDbInstances: 'No selectable MySQL instances',
    dbHost: 'Database host',
    dbHostPlaceholder: 'For example 192.168.74.132',
    dbPort: 'Database port',
    dbName: 'Database name',
    dbCredential: 'MySQL credential',
    dbCredentialPlaceholder: 'Select a MySQL credential from the credential center',
    dbCredentialManual: 'Enter database account manually',
    dbUser: 'Database user',
    dbPassword: 'Database password',
    initDatabase: 'Initialize Nacos SQL',
    portInvalid: 'Port must be between 1 and 65535',
    textRequired: 'This value is required'
  }
}

export function resolveNacosLocale(locale?: string): NacosLocale {
  return resolveAppLocale(locale)
}

export function nacosCopy(locale?: string) {
  return nacosMessages[resolveNacosLocale(locale)]
}

export function nacosTopologies(locale?: string): AppTopologyDefinition[] {
  const copy = nacosCopy(locale)
  return [
    { name: 'standalone', label: copy.standalone, targetMode: 'single', minTargets: 1, default: true },
    { name: 'cluster', label: copy.cluster, targetMode: 'multiple', minTargets: 3 }
  ]
}

export function nacosInstallDialogProps(locale?: string, context?: AppInstallDialogContext): AppInstallDialogConfig {
  const copy = nacosCopy(locale)
  const topologies = nacosTopologies(locale)
  const mysqlOptions = mysqlInstanceOptions(context)
  const mysqlSourceDefault = 'local'
  const mysqlSelectOptions = mysqlOptions.length ? mysqlOptions : [{ label: copy.noDbInstances, value: '', disabled: true }]
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
    targetModeResolver: targetModeResolver(topologies),
    hideTargetSelectorResolver: (values) => values.topology === 'cluster',
    targetIdsResolver: (values) => values.topology === 'cluster' ? stringArray(values.nacosServerIds) : [],
    targetCountResolver: (values) => values.topology === 'cluster' ? stringArray(values.nacosServerIds).length : 0,
    copy: dialogCopy,
    fields: [
      topologySelectField(copy.topology, topologies),
      {
        name: 'nacosServerIds',
        label: copy.clusterServers,
        type: 'select',
        multiple: true,
        placeholder: copy.clusterServersPlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'cluster',
        optionsResolver: (_values, context) => serverOptions(context.servers),
        validate: (value) => stringArray(value).length === 3 ? undefined : copy.clusterServersRequired
      },
      {
        ...selectField('nacosCredentialId', copy.nacosCredential, credentialOptions(context, 'nacos', copy.nacosCredentialManual), '', copy, copy.nacosCredentialPlaceholder, false)
      },
      {
        ...requiredText('nacosUser', copy.nacosUser, 'nacos', copy),
        visibleWhen: (values) => !values.nacosCredentialId
      },
      {
        ...requiredText('nacosPassword', copy.nacosPassword, '', copy),
        type: 'password',
        visibleWhen: (values) => !values.nacosCredentialId
      },
      {
        ...selectField('dbSource', copy.dbSource, [
          { label: copy.dbSourceLocal, value: 'local' },
          { label: copy.dbSourceExisting, value: 'existing', disabled: mysqlOptions.length === 0 },
          { label: copy.dbSourceManual, value: 'manual' }
        ], mysqlSourceDefault, copy),
      },
      {
        ...selectField('dbInstanceId', copy.dbInstance, mysqlSelectOptions, mysqlOptions[0]?.value ?? '', copy, copy.dbInstancePlaceholder),
        visibleWhen: sourceIs('dbSource', 'existing')
      },
      {
        ...requiredText('dbHost', copy.dbHost, '', copy, copy.dbHostPlaceholder),
        visibleWhen: sourceIs('dbSource', 'manual')
      },
      {
        ...portField('dbPort', copy.dbPort, 3306, copy),
        visibleWhen: sourceIs('dbSource', 'manual')
      },
      {
        ...selectField('dbCredentialId', copy.dbCredential, credentialOptions(context, 'mysql', copy.dbCredentialManual), '', copy, copy.dbCredentialPlaceholder, false),
        visibleWhen: sourceIsNot('dbSource', 'local')
      },
      {
        ...requiredText('dbUser', copy.dbUser, 'root', copy),
        visibleWhen: allVisible(sourceIsNot('dbSource', 'local'), (values) => !values.dbCredentialId)
      },
      {
        ...requiredText('dbPassword', copy.dbPassword, '', copy),
        type: 'password',
        visibleWhen: allVisible(sourceIsNot('dbSource', 'local'), (values) => !values.dbCredentialId)
      },
      {
        name: 'initDatabase',
        label: copy.initDatabase,
        type: 'switch',
        defaultValue: false,
        visibleWhen: sourceIsNot('dbSource', 'local')
      }
    ]
  }
}

function requiredText(
  name: string,
  label: string,
  defaultValue: string,
  copy: ReturnType<typeof nacosCopy>,
  placeholder?: string
) {
  return {
    name,
    label,
    type: 'text' as const,
    defaultValue,
    placeholder,
    required: true,
    validate: (value: unknown) => String(value ?? '').trim() ? undefined : copy.textRequired
  }
}

function portField(name: string, label: string, defaultValue: number, copy: ReturnType<typeof nacosCopy>) {
  return {
    name,
    label,
    type: 'number' as const,
    defaultValue,
    required: true,
    min: 1,
    max: 65535,
    step: 1,
    validate: (value: unknown) => validPort(value) ? undefined : copy.portInvalid
  }
}

function selectField(
  name: string,
  label: string,
  options: AppInstallFieldOption[],
  defaultValue: string | number | boolean,
  copy: ReturnType<typeof nacosCopy>,
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

function serverOptions(servers: ServerOption[]): AppInstallFieldOption[] {
  return servers.map((server) => ({
    label: serverLabel(server),
    value: server.id
  }))
}

function serverLabel(server: ServerOption) {
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || server.id
}

function sourceIs(name: string, value: string) {
  return (values: AppInstallFieldValues) => values[name] === value
}

function sourceIsNot(name: string, value: string) {
  return (values: AppInstallFieldValues) => values[name] !== value
}

function allVisible(...checks: Array<(values: AppInstallFieldValues) => boolean>) {
  return (values: AppInstallFieldValues) => checks.every((check) => check(values))
}

function mysqlInstanceOptions(context?: AppInstallDialogContext): AppInstallFieldOption[] {
  return (context?.instances ?? [])
    .filter((instance) => instance.app === 'mysql' || instance.app === 'mysql-router')
    .map((instance) => ({
      label: dependencyLabel(instance, context, instance.app === 'mysql-router' ? 'MySQL Router' : 'MySQL'),
      value: instance.id
    }))
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

function dependencyLabel(instance: AppInstanceOption, context: AppInstallDialogContext | undefined, prefix: string) {
  const metadata = parseMetadata(instance.metadata)
  const topology = String(instance.topology || metadata.topology || '').trim()
  const endpoint = String(metadata.endpoint || metadata.clusterEndpoint || '').trim()
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

function validPort(value: unknown) {
  const port = Number(value)
  return Number.isInteger(port) && port >= 1 && port <= 65535
}

function stringArray(value: unknown) {
  if (Array.isArray(value)) {
    return uniqueStrings(value.map((item) => String(item ?? '')))
  }
  if (typeof value === 'string') {
    return uniqueStrings(value.split(','))
  }
  return []
}

function uniqueStrings(values: string[]) {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    const normalized = value.trim()
    if (!normalized || seen.has(normalized)) {
      continue
    }
    seen.add(normalized)
    out.push(normalized)
  }
  return out
}
