import type { AifarRuntimeLogsResponse } from './types'

export const runtimeLogMaxRows = 3000
export const runtimeLogRowHeight = 32
export const runtimeLogVisibleCount = 100
export const runtimeLogLevelOptions = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE']

export type ParsedRuntimeLogLine = {
  time: string
  timestamp: number
  level: string
  message: string
  errorContext?: boolean
}

export type RuntimeLogParseContext = {
  error: boolean
}

export function parseRuntimeLogsEvent(raw: string) {
  try {
    return JSON.parse(raw) as AifarRuntimeLogsResponse
  } catch {
    return null
  }
}

export function parseRuntimeLogErrorEvent(raw: string) {
  try {
    const parsed = JSON.parse(raw) as { message?: string }
    return String(parsed.message || '').trim()
  } catch {
    return ''
  }
}

export function parseRuntimeLogLine(line: string) {
  const raw = String(line ?? '')
  const structured = parseStructuredRuntimeLog(raw)
  if (structured) {
    return structured
  }

  const timestampMatch = raw.match(/^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?)\s+(.*)$/)
  const time = timestampMatch?.[1] || '-'
  const content = timestampMatch?.[2] || raw
  const levelMatch = content.match(/^\[?(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|SEVERE)\]?\s+(.*)$/i)
  const level = normalizeRuntimeLogLevel(levelMatch?.[1] || detectRuntimeLogLevel(content))
  const parsedTime = time === '-' ? Number.NaN : Date.parse(time.replace(' ', 'T').replace(',', '.'))
  return {
    time,
    timestamp: Number.isFinite(parsedTime) ? parsedTime : 0,
    level,
    message: (levelMatch?.[2] || content).trim() || raw
  }
}

export function parseRuntimeLogLines(
  lines: string[],
  context: RuntimeLogParseContext = { error: false }
) {
  const next = { ...context }
  const parsed = lines.map((line): ParsedRuntimeLogLine => {
    const row = parseRuntimeLogLine(line)
    if (!row.level && next.error && isRuntimeErrorContinuation(line)) {
      row.level = 'ERROR'
    }
    next.error = row.level === 'ERROR'
    return { ...row, errorContext: row.level === 'ERROR' }
  })
  return { lines: parsed, context: next }
}

export function detectRuntimeLogLevel(line: string) {
  const upper = String(line || '').toUpperCase()
  if (/\b(FATAL|SEVERE|ERROR)\b/.test(upper)) return 'ERROR'
  if (/\b(WARN|WARNING)\b/.test(upper)) return 'WARN'
  if (/\bINFO\b/.test(upper)) return 'INFO'
  if (/\b(DEBUG|TRACE)\b/.test(upper)) return 'DEBUG'
  return ''
}

export function isRuntimeErrorContinuation(line: string) {
  const raw = String(line || '')
  return /^\s*(?:Caused by|Suppressed):/i.test(raw)
    || /^\s*at\s+[\w.$<>]+\([^)]*\)\s*$/.test(raw)
    || /^\s*\.\.\.\s+\d+\s+more\s*$/i.test(raw)
    || /^\s*(?:[\w$]+\.)*[\w$]*(?:Exception|Error)(?::|\s|$)/.test(raw)
}

export function runtimeLogLevelTag(level: string) {
  switch (String(level || '').toUpperCase()) {
    case 'ERROR':
    case 'FATAL':
    case 'SEVERE':
      return 'danger'
    case 'WARN':
    case 'WARNING':
      return 'warning'
    case 'INFO':
      return 'success'
    case 'DEBUG':
    case 'TRACE':
      return 'info'
    default:
      return ''
  }
}

function parseStructuredRuntimeLog(raw: string): ParsedRuntimeLogLine | null {
  if (!raw.trimStart().startsWith('{')) return null
  try {
    const value = JSON.parse(raw) as Record<string, unknown>
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null
    const time = firstText(value.timestamp, value.time, value['@timestamp'], value.ts) || '-'
    const level = normalizeRuntimeLogLevel(firstText(value.level, value.severity, value['log.level']))
    const message = firstText(value.message, value.msg) || raw
    const parsedTime = time === '-' ? Number.NaN : Date.parse(time)
    return {
      time,
      timestamp: Number.isFinite(parsedTime) ? parsedTime : 0,
      level: level || detectRuntimeLogLevel(message),
      message
    }
  } catch {
    return null
  }
}

function normalizeRuntimeLogLevel(level: string) {
  switch (String(level || '').trim().toUpperCase()) {
    case 'WARNING':
      return 'WARN'
    case 'FATAL':
    case 'SEVERE':
      return 'ERROR'
    default:
      return String(level || '').trim().toUpperCase()
  }
}

function firstText(...values: unknown[]) {
  for (const value of values) {
    const text = String(value ?? '').trim()
    if (text) return text
  }
  return ''
}
