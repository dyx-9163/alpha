<template>
  <el-select
    v-model="selected"
    :multiple="multiple"
    :clearable="clearable"
    :filterable="filterable"
    :disabled="disabled"
    :collapse-tags="multiple"
    collapse-tags-tooltip
    :placeholder="placeholder"
    :size="size"
    class="server-selector"
  >
    <el-option v-for="server in servers" :key="server.id" :label="serverLabel(server)" :value="server.id" />
  </el-select>
</template>

<script setup lang="ts">
import { computed } from 'vue'

export type ServerSelectorOption = {
  id: string
  name?: string
  host?: string
  status?: string
  dockerHost?: string
}

const props = withDefaults(defineProps<{
  modelValue: string | string[]
  servers: ServerSelectorOption[]
  placeholder?: string
  multiple?: boolean
  clearable?: boolean
  filterable?: boolean
  disabled?: boolean
  size?: 'large' | 'default' | 'small'
}>(), {
  servers: () => [],
  placeholder: '',
  clearable: true,
  filterable: true,
  size: 'default'
})

const emit = defineEmits<{
  'update:modelValue': [value: string | string[]]
}>()

const selected = computed({
  get: () => props.modelValue,
  set: (value: string | string[]) => emit('update:modelValue', value)
})

function serverLabel(server: ServerSelectorOption) {
  if (server.name && server.host) {
    return `${server.name} (${server.host})`
  }
  return server.name || server.host || server.id
}
</script>

<style scoped>
.server-selector {
  width: 100%;
  min-width: 0;
}
</style>
