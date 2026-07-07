<template>
  <div class="global-realtime-status" :class="statusClass">
    <div class="realtime-main">
      <span class="realtime-dot" />
      <strong>{{ statusText }}</strong>
      <span>{{ detailText }}</span>
    </div>
    <div class="realtime-meta">
      <span v-if="lastEventText">{{ lastEventText }}</span>
      <el-button v-if="!realtime.connected" size="small" text @click="realtime.connect">{{ t('realtime.reconnect') }}</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '../i18n'
import { useRealtimeStore } from '../stores/realtime'

const realtime = useRealtimeStore()
const { t } = useI18n()

const statusClass = computed(() => `is-${realtime.status}`)
const statusText = computed(() => {
  switch (realtime.status) {
    case 'connected':
      return t('realtime.connected')
    case 'connecting':
      return t('realtime.connecting')
    case 'reconnecting':
      return t('realtime.reconnecting')
    case 'disconnected':
      return t('realtime.disconnected')
    default:
      return t('realtime.idle')
  }
})
const detailText = computed(() => {
  if (realtime.connected) {
    return t('realtime.liveDetail')
  }
  return realtime.error || t('realtime.fallbackDetail')
})
const lastEventText = computed(() => {
  const event = realtime.lastEvent
  if (!event?.type || event.type === 'realtime.connected') {
    return ''
  }
  const target = event.resourceId || event.serverId || event.instanceId || event.taskId || event.resource || ''
  return target ? `${event.type} · ${target}` : event.type
})
</script>

<style scoped>
.global-realtime-status {
  flex: 0 0 auto;
  min-height: 34px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 12px;
  border-bottom: 1px solid var(--aifar-border-soft);
  background: rgba(255, 255, 255, .92);
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.realtime-main,
.realtime-meta {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.realtime-main strong {
  color: var(--aifar-text);
  font-size: 12px;
  line-height: 18px;
  font-weight: 850;
}

.realtime-main span:last-child,
.realtime-meta span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.realtime-meta {
  justify-content: flex-end;
  color: var(--aifar-text-tertiary);
}

.realtime-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--aifar-warning);
  box-shadow: 0 0 0 3px rgba(250, 173, 20, .14);
  flex: 0 0 auto;
}

.global-realtime-status.is-connected .realtime-dot {
  background: var(--aifar-success);
  box-shadow: 0 0 0 3px rgba(82, 196, 26, .14);
}

.global-realtime-status.is-reconnecting .realtime-dot,
.global-realtime-status.is-connecting .realtime-dot {
  background: var(--aifar-primary);
  box-shadow: 0 0 0 3px rgba(22, 119, 255, .14);
}

.global-realtime-status.is-disconnected .realtime-dot {
  background: var(--aifar-danger);
  box-shadow: 0 0 0 3px rgba(255, 77, 79, .14);
}

@media (max-width: 760px) {
  .global-realtime-status {
    display: grid;
  }

  .realtime-meta {
    justify-content: flex-start;
  }
}
</style>
