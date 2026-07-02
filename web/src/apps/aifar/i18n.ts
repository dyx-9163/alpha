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
    hint: '目标服务器需要先安装 Docker Engine 和 Docker Compose；可选择已部署 Nacos/MySQL/Redis/MinIO，连接参数会写入服务环境变量。勾选初始化 SQL 时，目标服务器还需要 mysql 客户端。',
    version: '版本',
    versionPlaceholder: '选择 docker-apps 资源包',
    servers: '目标服务器',
    serversPlaceholder: '选择一台已安装 Docker Engine 和 Docker Compose 的服务器',
    noServers: '暂无已安装 Docker Engine 和 Docker Compose 的服务器，请先在应用商店安装 Docker 并执行检测。',
    noDockerReadyServers: '暂无已安装 Docker Engine 和 Docker Compose 的服务器，请先在应用商店安装 Docker 并执行检测。',
    selectedCount: (count: number) => `已选择 ${count} 台服务器`,
    cancel: '取消',
    submit: '开始安装',
    topologySingle: '单服务器',
    timezone: '时区',
    networkName: 'Docker 网络',
    appCPUs: 'CPU 限制',
    appMemoryLimit: '内存限制',
    nacosSource: 'Nacos 来源',
    nacosSourceExisting: '选择已部署 Nacos',
    nacosSourceManual: '手动填写 Nacos',
    nacosInstance: '已部署 Nacos',
    nacosInstancePlaceholder: '选择 Nacos 实例',
    noNacosInstances: '暂无可选 Nacos 实例',
    nacosHost: 'Nacos 主机',
    nacosPort: 'Nacos 端口',
    nacosCredential: 'Nacos 凭据',
    nacosCredentialPlaceholder: '可选择凭据中心已有 Nacos 凭据',
    nacosCredentialManual: '手动输入 Nacos 账号',
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
    dbCredential: 'MySQL 凭据',
    dbCredentialPlaceholder: '可选择凭据中心已有 MySQL 凭据',
    dbCredentialManual: '手动输入数据库账号',
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
    redisCredential: 'Redis 凭据',
    redisCredentialPlaceholder: '可选择凭据中心已有 Redis 凭据',
    redisCredentialManual: '手动输入 Redis 密码',
    redisPassword: 'Redis 密码',
    redisDatabase: 'Redis 数据库',
    minioEnableStorage: '启用 MinIO 存储',
    minioSource: 'MinIO 来源',
    minioSourceExisting: '选择已部署 MinIO',
    minioSourceManual: '手动填写 MinIO',
    minioInstance: '已部署 MinIO',
    minioInstancePlaceholder: '选择 MinIO 实例',
    noMinioInstances: '暂无可选 MinIO 实例',
    minioEndpoint: 'MinIO 地址',
    minioPlatform: '存储平台标识',
    minioCredential: 'MinIO 凭据',
    minioCredentialPlaceholder: '可选择凭据中心已有 MinIO 凭据',
    minioCredentialManual: '手动输入 MinIO 密钥',
    minioAccessKey: 'MinIO Access Key',
    minioSecretKey: 'MinIO Secret Key',
    minioBucketName: 'MinIO Bucket',
    minioDomain: '访问域名',
    minioBasePath: '基础路径',
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
    hint: 'Target server must already have Docker Engine and Docker Compose. Deployed Nacos/MySQL/Redis/MinIO instances can be selected and connection settings are written to service environment variables. SQL initialization also requires mysql client on the target server.',
    version: 'Version',
    versionPlaceholder: 'Select docker-apps bundle',
    servers: 'Target server',
    serversPlaceholder: 'Select one Docker Engine + Docker Compose ready server',
    noServers: 'No Docker Engine + Docker Compose ready servers. Install Docker from the app store and run a check first.',
    noDockerReadyServers: 'No Docker Engine + Docker Compose ready servers. Install Docker from the app store and run a check first.',
    selectedCount: (count: number) => `${count} server(s) selected`,
    cancel: 'Cancel',
    submit: 'Start install',
    topologySingle: 'Single server',
    timezone: 'Timezone',
    networkName: 'Docker network',
    appCPUs: 'CPU limit',
    appMemoryLimit: 'Memory limit',
    nacosSource: 'Nacos source',
    nacosSourceExisting: 'Use deployed Nacos',
    nacosSourceManual: 'Enter Nacos manually',
    nacosInstance: 'Deployed Nacos',
    nacosInstancePlaceholder: 'Select a Nacos instance',
    noNacosInstances: 'No selectable Nacos instances',
    nacosHost: 'Nacos host',
    nacosPort: 'Nacos port',
    nacosCredential: 'Nacos credential',
    nacosCredentialPlaceholder: 'Select a Nacos credential from the credential center',
    nacosCredentialManual: 'Enter Nacos account manually',
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
    dbCredential: 'MySQL credential',
    dbCredentialPlaceholder: 'Select a MySQL credential from the credential center',
    dbCredentialManual: 'Enter database account manually',
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
    redisCredential: 'Redis credential',
    redisCredentialPlaceholder: 'Select a Redis credential from the credential center',
    redisCredentialManual: 'Enter Redis password manually',
    redisPassword: 'Redis password',
    redisDatabase: 'Redis database',
    minioEnableStorage: 'Enable MinIO storage',
    minioSource: 'MinIO source',
    minioSourceExisting: 'Use deployed MinIO',
    minioSourceManual: 'Enter MinIO manually',
    minioInstance: 'Deployed MinIO',
    minioInstancePlaceholder: 'Select a MinIO instance',
    noMinioInstances: 'No selectable MinIO instances',
    minioEndpoint: 'MinIO endpoint',
    minioPlatform: 'Storage platform',
    minioCredential: 'MinIO credential',
    minioCredentialPlaceholder: 'Select a MinIO credential from the credential center',
    minioCredentialManual: 'Enter MinIO keys manually',
    minioAccessKey: 'MinIO access key',
    minioSecretKey: 'MinIO secret key',
    minioBucketName: 'MinIO bucket',
    minioDomain: 'Access domain',
    minioBasePath: 'Base path',
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
  const nacosOptions = nacosInstanceOptions(context)
  const mysqlOptions = mysqlInstanceOptions(context)
  const redisOptions = redisInstanceOptions(context)
  const minioOptions = minioInstanceOptions(context)
  const nacosSourceDefault = nacosOptions.length ? 'existing' : 'manual'
  const mysqlSourceDefault = mysqlOptions.length ? 'existing' : 'manual'
  const redisSourceDefault = redisOptions.length ? 'existing' : 'manual'
  const minioSourceDefault = minioOptions.length ? 'existing' : 'manual'
  const nacosSelectOptions = nacosOptions.length ? nacosOptions : [{ label: copy.noNacosInstances, value: '', disabled: true }]
  const mysqlSelectOptions = mysqlOptions.length ? mysqlOptions : [{ label: copy.noDbInstances, value: '', disabled: true }]
  const redisSelectOptions = redisOptions.length ? redisOptions : [{ label: copy.noRedisInstances, value: '', disabled: true }]
  const minioSelectOptions = minioOptions.length ? minioOptions : [{ label: copy.noMinioInstances, value: '', disabled: true }]
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
    targetServerFilter: (server, filterContext) => dockerReadyServerIds(filterContext).has(server.id),
    copy: dialogCopy,
    fields: [
      requiredText('timezone', copy.timezone, 'system', copy),
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
      selectField('nacosSource', copy.nacosSource, [
        { label: copy.nacosSourceExisting, value: 'existing', disabled: nacosOptions.length === 0 },
        { label: copy.nacosSourceManual, value: 'manual' }
      ], nacosSourceDefault, copy),
      {
        ...selectField('nacosInstanceId', copy.nacosInstance, nacosSelectOptions, nacosOptions[0]?.value ?? '', copy, copy.nacosInstancePlaceholder),
        visibleWhen: sourceIs('nacosSource', 'existing')
      },
      {
        ...requiredText('nacosHost', copy.nacosHost, '', copy),
        visibleWhen: sourceIsNot('nacosSource', 'existing')
      },
      portField('nacosPort', copy.nacosPort, 8848, copy),
      selectField('nacosCredentialId', copy.nacosCredential, credentialOptions(context, 'nacos', copy.nacosCredentialManual), '', copy, copy.nacosCredentialPlaceholder, false),
      {
        ...requiredText('nacosUser', copy.nacosUser, 'nacos', copy),
        visibleWhen: (values) => !values.nacosCredentialId
      },
      {
        ...requiredText('nacosPassword', copy.nacosPassword, 'oversea.nacos', copy),
        type: 'password',
        visibleWhen: (values) => !values.nacosCredentialId
      },
      requiredText('nacosNamespace', copy.nacosNamespace, 'prod', copy),
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
      selectField('dbCredentialId', copy.dbCredential, credentialOptions(context, 'mysql', copy.dbCredentialManual), '', copy, copy.dbCredentialPlaceholder, false),
      {
        ...requiredText('dbUser', copy.dbUser, 'root', copy),
        visibleWhen: (values) => !values.dbCredentialId
      },
      {
        ...requiredText('dbPassword', copy.dbPassword, '', copy),
        type: 'password',
        visibleWhen: (values) => !values.dbCredentialId
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
        name: 'redisCredentialId',
        label: copy.redisCredential,
        type: 'select',
        defaultValue: '',
        placeholder: copy.redisCredentialPlaceholder,
        options: credentialOptions(context, 'redis', copy.redisCredentialManual)
      },
      {
        name: 'redisPassword',
        label: copy.redisPassword,
        type: 'password',
        defaultValue: '',
        visibleWhen: (values) => !values.redisCredentialId
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
        name: 'minioEnableStorage',
        label: copy.minioEnableStorage,
        type: 'switch',
        defaultValue: true
      },
      {
        ...selectField('minioSource', copy.minioSource, [
          { label: copy.minioSourceExisting, value: 'existing', disabled: minioOptions.length === 0 },
          { label: copy.minioSourceManual, value: 'manual' }
        ], minioSourceDefault, copy),
        visibleWhen: valueTruthy('minioEnableStorage')
      },
      {
        ...selectField('minioInstanceId', copy.minioInstance, minioSelectOptions, minioOptions[0]?.value ?? '', copy, copy.minioInstancePlaceholder),
        visibleWhen: enabledSourceIs('minioEnableStorage', 'minioSource', 'existing')
      },
      {
        ...requiredText('minioEndpoint', copy.minioEndpoint, '', copy),
        visibleWhen: enabledSourceIsNot('minioEnableStorage', 'minioSource', 'existing')
      },
      {
        ...requiredText('minioPlatform', copy.minioPlatform, 'minio-1', copy),
        visibleWhen: valueTruthy('minioEnableStorage')
      },
      {
        name: 'minioCredentialId',
        label: copy.minioCredential,
        type: 'select',
        defaultValue: '',
        placeholder: copy.minioCredentialPlaceholder,
        options: credentialOptions(context, 'minio', copy.minioCredentialManual),
        visibleWhen: valueTruthy('minioEnableStorage')
      },
      {
        ...requiredText('minioAccessKey', copy.minioAccessKey, '', copy),
        type: 'password',
        visibleWhen: allVisible(valueTruthy('minioEnableStorage'), (values) => !values.minioCredentialId)
      },
      {
        ...requiredText('minioSecretKey', copy.minioSecretKey, '', copy),
        type: 'password',
        visibleWhen: allVisible(valueTruthy('minioEnableStorage'), (values) => !values.minioCredentialId)
      },
      {
        ...requiredText('minioBucketName', copy.minioBucketName, 'aifar', copy),
        visibleWhen: valueTruthy('minioEnableStorage')
      },
      {
        name: 'minioDomain',
        label: copy.minioDomain,
        type: 'text',
        defaultValue: '',
        visibleWhen: valueTruthy('minioEnableStorage')
      },
      {
        name: 'minioBasePath',
        label: copy.minioBasePath,
        type: 'text',
        defaultValue: '',
        visibleWhen: valueTruthy('minioEnableStorage')
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

export function aifarDeployDisabledReason(locale?: string, context?: AppInstallDialogContext) {
  const copy = aifarCopy(locale)
  return dockerReadyServerIds(context).size > 0 ? '' : copy.noDockerReadyServers
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
  placeholder?: string,
  required = true
): AppInstallField {
  return {
    name,
    label,
    type: 'select',
    options,
    defaultValue,
    placeholder,
    required,
    validate: required ? (value) => String(value ?? '').trim() ? undefined : copy.textRequired : undefined
  }
}

function sourceIs(name: string, value: string) {
  return (values: AppInstallFieldValues) => values[name] === value
}

function sourceIsNot(name: string, value: string) {
  return (values: AppInstallFieldValues) => values[name] !== value
}

function valueTruthy(name: string) {
  return (values: AppInstallFieldValues) => values[name] !== false
}

function enabledSourceIs(enabledName: string, sourceName: string, value: string) {
  return (values: AppInstallFieldValues) => values[enabledName] !== false && values[sourceName] === value
}

function enabledSourceIsNot(enabledName: string, sourceName: string, value: string) {
  return (values: AppInstallFieldValues) => values[enabledName] !== false && values[sourceName] !== value
}

function allVisible(...checks: Array<(values: AppInstallFieldValues) => boolean>) {
  return (values: AppInstallFieldValues) => checks.every((check) => check(values))
}

function credentialOptions(context: AppInstallDialogContext | undefined, kind: string, manualLabel: string): AppInstallFieldOption[] {
  return [
    { label: manualLabel, value: '' },
    ...(context?.credentials ?? [])
      .filter((credential) => credential.kind === kind && credential.status !== 'retired')
      .map((credential) => ({
        label: [credential.name, credential.username, credential.endpoint].filter(Boolean).join(' / '),
        value: credential.id
      }))
  ]
}

function nacosInstanceOptions(context?: AppInstallDialogContext): AppInstallFieldOption[] {
  return (context?.instances ?? [])
    .filter((instance) => instance.app === 'nacos')
    .map((instance) => ({
      label: dependencyLabel(instance, context, 'Nacos'),
      value: instance.id
    }))
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
      label: redisDependencyLabel(instance, context),
      value: instance.id
    }))
}

