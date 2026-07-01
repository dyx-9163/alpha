import { resolveAppLocale, type AppLocale } from '../registry/types'
import type {
  AppInstallDialogConfig,
  AppInstallDialogContext,
  AppInstallFieldOption,
  AppInstallFieldValues,
  ServerOption
} from '../registry/contract'

export type RedisSentinelLocale = AppLocale

export const redisSentinelMessages = {
  zh: {
    title: 'Redis 哨兵',
    categoryLabel: '数据库',
    sourceLabel: '官方源码包',
    description: '在 Redis 基础服务之上安装并配置 Redis Sentinel 高可用监控。',
    installTitle: '安装 Redis 哨兵',
    hint: '请先安装 Redis 基础服务，再选择 1 个 Master、至少 1 个 Replica，以及至少 3 个 Sentinel 节点。Sentinel 可以与 Master/Replica 同机，也可以部署在独立服务器上。',
    version: '版本',
    versionPlaceholder: '选择版本',
    servers: 'Sentinel 目标服务器',
    serversPlaceholder: '选择运行 Sentinel 的服务器',
    noServers: '暂无服务器，请先在服务器工作台添加目标主机。',
    selectedCount: (count: number) => `已选择 ${count} 台服务器`,
    cancel: '取消',
    submit: '开始安装',
    noRedisBase: '请先安装至少 2 个 Redis 基础服务，再安装 Redis 哨兵。',
    port: 'Redis 端口',
    sentinelPort: 'Sentinel 端口',
    sentinelMasterName: '监控组名称',
    sentinelMasterNamePlaceholder: '例如 aifar-master，必须在 Sentinel 内唯一',
    sentinelMasterNameInvalid: '监控组名称只能包含字母、数字、点、横线和下划线，最多 64 个字符',
    sentinelMaster: 'Redis Master',
    sentinelMasterPlaceholder: '请选择已安装 Redis 基础服务的 Master 节点',
    sentinelMasterRequired: '必须选择 1 个 Redis Master 节点',
    sentinelReplicas: 'Redis Replica',
    sentinelReplicasPlaceholder: '选择一个或多个已安装 Redis 基础服务的 Replica 节点',
    sentinelReplicasRequired: '至少需要选择 1 个 Redis Replica 节点',
    sentinelReplicaCannotIncludeMaster: 'Replica 节点不能包含 Redis Master',
    sentinelNodes: 'Sentinel 节点',
    sentinelNodesPlaceholder: '选择运行 Sentinel 的服务器，可与 Redis 节点同机',
    sentinelNodesRequired: '至少需要选择 3 个 Sentinel 节点',
    password: 'Redis 密码',
    passwordPlaceholder: '请输入 Redis 访问密码'
  },
  en: {
    title: 'Redis Sentinel',
    categoryLabel: 'Database',
    sourceLabel: 'Official source archive',
    description: 'Install and configure Redis Sentinel high availability for Redis base services.',
    installTitle: 'Install Redis Sentinel',
    hint: 'Install Redis base services first, then select exactly one master, at least one replica, and at least three Sentinel nodes. Sentinel can be colocated with Redis nodes or installed on dedicated servers.',
    version: 'Version',
    versionPlaceholder: 'Select version',
    servers: 'Sentinel target servers',
    serversPlaceholder: 'Select servers for Sentinel',
    noServers: 'No servers yet. Add target hosts in the server workbench first.',
    selectedCount: (count: number) => `${count} server(s) selected`,
    cancel: 'Cancel',
    submit: 'Start install',
    noRedisBase: 'Install at least 2 Redis base services before installing Redis Sentinel.',
    port: 'Redis port',
    sentinelPort: 'Sentinel port',
    sentinelMasterName: 'Monitor name',
    sentinelMasterNamePlaceholder: 'For example aifar-master; must be unique inside Sentinel',
    sentinelMasterNameInvalid: 'Monitor name can contain only letters, numbers, dot, dash, and underscore, up to 64 characters',
    sentinelMaster: 'Redis master',
    sentinelMasterPlaceholder: 'Select the Redis base-service node that acts as master',
    sentinelMasterRequired: 'Select exactly one Redis master node',
    sentinelReplicas: 'Redis replicas',
    sentinelReplicasPlaceholder: 'Select one or more Redis base-service nodes as replicas',
    sentinelReplicasRequired: 'Select at least one Redis replica node',
    sentinelReplicaCannotIncludeMaster: 'Replica nodes cannot include the Redis master',
    sentinelNodes: 'Sentinel nodes',
    sentinelNodesPlaceholder: 'Select servers that run Sentinel; they can be colocated with Redis nodes',
    sentinelNodesRequired: 'Select at least 3 Sentinel nodes',
    password: 'Redis password',
    passwordPlaceholder: 'Enter Redis access password'
  }
}

export function resolveRedisSentinelLocale(locale?: string): RedisSentinelLocale {
  return resolveAppLocale(locale)
}

export function redisSentinelCopy(locale?: string) {
  return redisSentinelMessages[resolveRedisSentinelLocale(locale)]
}

