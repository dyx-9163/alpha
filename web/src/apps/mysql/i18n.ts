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

export type MysqlLocale = AppLocale

export const mysqlMessages = {
  zh: {
    title: 'MySQL',
    categoryLabel: '数据库',
    sourceLabel: '官方二进制包',
    description: '基于离线官方 8.0 二进制包安装 MySQL 单体或 InnoDB Cluster，并可在集群部署时同步安装 MySQL Router。',
    installTitle: '安装 MySQL',
    hint: '单体使用单台服务器；InnoDB Cluster 需要至少 3 台 MySQL 数据节点。MySQL Router 已合并到集群部署中，可选择与数据节点同机，也可选择独立服务器。',
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
    cluster: 'InnoDB Cluster',
    clusterServers: 'MySQL 数据节点',
    clusterServersPlaceholder: '选择至少 3 台 MySQL 数据节点',
    clusterServersRequired: 'InnoDB Cluster 至少需要 3 台 MySQL 数据节点。',
    clusterName: '集群名称',
    installRouter: '同时安装 MySQL Router',
    routerServers: 'Router 节点',
    routerServersPlaceholder: '选择安装 MySQL Router 的服务器，可与数据节点不同',
    routerServersRequired: '安装 MySQL Router 至少需要 1 台目标服务器。',
    routerBasePort: 'Router 起始端口',
    routerBasePortInvalid: 'Router 起始端口需要在 1 到 65532 之间',
    port: 'MySQL 端口',
    rootUser: '管理员账号',
    rootUserPlaceholder: '请输入 MySQL 管理员账号',
    rootPassword: '管理员密码',
    rootPasswordPlaceholder: '请输入 MySQL 管理员密码'
  },
  en: {
    title: 'MySQL',
    categoryLabel: 'Database',
    sourceLabel: 'Official binary bundle',
    description: 'Install MySQL standalone or InnoDB Cluster from the offline official 8.0 binary bundle, with MySQL Router integrated into cluster deployment.',
    installTitle: 'Install MySQL',
    hint: 'Standalone uses one server; InnoDB Cluster requires at least 3 MySQL data nodes. MySQL Router is installed from the same cluster wizard and can run on data nodes or dedicated servers.',
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
    cluster: 'InnoDB Cluster',
    clusterServers: 'MySQL data nodes',
    clusterServersPlaceholder: 'Select at least 3 MySQL data nodes',
    clusterServersRequired: 'InnoDB Cluster requires at least 3 MySQL data nodes.',
    clusterName: 'Cluster name',
    installRouter: 'Install MySQL Router',
    routerServers: 'Router nodes',
    routerServersPlaceholder: 'Select servers for MySQL Router; they may differ from data nodes',
    routerServersRequired: 'MySQL Router install requires at least one target server.',
    routerBasePort: 'Router base port',
    routerBasePortInvalid: 'Router base port must be between 1 and 65532',
    port: 'MySQL port',
    rootUser: 'Admin user',
    rootUserPlaceholder: 'Enter MySQL admin user',
    rootPassword: 'Admin password',
    rootPasswordPlaceholder: 'Enter MySQL admin password'
  }
}

export function resolveMysqlLocale(locale?: string): MysqlLocale {
  return resolveAppLocale(locale)
}

export function mysqlCopy(locale?: string) {
  return mysqlMessages[resolveMysqlLocale(locale)]
}

export function mysqlTopologies(locale?: string): AppTopologyDefinition[] {
  const copy = mysqlCopy(locale)
  return [
    { name: 'standalone', label: copy.standalone, targetMode: 'single', minTargets: 1, default: true },
    { name: 'innodb-cluster', label: copy.cluster, targetMode: 'multiple', minTargets: 3 }
  ]
}

export function mysqlInstallDialogProps(locale?: string): AppInstallDialogConfig {
  const copy = mysqlCopy(locale)
  const topologies = mysqlTopologies(locale)
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
    hideTargetSelectorResolver: (values) => values.topology === 'innodb-cluster',
    targetIdsResolver: (values) => mysqlClusterTargetIds(values),
    targetCountResolver: (values) => mysqlClusterTargetIds(values).length,
    copy: dialogCopy,
    fields: [
      topologySelectField(copy.topology, topologies),
      {
        name: 'mysqlServerIds',
        label: copy.clusterServers,
        type: 'select',
        multiple: true,
        placeholder: copy.clusterServersPlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'innodb-cluster',
        optionsResolver: (_values, context) => serverOptions(context.servers),
        validate: (value) => stringArray(value).length >= 3 ? undefined : copy.clusterServersRequired
      },
      {
        name: 'installRouter',
        label: copy.installRouter,
        type: 'switch',
        defaultValue: true,
        visibleWhen: (values) => values.topology === 'innodb-cluster'
      },
      {
        name: 'routerServerIds',
        label: copy.routerServers,
        type: 'select',
        multiple: true,
        placeholder: copy.routerServersPlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'innodb-cluster' && values.installRouter !== false,
        optionsResolver: (_values, context) => serverOptions(context.servers),
        validate: (value, values) => values.installRouter === false || stringArray(value).length > 0 ? undefined : copy.routerServersRequired
      },
      {
        name: 'rootUser',
        label: copy.rootUser,
        type: 'text',
        defaultValue: 'root',
        placeholder: copy.rootUserPlaceholder,
        required: true
      },
      {
        name: 'rootPassword',
        label: copy.rootPassword,
        type: 'password',
        defaultValue: '',
        placeholder: copy.rootPasswordPlaceholder,
        required: true
      }
    ]
  }
}

function mysqlClusterTargetIds(values: AppInstallFieldValues) {
  if (values.topology !== 'innodb-cluster') {
    return []
  }
  const dataServers = stringArray(values.mysqlServerIds)
  if (values.installRouter === false) {
    return dataServers
  }
  return uniqueStrings([
    ...dataServers,
    ...stringArray(values.routerServerIds)
  ])
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
