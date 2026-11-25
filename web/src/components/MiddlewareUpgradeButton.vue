<template>
  <template v-if="eligible">
    <el-button type="warning" plain :disabled="isDisabled" @click="open">
      {{ actionLabel }}
    </el-button>

    <el-dialog
      v-model="visible"
      :title="actionLabel"
      width="min(94vw, 520px)"
      append-to-body
      :close-on-click-modal="false"
      :show-close="!submitting"
      :close-on-press-escape="!submitting"
    >
      <el-space direction="vertical" alignment="start" fill :size="12">
        <strong>{{ title }} · {{ target?.sourceVersion }} → {{ target?.version }}</strong>
        <el-alert type="warning" :closable="false" :title="t(`middlewareUpgrade.${app}.impact`)" show-icon />
        <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon />
        <el-checkbox v-model="confirmed" :disabled="submitting">
          {{ t('middlewareUpgrade.confirm') }}
        </el-checkbox>
      </el-space>
      <template #footer>
        <el-button :disabled="submitting" @click="visible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="warning" :disabled="!confirmed || isDisabled" :loading="submitting" @click="submit">
          {{ t('middlewareUpgrade.submit') }}
        </el-button>
      </template>
    </el-dialog>
  </template>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { apiPost } from '../api/client'
import { middlewareUpgradeTarget, type MiddlewareUpgradeApp, type MiddlewareUpgradeInstance } from '../apps/middlewareUpgrade'
import { useI18n } from '../i18n'
import { useSessionStore } from '../stores/session'

const props = defineProps<{
  app: MiddlewareUpgradeApp
  title: string
  instances: MiddlewareUpgradeInstance[]
  disabled?: boolean
  resumeDisabled?: boolean
}>()

const router = useRouter()
const session = useSessionStore()
const { t } = useI18n()
const target = computed(() => middlewareUpgradeTarget(props.app, props.instances))
const eligible = computed(() => session.role.trim().toLowerCase() === 'owner' && target.value !== null)
const isDisabled = computed(() => target.value?.resume ? (props.resumeDisabled ?? props.disabled) : props.disabled)
const actionLabel = computed(() => t(target.value?.resume ? 'middlewareUpgrade.mysql.resume' : `middlewareUpgrade.${props.app}.action`))
const visible = ref(false)
const confirmed = ref(false)
const submitting = ref(false)
const error = ref('')

function open() {
  confirmed.value = false
  error.value = ''
  visible.value = true
}

async function submit() {
  const selected = target.value
  if (!selected || !eligible.value || !confirmed.value || submitting.value || isDisabled.value) return
  submitting.value = true
  error.value = ''
  try {
    const result = await apiPost<{ taskId: string }>(`/apps/instances/${encodeURIComponent(selected.instanceId)}/upgrade`, {
      targetVersion: selected.version,
      confirmed: true
    })
    visible.value = false
    await router.push({ path: '/tasks', query: { taskId: result.taskId } })
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    submitting.value = false
  }
}
</script>
