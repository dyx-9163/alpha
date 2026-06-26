<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('servers.title') }}</h1>
        <p class="page-subtitle">{{ t('servers.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-button @click="load">{{ t('servers.refresh') }}</el-button>
        <el-button type="primary" @click="open()">{{ t('servers.add') }}</el-button>
      </div>
    </div>

    <div class="metric-grid server-summary">
      <div class="metric-card">
        <div class="label">{{ t('servers.total') }}</div>
        <div class="value">{{ summary.total }}</div>
      </div>
      <div class="metric-card">
        <div class="label">{{ t('servers.availableCount') }}</div>
        <div class="value">{{ summary.available }}</div>
      </div>
      <div class="metric-card">
        <div class="label">{{ t('servers.unknownCount') }}</div>
        <div class="value">{{ summary.unknown }}</div>
      </div>
    </div>

    <div class="server-workbench">
      <ServerInventoryList
        :servers="filteredServers"
        :selected-id="selectedId"
        v-model:search="search"
        @select="selectedId = $event"
      />

      <ServerDetailPanel
        :server="selectedServer"
        :probing="selectedProbing"
        v-model:active-tab="activeTab"
        @edit="open"
        @probe="probe"
        @remove="remove"
      />
    </div>

    <ServerFormDrawer v-model:visible="drawer" :form="form" @save="save" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from '../i18n'
import ServerDetailPanel from '../servers/components/ServerDetailPanel.vue'
import ServerFormDrawer from '../servers/components/ServerFormDrawer.vue'
import ServerInventoryList from '../servers/components/ServerInventoryList.vue'
import { useServerWorkbench } from '../servers/useServerWorkbench'

const { t } = useI18n()
const {
  filteredServers,
  selectedServer,
  selectedId,
  search,
  drawer,
  activeTab,
  probingIds,
  form,
  summary,
  loadDefaults,
  load,
  open,
  save,
  remove,
  probe,
  probeSelectedOnce
} = useServerWorkbench(t)

const selectedProbing = computed(() => selectedServer.value ? probingIds.value.has(selectedServer.value.id) : false)

onMounted(async () => {
  await loadDefaults()
  await load()
  await probeSelectedOnce()
})
</script>

<style scoped>
.server-summary {
  margin-bottom: 0;
}

.server-workbench {
  display: grid;
  grid-template-columns: clamp(240px, 18vw, 320px) minmax(0, 1fr);
  gap: 12px;
  align-items: stretch;
  min-width: 0;
}

@media (max-width: 1100px) {
  .server-workbench {
    grid-template-columns: 1fr;
  }
}
</style>
