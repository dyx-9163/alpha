import { resolveAppLocale, type AppLocale } from '../registry/types'
import { targetModeResolver, topologySelectField } from '../registry/topology'
import type { AppInstallDialogConfig, AppInstallDialogCopy } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/types'

export type RedisLocale = AppLocale

export const redisMessages = {
  zh: {
    title: 'Redis',
    categoryLabel: '数据库',
    sourceLabel: '官方源码包',
    description: '基于离线源码包安装 Redis 基础服务，支持单体和 Cluster 拓扑。',
    installTitle: '安装 Redis',
    hint: '这里安装普通 Redis 单体或 Redis Cluster。需要主从高可用时，请直接使用 Redis 哨兵安装入口。',
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
    cluster: 'Cluster',
    port: 'Redis 端口',
    replicas: 'Cluster 副本数',
    password: 'Redis 密码',
    passwordPlaceholder: '请输入 Redis 访问密码'
  },
  en: {
    title: 'Redis',
    categoryLabel: 'Database',
    sourceLabel: 'Official source archive',
    description: 'Build and install Redis base services for standalone or Cluster topology from the offline source archive.',
    installTitle: 'Install Redis',
    hint: 'Install regular Redis standalone or Redis Cluster here. For master-replica high availability, use the Redis Sentinel installer directly.',
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
    cluster: 'Cluster',
    port: 'Redis port',
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
