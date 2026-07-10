const overrideKey = 'AIFAR_ALLOW_INSECURE_DEFAULTS'

export function selectDevelopmentAddress(processEnv, toolEnv, primaryKey) {
  if (Object.hasOwn(processEnv, primaryKey)) {
    return processEnv[primaryKey]
  }
  if (primaryKey === 'AIFAR_ADDR' && Object.hasOwn(processEnv, 'AIFAR_DEV_ADDR')) {
    return processEnv.AIFAR_DEV_ADDR
  }
  if (Object.hasOwn(toolEnv, 'AIFAR_DEV_ADDR')) {
    return toolEnv.AIFAR_DEV_ADDR
  }
  return '127.0.0.1:8080'
}

export function isLoopbackListenerAddress(addr) {
  const value = String(addr ?? '')
  let host

  if (value.startsWith('[')) {
    const closingBracket = value.indexOf(']')
    const port = value.slice(closingBracket + 1)
    if (closingBracket < 0 || !port.startsWith(':') || port.length === 1 || /[:\[\]]/.test(port.slice(1))) {
      return false
    }
    host = value.slice(1, closingBracket)
  } else {
    const separator = value.lastIndexOf(':')
    const port = value.slice(separator + 1)
    if (separator <= 0 || port === '' || value.slice(0, separator).includes(':') || /[\[\]]/.test(port)) {
      return false
    }
    host = value.slice(0, separator)
  }

  return host === '127.0.0.1' || host === 'localhost' || host === '::1'
}

export function developmentSecurityEnv(addr, processEnv = process.env) {
  if (Object.hasOwn(processEnv, overrideKey)) {
    return { [overrideKey]: processEnv[overrideKey] }
  }
  if (isLoopbackListenerAddress(addr)) {
    return { [overrideKey]: 'true' }
  }
  return {}
}
