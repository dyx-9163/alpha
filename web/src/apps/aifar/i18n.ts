import { resolveAppLocale, type AppLocale } from '../registry/types'
import type { AppInstallDialogConfig, AppInstallDialogCopy } from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/types'

export type AifarLocale = AppLocale

export const aifarMessages = {
  zh: {
    title: 'AIFAR 服务',
    categoryLabel: '应用服务',
    sourceLabel: 'Docker Compose 离线包',
    description: '基于 resources/aifar/docker-apps 离线包部署 AIFAR 微服务。',
    installTitle: '安装 AIFAR 服务',
    hint: '目标服务器需要先安装 Docker Engine 和 Docker Compose；数据库参数会写入 Nacos 配置。勾选初始化 SQL 时，目标服务器还需要 mysql 客户端。',
    version: '版本',
    versionPlaceholder: '选择 docker-apps 资源包',
    servers: '目标服务器',
    serversPlaceholder: '选择一台已安装 Docker 的服务器',
    noServers: '暂无服务器，请先在服务器工作台添加目标主机。',
    selectedCount: (count: number) => `已选择 ${count} 台服务器`,
    cancel: '取消',
    submit: '开始安装',
    topologySingle: '单服务器',
    timezone: '时区',
    networkName: 'Docker 网络',
    appCPUs: 'CPU 限制',
    appMemoryLimit: '内存限制',
    gatewayPort: 'Gateway 端口',
    webPort: 'Web 端口',
    nacosWebPort: 'Nacos Web 端口',
    nacosApiPort: 'Nacos API 端口',
    nacosUser: 'Nacos 用户',
    nacosPassword: 'Nacos 密码',
    nacosNamespace: 'Nacos 命名空间',
    dbHost: '数据库主机',
    dbPort: '数据库端口',
    dbNameNacos: 'Nacos 数据库',
    dbUser: '数据库用户',
    dbPassword: '数据库密码',
    initSql: '初始化 SQL',
    portInvalid: '端口必须在 1-65535 之间',
    textRequired: '该配置不能为空',
    networkInvalid: 'Docker 网络名不能包含空格'
  },
  en: {
    title: 'AIFAR Service',
    categoryLabel: 'Application',
    sourceLabel: 'Docker Compose bundle',
    description: 'Deploy AIFAR microservices from the resources/aifar/docker-apps offline bundle.',
    installTitle: 'Install AIFAR Service',
    hint: 'Target server must already have Docker Engine and Docker Compose. Database settings are written to Nacos configuration. SQL initialization also requires mysql client on the target server.',
    version: 'Version',
    versionPlaceholder: 'Select docker-apps bundle',
    servers: 'Target server',
    serversPlaceholder: 'Select one Docker-ready server',
    noServers: 'No servers yet. Add target hosts in the server workbench first.',
    selectedCount: (count: number) => `${count} server(s) selected`,
    cancel: 'Cancel',
    submit: 'Start install',
    topologySingle: 'Single server',
    timezone: 'Timezone',
    networkName: 'Docker network',
    appCPUs: 'CPU limit',
    appMemoryLimit: 'Memory limit',
    gatewayPort: 'Gateway port',
    webPort: 'Web port',
    nacosWebPort: 'Nacos web port',
    nacosApiPort: 'Nacos API port',
    nacosUser: 'Nacos user',
    nacosPassword: 'Nacos password',
    nacosNamespace: 'Nacos namespace',
    dbHost: 'Database host',
    dbPort: 'Database port',
    dbNameNacos: 'Nacos database',
    dbUser: 'Database user',
    dbPassword: 'Database password',
    initSql: 'Initialize SQL',
    portInvalid: 'Port must be between 1 and 65535',
    textRequired: 'This value is required',
    networkInvalid: 'Docker network name must not contain whitespace'
  }
}

export function resolveAifarLocale(locale?: string): AifarLocale {
  return resolveAppLocale(locale)
}

export function aifarCopy(locale?: string) {
  return aifarMessages[resolveAifarLocale(locale)]
}

export function aifarTopologies(locale?: string): AppTopologyDefinition[] {
  const copy = aifarCopy(locale)
  return [{ name: 'single', label: copy.topologySingle, targetMode: 'single', minTargets: 1, default: true }]
}

export function aifarInstallDialogProps(locale?: string): AppInstallDialogConfig {
  const copy = aifarCopy(locale)
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
    copy: dialogCopy,
    fields: [
      requiredText('timezone', copy.timezone, 'Asia/Phnom_Penh', copy),
      {
        ...requiredText('networkName', copy.networkName, 'alpha-network', copy),
        validate: (value) => {
          const text = String(value ?? '').trim()
          if (!text) {
            return copy.textRequired
          }
          return /\s/.test(text) ? copy.networkInvalid : undefined
        }
      },
      requiredText('appCPUs', copy.appCPUs, '2.0', copy),
      requiredText('appMemoryLimit', copy.appMemoryLimit, '2GB', copy),
      portField('gatewayPort', copy.gatewayPort, 38000, copy),
      portField('webPort', copy.webPort, 8080, copy),
      portField('nacosWebPort', copy.nacosWebPort, 30099, copy),
      portField('nacosApiPort', copy.nacosApiPort, 31099, copy),
      requiredText('nacosUser', copy.nacosUser, 'nacos', copy),
      {
        ...requiredText('nacosPassword', copy.nacosPassword, 'oversea.nacos', copy),
        type: 'password'
      },
      requiredText('nacosNamespace', copy.nacosNamespace, 'dyx', copy),
      requiredText('dbHost', copy.dbHost, '', copy),
      portField('dbPort', copy.dbPort, 3306, copy),
      requiredText('dbNameNacos', copy.dbNameNacos, 'alpha_cloud_nacos', copy),
      requiredText('dbUser', copy.dbUser, 'root', copy),
      {
        ...requiredText('dbPassword', copy.dbPassword, '', copy),
        type: 'password'
      },
      {
        name: 'initSql',
        label: copy.initSql,
        type: 'switch',
        defaultValue: false
      }
    ]
  }
}

function requiredText(name: string, label: string, defaultValue: string, copy: ReturnType<typeof aifarCopy>) {
  return {
    name,
    label,
    type: 'text' as const,
    defaultValue,
    required: true,
    validate: (value: unknown) => String(value ?? '').trim() ? undefined : copy.textRequired
  }
}

function portField(name: string, label: string, defaultValue: number, copy: ReturnType<typeof aifarCopy>) {
  return {
    name,
    label,
    type: 'number' as const,
    defaultValue,
    required: true,
    min: 1,
    max: 65535,
    step: 1,
    validate: (value: unknown) => {
      const port = Number(value)
      return Number.isInteger(port) && port >= 1 && port <= 65535 ? undefined : copy.portInvalid
    }
  }
}
