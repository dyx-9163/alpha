import { resolveAppLocale, type AppLocale } from '../registry/locale'
import { targetModeResolver, topologySelectField } from '../registry/topology'
import type { AppInstallDialogConfig, AppInstallDialogCopy } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/model'

export type MinioLocale = AppLocale

export const minioMessages = {
  zh: {
    title: 'MinIO',
    categoryLabel: '对象存储',
    sourceLabel: '官方源码包',
    description: '基于离线源码包安装 MinIO 单体或分布式拓扑。',
    installTitle: '安装 MinIO',
    hint: '单体使用单台服务器；分布式拓扑至少需要 4 台服务器。安装器会构建 MinIO，写入 systemd，再统一配置分布式卷列表。',
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
    distributed: '分布式',
    apiPort: 'API 端口',
    consolePort: '控制台端口',
    rootUser: '管理员账号',
    rootUserPlaceholder: '请输入 MinIO 管理员账号',
    rootPassword: '管理员密码',
    rootPasswordPlaceholder: '请输入 MinIO 管理员密码'
  },
  en: {
    title: 'MinIO',
    categoryLabel: 'Storage',
    sourceLabel: 'Official source archive',
    description: 'Build and install MinIO standalone or distributed topology from the offline source archive.',
    installTitle: 'Install MinIO',
    hint: 'Standalone uses one server; distributed topology requires at least 4 servers. The installer builds MinIO, writes systemd, then configures the distributed volume set.',
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
    distributed: 'Distributed',
    apiPort: 'API port',
    consolePort: 'Console port',
    rootUser: 'Root user',
    rootUserPlaceholder: 'Enter MinIO root user',
    rootPassword: 'Root password',
    rootPasswordPlaceholder: 'Enter MinIO root password'
  }
}

export function resolveMinioLocale(locale?: string): MinioLocale {
  return resolveAppLocale(locale)
}

export function minioCopy(locale?: string) {
  return minioMessages[resolveMinioLocale(locale)]
}

export function minioTopologies(locale?: string): AppTopologyDefinition[] {
  const copy = minioCopy(locale)
  return [
    { name: 'standalone', label: copy.standalone, targetMode: 'single', minTargets: 1, default: true },
    { name: 'distributed', label: copy.distributed, targetMode: 'multiple', minTargets: 4 }
  ]
}

export function minioInstallDialogProps(locale?: string): AppInstallDialogConfig {
  const copy = minioCopy(locale)
  const topologies = minioTopologies(locale)
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
        name: 'apiPort',
        label: copy.apiPort,
        type: 'number',
        defaultValue: 9000,
        required: true
      },
      {
        name: 'consolePort',
        label: copy.consolePort,
        type: 'number',
        defaultValue: 9001,
        required: true
      },
      {
        name: 'rootUser',
        label: copy.rootUser,
        type: 'text',
        defaultValue: 'admin',
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
