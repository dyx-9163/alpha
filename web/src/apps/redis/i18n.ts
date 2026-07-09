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

export type RedisLocale = AppLocale

export const redisMessages = {
  zh: {
    title: 'Redis',
    categoryLabel: '数据库',
    sourceLabel: '官方源码包',
    description: '基于离线源码包安装 Redis，支持单体、Sentinel 高可用和 Cluster 拓扑。',
    installTitle: '安装 Redis',
    hint: '单体、Sentinel 高可用和 Cluster 在这里统一安装。Sentinel 可把 Redis 数据节点和 Sentinel 节点分开选择，也可以选择同一批服务器形成 1 主 2 从 + 3 哨兵。',
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
    sentinel: 'Sentinel 高可用',
    cluster: 'Cluster',
    dataServers: 'Redis 数据节点',
    dataServersPlaceholder: '选择至少 3 台 Redis 数据节点（1 主 2 从）',
    dataServersRequired: 'Redis Sentinel 高可用至少需要 3 台 Redis 数据节点。',
    sentinelServers: 'Sentinel 节点',
    sentinelServersPlaceholder: '选择至少 3 台 Sentinel 节点，可与数据节点不同',
    sentinelServersRequired: 'Redis Sentinel 高可用至少需要 3 台 Sentinel 节点。',
    sentinelMaster: '初始 Master 节点',
    sentinelMasterPlaceholder: '从 Redis 数据节点中选择初始 Master',
    sentinelMasterRequired: '必须从 Redis 数据节点中选择 1 个初始 Master 节点。',
    sentinelMasterName: '监控组名称',
    sentinelMasterNamePlaceholder: '例如 aifar-master，必须在 Sentinel 内唯一',
    sentinelMasterNameInvalid: '监控组名称只能包含字母、数字、点、横线和下划线，最多 64 个字符',
    port: 'Redis 端口',
    sentinelPort: 'Sentinel 端口',
    replicas: 'Cluster 副本数',
    password: 'Redis 密码',
    passwordPlaceholder: '请输入 Redis 访问密码'
  },
  en: {
    title: 'Redis',
    categoryLabel: 'Database',
    sourceLabel: 'Official source archive',
    description: 'Build and install Redis for standalone, Sentinel HA, or Cluster topology from the offline source archive.',
    installTitle: 'Install Redis',
    hint: 'Install standalone, Sentinel high availability, or Cluster Redis here. Sentinel can use separate Redis data nodes and Sentinel nodes, or the same three servers for 1 master, 2 replicas, and 3 Sentinels.',
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
    sentinel: 'Sentinel HA',
    cluster: 'Cluster',
    dataServers: 'Redis data nodes',
    dataServersPlaceholder: 'Select at least 3 Redis data nodes (1 master, 2 replicas)',
    dataServersRequired: 'Redis Sentinel HA requires at least 3 Redis data nodes.',
    sentinelServers: 'Sentinel nodes',
    sentinelServersPlaceholder: 'Select at least 3 Sentinel nodes; they may differ from data nodes',
    sentinelServersRequired: 'Redis Sentinel HA requires at least 3 Sentinel nodes.',
    sentinelMaster: 'Initial master node',
    sentinelMasterPlaceholder: 'Select the initial master from Redis data nodes',
    sentinelMasterRequired: 'Select one initial master from the Redis data nodes.',
    sentinelMasterName: 'Monitor name',
    sentinelMasterNamePlaceholder: 'For example aifar-master; must be unique inside Sentinel',
    sentinelMasterNameInvalid: 'Monitor name can contain only letters, numbers, dot, dash, and underscore, up to 64 characters',
    port: 'Redis port',
    sentinelPort: 'Sentinel port',
    replicas: 'Cluster replicas',
    password: 'Redis password',
    passwordPlaceholder: 'Enter Redis access password'
  }
}

export function resolveRedisLocale(locale?: string): RedisLocale {
  return resolveAppLocale(locale)
}

export function redisCopy(locale?: string) {
  return redisMessages[resolveRedisLocale(locale)]
}

export function redisTopologies(locale?: string): AppTopologyDefinition[] {
  const copy = redisCopy(locale)
  return [
    { name: 'standalone', label: copy.standalone, targetMode: 'single', minTargets: 1, default: true },
    { name: 'sentinel', label: copy.sentinel, targetMode: 'multiple', minTargets: 3 },
    { name: 'cluster', label: copy.cluster, targetMode: 'multiple', minTargets: 3 }
  ]
}

export function redisInstallDialogProps(locale?: string): AppInstallDialogConfig {
  const copy = redisCopy(locale)
  const topologies = redisTopologies(locale)
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
    hideTargetSelectorResolver: (values) => values.topology === 'sentinel',
    targetIdsResolver: (values) => sentinelTargetIds(values),
    targetCountResolver: (values) => sentinelTargetIds(values).length,
    copy: dialogCopy,
    fields: [
      topologySelectField(copy.topology, topologies),
      {
        name: 'redisDataServerIds',
        label: copy.dataServers,
        type: 'select',
        multiple: true,
        placeholder: copy.dataServersPlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'sentinel',
        optionsResolver: (_values, context) => serverOptions(context.servers),
        validate: (value) => stringArray(value).length >= 3 ? undefined : copy.dataServersRequired
      },
      {
        name: 'sentinelMasterId',
        label: copy.sentinelMaster,
        type: 'select',
        placeholder: copy.sentinelMasterPlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'sentinel',
        optionsResolver: (values, context) => selectedServerOptions(values.redisDataServerIds, context.servers),
        validate: (value, values) => validateSelectedMaster(value, values, copy)
      },
      {
        name: 'sentinelServerIds',
        label: copy.sentinelServers,
        type: 'select',
        multiple: true,
        placeholder: copy.sentinelServersPlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'sentinel',
        optionsResolver: (_values, context) => serverOptions(context.servers),
        validate: (value) => stringArray(value).length >= 3 ? undefined : copy.sentinelServersRequired
      },
      {
        name: 'replicas',
        label: copy.replicas,
        type: 'number',
        defaultValue: 0,
        visibleWhen: (values) => values.topology === 'cluster'
      },
      {
        name: 'password',
        label: copy.password,
        type: 'password',
        defaultValue: '',
        placeholder: copy.passwordPlaceholder,
        required: true
      }
    ]
  }
}

function validateSelectedMaster(value: unknown, values: AppInstallFieldValues, copy: ReturnType<typeof redisCopy>) {
  const selected = stringValue(value)
  const dataServerIds = stringArray(values.redisDataServerIds)
  return selected && dataServerIds.includes(selected) ? undefined : copy.sentinelMasterRequired
}

function sentinelTargetIds(values: AppInstallFieldValues) {
  if (values.topology !== 'sentinel') {
    return []
  }
  return uniqueStrings([
    ...stringArray(values.redisDataServerIds),
    ...stringArray(values.sentinelServerIds)
  ])
}

function selectedServerOptions(value: unknown, servers: ServerOption[]): AppInstallFieldOption[] {
  const selected = new Set(stringArray(value))
  return serverOptions(servers.filter((server) => selected.has(server.id)))
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

function stringValue(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
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
