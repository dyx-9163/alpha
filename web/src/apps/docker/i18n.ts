import { resolveAppLocale, type AppLocale } from '../registry/types'
import type { AppInstallDialogConfig, AppInstallDialogCopy, AppInstallField, AppInstallValidationContext, ServerOption } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/types'

export type DockerLocale = AppLocale

export const dockerMessages = {
  zh: {
    title: 'Docker Engine + Compose',
    categoryLabel: 'DevOps',
    sourceLabel: '官方二进制包',
    description: '安装 Docker Engine、Docker Compose 和 daemon 远程 API，并登记为 Docker 主机。',
    installTitle: '安装 Docker Engine + Compose',
    batchHint: 'Docker 支持多选服务器，系统会在同一个任务中逐台安装并记录每台服务器日志。',
    version: '版本',
    versionPlaceholder: '选择版本',
    servers: '目标服务器',
    serversPlaceholder: '可多选服务器',
    noServers: '暂无服务器，请先在服务器工作台添加目标主机。',
    selectedCount: (count: number) => `已选择 ${count} 台服务器`,
    bridgeCIDR: 'Docker 网桥网段',
    bridgeCIDRPlaceholder: '例如 172.18.0.1/16',
    bridgeCIDRInvalid: '请输入合法的 IPv4 CIDR，例如 172.18.0.1/16。',
    bridgeCIDRConflict: (server: string, cidr: string) => `网段 ${cidr} 与服务器 ${server} 的宿主机 IP 冲突，请选择其他网段。`,
    remoteAPIPort: '远程 API 端口',
    remoteAPIPortPlaceholder: '默认 2375',
    remoteAPIPortInvalid: '端口必须在 1-65535 之间。',
    cancel: '取消',
    submit: '开始批量安装'
  },
  en: {
    title: 'Docker Engine + Compose',
    categoryLabel: 'DevOps',
    sourceLabel: 'Official binary bundle',
    description: 'Install Docker Engine, Docker Compose, daemon remote API, and register Docker hosts.',
    installTitle: 'Install Docker Engine + Compose',
    batchHint: 'Docker supports selecting multiple servers. A single task installs hosts one by one and records logs for each target.',
    version: 'Version',
    versionPlaceholder: 'Select version',
    servers: 'Target servers',
    serversPlaceholder: 'Select one or more servers',
    noServers: 'No servers yet. Add target hosts in the server workbench first.',
    selectedCount: (count: number) => `${count} server(s) selected`,
    bridgeCIDR: 'Docker bridge CIDR',
    bridgeCIDRPlaceholder: 'For example 172.18.0.1/16',
    bridgeCIDRInvalid: 'Enter a valid IPv4 CIDR, for example 172.18.0.1/16.',
    bridgeCIDRConflict: (server: string, cidr: string) => `CIDR ${cidr} conflicts with host IP of ${server}. Choose another CIDR.`,
    remoteAPIPort: 'Remote API port',
    remoteAPIPortPlaceholder: 'Default 2375',
    remoteAPIPortInvalid: 'Port must be between 1 and 65535.',
    cancel: 'Cancel',
    submit: 'Start batch install'
  }
}

export function resolveDockerLocale(locale?: string): DockerLocale {
  return resolveAppLocale(locale)
}

export function dockerCopy(locale?: string) {
  return dockerMessages[resolveDockerLocale(locale)]
}

export function dockerTopologies(locale?: string): AppTopologyDefinition[] {
  const copy = dockerCopy(locale)
  return [{ name: 'default', label: copy.title, targetMode: 'multiple', minTargets: 1, default: true }]
}

export function dockerInstallDialogProps(locale?: string): AppInstallDialogConfig {
  const copy = dockerCopy(locale)
  const dialogCopy: AppInstallDialogCopy = {
    title: copy.installTitle,
    hint: copy.batchHint,
    versionLabel: copy.version,
    versionPlaceholder: copy.versionPlaceholder,
    serversLabel: copy.servers,
    serversPlaceholder: copy.serversPlaceholder,
    noServers: copy.noServers,
    selectedCount: copy.selectedCount,
    cancel: copy.cancel,
    submit: copy.submit
  }
  const fields: AppInstallField[] = [
    {
      name: 'dockerBridgeCIDR',
      label: copy.bridgeCIDR,
      type: 'text',
      required: true,
      defaultValue: '172.17.0.1/16',
      placeholder: copy.bridgeCIDRPlaceholder,
      validate: dockerBridgeCIDRValidator(copy)
    },
    {
      name: 'remoteAPIPort',
      label: copy.remoteAPIPort,
      type: 'number',
      required: true,
      defaultValue: 2375,
      min: 1,
      max: 65535,
      step: 1,
      placeholder: copy.remoteAPIPortPlaceholder,
      validate: (value) => {
        const port = Number(value)
        return Number.isInteger(port) && port >= 1 && port <= 65535 ? undefined : copy.remoteAPIPortInvalid
      }
    }
  ]
  return {
    targetMode: 'multiple',
    copy: dialogCopy,
    fields
  }
}

function dockerBridgeCIDRValidator(copy: ReturnType<typeof dockerCopy>) {
  return (value: unknown, _values: Record<string, unknown>, context: AppInstallValidationContext) => {
    const cidrText = String(value ?? '').trim()
    const cidr = parseIPv4CIDR(cidrText)
    if (!cidr) {
      return copy.bridgeCIDRInvalid
    }
    const conflict = context.selectedServers.find((server) => {
      const ip = parseIPv4(server.host)
      return ip !== null && isIPv4InCIDR(ip, cidr.network, cidr.mask)
    })
    if (!conflict) {
      return undefined
    }
    return copy.bridgeCIDRConflict(serverLabel(conflict), cidrText)
  }
}

function serverLabel(server: ServerOption) {
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || server.id
}

function parseIPv4CIDR(value: string) {
  const [ipText, prefixText] = value.split('/')
  const ip = parseIPv4(ipText)
  const prefix = Number(prefixText)
  if (ip === null || !Number.isInteger(prefix) || prefix < 1 || prefix > 30) {
    return null
  }
  const mask = (0xffffffff << (32 - prefix)) >>> 0
  return {
    network: (ip & mask) >>> 0,
    mask
  }
}

function parseIPv4(value: string) {
  const parts = value.trim().split('.')
  if (parts.length !== 4) {
    return null
  }
  let out = 0
  for (const part of parts) {
    if (!/^\d+$/.test(part)) {
      return null
    }
    const n = Number(part)
    if (n < 0 || n > 255) {
      return null
    }
    out = ((out << 8) | n) >>> 0
  }
  return out >>> 0
}

function isIPv4InCIDR(ip: number, network: number, mask: number) {
  return ((ip & mask) >>> 0) === network
}
