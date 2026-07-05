<template>
  <el-tag :type="type">{{ label }}</el-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '../i18n'

const props = defineProps<{ status?: string; label?: string }>()
const { t } = useI18n()

const label = computed(() => {
  if (props.label) {
    return props.label
  }
  switch (props.status) {
    case 'ok':
      return 'OK'
    case 'degraded':
      return t('status.degraded')
    case 'success':
      return t('status.success')
    case 'installed':
      return t('apps.installed')
    case 'running':
      return t('common.running')
    case 'stopped':
      return t('common.stopped')
    case 'missing':
      return t('status.missing')
    case 'available':
      return t('common.available')
    case 'unavailable':
      return t('status.unavailable')
    case 'failed':
    case 'install_failed':
      return t('status.failed')
    case 'error':
      return t('status.error')
    case 'deploying':
      return t('status.deploying')
    case 'pending':
      return t('status.pending')
    case 'staged':
      return t('status.staged')
    case 'probing':
    case 'checking':
      return t('status.probing')
    case 'unknown':
      return t('common.unknown')
    default:
      return props.status || t('common.unknown')
  }
})

const type = computed(() => {
  switch (props.status) {
    case 'ok':
    case 'success':
    case 'installed':
    case 'running':
    case 'available':
      return 'success'
    case 'unavailable':
    case 'failed':
    case 'install_failed':
    case 'error':
      return 'danger'
    case 'deploying':
    case 'pending':
    case 'staged':
    case 'probing':
    case 'checking':
    case 'stopped':
    case 'degraded':
      return 'warning'
    case 'missing':
      return 'danger'
    default:
      return 'info'
  }
})
</script>
