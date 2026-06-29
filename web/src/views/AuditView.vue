<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('audit.title') }}</h1>
        <p class="page-subtitle">{{ t('audit.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-button @click="load">{{ t('common.refresh') }}</el-button>
        <ConfirmAction
          :message="t('audit.confirmDeleteSelected', { count: selectedRows.length })"
          :title="t('common.delete')"
          :confirm-text="t('common.delete')"
          :cancel-text="t('common.cancel')"
          :disabled="!selectedRows.length"
          @confirm="deleteSelected"
        >
          <template #default="{ confirm }">
            <el-button type="danger" plain :disabled="!selectedRows.length" @click="confirm">
              {{ t('audit.deleteSelected', { count: selectedRows.length }) }}
            </el-button>
          </template>
        </ConfirmAction>
      </div>
    </div>

    <div class="workspace-card audit-card">
      <div class="filter-row">
        <el-select v-model="moduleFilter" :placeholder="t('common.module')" clearable class="toolbar-control is-sm" @change="resetAndLoad">
          <el-option v-for="name in modules" :key="name" :label="name" :value="name" />
        </el-select>
        <el-select v-model="statusFilter" :placeholder="t('common.status')" clearable class="toolbar-control is-sm" @change="resetAndLoad">
          <el-option :label="t('status.success')" value="success" />
          <el-option :label="t('common.running')" value="running" />
          <el-option :label="t('status.failed')" value="failed" />
        </el-select>
      </div>

      <div class="audit-table-wrap">
        <el-table
          :data="items"
          height="100%"
          @selection-change="selectedRows = $event"
        >
          <el-table-column type="selection" width="42" />
          <el-table-column prop="createdAt" :label="t('common.time')" min-width="170" />
          <el-table-column prop="actor" :label="t('common.actor')" min-width="120" />
          <el-table-column :label="t('common.module')" width="110">
            <template #default="{ row }">{{ row.action?.split('.')[0] }}</template>
          </el-table-column>
          <el-table-column prop="action" :label="t('common.action')" min-width="180" show-overflow-tooltip />
          <el-table-column prop="target" :label="t('common.target')" min-width="180" show-overflow-tooltip />
          <el-table-column :label="t('common.provider')" width="110">
            <template #default>{{ t('common.real') }}</template>
          </el-table-column>
          <el-table-column prop="status" :label="t('common.status')" width="110">
            <template #default="{ row }"><StatusTag :status="row.status" /></template>
          </el-table-column>
          <el-table-column prop="message" :label="t('common.details')" min-width="180" show-overflow-tooltip />
        </el-table>
      </div>

      <div class="audit-footer">
        <span>{{ t('audit.total', { count: total }) }}</span>
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          background
          layout="sizes, prev, pager, next, jumper"
          :page-sizes="[20, 50, 100, 200]"
          :total="total"
          @size-change="resetAndLoad"
          @current-change="load"
        />
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiDelete, apiGet, asArray } from '../api/client'
import ConfirmAction from '../components/ConfirmAction.vue'
import StatusTag from '../components/StatusTag.vue'
import { useI18n } from '../i18n'

type AuditItem = {
  id: number
  actor: string
  action: string
  target: string
  status: string
  message: string
  createdAt: string
}

type AuditPage = {
  items: AuditItem[]
  total: number
  page: number
  pageSize: number
}

const { t } = useI18n()
const items = ref<AuditItem[]>([])
const selectedRows = ref<AuditItem[]>([])
const moduleFilter = ref('')
const statusFilter = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const modules = ['auth', 'apps', 'servers', 'tasks', 'audit', 'containers', 'database', 'storage', 'settings', 'resources', 'terminal']

async function load() {
  const params = new URLSearchParams({
    page: String(page.value),
    pageSize: String(pageSize.value)
  })
  if (moduleFilter.value) params.set('module', moduleFilter.value)
  if (statusFilter.value) params.set('status', statusFilter.value)
  const result = await apiGet<AuditPage>(`/audit?${params.toString()}`).catch(() => ({ items: [], total: 0, page: 1, pageSize: pageSize.value }))
  items.value = asArray<AuditItem>(result.items)
  total.value = result.total ?? 0
  page.value = result.page ?? page.value
  pageSize.value = result.pageSize ?? pageSize.value
  const maxPage = Math.max(1, Math.ceil(total.value / pageSize.value))
  if (!items.value.length && total.value > 0 && page.value > maxPage) {
    page.value = maxPage
    await load()
    return
  }
  selectedRows.value = []
}

async function resetAndLoad() {
  page.value = 1
  await load()
}

async function deleteSelected() {
  const ids = selectedRows.value.map((item) => item.id).filter(Boolean)
  if (!ids.length) {
    return
  }
  try {
    await apiDelete('/audit', { ids })
    ElMessage.success(t('audit.deleted', { count: ids.length }))
    await load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
}

onMounted(load)
</script>

<style scoped>
.workspace-card.audit-card {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr) auto;
  overflow: hidden;
  min-height: 0;
}

.filter-row {
  padding: 12px;
  border-bottom: 1px solid var(--aifar-border-soft);
  background: linear-gradient(180deg, #fff, #fbfdff);
}

.audit-table-wrap {
  min-height: 0;
  overflow: hidden;
}

.audit-table-wrap :deep(.el-table) {
  height: 100%;
}

.audit-footer {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: center;
  padding: 10px 12px;
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  border-top: 1px solid var(--aifar-border-soft);
}

@media (max-width: 720px) {
  .audit-footer {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
