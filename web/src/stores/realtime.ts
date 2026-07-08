import { defineStore } from 'pinia'
import { eventStreamUrl } from '../api/client'
import { useAlertsStore } from './alerts'
import { useTaskProgressStore } from './taskProgress'

export type RealtimeEvent = {
  id?: string
  type?: string
  resource?: string
  resourceId?: string
  serverId?: string
  instanceId?: string
  taskId?: string
  status?: string
  version?: number
  collectedAt?: string
  createdAt?: string
  payload?: Record<string, unknown>
}

let source: EventSource | null = null
let reconnectTimer: number | undefined

export const useRealtimeStore = defineStore('realtime', {
  state: () => ({
    status: 'idle' as 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'disconnected',
    error: '',
    lastEvent: null as RealtimeEvent | null,
    lastEventAt: 0,
    connectedAt: 0,
    reconnectAttempts: 0,
    revision: 0
  }),
  getters: {
    connected(state): boolean {
      return state.status === 'connected'
    }
  },
  actions: {
    connect() {
      if (!localStorage.getItem('aifar-session-token')) {
        this.disconnect()
        return
      }
      if (source && (this.status === 'connected' || this.status === 'connecting')) {
        return
      }
      clearReconnectTimer()
      this.status = this.reconnectAttempts > 0 ? 'reconnecting' : 'connecting'
      this.error = ''
      source = new EventSource(eventStreamUrl())
      source.addEventListener('open', () => {
        this.status = 'connected'
        this.connectedAt = Date.now()
        this.reconnectAttempts = 0
        this.error = ''
      })
      source.addEventListener('aifar-event', (event) => {
        this.applyEvent(parseRealtimeEvent((event as MessageEvent).data))
      })
      source.addEventListener('heartbeat', () => {
        this.lastEventAt = Date.now()
      })
      source.addEventListener('error', () => {
        this.status = 'reconnecting'
        this.error = 'event stream disconnected'
        this.scheduleReconnect()
      })
    },
    disconnect() {
      clearReconnectTimer()
      if (source) {
        source.close()
        source = null
      }
      this.status = 'disconnected'
      this.error = ''
    },
    scheduleReconnect() {
      if (reconnectTimer !== undefined) {
        return
      }
      if (source) {
        source.close()
        source = null
      }
      this.reconnectAttempts += 1
      const delay = Math.min(30000, 1200 * this.reconnectAttempts)
      reconnectTimer = window.setTimeout(() => {
        reconnectTimer = undefined
        this.connect()
      }, delay)
    },
    applyEvent(event: RealtimeEvent | null) {
      if (!event?.type) {
        return
      }
      this.lastEvent = event
      this.lastEventAt = Date.now()
      this.revision += 1
      if (event.taskId && (event.type === 'task.updated' || event.type === 'task.finished')) {
        void useTaskProgressStore().refreshTask(event.taskId)
      }
      if (event.type.startsWith('alert.')) {
        useAlertsStore().applyRealtimeEvent(event)
      }
    }
  }
})

function parseRealtimeEvent(raw: string) {
  try {
    return JSON.parse(raw) as RealtimeEvent
  } catch {
    return null
  }
}

function clearReconnectTimer() {
  if (reconnectTimer !== undefined) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = undefined
  }
}