function minioInstanceOptions(context?: AppInstallDialogContext): AppInstallFieldOption[] {
  return (context?.instances ?? [])
    .filter((instance) => instance.app === 'minio')
    .map((instance) => ({
      label: dependencyLabel(instance, context, 'MinIO'),
      value: instance.id
    }))
}

function redisDependencyLabel(instance: AppInstanceOption, context: AppInstallDialogContext | undefined) {
  const metadata = parseMetadata(instance.metadata)
  const topology = String(instance.topology || metadata.topology || '').trim()
  if (topology.toLowerCase() !== 'sentinel') {
    return dependencyLabel(instance, context, 'Redis')
  }
  const sentinelPort = numberFromMetadata(metadata.sentinelPort, 26379)
  const sentinelEndpoint =
    endpointWithDefaultPort(firstEndpoint(metadata.sentinelEndpoint), sentinelPort) ||
    redisSentinelEndpointForServer(instance, context, metadata, sentinelPort) ||
    endpointFromHost(firstEndpoint(metadata.endpoint) || firstEndpoint(metadata.currentMasterEndpoint), sentinelPort)
  return dependencyLabel(instance, context, 'Redis', sentinelEndpoint)
}

function dependencyLabel(instance: AppInstanceOption, context: AppInstallDialogContext | undefined, prefix: string, preferredEndpoint?: string) {
  const metadata = parseMetadata(instance.metadata)
  const topology = String(instance.topology || metadata.topology || '').trim()
  const endpoint = String(preferredEndpoint || metadata.endpoint || metadata.clusterEndpoint || metadata.currentMasterEndpoint || '').trim()
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

function firstEndpoint(value: unknown) {
  if (Array.isArray(value)) {
    for (const item of value) {
      const endpoint = String(item ?? '').trim()
      if (endpoint) {
        return endpoint
      }
    }
    return ''
  }
  return String(value ?? '').trim()
}

function redisSentinelEndpointForServer(
  instance: AppInstanceOption,
  context: AppInstallDialogContext | undefined,
  metadata: Record<string, unknown>,
  port: number
) {
  const server = (context?.servers ?? []).find((item) => item.id === instance.serverId)
  const host = String(server?.host ?? '').trim()
  if (!host) {
    return ''
  }
  const matching = endpointList(metadata.sentinelEndpoints).find((endpoint) => endpointHost(endpoint) === host)
  return endpointWithDefaultPort(matching, port) || `${host}:${port}`
}

function endpointList(value: unknown) {
  if (Array.isArray(value)) {
    return value.map((item) => String(item ?? '').trim()).filter(Boolean)
  }
  return String(value ?? '')
    .split(/[,\n\r;]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function endpointWithDefaultPort(endpoint: string | undefined, port: number) {
  const text = String(endpoint ?? '').trim()
  if (!text) {
    return ''
  }
  return text.includes(':') ? text : `${text}:${port}`
}

function endpointFromHost(endpoint: string, port: number) {
  const host = endpointHost(endpoint)
  return host ? `${host}:${port}` : ''
}

function endpointHost(endpoint: string | undefined) {
  let text = String(endpoint ?? '').trim()
  if (!text) {
    return ''
  }
  const schemeIndex = text.indexOf('://')
  if (schemeIndex >= 0) {
    text = text.slice(schemeIndex + 3)
  }
  const slashIndex = text.indexOf('/')
  if (slashIndex >= 0) {
    text = text.slice(0, slashIndex)
  }
  return text.split(':')[0]?.trim() ?? ''
}

function numberFromMetadata(value: unknown, fallback: number) {
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback
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

function dockerReadyServerIds(context?: AppInstallDialogContext) {
  const out = new Set<string>()
  for (const instance of context?.instances ?? []) {
    if (isDockerReadyInstance(instance)) {
      out.add(instance.serverId || '')
    }
  }
  out.delete('')
  return out
}

function isDockerReadyInstance(instance: AppInstanceOption) {
  if (instance.app !== 'docker' || !instance.serverId) {
    return false
  }
  if (!statusReady(instance.status)) {
    return false
  }
  const metadata = parseMetadata(instance.metadata)
  const lastCheck = metadataRecord(metadata.lastCheck)
  if (!lastCheck) {
    return true
  }
  const checkedStatus = String(lastCheck.status ?? instance.status ?? '').trim()
  if (checkedStatus && !statusReady(checkedStatus)) {
    return false
  }
  return String(lastCheck.dockerVersion ?? '').trim() !== '' && String(lastCheck.composeVersion ?? '').trim() !== ''
}

function metadataRecord(value: unknown) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function statusReady(value: unknown) {
  return ['installed', 'running', 'available', 'ok', 'success'].includes(String(value ?? '').trim().toLowerCase())
}
