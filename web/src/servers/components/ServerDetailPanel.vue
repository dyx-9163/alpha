<template>
  <main class="server-detail workspace-card">
    <template v-if="server">
      <div class="detail-head">
        <div>
          <h2>{{ server.name }}</h2>
          <p>{{ server.username }}@{{ server.host }}:{{ server.port }}</p>
        </div>
        <div class="head-actions">
          <el-tooltip :content="props.disabledReason" :disabled="props.canManage || !props.disabledReason" placement="top">
            <span><el-button :disabled="!props.canManage" @click="$emit('edit', server)">{{ t('servers.edit') }}</el-button></span>
          </el-tooltip>
          <el-tooltip :content="props.disabledReason" :disabled="props.canManage || !props.disabledReason" placement="top">
            <span><el-button :loading="props.probing" :disabled="!props.canManage" @click="$emit('probe', server)">{{ t('servers.probe') }}</el-button></span>
          </el-tooltip>
          <el-tooltip :content="props.disabledReason" :disabled="props.canManage || !props.disabledReason" placement="top">
            <span><el-button type="danger" :disabled="!props.canManage" @click="$emit('remove', server)">{{ t('common.delete') }}</el-button></span>
          </el-tooltip>
        </div>
      </div>

      <el-tabs v-model="tab" class="detail-tabs">
        <el-tab-pane :label="t('servers.overview')" name="overview" />
        <el-tab-pane :label="t('servers.access')" name="access" />
        <el-tab-pane :label="t('servers.runtime')" name="runtime" />
      </el-tabs>

      <div v-if="tab === 'overview'" class="detail-grid">
        <section class="info-block">
          <h3>{{ t('servers.profile') }}</h3>
          <div class="kv-grid compact-kv">
            <div class="key">{{ t('servers.name') }}</div><div>{{ server.name }}</div>
            <div class="key">{{ t('servers.host') }}</div><div>{{ server.host }}</div>
            <div class="key">{{ t('servers.username') }}</div><div>{{ server.username }}</div>
            <div class="key">{{ t('common.status') }}</div><div><StatusTag :status="displayedStatus" /></div>
            <div class="key">{{ t('servers.deployDir') }}</div><div>{{ server.deployDir || '/aifar/apps' }}</div>
            <div class="key">{{ t('servers.tags') }}</div><div>{{ server.tags || '-' }}</div>
            <div class="key">{{ t('common.message') }}</div><div>{{ server.lastError || t('common.available') }}</div>
            <div class="key">{{ t('servers.note') }}</div><div>{{ server.note || '-' }}</div>
          </div>
        </section>

        <section class="info-block">
          <h3>{{ t('servers.operations') }}</h3>
          <div class="capability-grid">
            <div class="capability-card">
              <span>{{ t('servers.sshCapability') }}</span>
              <StatusTag :status="displayedStatus" />
            </div>
          </div>
        </section>
      </div>

      <div v-else-if="tab === 'access'" class="kv-grid">
        <div class="key">{{ t('servers.authType') }}</div><div>{{ authTypeLabel }}</div>
        <div class="key">{{ t('servers.password') }}</div><div>{{ server.authType === 'password' ? t('servers.passwordConfigured') : t('servers.notConfigured') }}</div>
        <div class="key">{{ t('servers.privateKey') }}</div><div>{{ server.authType === 'privateKey' ? t('servers.privateKeyConfigured') : t('servers.notConfigured') }}</div>
      </div>

      <div v-else-if="tab === 'runtime'" class="capability-grid runtime-grid">
        <div class="metric-card">
          <div class="label">SSH</div>
          <div class="value">{{ runtimeStatusText }}</div>
        </div>
        <div class="metric-card">
          <div class="label">{{ t('servers.deployDir') }}</div>
          <div class="value small-value">{{ server.deployDir || '/aifar/apps' }}</div>
        </div>
      </div>
    </template>
    <div v-else class="empty-state"><div><strong>{{ t('servers.selectTitle') }}</strong><span>{{ t('servers.selectDesc') }}</span></div></div>
  </main>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import StatusTag from '../../components/StatusTag.vue'
import { useI18n } from '../../i18n'
import type { ServerRecord } from '../types'

const props = withDefaults(defineProps<{ server: ServerRecord | null, probing?: boolean, canManage?: boolean, disabledReason?: string }>(), {
  probing: false,
  canManage: true,
  disabledReason: ''
})
const tab = defineModel<string>('activeTab', { default: 'overview' })
defineEmits<{
  edit: [server: ServerRecord]
  probe: [server: ServerRecord]
  remove: [server: ServerRecord]
}>()
const { t } = useI18n()
const authTypeLabel = computed(() => props.server?.authType === 'privateKey' ? t('servers.authPrivateKey') : t('servers.authPassword'))
const displayedStatus = computed(() => props.probing ? 'probing' : props.server?.status || 'unknown')
const runtimeStatusText = computed(() => {
  if (displayedStatus.value === 'available') {
    return t('common.available')
  }
  if (displayedStatus.value === 'probing') {
    return t('common.probing')
  }
  return t('common.unknown')
})
</script>

<style scoped>
.server-detail {
  padding: 12px;
  min-height: 0;
  height: 100%;
  overflow: auto;
  scrollbar-width: none;
  -ms-overflow-style: none;
}

.server-detail::-webkit-scrollbar {
  width: 0;
  height: 0;
}

.detail-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}

.detail-head h2 {
  margin: 0;
  font-size: 18px;
  color: var(--aifar-ink);
}

.detail-head p {
  margin: 4px 0 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.detail-tabs :deep(.el-tabs__header) {
  margin-bottom: 10px;
}

.detail-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.6fr) minmax(240px, .8fr);
  gap: 12px;
}

.info-block {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  overflow: hidden;
  box-shadow: 0 1px 2px rgba(15, 35, 68, .03);
}

.info-block h3 {
  margin: 0;
  padding: 10px 12px;
  border-bottom: 1px solid var(--aifar-border-soft);
  color: var(--aifar-ink);
  font-size: 13px;
  background: linear-gradient(180deg, #fff, #f8fbff);
}

.compact-kv {
  grid-template-columns: minmax(116px, 140px) minmax(0, 1fr);
  border-top: 0;
}

.capability-grid {
  display: grid;
  gap: 10px;
  padding: 12px;
}

.capability-card {
  min-height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius);
  padding: 10px 12px;
  background: #f7fbff;
}

.capability-card span:first-child {
  color: var(--aifar-text-secondary);
  font-weight: 800;
}

.runtime-grid {
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  padding: 0;
}

.small-value {
  font-size: 14px !important;
  color: var(--aifar-ink) !important;
}

@media (max-width: 1200px) {
  .detail-grid {
    grid-template-columns: 1fr;
  }
}
</style>
