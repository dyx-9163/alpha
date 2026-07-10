import { existsSync, readFileSync } from 'node:fs'

const sensitiveKeys = [
  'AIFAR_DEFAULT_PASSWORD',
  'AIFAR_BOOTSTRAP_PASSWORD',
  'AIFAR_JWT_SECRET',
  'AIFAR_CREDENTIAL_SECRET',
  'AIFAR_PREVIOUS_CREDENTIAL_SECRET'
]

export function parseDefaultsEnv(text) {
  return String(text ?? '').split(/\r?\n/).reduce((env, line, index) => {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) {
      return env
    }
    const match = /^(AIFAR_[A-Z0-9_]+)=(.*)$/.exec(trimmed)
    if (!match) {
      throw new Error(`Malformed configuration line ${index + 1}`)
    }
    const [, key, rawValue] = match
    if (Object.hasOwn(env, key)) {
      throw new Error(`Duplicate configuration key: ${key}`)
    }
    env[key] = rawValue.trim().replace(/^(['"])(.*)\1$/, '$2')
    return env
  }, {})
}

export function releaseDefaultsProblems(env) {
  const problems = []
  if (parseBoolean(env.AIFAR_ALLOW_INSECURE_DEFAULTS)) {
    problems.push('AIFAR_ALLOW_INSECURE_DEFAULTS must not be true in a release package')
  }
  for (const key of sensitiveKeys) {
    const value = String(env[key] ?? '').trim()
    if (value) {
      problems.push(`${key} must be blank in a distributable release package; inject it during deployment`)
    }
  }
  return problems
}

export function assertReleaseDefaultsFile(filePath) {
  if (!existsSync(filePath)) {
    throw new Error(`Missing release security configuration: ${filePath}`)
  }
  const problems = releaseDefaultsProblems(parseDefaultsEnv(readFileSync(filePath, 'utf8')))
  if (problems.length > 0) {
    throw new Error(`Unsafe release security configuration in ${filePath}: ${problems.join('; ')}`)
  }
}

function parseBoolean(value) {
  return ['1', 't', 'true', 'yes', 'on'].includes(String(value ?? '').trim().toLowerCase())
}
