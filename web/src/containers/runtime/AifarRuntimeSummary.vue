<template>
  <dl class="runtime-summary" :aria-label="label">
    <div v-for="item in items" :key="item.key || item.label" class="runtime-summary-item">
      <dt>{{ item.label }}</dt>
      <dd :title="String(item.value ?? '-')">
        <StatusTag v-if="item.status" :status="item.status" :label="String(item.value ?? '-')" />
        <span v-else>{{ item.value ?? '-' }}</span>
      </dd>
    </div>
  </dl>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import type { KeyValueItem } from './context'

defineProps<{ items: KeyValueItem[]; label: string }>()
</script>
