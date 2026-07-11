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

      <el-form label-width="108px" class="install-form">
        <el-form-item :label="dialogCopy.versionLabel">
          <el-select v-model="selectedVersion" :placeholder="dialogCopy.versionPlaceholder" style="width: 100%">
            <el-option v-for="version in versions" :key="version" :label="version" :value="version" />
          </el-select>
        </el-form-item>

        <el-form-item v-if="!targetSelectorHidden" :label="dialogCopy.serversLabel">
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
          <div v-if="field.type === 'server-disk-select'" class="server-disk-select-list">
            <div v-for="server in selectedTargetServers" :key="server.id" class="server-disk-row">
              <div class="server-disk-label">{{ serverLabel(server) }}</div>
              <el-select
                :model-value="serverDiskValue(field.name, server.id, field.multiple)"
                :placeholder="field.placeholder"
                :loading="serverDiskLoading(server.id)"
                :multiple="field.multiple"
                :collapse-tags="field.multiple"
                :collapse-tags-tooltip="field.multiple"
                filterable
                style="width: 100%"
                @update:model-value="(value: string | string[]) => setServerDiskValue(field.name, server.id, value, field.multiple)"
              >
                <el-option
                  v-for="option in serverDiskOptions(server.id)"
                  :key="String(option.value)"
                  :label="option.label"
                  :value="option.value"
                  :disabled="option.disabled"
                />
              </el-select>
              <div v-if="serverDiskError(server.id)" class="field-error">{{ serverDiskError(server.id) }}</div>
              <div v-else-if="serverDiskEmpty(server.id)" class="field-hint">{{ t('apps.noDiskCandidates') }}</div>
            </div>
            <div v-if="!selectedTargetServers.length" class="field-hint">{{ dialogCopy.serversPlaceholder }}</div>
          </div>
          <el-select
            v-else-if="field.type === 'select'"
            v-model="fieldValues[field.name]"
            :placeholder="field.placeholder"
            :multiple="field.multiple"
            :collapse-tags="field.multiple"
            :collapse-tags-tooltip="field.multiple"
            style="width: 100%"
          >
            <el-option
              v-for="option in fieldOptions(field)"
              :key="String(option.value)"
              :label="option.label"
              :value="option.value"
              :disabled="option.disabled"
            />
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

      <el-alert
        v-if="targetValidationMessage"
        type="warning"
        show-icon
        :closable="false"
        :title="targetValidationMessage"
      />

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
import { computed, onUnmounted, ref, watch } from 'vue'
import { apiGet } from '../api/client'
import type { AppStoreItem } from '../apps/registry/catalog'
import type { AppInstallDialogCopy, AppInstallField, AppInstallFieldOption, AppInstallFieldValues, AppInstallPayload, AppInstallValidationContext, ServerOption } from '../apps/registry/contract'
import type { AppTargetMode } from '../apps/registry/types'
import { useI18n } from '../i18n'
import { appInstallResetKey, latestInstallVersion } from './appInstallDialogState'
import ServerSelector from './ServerSelector.vue'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  modelValue: boolean
  app: AppStoreItem | null
  servers?: ServerOption[] | null
  submitting?: boolean
  locale?: string
  targetMode?: AppTargetMode
  targetModeResolver?: (values: AppInstallFieldValues) => AppTargetMode
  hideTargetSelector?: boolean
  hideTargetSelectorResolver?: (values: AppInstallFieldValues) => boolean
  targetCountResolver?: (values: AppInstallFieldValues, context: AppInstallValidationContext) => number
  targetIdsResolver?: (values: AppInstallFieldValues, context: AppInstallValidationContext) => string[]
  targetValidationResolver?: (values: AppInstallFieldValues, context: AppInstallValidationContext) => string | undefined | null
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
const fieldValues = ref<AppInstallFieldValues>({})
const serverDiskStates = ref<Record<string, ServerDiskState>>({})
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
  if (props.app.versions.length) {
    return props.app.versions
  }
  return props.app.fallbackVersion ? [props.app.fallbackVersion] : []
})
const effectiveTargetMode = computed(() => props.targetModeResolver?.(fieldValues.value) ?? props.targetMode)
const targetSelectorHidden = computed(() => props.hideTargetSelectorResolver?.(fieldValues.value) ?? props.hideTargetSelector ?? false)
const defaultSelectedTargetIds = computed(() => {
  if (effectiveTargetMode.value === 'multiple') {
    return uniqueStringValues(selectedServerIds.value)
  }
  return selectedServerId.value ? [selectedServerId.value] : []
})
const targetResolverContext = computed<AppInstallValidationContext>(() => ({
  servers: safeServers.value,
  selectedServers: serversByIds(defaultSelectedTargetIds.value),
  targetMode: effectiveTargetMode.value
}))
const hiddenTargetIds = computed(() => uniqueStringValues(props.targetIdsResolver?.(fieldValues.value, targetResolverContext.value) ?? []))
const selectedTargetIds = computed(() => targetSelectorHidden.value ? hiddenTargetIds.value : defaultSelectedTargetIds.value)
const selectedTargetServers = computed(() => serversByIds(selectedTargetIds.value))
const validationContext = computed<AppInstallValidationContext>(() => ({
  servers: safeServers.value,
  selectedServers: selectedTargetServers.value,
  targetMode: effectiveTargetMode.value
}))
const selectedTargetCount = computed(() => {
  if (targetSelectorHidden.value) {
    return Math.max(0, props.targetCountResolver?.(fieldValues.value, validationContext.value) ?? selectedTargetIds.value.length)
  }
  return selectedTargetIds.value.length
})
const targetValidationMessage = computed(() => props.targetValidationResolver?.(fieldValues.value, validationContext.value) || '')
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
const targetReady = computed(() => targetSelectorHidden.value ? (!props.targetIdsResolver || selectedTargetCount.value > 0) : selectedTargetCount.value > 0)
const canSubmit = computed(() => Boolean(selectedVersion.value && targetReady.value && requiredFieldsReady.value && !hasFieldValidationErrors.value && !targetValidationMessage.value && !props.submitting))
const resetTriggerKey = computed(() => appInstallResetKey({
  visible: props.modelValue,
  appName: props.app?.name,
  versions: props.app?.versions ?? [],
  fallbackVersion: props.app?.fallbackVersion,
  targetMode: props.targetMode,
  fields: allFields.value
}))

