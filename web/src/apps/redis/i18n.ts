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
    hint: '单体使用单台服务器；Sentinel 和 Cluster 需要多台服务器。安装器会上传源码包和 RPM 缓存，远程编译，写入 systemd，并在健康检查通过后记录实例。',
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
    sentinelMaster: 'Sentinel 主节点',
    sentinelMasterPlaceholder: '请选择 Redis 主节点；其他服务器作为从节点，所有节点运行 Sentinel',
    sentinelMasterRequired: 'Sentinel 模式必须选择主节点',
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
    hint: 'Standalone uses one server; Sentinel and Cluster require multiple servers. The installer uploads the source archive and RPM cache, builds Redis remotely, writes systemd, and records instances after health checks pass.',
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
    sentinelMaster: 'Sentinel master',
    sentinelMasterPlaceholder: 'Select the Redis master; other servers become replicas and every selected server runs Sentinel',
    sentinelMasterRequired: 'Sentinel mode requires a master server',
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
        defaultValue: 26379
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
        defaultValue: 2
      },
      {
        name: 'replicas',
        label: copy.replicas,
        type: 'number',
        defaultValue: 0
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
