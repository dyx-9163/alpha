<template>
  <aside class="server-list workspace-card">
    <div class="inventory-head">
      <strong>{{ t('servers.inventory') }}</strong>
      <span class="status-pill">{{ servers.length }}</span>
    </div>
    <el-input v-model="model" :placeholder="t('servers.search')" clearable />
    <DraggableList
      v-if="servers.length"
      class="server-draggable-list"
      :items="servers"
      item-key="id"
      :disabled="dragDisabled"
      @reorder="handleReorder"
    >
      <template #default="{ item: server }">
        <button
          class="server-card"
          :class="{ active: server.id === selectedId }"
          @click="emit('select', server.id)"
        >
          <strong>{{ server.name }}</strong>
          <span>{{ server.username }}@{{ server.host }}:{{ server.port }}</span>
          <span class="server-tags">
            <StatusTag :status="server.status" />
          </span>
        </button>
      </template>
    </DraggableList>
    <div v-if="!servers.length" class="empty-state compact-empty">
      <div><strong>{{ t('servers.emptyTitle') }}</strong><span>{{ t('servers.emptyDesc') }}</span></div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import DraggableList from '../../components/DraggableList.vue'
import StatusTag from '../../components/StatusTag.vue'
import { useI18n } from '../../i18n'
import type { ServerRecord } from '../types'

type ReorderPayload = {
  keys: Array<string | number>
}

const props = defineProps<{
  servers: ServerRecord[]
  selectedId: string
  search: string
  dragDisabled?: boolean
}>()
const emit = defineEmits<{
  'update:search': [value: string]
  select: [id: string]
  reorder: [ids: string[]]
}>()
const { t } = useI18n()
const model = computed({
  get: () => props.search,
  set: (value: string) => emit('update:search', value)
})

function handleReorder(payload: ReorderPayload) {
  emit('reorder', payload.keys.map(String))
}
</script>

<style scoped>
.server-list {
  padding: 10px;
  min-height: 0;
  height: 100%;
  overflow: auto;
  scrollbar-width: none;
  -ms-overflow-style: none;
}

.server-list::-webkit-scrollbar {
  width: 0;
  height: 0;
}

.inventory-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 12px;
  color: var(--aifar-ink);
}

.server-draggable-list {
  margin-top: 10px;
}

.server-card {
  width: 100%;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 10px;
  text-align: left;
  cursor: pointer;
  box-shadow: 0 1px 2px rgba(15, 35, 68, .03);
  transition: border-color .16s ease, box-shadow .16s ease, background .16s ease;
}

.server-card:hover {
  border-color: #91caff;
  box-shadow: 0 6px 18px rgba(22, 119, 255, .1);
}

.server-card.active {
  border-color: #7db8ff;
  background: var(--aifar-primary-soft);
}

.server-card strong {
  color: var(--aifar-ink);
}

.server-card strong,
.server-card span {
  display: block;
}

.server-card > span {
  margin-top: 4px;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.server-tags {
  display: flex !important;
  gap: 6px;
  margin-top: 8px !important;
}

.compact-empty {
  min-height: 220px;
}

@media (max-width: 1100px) {
  .server-list {
    min-height: auto;
    height: auto;
  }
}
</style>
