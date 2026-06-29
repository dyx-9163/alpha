<template>
  <el-dialog v-model="visible" :title="dialogCopy.title" width="min(720px, calc(100vw - 32px))" destroy-on-close>
    <div v-if="app" class="app-install-dialog">
      <div class="install-summary">
        <div class="app-icon small">{{ app.icon }}</div>
        <div>
          <h3>{{ app.title }}</h3>
          <p>{{ app.description }}</p>
        </div>
      </div>

      <el-alert v-if="dialogCopy.hint" type="info" show-icon :closable="false" :title="dialogCopy.hint" />

      <el-form label-width="96px" class="install-form">
        <el-form-item :label="dialogCopy.versionLabel">
          <el-select v-model="selectedVersion" :placeholder="dialogCopy.versionPlaceholder" style="width: 100%">
            <el-option v-for="version in versions" :key="version" :label="version" :value="version" />
          </el-select>
        </el-form-item>

        <el-form-item :label="dialogCopy.serversLabel">
          <ServerSelector
            v-if="effectiveTargetMode === 'multiple'"
            v-model="selectedServerIds"
            :servers="safeServers"
            multiple
            :placeholder="dialogCopy.serversPlaceholder"
          />
          <ServerSelector
            v-else
            v-model="selectedServerId"
            :servers="safeServers"
            :placeholder="dialogCopy.serversPlaceholder"
          />
        </el-form-item>

        <el-form-item v-for="field in installFields" :key="field.name" :label="field.label" :required="field.required">
          <el-select
            v-if="field.type === 'select'"
            v-model="fieldValues[field.name]"
            :placeholder="field.placeholder"
            style="width: 100%"
          >
            <el-option v-for="option in fieldOptions(field)" :key="String(option.value)" :label="option.label" :value="option.value" />
          </el-select>
          <el-switch v-else-if="field.type === 'switch'" v-model="fieldValues[field.name]" />
          <el-input-number
            v-else-if="field.type === 'number'"
            v-model="fieldValues[field.name]"
            :placeholder="field.placeholder"
            :min="field.min"
            :max="field.max"
            :step="field.step"
            style="width: 100%"
          />
          <el-input
            v-else
            v-model="fieldValues[field.name]"
            :type="field.type === 'password' ? 'password' : 'text'"
            :show-password="field.type === 'password'"
            :placeholder="field.placeholder"
          />
          <div v-if="fieldValidationMessages[field.name]" class="field-error">{{ fieldValidationMessages[field.name] }}</div>
        </el-form-item>
      </el-form>

      <slot name="extra" :app="app" :servers="safeServers" :selected-server-ids="selectedServerIds" :selected-server-id="selectedServerId" />

      <div v-if="!safeServers.length" class="empty-server-hint">{{ dialogCopy.noServers }}</div>
      <div v-else-if="selectedTargetCount" class="target-summary">{{ dialogCopy.selectedCount(selectedTargetCount) }}</div>
    </div>

    <template #footer>
      <el-button @click="visible = false">{{ dialogCopy.cancel }}</el-button>
      <el-button type="primary" :loading="submitting" :disabled="!canSubmit" @click="submit">
        {{ dialogCopy.submit }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { AppStoreItem } from '../apps/registry/catalog'
import type { AppInstallDialogCopy, AppInstallField, AppInstallPayload, AppInstallValidationContext, ServerOption } from '../apps/registry/contract'
import type { AppTargetMode } from '../apps/registry/model'
import { useI18n } from '../i18n'
import ServerSelector from './ServerSelector.vue'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  modelValue: boolean
  app: AppStoreItem | null
  servers?: ServerOption[] | null
  submitting?: boolean
  locale?: string
  targetMode?: AppTargetMode
  targetModeResolver?: (values: Record<string, unknown>) => AppTargetMode
  copy?: Partial<AppInstallDialogCopy>
  fields?: AppInstallField[] | null
}>(), {
  targetMode: 'single'
})

const emit = defineEmits<{
  (event: 'update:modelValue', value: boolean): void
  (event: 'submit', payload: AppInstallPayload): void
}>()

