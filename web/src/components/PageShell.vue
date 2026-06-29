<template>
  <section class="page-shell">
    <div class="page-head">
      <div class="page-shell-copy">
        <h1 class="page-title">{{ title }}</h1>
        <p v-if="subtitle" class="page-subtitle">{{ subtitle }}</p>
      </div>
      <div v-if="$slots.actions" class="head-actions">
        <slot name="actions" />
      </div>
    </div>
    <slot />
  </section>
</template>

<script setup lang="ts">
defineProps<{
  title: string
  subtitle?: string
}>()
</script>

<style scoped>
.page-shell {
  min-width: 0;
}

.page-shell-copy {
  min-width: 0;
}
</style>