watch(
  resetTriggerKey,
  () => {
    if (!props.modelValue || !props.app) {
      return
    }
    selectedVersion.value = latestInstallVersion(props.app.versions, props.app.fallbackVersion)
    selectedServerId.value = ''
    selectedServerIds.value = []
    serverDiskStates.value = {}
    resetFieldValues()
  },
  { immediate: true }
)

watch(() => props.modelValue, (isVisible) => {
  if (!isVisible) {
    clearSensitiveFieldValues()
  }
})

onUnmounted(clearSensitiveFieldValues)

watch(
  () => ({
    visible: props.modelValue,
    diskFields: installFields.value.filter((field) => field.type === 'server-disk-select').map((field) => field.name).join('|'),
    targets: selectedTargetServers.value.map((server) => server.id).join('|')
  }),
  () => {
    pruneServerDiskFieldValues()
    void refreshServerDiskOptions()
  },
  { immediate: true }
)

function resetFieldValues() {
  const next: AppInstallFieldValues = {}
  for (const field of allFields.value) {
    next[field.name] = normalizeFieldValue(field.defaultValue)
  }
  fieldValues.value = next
}

function uniqueStringValues(values: string[]) {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    const next = String(value ?? '').trim()
    if (!next || seen.has(next)) {
      continue
    }
    seen.add(next)
    out.push(next)
  }
  return out
}

function serversByIds(ids: string[]) {
  const selected = new Set(ids)
  return safeServers.value.filter((server) => selected.has(server.id))
}

function fieldOptions(field: AppInstallField) {
  return field.optionsResolver?.(fieldValues.value, validationContext.value) ?? field.options ?? []
}

function normalizeFieldValue(value: unknown) {
  if (Array.isArray(value)) {
    return value.filter((item): item is string | number | boolean => typeof item === 'string' || typeof item === 'number' || typeof item === 'boolean')
  }
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return value
  }
  if (isServerDiskRecord(value)) {
    return cloneServerDiskRecord(value)
  }
  return undefined
}

