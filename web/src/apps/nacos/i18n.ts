import { resolveAppLocale, type AppLocale } from '../registry/types'
import { targetModeResolver, topologySelectField } from '../registry/topology'
import type {
  AppInstallDialogConfig,
  AppInstallDialogCopy,
  AppInstallFieldOption,
  AppInstallFieldValues,
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
    hint: '单体模式使用内置存储；Cluster 模式固定选择 3 台 Nacos 节点，并需要填写外部 MySQL 数据库连接信息。',
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
    dbHost: '数据库主机',
    dbHostPlaceholder: '例如 192.168.74.132',
    dbPort: '数据库端口',
    dbName: '数据库名',
    dbUser: '数据库用户',
    dbPassword: '数据库密码',
    initDatabase: '初始化 Nacos SQL'
  },
  en: {
    title: 'Nacos',
    categoryLabel: 'DevOps',
    sourceLabel: 'Offline resources/nacos package',
    description: 'Install Nacos standalone or three-node cluster mode from the offline Nacos package.',
    installTitle: 'Install Nacos',
    hint: 'Standalone mode uses embedded storage. Cluster mode always uses exactly 3 Nacos nodes and requires an external MySQL database connection.',
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
    dbHost: 'Database host',
    dbHostPlaceholder: 'For example 192.168.74.132',
    dbPort: 'Database port',
    dbName: 'Database name',
    dbUser: 'Database user',
    dbPassword: 'Database password',
    initDatabase: 'Initialize Nacos SQL'
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

export function nacosInstallDialogProps(locale?: string): AppInstallDialogConfig {
  const copy = nacosCopy(locale)
  const topologies = nacosTopologies(locale)
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
        name: 'port',
        label: copy.port,
        type: 'number',
        defaultValue: 8848,
        min: 1,
        max: 65535,
        required: true
      },
      {
        name: 'jvmXms',
        label: copy.jvmXms,
        type: 'text',
        defaultValue: '512m',
        required: true
      },
      {
        name: 'jvmXmx',
        label: copy.jvmXmx,
        type: 'text',
        defaultValue: '512m',
        required: true
      },
      {
        name: 'jvmXmn',
        label: copy.jvmXmn,
        type: 'text',
        defaultValue: '256m',
        required: true
      },
      {
        name: 'dbHost',
        label: copy.dbHost,
        type: 'text',
        placeholder: copy.dbHostPlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'cluster'
      },
      {
        name: 'dbPort',
        label: copy.dbPort,
        type: 'number',
        defaultValue: 3306,
        min: 1,
        max: 65535,
        required: true,
        visibleWhen: (values) => values.topology === 'cluster'
      },
      {
        name: 'dbName',
        label: copy.dbName,
        type: 'text',
        defaultValue: 'nacos_config',
        required: true,
        visibleWhen: (values) => values.topology === 'cluster'
      },
      {
        name: 'dbUser',
        label: copy.dbUser,
        type: 'text',
        defaultValue: 'nacos',
        required: true,
        visibleWhen: (values) => values.topology === 'cluster'
      },
      {
        name: 'dbPassword',
        label: copy.dbPassword,
        type: 'password',
        required: true,
        visibleWhen: (values) => values.topology === 'cluster'
      },
      {
        name: 'initDatabase',
        label: copy.initDatabase,
        type: 'switch',
        defaultValue: false,
        visibleWhen: (values) => values.topology === 'cluster'
      }
    ]
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
