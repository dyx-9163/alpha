import { resolveAppLocale, type AppLocale } from '../registry/types'
import type {
  AppInstallDialogConfig,
  AppInstallDialogContext,
  AppInstallDialogCopy,
  AppInstallField,
  AppInstallFieldOption,
  AppInstallFieldValues,
  AppInstanceOption
} from '../registry/contract'
import type { AppTopologyDefinition } from '../registry/types'

export type AifarLocale = AppLocale

export const aifarMessages = {
  zh: {
    title: 'AIFAR 服务',
    categoryLabel: '应用服务',
    sourceLabel: 'Docker Compose 离线包',
    description: '基于 resources/aifar/docker-apps 离线包部署 AIFAR 微服务。',
    installTitle: '安装 AIFAR 服务',
    hint: '目标服务器需要先安装 Docker Engine 和 Docker Compose；可选择已部署 MySQL/Redis，连接参数会写入服务环境变量。勾选初始化 SQL 时，目标服务器还需要 mysql 客户端。',
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
    dbSource: 'MySQL 来源',
    dbSourceExisting: '选择已部署 MySQL',
    dbSourceManual: '手动填写 MySQL',
    dbInstance: '已部署 MySQL',
    dbInstancePlaceholder: '选择 MySQL 或 MySQL Router 实例',
    noDbInstances: '暂无可选 MySQL 实例',
    dbHost: '数据库主机',
    dbPort: '数据库端口',
    dbNameNacos: 'Nacos 数据库',
    dbUser: '数据库用户',
    dbPassword: '数据库密码',
    redisSource: 'Redis 来源',
    redisSourceExisting: '选择已部署 Redis',
    redisSourceManual: '手动填写 Redis',
    redisInstance: '已部署 Redis',
    redisInstancePlaceholder: '选择 Redis 实例',
    noRedisInstances: '暂无可选 Redis 实例',
    redisHost: 'Redis 主机',
    redisPort: 'Redis 端口',
    redisPassword: 'Redis 密码',
    redisDatabase: 'Redis 数据库',
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
    hint: 'Target server must already have Docker Engine and Docker Compose. Deployed MySQL/Redis instances can be selected and connection settings are written to service environment variables. SQL initialization also requires mysql client on the target server.',
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
    dbSource: 'MySQL source',
    dbSourceExisting: 'Use deployed MySQL',
    dbSourceManual: 'Enter MySQL manually',
    dbInstance: 'Deployed MySQL',
    dbInstancePlaceholder: 'Select a MySQL or MySQL Router instance',
    noDbInstances: 'No selectable MySQL instances',
    dbHost: 'Database host',
    dbPort: 'Database port',
    dbNameNacos: 'Nacos database',
    dbUser: 'Database user',
    dbPassword: 'Database password',
    redisSource: 'Redis source',
    redisSourceExisting: 'Use deployed Redis',
    redisSourceManual: 'Enter Redis manually',
    redisInstance: 'Deployed Redis',
    redisInstancePlaceholder: 'Select a Redis instance',
    noRedisInstances: 'No selectable Redis instances',
    redisHost: 'Redis host',
    redisPort: 'Redis port',
    redisPassword: 'Redis password',
    redisDatabase: 'Redis database',
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

export function aifarInstallDialogProps(locale?: string, context?: AppInstallDialogContext): AppInstallDialogConfig {
  const copy = aifarCopy(locale)
  const mysqlOptions = mysqlInstanceOptions(context)
  const redisOptions = redisInstanceOptions(context)
  const mysqlSourceDefault = mysqlOptions.length ? 'existing' : 'manual'
  const redisSourceDefault = redisOptions.length ? 'existing' : 'manual'
  const mysqlSelectOptions = mysqlOptions.length ? mysqlOptions : [{ label: copy.noDbInstances, value: '', disabled: true }]
  const redisSelectOptions = redisOptions.length ? redisOptions : [{ label: copy.noRedisInstances, value: '', disabled: true }]
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
        ...requiredText('networkName', copy.networkName, 'aifar-network', copy),
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
      selectField('dbSource', copy.dbSource, [
        { label: copy.dbSourceExisting, value: 'existing', disabled: mysqlOptions.length === 0 },
        { label: copy.dbSourceManual, value: 'manual' }
      ], mysqlSourceDefault, copy),
      {
        ...selectField('dbInstanceId', copy.dbInstance, mysqlSelectOptions, mysqlOptions[0]?.value ?? '', copy, copy.dbInstancePlaceholder),
        visibleWhen: sourceIs('dbSource', 'existing')
      },
      {
        ...requiredText('dbHost', copy.dbHost, '', copy),
        visibleWhen: sourceIsNot('dbSource', 'existing')
      },
      {
        ...portField('dbPort', copy.dbPort, 3306, copy),
        visibleWhen: sourceIsNot('dbSource', 'existing')
      },
      requiredText('dbNameNacos', copy.dbNameNacos, 'aifar_nacos', copy),
      requiredText('dbUser', copy.dbUser, 'root', copy),
      {
        ...requiredText('dbPassword', copy.dbPassword, '', copy),
        type: 'password'
      },
      selectField('redisSource', copy.redisSource, [
        { label: copy.redisSourceExisting, value: 'existing', disabled: redisOptions.length === 0 },
        { label: copy.redisSourceManual, value: 'manual' }
      ], redisSourceDefault, copy),
      {
        ...selectField('redisInstanceId', copy.redisInstance, redisSelectOptions, redisOptions[0]?.value ?? '', copy, copy.redisInstancePlaceholder),
        visibleWhen: sourceIs('redisSource', 'existing')
      },
      {
        ...requiredText('redisHost', copy.redisHost, 'localhost', copy),
        visibleWhen: sourceIsNot('redisSource', 'existing')
      },
      {
        ...portField('redisPort', copy.redisPort, 6379, copy),
        visibleWhen: sourceIsNot('redisSource', 'existing')
      },
      {
        name: 'redisPassword',
        label: copy.redisPassword,
        type: 'password',
        defaultValue: ''
      },
      {
        name: 'redisDatabase',
        label: copy.redisDatabase,
        type: 'number',
        defaultValue: 1,
        required: true,
        min: 0,
        max: 15,
        step: 1,
        validate: (value) => {
          const database = Number(value)
          return Number.isInteger(database) && database >= 0 && database <= 15 ? undefined : copy.textRequired
        }
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

function selectField(
  name: string,
  label: string,
  options: AppInstallFieldOption[],
  defaultValue: string | number | boolean,
  copy: ReturnType<typeof aifarCopy>,
  placeholder?: string
): AppInstallField {
  return {
    name,
    label,
    type: 'select',
    options,
    defaultValue,
    placeholder,
    required: true,
    validate: (value) => String(value ?? '').trim() ? undefined : copy.textRequired
  }
}

function sourceIs(name: string, value: string) {
  return (values: AppInstallFieldValues) => values[name] === value
}

function sourceIsNot(name: string, value: string) {
  return (values: AppInstallFieldValues) => values[name] !== value
}

function mysqlInstanceOptions(context?: AppInstallDialogContext): AppInstallFieldOption[] {
  return (context?.instances ?? [])
    .filter((instance) => instance.app === 'mysql' || instance.app === 'mysql-router')
    .map((instance) => ({
      label: dependencyLabel(instance, context, instance.app === 'mysql-router' ? 'MySQL Router' : 'MySQL'),
      value: instance.id
    }))
}

function redisInstanceOptions(context?: AppInstallDialogContext): AppInstallFieldOption[] {
  return (context?.instances ?? [])
    .filter((instance) => instance.app === 'redis')
    .map((instance) => ({
      label: dependencyLabel(instance, context, 'Redis'),
      value: instance.id
    }))
}

function dependencyLabel(instance: AppInstanceOption, context: AppInstallDialogContext | undefined, prefix: string) {
  const metadata = parseMetadata(instance.metadata)
  const topology = String(instance.topology || metadata.topology || '').trim()
  const endpoint = String(metadata.endpoint || metadata.clusterEndpoint || metadata.currentMasterEndpoint || '').trim()
  const server = (context?.servers ?? []).find((item) => item.id === instance.serverId)
  const serverText = server ? `${server.name || server.id} (${server.host})` : instance.serverId
  const parts = [prefix]
  if (topology) {
    parts.push(topology)
  }
  if (endpoint) {
    parts.push(endpoint)
  } else if (serverText) {
    parts.push(serverText)
  }
  return parts.join(' / ')
}

function parseMetadata(value?: string) {
  if (!value) {
    return {} as Record<string, unknown>
  }
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' ? parsed as Record<string, unknown> : {}
  } catch {
    return {}
  }
}
