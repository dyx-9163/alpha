import type { AifarRuntimeLogsResponse } from './types'

export const runtimeLogMaxRows = 3000
export const runtimeLogRowHeight = 32
export const runtimeLogVisibleCount = 100
export const runtimeLogLevelOptions = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE']

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
  const match = raw.match(/^(\d{4}-\d{2}-\d{2}[T ][^\s]+)\s+(?:(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|SEVERE)\s+)?(.*)$/i)
  if (!match) {
    return {
      time: '-',
      timestamp: 0,
      level: detectRuntimeLogLevel(raw),
      message: raw
    }
  }
  const time = match[1]
  const parsedTime = Date.parse(time)
  return {
    time,
    timestamp: Number.isFinite(parsedTime) ? parsedTime : 0,
    level: (match[2] || detectRuntimeLogLevel(raw)).toUpperCase(),
    message: (match[3] || raw).trim() || raw
  }
}

export function detectRuntimeLogLevel(line: string) {
  const upper = String(line || '').toUpperCase()
  if (/\b(FATAL|SEVERE|ERROR)\b/.test(upper)) return 'ERROR'
  if (/\b(WARN|WARNING)\b/.test(upper)) return 'WARN'
  if (/\bINFO\b/.test(upper)) return 'INFO'
  if (/\b(DEBUG|TRACE)\b/.test(upper)) return 'DEBUG'
  return ''
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
