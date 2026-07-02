import { getCurrentLocale } from '../i18n'

export type ApiError = Error & { status?: number; details?: unknown }

const API_PREFIX = '/api/v2'

function headers(json = true) {
  const out = new Headers()
  if (json) out.set('Content-Type', 'application/json')
  out.set('X-AIFAR-Language', getCurrentLocale())
  const token = localStorage.getItem('aifar-session-token')
  if (token) out.set('Authorization', `Bearer ${token}`)
  return out
}

async function handle<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const body = await response.json().catch(() => ({ message: response.statusText }))
    if (response.status === 401) {
      clearLocalSession()
    }
    const err = new Error(body.message ?? response.statusText) as ApiError
    err.status = response.status
    err.details = body.details
    throw err
  }
  return response.json()
}

function clearLocalSession() {
  localStorage.removeItem('aifar-session-token')
  localStorage.removeItem('aifar-username')
  localStorage.removeItem('aifar-role')
  localStorage.removeItem('aifar-token-version')
  localStorage.removeItem('aifar-permissions')
}

export function apiGet<T>(path: string) {
  return fetch(`${API_PREFIX}${path}`, { headers: headers(false) }).then((r) => handle<T>(r))
}

export function apiPost<T>(path: string, body?: unknown) {
  return fetch(`${API_PREFIX}${path}`, { method: 'POST', headers: headers(), body: JSON.stringify(body ?? {}) }).then((r) => handle<T>(r))
}

export function apiPostForm<T>(path: string, body: FormData) {
  return fetch(`${API_PREFIX}${path}`, { method: 'POST', headers: headers(false), body }).then((r) => handle<T>(r))
}

export function apiPut<T>(path: string, body?: unknown) {
  return fetch(`${API_PREFIX}${path}`, { method: 'PUT', headers: headers(), body: JSON.stringify(body ?? {}) }).then((r) => handle<T>(r))
}

export function apiDelete<T>(path: string, body?: unknown) {
  return fetch(`${API_PREFIX}${path}`, {
    method: 'DELETE',
    headers: headers(body !== undefined),
    body: body === undefined ? undefined : JSON.stringify(body)
  }).then((r) => handle<T>(r))
}

export async function apiDownload(path: string) {
  const response = await fetch(`${API_PREFIX}${path}`, { headers: headers(false) })
  if (!response.ok) {
    await handle<unknown>(response)
  }
  return {
    blob: await response.blob(),
    filename: filenameFromDisposition(response.headers.get('Content-Disposition')),
    sha256: response.headers.get('X-AIFAR-Backup-SHA256') ?? ''
  }
}

export function asArray<T = unknown>(value: unknown): T[] {
  return Array.isArray(value) ? value as T[] : []
}

function filenameFromDisposition(value: string | null) {
  if (!value) {
    return ''
  }
  const match = /filename\*=UTF-8''([^;]+)|filename="?([^";]+)"?/i.exec(value)
  const encoded = match?.[1] || match?.[2] || ''
  if (!encoded) {
    return ''
  }
  try {
    return decodeURIComponent(encoded)
  } catch {
    return encoded
  }
}

export function terminalUrl(serverId: string) {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const params = new URLSearchParams()
  const token = localStorage.getItem('aifar-session-token')
  if (token) {
    params.set('token', token)
  }
  params.set('lang', getCurrentLocale())
  return `${proto}://${window.location.host}${API_PREFIX}/servers/${serverId}/terminal/ws?${params.toString()}`
}

export function terminalProtocols() {
  const token = localStorage.getItem('aifar-session-token') ?? ''
  const encoded = btoa(unescape(encodeURIComponent(token))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
  return token ? ['aifar.terminal', `aifar.auth.${encoded}`] : ['aifar.terminal']
}
