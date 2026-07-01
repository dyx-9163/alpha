import { resolveAppLocale, type AppLocale } from '../registry/types'
import type {
  AppInstallDialogConfig,
  AppInstallDialogContext,
  AppInstallFieldOption,
  AppInstallValidationContext,
  ServerOption
} from '../registry/contract'

export type RedisSentinelLocale = AppLocale

export const redisSentinelMessages = {
  zh: {
    title: 'Redis 哨兵',
    categoryLabel: '数据库',
    sourceLabel: '官方源码包',
    description: '一体化部署 Redis Sentinel 高可用：Redis 数据服务默认 1 主多从，所有目标服务器运行 Sentinel。',
    installTitle: '安装 Redis 哨兵',
    hint: '请选择至少 3 台服务器；面板会在所有目标服务器安装 Redis 数据服务和 Sentinel，默认形成 1 主 N 从 + N 个 Sentinel。生产建议 3 台服务器起步。',
    version: '版本',
    versionPlaceholder: '选择版本',
    servers: '目标服务器',
    serversPlaceholder: '选择至少 3 台服务器',
    noServers: '暂无服务器，请先在服务器工作台添加目标主机。',
    selectedCount: (count: number) => `已选择 ${count} 台服务器（1 主 ${Math.max(0, count - 1)} 从 / ${count} 哨兵）`,
    cancel: '取消',
    submit: '开始安装',
    needServers: 'Redis 哨兵高可用至少需要 3 台服务器。',
    port: 'Redis 端口',
    sentinelPort: 'Sentinel 端口',
    sentinelMasterName: '监控组名称',
    sentinelMasterNamePlaceholder: '例如 aifar-master，必须在 Sentinel 内唯一',
    sentinelMasterNameInvalid: '监控组名称只能包含字母、数字、点、横线和下划线，最多 64 个字符',
    sentinelMaster: '初始 Master 节点',
    sentinelMasterPlaceholder: '从已选服务器中选择初始 Master',
    sentinelMasterRequired: '必须从已选服务器中选择 1 个初始 Master 节点',
    sentinelNodesRequired: 'Redis 哨兵模式至少需要选择 3 台目标服务器',
    password: 'Redis 密码',
    passwordPlaceholder: '请输入 Redis 访问密码'
  },
  en: {
    title: 'Redis Sentinel',
    categoryLabel: 'Database',
    sourceLabel: 'Official source archive',
    description: 'Deploy Redis Sentinel high availability in one flow: Redis data services with one master and replicas, plus Sentinel on every target server.',
    installTitle: 'Install Redis Sentinel',
    hint: 'Select at least 3 servers. The panel installs Redis data services and Sentinel on every target, forming 1 master, N replicas, and N Sentinel nodes. Production should start with 3 servers.',
    version: 'Version',
    versionPlaceholder: 'Select version',
    servers: 'Target servers',
    serversPlaceholder: 'Select at least 3 servers',
    noServers: 'No servers yet. Add target hosts in the server workbench first.',
    selectedCount: (count: number) => `${count} server(s) selected (1 master, ${Math.max(0, count - 1)} replica(s), ${count} Sentinel node(s))`,
    cancel: 'Cancel',
    submit: 'Start install',
    needServers: 'Redis Sentinel high availability requires at least 3 servers.',
    port: 'Redis port',
    sentinelPort: 'Sentinel port',
    sentinelMasterName: 'Monitor name',
    sentinelMasterNamePlaceholder: 'For example aifar-master; must be unique inside Sentinel',
    sentinelMasterNameInvalid: 'Monitor name can contain only letters, numbers, dot, dash, and underscore, up to 64 characters',
    sentinelMaster: 'Initial master node',
    sentinelMasterPlaceholder: 'Select the initial master from selected servers',
    sentinelMasterRequired: 'Select one initial master from the selected servers',
    sentinelNodesRequired: 'Redis Sentinel mode requires at least 3 target servers',
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

export function redisSentinelInstallDialogProps(locale?: string): AppInstallDialogConfig {
  const copy = redisSentinelCopy(locale)
  return {
    targetMode: 'multiple',
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
        optionsResolver: (_values, context) => serverOptions(context.selectedServers),
        validate: (value, _values, context) => validateSelectedMaster(value, context, copy)
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
  return context.servers.length >= 3 ? '' : redisSentinelCopy(locale).needServers
}

function validateSelectedMaster(value: unknown, context: AppInstallValidationContext, copy: ReturnType<typeof redisSentinelCopy>) {
  if (context.selectedServers.length < 3) {
    return copy.sentinelNodesRequired
  }
  const selected = stringField(value)
  return selected && context.selectedServers.some((server) => server.id === selected) ? undefined : copy.sentinelMasterRequired
}

function serverOptions(servers: ServerOption[]): AppInstallFieldOption[] {
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

function stringField(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}
