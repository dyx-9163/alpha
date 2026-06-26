import { resolveAppLocale, type AppLocale } from '../registry/locale'
import { targetModeResolver, topologySelectField } from '../registry/topology'
import type { AppInstallDialogConfig, AppInstallDialogCopy } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/model'

export type MysqlLocale = AppLocale

export const mysqlMessages = {
  zh: {
    title: 'MySQL',
    categoryLabel: '数据库',
    sourceLabel: '官方二进制包',
    description: '基于离线官方 8.0 二进制包安装 MySQL 单体或 InnoDB Cluster。',
    installTitle: '安装 MySQL',
    hint: '单体使用单台服务器；InnoDB Cluster 需要多台服务器，并要求目标环境可用 mysqlsh。安装器会先安装基础 MySQL，再在第一台服务器执行集群初始化。',
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
    clusterName: '集群名称',
    port: '端口',
    rootUser: '管理员账号',
    rootUserPlaceholder: '请输入 MySQL 管理员账号',
    rootPassword: '管理员密码',
    rootPasswordPlaceholder: '请输入 MySQL 管理员密码'
  },
  en: {
    title: 'MySQL',
    categoryLabel: 'Database',
    sourceLabel: 'Official binary bundle',
    description: 'Install MySQL standalone or InnoDB Cluster from the offline official 8.0 binary bundle.',
    installTitle: 'Install MySQL',
    hint: 'Standalone uses one server; InnoDB Cluster requires multiple servers and mysqlsh on the target environment. The installer installs base MySQL first, then bootstraps the cluster from the first server.',
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
    clusterName: 'Cluster name',
    port: 'Port',
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
    copy: dialogCopy,
    fields: [
      topologySelectField(copy.topology, topologies),
      {
        name: 'clusterName',
        label: copy.clusterName,
        type: 'text',
        defaultValue: 'aifarCluster'
      },
      {
        name: 'port',
        label: copy.port,
        type: 'number',
        defaultValue: 3306,
        required: true
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
        placeholder: copy.rootPasswordPlaceholder,
        required: true
      }
    ]
  }
}
