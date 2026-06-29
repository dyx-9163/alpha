import { resolveAppLocale, type AppLocale } from '../registry/locale'
import { targetModeResolver, topologySelectField } from '../registry/topology'
import type { AppInstallDialogConfig, AppInstallDialogCopy } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/model'

export type RedisLocale = AppLocale

export const redisMessages = {
  zh: {
    title: 'Redis',
    categoryLabel: '数据库',
    sourceLabel: '官方源码包',
    description: '基于离线源码包安装 Redis 单体、Sentinel 或 Cluster 拓扑。',
    installTitle: '安装 Redis',
    hint: 'Sentinel 按官方 master group 模型部署：本次创建一个监控组，选择一个 Redis master，其余节点作为 replica，所有选中服务器运行 Sentinel；多个 master group 请分别部署。Redis Cluster 才是多主分片拓扑。',
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
    sentinelMaster: 'Redis Master 节点',
    sentinelMasterPlaceholder: '请选择当前监控组的 Redis master；其他服务器作为 replica，所有节点运行 Sentinel',
    sentinelMasterRequired: 'Sentinel 模式必须选择 Redis master 节点',
    quorum: 'Sentinel Quorum',
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
    hint: 'Sentinel follows the official master-group model: this install creates one monitored master group, one Redis master is selected, the other nodes become replicas, and every selected server runs Sentinel. Deploy separate master groups separately. Redis Cluster is the multi-master sharding topology.',
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
    sentinelMaster: 'Redis master node',
    sentinelMasterPlaceholder: 'Select the Redis master for this monitored group; other servers become replicas and every selected server runs Sentinel',
    sentinelMasterRequired: 'Sentinel mode requires a Redis master node',
    quorum: 'Sentinel quorum',
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
        name: 'sentinelMasterId',
        label: copy.sentinelMaster,
        type: 'select',
        placeholder: copy.sentinelMasterPlaceholder,
        visibleWhen: (values) => values.topology === 'sentinel',
        optionsResolver: (_values, context) => context.selectedServers.map((server) => ({
          label: `${server.name} (${server.host})`,
          value: server.id
        })),
        validate: (value, values, context) => {
          if (values.topology !== 'sentinel') {
            return undefined
          }
          const selected = String(value ?? '').trim()
          return selected && context.selectedServers.some((server) => server.id === selected) ? undefined : copy.sentinelMasterRequired
        }
      },
      {
        name: 'quorum',
        label: copy.quorum,
        type: 'number',
        defaultValue: 2,
        visibleWhen: (values) => values.topology === 'sentinel'
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
