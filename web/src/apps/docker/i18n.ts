import { resolveAppLocale, type AppLocale } from '../registry/locale'
import type { AppInstallDialogConfig, AppInstallDialogCopy } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/model'

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
  return {
    targetMode: 'multiple',
    copy: dialogCopy,
    fields: []
  }
}
