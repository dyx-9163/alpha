import { resolveAppLocale, type AppLocale } from '../registry/types'
import { targetModeResolver, topologySelectField } from '../registry/topology'
import type { AppInstallDialogConfig, AppInstallDialogCopy, AppInstallFieldValues, ServerOption } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/types'

export type RedisLocale = AppLocale

export const redisMessages = {
  zh: {
    title: 'Redis',
    categoryLabel: '数据库',
    sourceLabel: '官方源码包',
    description: '基于离线源码包安装 Redis 单体、Sentinel 或 Cluster 拓扑。',
    installTitle: '安装 Redis',
    hint: 'Sentinel 按官方 master group 模型部署：本次创建一个监控组，Redis Master 只能选 1 台，Replica 可选多台，Sentinel 节点可选多台且建议至少 3 台奇数节点；Sentinel 可与 Master/Replica 同机，也可独立部署。多个 master group 请分别部署，Redis Cluster 才是多主分片拓扑。',
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
    sentinel: 'Sentinel',
    cluster: 'Cluster',
    port: 'Redis 端口',
    sentinelPort: 'Sentinel 端口',
    sentinelMasterName: '监控组名称',
    sentinelMasterNamePlaceholder: '例如 aifar-master，必须在 Sentinel 内唯一',
    sentinelMasterNameInvalid: '监控组名称只能包含字母、数字、点、横线和下划线，最多 64 个字符',
    sentinelMaster: 'Master 节点',
    sentinelMasterPlaceholder: '请选择当前监控组唯一的 Redis master',
    sentinelMasterRequired: 'Sentinel 模式必须选择 Redis master 节点',
    sentinelReplicas: 'Replica 节点',
    sentinelReplicasPlaceholder: '选择一个或多个 Redis replica 节点',
    sentinelReplicasRequired: 'Sentinel 模式至少需要选择 1 个 Redis replica 节点',
    sentinelReplicaCannotIncludeMaster: 'Replica 节点不能包含 Redis master',
    sentinelNodes: 'Sentinel 节点',
    sentinelNodesPlaceholder: '选择运行 Sentinel 的服务器，可与 master/replica 同机',
    sentinelNodesRequired: 'Sentinel 模式至少需要选择 3 个 Sentinel 节点',
    replicas: 'Cluster 副本数',
    password: 'Redis 密码',
    passwordPlaceholder: '请输入 Redis 访问密码'
  },
  en: {
    title: 'Redis',
    categoryLabel: 'Database',
    sourceLabel: 'Official source archive',
    description: 'Build and install Redis standalone, Sentinel, or Cluster topology from the offline source archive.',
    installTitle: 'Install Redis',
    hint: 'Sentinel follows the official master-group model: this install creates one monitored group, exactly one Redis master is selected, replicas can be multiple nodes, and Sentinel can run on multiple nodes with at least 3 odd nodes recommended. Sentinel may be colocated with master/replicas or deployed on dedicated nodes. Deploy separate master groups separately; Redis Cluster is the multi-master sharding topology.',
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
    sentinel: 'Sentinel',
    cluster: 'Cluster',
    port: 'Redis port',
    sentinelPort: 'Sentinel port',
    sentinelMasterName: 'Monitor name',
    sentinelMasterNamePlaceholder: 'For example aifar-master; must be unique inside Sentinel',
    sentinelMasterNameInvalid: 'Monitor name can contain only letters, numbers, dot, dash, and underscore, up to 64 characters',
    sentinelMaster: 'Master node',
    sentinelMasterPlaceholder: 'Select the only Redis master for this monitored group',
    sentinelMasterRequired: 'Sentinel mode requires a Redis master node',
    sentinelReplicas: 'Replica nodes',
    sentinelReplicasPlaceholder: 'Select one or more Redis replica nodes',
    sentinelReplicasRequired: 'Sentinel mode requires at least 1 Redis replica node',
    sentinelReplicaCannotIncludeMaster: 'Replica nodes cannot include the Redis master',
    sentinelNodes: 'Sentinel nodes',
    sentinelNodesPlaceholder: 'Select servers that run Sentinel; they can be colocated with master/replicas',
    sentinelNodesRequired: 'Sentinel mode requires at least 3 Sentinel nodes',
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
    targetCountResolver: (values) => values.topology === 'sentinel' ? countRedisSentinelTargets(values) : 0,
    targetIdsResolver: (values) => values.topology === 'sentinel' ? redisSentinelTargetIds(values) : [],
    copy: dialogCopy,
    fields: [
      topologySelectField(copy.topology, topologies),
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
        visibleWhen: (values) => values.topology === 'sentinel',
        optionsResolver: (_values, context) => context.servers.map((server) => ({
          label: `${server.name} (${server.host})`,
          value: server.id
        })),
        validate: (value, values, context) => {
          if (values.topology !== 'sentinel') {
            return undefined
          }
          const selected = String(value ?? '').trim()
          return selected && context.servers.some((server) => server.id === selected) ? undefined : copy.sentinelMasterRequired
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
        visibleWhen: (values) => values.topology === 'sentinel',
        optionsResolver: (values, context) => serverOptions(context.servers.filter((server) => server.id !== stringField(values.sentinelMasterId))),
        validate: (value, values) => {
          if (values.topology !== 'sentinel') {
            return undefined
          }
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
        visibleWhen: (values) => values.topology === 'sentinel',
        optionsResolver: (_values, context) => serverOptions(context.servers),
        validate: (value, values) => {
          if (values.topology !== 'sentinel') {
            return undefined
          }
          return stringArrayField(value).length >= 3 ? undefined : copy.sentinelNodesRequired
        }
      },
      {
        name: 'sentinelPort',
        label: copy.sentinelPort,
        type: 'number',
        defaultValue: 26379,
        visibleWhen: (values) => values.topology === 'sentinel'
      },
      {
        name: 'masterName',
        label: copy.sentinelMasterName,
        type: 'text',
        defaultValue: 'aifar-master',
        placeholder: copy.sentinelMasterNamePlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'sentinel',
        validate: (value, values) => {
          if (values.topology !== 'sentinel') {
            return undefined
          }
          const name = String(value ?? '').trim()
          return /^[A-Za-z0-9_.-]{1,64}$/.test(name) ? undefined : copy.sentinelMasterNameInvalid
        }
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
        defaultValue: 'Oversea.123',
        placeholder: copy.passwordPlaceholder,
        required: true
      }
    ]
  }
}

function serverOptions(servers: ServerOption[]) {
  return servers.map((server) => ({
    label: `${server.name} (${server.host})`,
    value: server.id
  }))
}

function countRedisSentinelTargets(values: AppInstallFieldValues) {
  return redisSentinelTargetIds(values).length
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

function stringField(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function stringArrayField(value: unknown) {
  if (!Array.isArray(value)) {
    return []
  }
  return value.map((item) => String(item).trim()).filter(Boolean)
}
