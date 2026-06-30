<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('servers.title') }}</h1>
        <p class="page-subtitle">{{ t('servers.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-button @click="load">{{ t('servers.refresh') }}</el-button>
        <el-tooltip :content="deniedText" :disabled="canManageServers" placement="top">
          <span>
            <el-button type="primary" :disabled="!canManageServers" @click="openServerForm()">{{ t('servers.add') }}</el-button>
          </span>
        </el-tooltip>
      </div>
    </div>

    <MetricGrid :items="serverMetrics" class="server-summary" />

    <div class="server-workbench">
      <ServerInventoryList
        :servers="filteredServers"
        :selected-id="selectedId"
        :drag-disabled="Boolean(search.trim()) || !canManageServers"
        v-model:search="search"
        @select="selectedId = $event"
        @reorder="reorderServers"
      />

      <ServerDetailPanel
        :server="selectedServer"
        :probing="selectedProbing"
        :can-manage="canManageServers"
        :disabled-reason="deniedText"
        v-model:active-tab="activeTab"
        @edit="openServerForm"
        @probe="probeServerHost"
        @remove="removeServer"
      />
    </div>

    <ServerFormDrawer v-model:visible="drawer" :form="form" :can-save="canManageServers" :disabled-reason="deniedText" @save="saveServerForm" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import MetricGrid from '../components/MetricGrid.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import ServerDetailPanel from '../servers/components/ServerDetailPanel.vue'
import ServerFormDrawer from '../servers/components/ServerFormDrawer.vue'
import ServerInventoryList from '../servers/components/ServerInventoryList.vue'
import { useServerWorkbench } from '../servers/useServerWorkbench'

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const canManageServers = computed(() => can(permissions.serversManage))
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
  reorder,
  probe,
  probeAllOnce
} = useServerWorkbench(t)

const selectedProbing = computed(() => selectedServer.value ? probingIds.value.has(selectedServer.value.id) : false)
const serverMetrics = computed(() => [
  { label: t('servers.total'), value: summary.value.total },
  { label: t('servers.availableCount'), value: summary.value.available },
  { label: t('servers.unknownCount'), value: summary.value.unknown }
])

onMounted(async () => {
  await loadDefaults()
  await load()
  if (canManageServers.value) {
    await probeAllOnce()
  }
})

function ensureServerPermission() {
  if (canManageServers.value) {
    return true
  }
  ElMessage.warning(deniedText.value)
  return false
}

function openServerForm(row?: Parameters<typeof open>[0]) {
  if (!ensureServerPermission()) return
  open(row)
}

async function saveServerForm() {
  if (!ensureServerPermission()) return
  await save()
}

async function removeServer(row: Parameters<typeof remove>[0]) {
  if (!ensureServerPermission()) return
  await remove(row)
}

async function probeServerHost(row: Parameters<typeof probe>[0]) {
  if (!ensureServerPermission()) return
  await probe(row)
}

async function reorderServers(ids: string[]) {
  if (!ensureServerPermission()) return
  await reorder(ids)
}
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
