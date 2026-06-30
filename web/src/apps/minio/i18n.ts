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
    hint: '单体使用单台服务器；分布式拓扑至少需要 4 台服务器。存储可直接使用本地目录，或按每块磁盘独立挂载点的方式格式化并挂载多块未挂载磁盘。',
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
    storageMode: '存储方式',
    storageModeLocal: '直接使用本地目录',
    storageModeUnmounted: '使用未挂载磁盘',
    apiPort: 'API 端口',
    consolePort: '控制台端口',
    dataRoot: '数据目录根路径',
    dataRootPlaceholder: '例如 /data/minio；本地目录模式直接创建，磁盘模式会作为挂载点',
    dataRootInvalid: '请输入以 / 开头且不包含空格的绝对路径，不能直接填写 /',
    diskDevice: '磁盘设备',
    diskDevicePlaceholder: '请先选择目标服务器，再从检测到的未挂载磁盘中选择一块或多块',
    diskDeviceInvalid: '请为每台目标服务器至少选择一块未挂载磁盘',
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
    hint: 'Standalone uses one server; distributed topology requires at least 4 servers. Storage can use a local directory directly or format and mount one or more unmounted disks with one mount point per disk.',
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
    storageMode: 'Storage mode',
    storageModeLocal: 'Use local directory',
    storageModeUnmounted: 'Use unmounted disk',
    apiPort: 'API port',
    consolePort: 'Console port',
    dataRoot: 'Data root',
    dataRootPlaceholder: 'For example /data/minio; local mode creates it directly, disk mode uses it as the mount point',
    dataRootInvalid: 'Enter an absolute path starting with /, without whitespace, and not / itself',
    diskDevice: 'Disk device',
    diskDevicePlaceholder: 'Select target servers first, then choose one or more detected unmounted disks',
    diskDeviceInvalid: 'Select at least one unmounted disk for each target server',
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
        name: 'storageMode',
        label: copy.storageMode,
        type: 'select',
        defaultValue: 'local-disk',
        required: true,
        options: [
          { label: copy.storageModeLocal, value: 'local-disk' },
          { label: copy.storageModeUnmounted, value: 'unmounted-disk' }
        ]
      },
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
        name: 'dataRoot',
        label: copy.dataRoot,
        type: 'text',
        defaultValue: '/data/minio',
        placeholder: copy.dataRootPlaceholder,
        required: true,
        validate: (value) => {
          const text = String(value ?? '').trim()
          return text && (!text.startsWith('/') || text.replace(/\//g, '') === '' || /\s/.test(text)) ? copy.dataRootInvalid : undefined
        }
      },
      {
        name: 'diskDevice',
        label: copy.diskDevice,
        type: 'server-disk-select',
        placeholder: copy.diskDevicePlaceholder,
        required: true,
        multiple: true,
        visibleWhen: (values) => values.storageMode === 'unmounted-disk',
        validate: (value, _values, context) => {
          if (!context.selectedServers.length) {
            return undefined
          }
          const selected = value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, string | string[]> : {}
          return context.selectedServers.every((server) => {
            const devices = selected[server.id]
            return Array.isArray(devices) ? devices.length > 0 : Boolean(devices)
          }) ? undefined : copy.diskDeviceInvalid
        }
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
        defaultValue: 'Oversea.123',
        placeholder: copy.rootPasswordPlaceholder,
        required: true
      }
    ]
  }
}
