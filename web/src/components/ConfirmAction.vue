<template>
  <slot :confirm="confirm" />
</template>

<script setup lang="ts">
import { confirmAction } from '../composables/useConfirmAction'

const props = withDefaults(defineProps<{
  message: string
  title?: string
  confirmText?: string
  cancelText?: string
  type?: 'success' | 'warning' | 'info' | 'error'
  disabled?: boolean
}>(), {
  type: 'warning'
})

const emit = defineEmits<{
  confirm: []
  cancel: []
}>()

async function confirm() {
  if (props.disabled) {
    return
  }
  try {
    await confirmAction({
      message: props.message,
      title: props.title,
      type: props.type,
      confirmText: props.confirmText,
      cancelText: props.cancelText
    })
    emit('confirm')
  } catch {
    emit('cancel')
  }
}
</script>
