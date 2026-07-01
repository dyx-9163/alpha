import { resolveAppLocale, type AppLocale } from '../registry/types'
import { targetModeResolver, topologySelectField } from '../registry/topology'
import type { AppInstallDialogConfig, AppInstallDialogContext, AppInstallDialogCopy } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/types'

export type MinioLocale = AppLocale

export const minioMessages = {
  zh: {
    title: 'MinIO',
    categoryLabel: '对象存储',
    sourceLabel: '官方源码包',
    description: '基于离线源码包安装 MinIO 单体、Bucket 复制容灾或分布式拓扑。',
    installTitle: '安装 MinIO',
    hint: '单体使用单台服务器；Bucket 复制容灾使用 2 台单节点 MinIO 并配置双向异步 Bucket replication；分布式拓扑至少需要 4 台服务器。',
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
    bucketReplication: 'Bucket 复制容灾',
    distributed: '分布式',
    replicationBuckets: '复制 Bucket',
    replicationBucketsPlaceholder: '多个 Bucket 用逗号分隔，例如 aifar, logs',
    replicationBucketsInvalid: 'Bucket 名称只能使用小写字母、数字、点和短横线，长度 3-63，多个名称用逗号分隔',
    replicationPriority: '复制优先级',
    replicationPrioritySlow: '保守',
    replicationPriorityAuto: '均衡',
    replicationPriorityFast: '快速',
    replicationMaxWorkers: '复制并发',
    replicationMaxLargeWorkers: '大文件并发',
    replicateDeletes: '复制删除',
    storageMode: '存储方式',
    storageModeLocal: '直接使用本地目录',
    storageModeUnmounted: '使用未挂载磁盘',
    apiPort: 'API 端口',
    consolePort: '控制台端口',
    dataRoot: '数据目录根路径',
    dataRootPlaceholder: '例如 /aifar/apps/minio/data；本地目录模式直接创建，磁盘模式会作为挂载点',
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
    description: 'Build and install MinIO standalone, bucket replication DR, or distributed topology from the offline source archive.',
    installTitle: 'Install MinIO',
    hint: 'Standalone uses one server; bucket replication DR uses two standalone MinIO nodes with two-way async bucket replication; distributed topology requires at least 4 servers.',
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
    bucketReplication: 'Bucket replication DR',
    distributed: 'Distributed',
    replicationBuckets: 'Replication buckets',
    replicationBucketsPlaceholder: 'Separate multiple buckets with commas, for example aifar, logs',
    replicationBucketsInvalid: 'Bucket names must use lowercase letters, numbers, dots, and hyphens, be 3-63 chars, and be separated by commas',
    replicationPriority: 'Replication priority',
    replicationPrioritySlow: 'Conservative',
    replicationPriorityAuto: 'Balanced',
    replicationPriorityFast: 'Fast',
    replicationMaxWorkers: 'Replication workers',
    replicationMaxLargeWorkers: 'Large object workers',
    replicateDeletes: 'Replicate deletes',
    storageMode: 'Storage mode',
    storageModeLocal: 'Use local directory',
    storageModeUnmounted: 'Use unmounted disk',
    apiPort: 'API port',
    consolePort: 'Console port',
    dataRoot: 'Data root',
    dataRootPlaceholder: 'For example /aifar/apps/minio/data; local mode creates it directly, disk mode uses it as the mount point',
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
    { name: 'bucket-replication', label: copy.bucketReplication, targetMode: 'multiple', minTargets: 2 },
    { name: 'distributed', label: copy.distributed, targetMode: 'multiple', minTargets: 4 }
  ]
}

export function minioInstallDialogProps(locale?: string, context?: AppInstallDialogContext): AppInstallDialogConfig {
  const copy = minioCopy(locale)
  const topologies = minioTopologies(locale)
  const defaultDataRoot = minioDefaultDataRoot(context?.defaultDeployDir)
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
        name: 'replicationBuckets',
        label: copy.replicationBuckets,
        type: 'text',
        defaultValue: 'aifar',
        placeholder: copy.replicationBucketsPlaceholder,
        required: true,
        visibleWhen: (values) => values.topology === 'bucket-replication',
        validate: (value) => validateBucketList(value, copy.replicationBucketsInvalid)
      },
      {
        name: 'replicationPriority',
        label: copy.replicationPriority,
        type: 'select',
        defaultValue: 'slow',
        required: true,
        visibleWhen: (values) => values.topology === 'bucket-replication',
        options: [
          { label: copy.replicationPrioritySlow, value: 'slow' },
          { label: copy.replicationPriorityAuto, value: 'auto' },
          { label: copy.replicationPriorityFast, value: 'fast' }
        ]
      },
      {
        name: 'replicationMaxWorkers',
        label: copy.replicationMaxWorkers,
        type: 'number',
        defaultValue: 8,
        min: 1,
        max: 512,
        step: 1,
        required: true,
        visibleWhen: (values) => values.topology === 'bucket-replication'
      },
      {
        name: 'replicationMaxLargeWorkers',
        label: copy.replicationMaxLargeWorkers,
        type: 'number',
        defaultValue: 1,
        min: 1,
        max: 64,
        step: 1,
        required: true,
        visibleWhen: (values) => values.topology === 'bucket-replication'
      },
      {
        name: 'replicateDeletes',
        label: copy.replicateDeletes,
        type: 'switch',
        defaultValue: false,
        visibleWhen: (values) => values.topology === 'bucket-replication'
      },
      {
        name: 'dataRoot',
        label: copy.dataRoot,
        type: 'text',
        defaultValue: defaultDataRoot,
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

function minioDefaultDataRoot(defaultDeployDir?: string) {
  const deployDir = String(defaultDeployDir || '/aifar/apps').trim()
  if (!deployDir || !deployDir.startsWith('/') || /\s/.test(deployDir)) {
    return '/aifar/apps/minio/data'
  }
  return `${deployDir.replace(/\/+$/, '')}/minio/data`
}

function validateBucketList(value: unknown, message: string) {
  const text = String(value ?? '').trim()
  if (!text) {
    return message
  }
  const names = text.split(/[,\s;]+/).map((item) => item.trim()).filter(Boolean)
  const bucketRe = /^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/
  return names.every((name) => bucketRe.test(name) && !name.includes('..') && !name.includes('.-') && !name.includes('-.'))
    ? undefined
    : message
}
