<template>
  <div class="metric-grid">
    <div v-for="item in items" :key="item.key || item.label" class="metric-card">
      <div class="label">{{ item.label }}</div>
      <div class="value">{{ item.value }}</div>
      <div v-if="item.note" class="subtle-note">{{ item.note }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
type MetricItem = {
  key?: string
  label: string
  value: string | number
  note?: string
}

defineProps<{
  items: MetricItem[]
}>()
</script>

<style scoped>
.metric-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 12px;
}

@media (max-width: 640px) {
  .metric-grid {
    grid-template-columns: 1fr;
  }
}
</style>
