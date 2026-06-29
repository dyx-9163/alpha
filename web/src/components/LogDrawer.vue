<template>
  <el-drawer :model-value="modelValue" :title="title" :size="size" @update:model-value="emit('update:modelValue', $event)">
    <LogOutput :text="text" :lines="lines" :empty-text="emptyText" min-height="calc(100vh - 140px)" auto-scroll />
  </el-drawer>
</template>

<script setup lang="ts">
import LogOutput from './LogOutput.vue'

type LogLine = {
  id?: string | number
  level?: string
  message: string
  createdAt?: string
}

withDefaults(defineProps<{
  modelValue: boolean
  title: string
  text?: string
  lines?: LogLine[]
  emptyText: string
  size?: string
}>(), {
  text: '',
  lines: () => [],
  size: '52%'
})

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()
</script>
