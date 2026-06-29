<template>
  <div class="kv-grid">
    <template v-for="item in items" :key="item.key || item.label">
      <div class="key">{{ item.label }}</div>
      <div>
        <slot name="value" :item="item">
          <StatusTag v-if="item.status" :status="item.status" />
          <span v-else>{{ displayValue(item.value) }}</span>
        </slot>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import StatusTag from './StatusTag.vue'

type KeyValueItem = {
  key?: string
  label: string
  value?: string | number | boolean | null
  status?: string
}

defineProps<{
  items: KeyValueItem[]
}>()

function displayValue(value: KeyValueItem['value']) {
  if (value === undefined || value === null || value === '') {
    return '-'
  }
  return String(value)
}
</script>
