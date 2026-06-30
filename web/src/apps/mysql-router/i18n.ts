import { resolveAppLocale, type AppLocale } from '../registry/types'
import type { AppInstallDialogConfig, AppInstallDialogContext, AppInstallFieldOption } from '../registry/contract'

export type MysqlRouterLocale = AppLocale

type ClusterOption = {
  id: string
  name: string
  endpoint: string
  nodes: number
  rootUser: string
}

export const mysqlRouterMessages = {
  zh: {
    title: 'MySQL Router',
    categoryLabel: '数据库',
    sourceLabel: 'MySQL 官方离线包',
    description: '在已有 MySQL InnoDB Cluster 上安装 Router，提供稳定的读写和只读访问端口。',
    installTitle: '安装 MySQL Router',
    hint: '需要先完成 MySQL InnoDB Cluster 部署；Router 会从所选集群 bootstrap，并安装到选中的 Router 目标服务器。',
    version: '版本',
    versionPlaceholder: '选择版本',
    servers: 'Router 目标服务器',
    serversPlaceholder: '选择安装 Router 的服务器',
    noServers: '暂无服务器，请先在服务器工作台添加目标主机。',
    selectedCount: (count: number) => `已选择 ${count} 个 Router 目标`,
    cancel: '取消',
    submit: '开始安装',
    cluster: 'InnoDB Cluster',
    clusterPlaceholder: '选择已有 MySQL InnoDB Cluster',
    noCluster: '需要先部署至少一个 MySQL InnoDB Cluster，才能安装 MySQL Router',
    basePort: '起始端口',
    basePortInvalid: '起始端口需要在 1 到 65532 之间',
    rootUser: '集群账号',
    rootUserPlaceholder: '用于 Router bootstrap 的 MySQL 账号',
    rootPassword: '集群密码',
    rootPasswordPlaceholder: '用于 Router bootstrap 的 MySQL 密码'
  },
  en: {
    title: 'MySQL Router',
    categoryLabel: 'Database',
    sourceLabel: 'MySQL official bundle',
    description: 'Install MySQL Router for an existing MySQL InnoDB Cluster and expose stable read-write and read-only ports.',
    installTitle: 'Install MySQL Router',
    hint: 'Requires an existing MySQL InnoDB Cluster. The Router bootstraps from the selected cluster and is installed on the selected Router target servers.',
    version: 'Version',
    versionPlaceholder: 'Select version',
    servers: 'Router target servers',
    serversPlaceholder: 'Select servers for MySQL Router',
    noServers: 'No servers yet. Add target hosts in the server workbench first.',
    selectedCount: (count: number) => `${count} Router target(s) selected`,
    cancel: 'Cancel',
    submit: 'Start install',
    cluster: 'InnoDB Cluster',
    clusterPlaceholder: 'Select an existing MySQL InnoDB Cluster',
    noCluster: 'Deploy at least one MySQL InnoDB Cluster before installing MySQL Router',
    basePort: 'Base port',
    basePortInvalid: 'Base port must be between 1 and 65532',
    rootUser: 'Cluster user',
    rootUserPlaceholder: 'MySQL user for Router bootstrap',
    rootPassword: 'Cluster password',
    rootPasswordPlaceholder: 'MySQL password for Router bootstrap'
  }
}

export function resolveMysqlRouterLocale(locale?: string): MysqlRouterLocale {
  return resolveAppLocale(locale)
}

export function mysqlRouterCopy(locale?: string) {
  return mysqlRouterMessages[resolveMysqlRouterLocale(locale)]
}

export function mysqlRouterInstallDialogProps(locale?: string, context?: AppInstallDialogContext): AppInstallDialogConfig {
  const copy = mysqlRouterCopy(locale)
  const clusters = mysqlInnoDBClusters(context)
  const defaultCluster = clusters[0]
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
        name: 'clusterId',
        label: copy.cluster,
        type: 'select',
        defaultValue: defaultCluster?.id,
        placeholder: copy.clusterPlaceholder,
        required: true,
        options: clusters.map(clusterOptionFor),
        validate: (value) => {
          if (!clusters.length) {
            return copy.noCluster
          }
          return value ? undefined : copy.clusterPlaceholder
        }
      },
      {
        name: 'basePort',
        label: copy.basePort,
        type: 'number',
        defaultValue: 6446,
        min: 1,
        max: 65532,
        required: true,
        validate: (value) => {
          const port = Number(value)
          return Number.isInteger(port) && port >= 1 && port <= 65532 ? undefined : copy.basePortInvalid
        }
      },
      {
        name: 'rootUser',
        label: copy.rootUser,
        type: 'text',
        defaultValue: defaultCluster?.rootUser || 'root',
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

export function mysqlRouterDeployDisabledReason(locale: string | undefined, context: AppInstallDialogContext) {
  return mysqlInnoDBClusters(context).length ? '' : mysqlRouterCopy(locale).noCluster
}

function mysqlInnoDBClusters(context?: AppInstallDialogContext): ClusterOption[] {
  const groups = new Map<string, ClusterOption>()
  const order: string[] = []
  for (const instance of context?.instances ?? []) {
    if (instance.app !== 'mysql') {
      continue
    }
    const metadata = parseMetadata(instance.metadata)
    const topology = String(instance.topology || metadata.topology || '').trim().toLowerCase()
    if (topology !== 'innodb-cluster') {
      continue
    }
    const id = text(metadata.clusterId) || text(metadata.clusterName) || instance.id
    if (!groups.has(id)) {
      groups.set(id, {
        id,
        name: text(metadata.clusterName) || id,
        endpoint: '',
        nodes: 0,
        rootUser: text(metadata.rootUser) || 'root'
      })
      order.push(id)
    }
    const cluster = groups.get(id)
    if (!cluster) {
      continue
    }
    cluster.nodes += 1
    cluster.name = text(metadata.clusterName) || cluster.name
    cluster.rootUser = text(metadata.rootUser) || cluster.rootUser
    const primaryEndpoint = text(metadata.currentPrimaryEndpoint) || text(metadata.primaryEndpoint)
    if (primaryEndpoint) {
      cluster.endpoint = primaryEndpoint
    } else if (!cluster.endpoint) {
      cluster.endpoint = text(metadata.endpoint)
    }
  }
  return order.map((id) => groups.get(id)).filter((cluster): cluster is ClusterOption => Boolean(cluster))
}

function clusterOptionFor(cluster: ClusterOption): AppInstallFieldOption {
  const detail = [cluster.nodes ? `${cluster.nodes} nodes` : '', cluster.endpoint].filter(Boolean).join(' / ')
  return {
    label: detail ? `${cluster.name} (${detail})` : cluster.name,
    value: cluster.id
  }
}

function parseMetadata(value?: string) {
  if (!value) {
    return {} as Record<string, unknown>
  }
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {}
  } catch {
    return {}
  }
}

function text(value: unknown) {
  if (value === undefined || value === null) {
    return ''
  }
  return String(value).trim()
}
