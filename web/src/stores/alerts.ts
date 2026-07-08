import { defineStore } from 'pinia'
import { apiGet, apiPost, asArray } from '../api/client'
import type { RealtimeEvent } from './realtime'

export type AlertItem = {
  id: string
  fingerprint: string
  severity: 'critical' | 'warning' | 'info' | string
  scope: string
  resourceId?: string
  serverId?: string
  app?: string
  instanceId?: string
  status: 'open' | 'resolved' | string
  title: string
  message?: string
  evidence?: Record<string, unknown>
  evidenceJson?: string
  requiredPermission?: string
  firstSeenAt?: string
  lastSeenAt?: string
  resolvedAt?: string
  mutedUntil?: string
  acknowledgedBy?: string
  acknowledgedAt?: string
  updatedAt?: string
}

type AlertsResponse = {
  items?: AlertItem[]
}

export const useAlertsStore = defineStore('alerts', {
  state: () => ({
    items: [] as AlertItem[],
    loading: false,
    loaded: false,
    error: '',
    drawerVisible: false
  }),
  getters: {
    openAlerts(state): AlertItem[] {
      return state.items
        .filter((item) => item.status === 'open')
        .sort(compareAlerts)
    },
    visibleOpenAlerts(): AlertItem[] {
      return this.openAlerts.filter((item) => !isMuted(item))
    },
    openCount(): number {
      return this.openAlerts.length
    },
    criticalCount(): number {
      return this.openAlerts.filter((item) => item.severity === 'critical').length
    },
    warningCount(): number {
      return this.openAlerts.filter((item) => item.severity === 'warning').length
    }
  },
  actions: {
    async load(status = 'open') {
      this.loading = true
      this.error = ''
      try {
        const response = await apiGet<AlertsResponse>(`/alerts?status=${encodeURIComponent(status)}`)
        this.items = asArray<AlertItem>(response.items)
        this.loaded = true
      } catch (err) {
        this.error = err instanceof Error ? err.message : String(err)
        throw err
      } finally {
        this.loading = false
      }
    },
    clear() {
      this.items = []
      this.error = ''
      this.loaded = false
      this.drawerVisible = false
    },
    openDrawer() {
      this.drawerVisible = true
    },
    closeDrawer() {
      this.drawerVisible = false
    },
    applyRealtimeEvent(event: RealtimeEvent) {
      const alert = normalizeRealtimeAlert(event)
      if (!alert?.id) {
        return
      }
      this.upsert(alert)
    },
    async ack(id: string) {
      const alert = await apiPost<AlertItem>(`/alerts/${encodeURIComponent(id)}/ack`)
      this.upsert(alert)
      return alert
    },
    async mute(id: string, minutes = 60) {
      const alert = await apiPost<AlertItem>(`/alerts/${encodeURIComponent(id)}/mute`, { minutes })
      this.upsert(alert)
      return alert
    },
    async resolve(id: string) {
      const alert = await apiPost<AlertItem>(`/alerts/${encodeURIComponent(id)}/resolve`, {})
      this.upsert(alert)
      return alert
    },
    upsert(alert: AlertItem) {
      const idx = this.items.findIndex((item) => item.id === alert.id)
      if (idx >= 0) {
        this.items.splice(idx, 1, { ...this.items[idx], ...alert })
      } else {
        this.items.unshift(alert)
      }
    }
  }
})

function normalizeRealtimeAlert(event: RealtimeEvent): AlertItem | null {
  const payload = event.payload ?? {}
  const raw = payload.alert && typeof payload.alert === 'object' ? payload.alert : payload
  if (!raw || typeof raw !== 'object') {
    return null
  }
  const item = raw as Partial<AlertItem>
  if (!item.id) {
    return null
  }
  return item as AlertItem
}

function compareAlerts(a: AlertItem, b: AlertItem) {
  const severityA = severityRank(a.severity)
  const severityB = severityRank(b.severity)
  if (severityA !== severityB) {
    return severityA - severityB
  }
  return timeValue(b.lastSeenAt ?? b.updatedAt) - timeValue(a.lastSeenAt ?? a.updatedAt)
}

function severityRank(severity: string) {
  if (severity === 'critical') return 0
  if (severity === 'warning') return 1
  return 2
}

function timeValue(value?: string) {
  if (!value) return 0
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? 0 : date.getTime()
}

function isMuted(alert: AlertItem) {
  if (!alert.mutedUntil) {
    return false
  }
  const until = new Date(alert.mutedUntil)
  return Number.isFinite(until.getTime()) && until.getTime() > Date.now()
}
