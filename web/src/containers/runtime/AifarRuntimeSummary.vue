<template>
  <dl class="runtime-summary" :aria-label="label">
    <div v-for="item in items" :key="item.key || item.label" class="runtime-summary-item">
      <dt>{{ item.label }}</dt>
      <dd :title="String(item.value ?? '-')" :aria-label="`${item.label}: ${String(item.value ?? '-')}`">
        <span class="runtime-summary-value">{{ item.value ?? '-' }}</span>
        <StatusTag v-if="item.status" :status="item.status" />
      </dd>
    </div>
  </dl>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import type { KeyValueItem } from './context'

defineProps<{ items: KeyValueItem[]; label: string }>()
</script>