const selectedVersion = ref('')
const selectedServerId = ref('')
const selectedServerIds = ref<string[]>([])
const fieldValues = ref<Record<string, string | number | boolean | undefined>>({})
const safeServers = computed(() => Array.isArray(props.servers) ? props.servers : [])
const allFields = computed(() => Array.isArray(props.fields) ? props.fields : [])
const installFields = computed(() => allFields.value.filter((field) => field.visibleWhen?.(fieldValues.value, validationContext.value) ?? true))
const defaultCopy = computed<AppInstallDialogCopy>(() => ({
  title: t('apps.install'),
  versionLabel: t('common.version'),
  versionPlaceholder: t('common.version'),
  serversLabel: t('database.targetServer'),
  serversPlaceholder: t('database.targetServer'),
  noServers: t('servers.emptyDesc'),
  selectedCount: (count: number) => t('apps.selectedTargets', { count }),
  cancel: t('common.cancel'),
  submit: t('apps.install')
}))
const dialogCopy = computed<AppInstallDialogCopy>(() => ({
  ...defaultCopy.value,
  ...props.copy,
  selectedCount: props.copy?.selectedCount ?? defaultCopy.value.selectedCount
}))
const visible = computed({
  get: () => props.modelValue,
  set: (value: boolean) => emit('update:modelValue', value)
})
const versions = computed(() => {
  if (!props.app) {
    return []
  }
  return props.app.versions.length ? props.app.versions : [props.app.fallbackVersion]
})
const effectiveTargetMode = computed(() => props.targetModeResolver?.(fieldValues.value) ?? props.targetMode)
const selectedTargetCount = computed(() => effectiveTargetMode.value === 'multiple' ? selectedServerIds.value.length : Number(Boolean(selectedServerId.value)))
const selectedTargetServers = computed(() => {
  if (effectiveTargetMode.value === 'multiple') {
    const selected = new Set(selectedServerIds.value)
    return safeServers.value.filter((server) => selected.has(server.id))
  }
  return safeServers.value.filter((server) => server.id === selectedServerId.value)
})
const validationContext = computed<AppInstallValidationContext>(() => ({
  servers: safeServers.value,
  selectedServers: selectedTargetServers.value,
  targetMode: effectiveTargetMode.value
}))
const fieldValidationMessages = computed(() => {
  return installFields.value.reduce<Record<string, string>>((messages, field) => {
    const message = field.validate?.(fieldValues.value[field.name], fieldValues.value, validationContext.value)
    if (message) {
      messages[field.name] = message
    }
    return messages
  }, {})
})
const hasFieldValidationErrors = computed(() => Object.keys(fieldValidationMessages.value).length > 0)
const requiredFieldsReady = computed(() => installFields.value.every((field) => !field.required || isFieldValueFilled(field, fieldValues.value[field.name])))
const canSubmit = computed(() => Boolean(selectedVersion.value && selectedTargetCount.value && requiredFieldsReady.value && !hasFieldValidationErrors.value && !props.submitting))

watch(
  () => [props.modelValue, props.app?.name, props.app?.versions.join('|'), props.targetMode, allFields.value.map((field) => field.name).join('|')],
  () => {
    if (!props.modelValue || !props.app) {
      return
    }
    selectedVersion.value = props.app.versions.at(-1) ?? props.app.fallbackVersion
    selectedServerId.value = ''
    selectedServerIds.value = []
    resetFieldValues()
  },
  { immediate: true }
)

function resetFieldValues() {
  const next: Record<string, string | number | boolean | undefined> = {}
  for (const field of allFields.value) {
    next[field.name] = normalizeFieldValue(field.defaultValue)
  }
  fieldValues.value = next
}

function fieldOptions(field: AppInstallField) {
  return field.optionsResolver?.(fieldValues.value, validationContext.value) ?? field.options ?? []
}

function normalizeFieldValue(value: unknown) {
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return value
  }
  return undefined
}

function isFieldValueFilled(field: AppInstallField, value: unknown) {
  if (field.type === 'switch') {
    return value !== undefined
  }
  return value !== undefined && value !== null && String(value).trim() !== ''
}

function extraPayload() {
  return installFields.value.reduce<Record<string, unknown>>((payload, field) => {
    payload[field.name] = fieldValues.value[field.name]
    return payload
  }, {})
}

function submit() {
  if (!canSubmit.value) {
    return
  }
  const extra = extraPayload()
  if (effectiveTargetMode.value === 'multiple') {
    emit('submit', { version: selectedVersion.value, serverIds: selectedServerIds.value, ...extra })
    return
  }
  emit('submit', { version: selectedVersion.value, serverId: selectedServerId.value, ...extra })
}
</script>

<style scoped>
.app-install-dialog {
  display: grid;
  gap: 14px;
}

.install-summary {
  display: flex;
  gap: 12px;
  padding: 12px;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #f7fbff;
}

.install-summary h3 {
  margin: 0 0 4px;
  font-size: 16px;
  line-height: 22px;
  color: var(--aifar-ink);
}

.install-summary p {
  margin: 0;
  color: var(--aifar-text-secondary);
  font-size: 13px;
  line-height: 20px;
}

.app-icon {
  width: 50px;
  height: 50px;
  border-radius: 6px;
  display: grid;
  place-items: center;
  color: var(--aifar-primary);
  background: var(--aifar-primary-soft);
  border: 1px solid #bae0ff;
  font-weight: 850;
  font-size: 16px;
}

.app-icon.small {
  width: 44px;
  height: 44px;
  font-size: 14px;
  flex: 0 0 44px;
}

.install-form {
  padding: 4px 4px 0;
}

.field-error {
  width: 100%;
  margin-top: 4px;
  color: #d93026;
  font-size: 12px;
  line-height: 18px;
}

.empty-server-hint,
.target-summary {
  min-height: 32px;
  display: flex;
  align-items: center;
  border-radius: 6px;
  padding: 0 12px;
  font-size: 13px;
}

.empty-server-hint {
  color: #d48806;
  background: #fffbe6;
  border: 1px solid #ffe58f;
}

.target-summary {
  color: #1677ff;
  background: #e6f4ff;
  border: 1px solid #91caff;
}

@media (max-width: 640px) {
  .install-summary {
    align-items: flex-start;
  }

  .install-form :deep(.el-form-item) {
    display: grid;
  }

  .install-form :deep(.el-form-item__label) {
    justify-content: flex-start;
    width: auto !important;
  }

  .install-form :deep(.el-form-item__content) {
    margin-left: 0 !important;
  }
}
</style>