function isFieldValueFilled(field: AppInstallField, value: unknown) {
  if (field.type === 'server-disk-select') {
    const values = serverDiskRecord(value)
    return selectedTargetServers.value.length > 0 && selectedTargetServers.value.every((server) => serverDiskSelectionFilled(values[server.id], field.multiple))
  }
  if (field.type === 'switch') {
    return value !== undefined
  }
  if (Array.isArray(value)) {
    return value.length > 0
  }
  return value !== undefined && value !== null && String(value).trim() !== ''
}

function extraPayload() {
  return installFields.value.reduce<Record<string, unknown>>((payload, field) => {
    payload[field.name] = field.type === 'server-disk-select' ? prunedServerDiskValue(field.name, field.multiple) : fieldValues.value[field.name]
    return payload
  }, {})
}

type ServerDiskDevice = {
  path?: string
  type?: string
  sizeHuman?: string
  model?: string
  fstype?: string
  candidate?: boolean
}

type ServerDiskResponse = {
  devices?: ServerDiskDevice[]
}

type ServerDiskState = {
  loading: boolean
  loaded: boolean
  error: string
  options: AppInstallFieldOption[]
}

function serverLabel(server: ServerOption) {
  if (server.name && server.host) {
    return `${server.name} (${server.host})`
  }
  return server.name || server.host || server.id
}

function serverDiskOptions(serverId: string) {
  return serverDiskStates.value[serverId]?.options ?? []
}

function serverDiskLoading(serverId: string) {
  return Boolean(serverDiskStates.value[serverId]?.loading)
}

function serverDiskError(serverId: string) {
  return serverDiskStates.value[serverId]?.error ?? ''
}

function serverDiskEmpty(serverId: string) {
  const state = serverDiskStates.value[serverId]
  return Boolean(state?.loaded && !state.loading && !state.error && state.options.length === 0)
}

function serverDiskValue(fieldName: string, serverId: string, multiple?: boolean) {
  const value = serverDiskRecord(fieldValues.value[fieldName])[serverId]
  if (multiple) {
    return Array.isArray(value) ? value : value ? [value] : []
  }
  return Array.isArray(value) ? value : value ?? ''
}

function setServerDiskValue(fieldName: string, serverId: string, value: string | string[], multiple?: boolean) {
  const current = serverDiskRecord(fieldValues.value[fieldName])
  fieldValues.value = {
    ...fieldValues.value,
    [fieldName]: {
      ...current,
      [serverId]: multiple ? stringArray(value) : String(value ?? '').trim()
    }
  }
}

function pruneServerDiskFieldValues() {
  const diskFields = allFields.value.filter((field) => field.type === 'server-disk-select')
  if (!diskFields.length) {
    return
  }
  const selected = new Set(selectedTargetServers.value.map((server) => server.id))
  const next = { ...fieldValues.value }
  let changed = false
  for (const field of diskFields) {
    const current = serverDiskRecord(next[field.name])
    const pruned: ServerDiskRecord = {}
    for (const serverId of selected) {
      if (serverDiskSelectionFilled(current[serverId], field.multiple)) {
        pruned[serverId] = current[serverId]
      }
    }
    if (JSON.stringify(current) !== JSON.stringify(pruned)) {
      next[field.name] = pruned
      changed = true
    }
  }
  if (changed) {
    fieldValues.value = next
  }
}

function prunedServerDiskValue(fieldName: string, multiple?: boolean) {
  const selected = new Set(selectedTargetServers.value.map((server) => server.id))
  const current = serverDiskRecord(fieldValues.value[fieldName])
  return Object.fromEntries(Object.entries(current).filter(([serverId, value]) => selected.has(serverId) && serverDiskSelectionFilled(value, multiple)))
}

async function refreshServerDiskOptions() {
  if (!props.modelValue || !installFields.value.some((field) => field.type === 'server-disk-select')) {
    return
  }
  await Promise.all(selectedTargetServers.value.map((server) => loadServerDiskOptions(server.id)))
}

async function loadServerDiskOptions(serverId: string) {
  const existing = serverDiskStates.value[serverId]
  if (existing?.loading || existing?.loaded) {
    return
  }
  serverDiskStates.value = {
    ...serverDiskStates.value,
    [serverId]: { loading: true, loaded: false, error: '', options: [] }
  }
  try {
    const response = await apiGet<ServerDiskResponse>(`/servers/${encodeURIComponent(serverId)}/disks`)
    const options = (Array.isArray(response.devices) ? response.devices : [])
      .filter((device) => device.candidate && device.path)
      .map((device) => ({ label: diskOptionLabel(device), value: String(device.path) }))
    serverDiskStates.value = {
      ...serverDiskStates.value,
      [serverId]: { loading: false, loaded: true, error: '', options }
    }
  } catch (error) {
    serverDiskStates.value = {
      ...serverDiskStates.value,
      [serverId]: {
        loading: false,
        loaded: true,
        error: error instanceof Error ? error.message : t('apps.diskDetectFailed'),
        options: []
      }
    }
  }
}

