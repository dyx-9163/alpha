<template>
  <el-popconfirm
    :title="title"
    :confirm-button-text="confirmText"
    :cancel-button-text="cancelText"
    :disabled="disabled"
    width="280"
    @confirm="emit('confirm')"
  >
    <template #reference>
      <slot />
    </template>
  </el-popconfirm>
</template>

<script setup lang="ts">
withDefaults(defineProps<{
  title: string
  confirmText?: string
  cancelText?: string
  disabled?: boolean
}>(), {
  confirmText: '',
  cancelText: ''
})

const emit = defineEmits<{
  confirm: []
}>()
</script>