export function redisSentinelInstallDialogProps(locale?: string, context?: AppInstallDialogContext): AppInstallDialogConfig {
  const copy = redisSentinelCopy(locale)
  return {
    targetMode: 'multiple',
    hideTargetSelector: true,
    targetCountResolver: (values) => redisSentinelTargetIds(values).length,
    targetIdsResolver: (values) => redisSentinelTargetIds(values),
    copy: {
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
    },
    fields: [
      {
        name: 'port',
        label: copy.port,
        type: 'number',
        defaultValue: 6379,
        required: true
      },
      {
        name: 'sentinelMasterId',
        label: copy.sentinelMaster,
        type: 'select',
        placeholder: copy.sentinelMasterPlaceholder,
        required: true,
        optionsResolver: () => redisDataNodeOptions(context),
        validate: (value) => {
          const selected = stringField(value)
          return selected && redisDataNodeOptions(context).some((item) => item.value === selected) ? undefined : copy.sentinelMasterRequired
        }
      },
      {
        name: 'replicaServerIds',
        label: copy.sentinelReplicas,
        type: 'select',
        multiple: true,
        defaultValue: [],
        required: true,
        placeholder: copy.sentinelReplicasPlaceholder,
        optionsResolver: (values) => redisDataNodeOptions(context).filter((item) => item.value !== stringField(values.sentinelMasterId)),
        validate: (value, values) => {
          const replicas = stringArrayField(value)
          if (!replicas.length) {
            return copy.sentinelReplicasRequired
          }
          return replicas.includes(stringField(values.sentinelMasterId)) ? copy.sentinelReplicaCannotIncludeMaster : undefined
        }
      },
      {
        name: 'sentinelServerIds',
        label: copy.sentinelNodes,
        type: 'select',
        multiple: true,
        defaultValue: [],
        required: true,
        placeholder: copy.sentinelNodesPlaceholder,
        optionsResolver: (_values, validationContext) => serverOptions(context?.servers ?? validationContext.servers),
        validate: (value) => stringArrayField(value).length >= 3 ? undefined : copy.sentinelNodesRequired
      },
      {
        name: 'sentinelPort',
        label: copy.sentinelPort,
        type: 'number',
        defaultValue: 26379,
        required: true
      },
      {
        name: 'masterName',
        label: copy.sentinelMasterName,
        type: 'text',
        defaultValue: 'aifar-master',
        placeholder: copy.sentinelMasterNamePlaceholder,
        required: true,
        validate: (value) => /^[A-Za-z0-9_.-]{1,64}$/.test(String(value ?? '').trim()) ? undefined : copy.sentinelMasterNameInvalid
      },
      {
        name: 'password',
        label: copy.password,
        type: 'password',
        defaultValue: 'Oversea.123',
        placeholder: copy.passwordPlaceholder,
        required: true
      }
    ]
  }
}

export function redisSentinelDeployDisabledReason(locale: string | undefined, context: AppInstallDialogContext) {
  return redisDataNodeOptions(context).length >= 2 ? '' : redisSentinelCopy(locale).noRedisBase
}

function redisDataNodeOptions(context?: AppInstallDialogContext): AppInstallFieldOption[] {
  const serversById = new Map((context?.servers ?? []).map((server) => [server.id, server]))
  const seen = new Set<string>()
  const options: AppInstallFieldOption[] = []
  for (const instance of context?.instances ?? []) {
    if (instance.app !== 'redis' || !instance.serverId || seen.has(instance.serverId)) {
      continue
    }
    const metadata = parseMetadata(instance.metadata)
    const topology = text(instance.topology || metadata.topology).toLowerCase()
    if (topology === 'cluster') {
      continue
    }
    const role = text(metadata.role).toLowerCase()
    if (role === 'sentinel') {
      continue
    }
    seen.add(instance.serverId)
    const server = serversById.get(instance.serverId)
    const roleText = role ? ` / ${role}` : ''
    options.push({
      label: `${serverLabel(server, instance.serverId)}${roleText}`,
      value: instance.serverId
    })
  }
  return options
}

function redisSentinelTargetIds(values: AppInstallFieldValues) {
  const selected = new Set<string>()
  const master = stringField(values.sentinelMasterId)
  if (master) {
    selected.add(master)
  }
  for (const id of stringArrayField(values.replicaServerIds)) {
    selected.add(id)
  }
  for (const id of stringArrayField(values.sentinelServerIds)) {
    selected.add(id)
  }
  return Array.from(selected)
}

function serverOptions(servers: ServerOption[]) {
  return servers.map((server) => ({
    label: serverLabel(server, server.id),
    value: server.id
  }))
}

function serverLabel(server: ServerOption | undefined, fallback: string) {
  if (!server) {
    return fallback
  }
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || fallback
}

function parseMetadata(value?: string) {
  if (!value) {
    return {} as Record<string, unknown>
  }
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {}
  } catch {
    return {}
  }
}

function text(value: unknown) {
  return value === undefined || value === null ? '' : String(value).trim()
}

function stringField(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function stringArrayField(value: unknown) {
  if (!Array.isArray(value)) {
    return []
  }
  return value.map((item) => String(item).trim()).filter(Boolean)
}