function diskOptionLabel(device: ServerDiskDevice) {
  return [device.path, device.sizeHuman, device.type, device.fstype ? `fs ${device.fstype}` : '', device.model]
    .filter((part) => typeof part === 'string' && part.trim())
    .join(' - ')
}

type ServerDiskValue = string | string[]
type ServerDiskRecord = Record<string, ServerDiskValue>

function isServerDiskRecord(value: unknown): value is ServerDiskRecord {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value) && Object.values(value).every((item) => {
    return typeof item === 'string' || (Array.isArray(item) && item.every((entry) => typeof entry === 'string'))
  }))
}

function serverDiskRecord(value: unknown): ServerDiskRecord {
  return isServerDiskRecord(value) ? cloneServerDiskRecord(value) : {}
}

function cloneServerDiskRecord(value: ServerDiskRecord): ServerDiskRecord {
  return Object.fromEntries(Object.entries(value).map(([serverId, item]) => [serverId, Array.isArray(item) ? [...item] : item]))
}

function stringArray(value: string | string[]) {
  return (Array.isArray(value) ? value : [value]).map((item) => String(item ?? '').trim()).filter(Boolean)
}

function serverDiskSelectionFilled(value: unknown, multiple?: boolean) {
  if (Array.isArray(value)) {
    return value.some((item) => String(item ?? '').trim())
  }
  if (multiple) {
    return false
  }
  return typeof value === 'string' && value.trim() !== ''
}

function submit() {
  if (!canSubmit.value) {
    return
  }
  const extra = extraPayload()
  if (targetSelectorHidden.value) {
    if (hiddenTargetIds.value.length > 1) {
      emitSubmit({ version: selectedVersion.value, serverIds: hiddenTargetIds.value, ...extra })
      return
    }
    emitSubmit({ version: selectedVersion.value, serverId: hiddenTargetIds.value[0], ...extra })
    return
  }
  if (effectiveTargetMode.value === 'multiple') {
    emitSubmit({ version: selectedVersion.value, serverIds: selectedServerIds.value, ...extra })
    return
  }
  emitSubmit({ version: selectedVersion.value, serverId: selectedServerId.value, ...extra })
}

function emitSubmit(payload: AppInstallPayload) {
  emit('submit', payload)
  clearSensitiveFieldValues()
}

function clearSensitiveFieldValues() {
  const next = { ...fieldValues.value }
  let changed = false
  for (const field of allFields.value) {
    if (!isSensitiveField(field) || next[field.name] === undefined) {
      continue
    }
    next[field.name] = field.multiple ? [] : ''
    changed = true
  }
  if (changed) {
    fieldValues.value = next
  }
}

function isSensitiveField(field: AppInstallField) {
  return field.type === 'password' || /password|secret|token|privatekey/i.test(field.name)
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

.install-form :deep(.el-form-item) {
  margin-bottom: 14px;
}

.install-form :deep(.el-form-item__label) {
  min-width: 0;
  line-height: 32px;
  white-space: nowrap;
}

.install-form :deep(.el-form-item__content) {
  min-width: 0;
}

.field-error {
  width: 100%;
  margin-top: 4px;
  color: #d93026;
  font-size: 12px;
  line-height: 18px;
}

.field-hint {
  width: 100%;
  margin-top: 4px;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  line-height: 18px;
}

.server-disk-select-list {
  width: 100%;
  display: grid;
  gap: 8px;
}

.server-disk-row {
  display: grid;
  grid-template-columns: minmax(160px, 220px) minmax(0, 1fr);
  gap: 8px;
  align-items: start;
}

.server-disk-label {
  min-height: 32px;
  display: flex;
  align-items: center;
  color: var(--aifar-text-secondary);
  font-size: 13px;
  line-height: 18px;
  word-break: break-word;
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

  .server-disk-row {
    grid-template-columns: 1fr;
    gap: 4px;
  }
}
</style>
