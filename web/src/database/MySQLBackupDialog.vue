<template>
  <el-dialog
    :model-value="modelValue"
    :title="t('database.mysqlBackup.createTitle')"
    width="min(720px, calc(100vw - 32px))"
    class="mysql-backup-dialog"
    :close-on-click-modal="false"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
    @closed="resetForm"
  >
    <el-alert type="info" :closable="false" show-icon>
      <template #title>{{ t('database.mysqlBackup.sourceSummary', { source: sourceLabel }) }}</template>
    </el-alert>
    <el-form class="backup-form" label-position="top" @submit.prevent="submit">
      <el-form-item :label="t('database.mysqlBackup.name')" required>
        <el-input
          v-model="form.name"
          maxlength="128"
          show-word-limit
          :placeholder="t('database.mysqlBackup.namePlaceholder')"
          :aria-label="t('database.mysqlBackup.name')"
        />
      </el-form-item>
	  <section class="schema-section" :aria-label="t('database.mysqlBackup.schemaSelection')">
		<div class="schema-heading">
		  <div>
			<strong>{{ t('database.mysqlBackup.schemaSelection') }}</strong>
			<p>{{ t('database.mysqlBackup.schemaSelectionHint') }}</p>
		  </div>
		  <el-button v-if="schemaError" size="small" @click="loadSchemas">{{ t('common.refresh') }}</el-button>
		</div>
		<el-skeleton v-if="schemasLoading" :rows="3" animated />
		<el-alert v-else-if="schemaError" type="error" :closable="false" show-icon :title="t('database.mysqlBackup.schemaLoadFailed')" />
		<div v-else class="schema-groups">
		  <div v-for="group in schemaGroups" :key="group.category" class="schema-group" :data-category="group.category">
			<div class="schema-group-title">
			  <span>{{ t(group.titleKey) }}</span>
			  <el-tag size="small" :type="group.category === 'business' ? 'success' : 'info'">{{ group.schemas.length }}</el-tag>
			</div>
			<p class="schema-group-hint">{{ t(group.hintKey) }}</p>
			<div v-if="group.schemas.length" class="schema-list">
			  <el-checkbox
				v-for="schema in group.schemas"
				:key="schema.name"
				:model-value="form.schemas.includes(schema.name)"
				:disabled="!schema.selectable"
				@change="toggleSchema(schema.name, $event)"
			  >{{ schema.name }}</el-checkbox>
			</div>
			<span v-else class="schema-empty">{{ t('database.mysqlBackup.schemaNone') }}</span>
		  </div>
		</div>
		<p v-if="!schemasLoading && !schemaError && form.schemas.length === 0" class="schema-required">{{ t('database.mysqlBackup.schemaRequired') }}</p>
	  </section>
      <div class="parameter-grid">
        <el-form-item :label="t('database.mysqlBackup.threads')" required>
          <el-input-number v-model="form.threads" :min="1" :max="64" :step="1" :precision="0" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('database.mysqlBackup.maxRate')" required>
          <el-input-number v-model="form.maxRateMBps" :min="0" :max="1048576" :step="1" :precision="0" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('database.mysqlBackup.keepLast')">
          <el-input-number v-model="form.keepLast" :min="1" :max="100000" :step="1" :precision="0" controls-position="right" />
        </el-form-item>
      </div>
      <p class="form-note">{{ t('database.mysqlBackup.rateHint') }}</p>
    </el-form>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="submitting" :disabled="!canSubmit" @click="submit">
        {{ t('database.mysqlBackup.createAction') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { useTaskProgressStore } from '../stores/taskProgress'
import { backupDefaults, discoverMySQLBackupSchemas, startMySQLBackup, type MySQLBackupDefaults, type MySQLBackupSchema, type MySQLBackupSchemaCategory } from './mysqlBackup'

const props = withDefaults(defineProps<{
  modelValue: boolean
  instanceId: string
  sourceLabel: string
  defaults: MySQLBackupDefaults
  submissionAllowed?: boolean
  beforeSubmit?: () => boolean | Promise<boolean>
}>(), { submissionAllowed: true })

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  submitted: []
}>()

const { t } = useI18n()
const taskProgress = useTaskProgressStore()
const submitting = computed(() => form.submitting)
const form = reactive({ name: '', threads: 4, maxRateMBps: 0, keepLast: undefined as number | undefined, schemas: [] as string[], submitting: false })
const schemas = ref<MySQLBackupSchema[]>([])
const schemasLoading = ref(false)
const schemaError = ref(false)
let schemaLoadVersion = 0
const schemaGroups = computed(() => ([
	{ category: 'server-system' as MySQLBackupSchemaCategory, titleKey: 'database.mysqlBackup.schemaServerSystem', hintKey: 'database.mysqlBackup.schemaServerSystemHint' },
	{ category: 'cluster-metadata' as MySQLBackupSchemaCategory, titleKey: 'database.mysqlBackup.schemaClusterMetadata', hintKey: 'database.mysqlBackup.schemaClusterMetadataHint' },
	{ category: 'business' as MySQLBackupSchemaCategory, titleKey: 'database.mysqlBackup.schemaBusiness', hintKey: 'database.mysqlBackup.schemaBusinessHint' }
].map((group) => ({ ...group, schemas: schemas.value.filter((schema) => schema.category === group.category) }))))
const canSubmit = computed(() => props.submissionAllowed && !!form.name.trim() && form.schemas.length > 0 && !schemasLoading.value && !schemaError.value && Number.isInteger(form.threads) && form.threads >= 1 && form.threads <= 64 && form.maxRateMBps >= 0 && !form.submitting)

watch(() => props.modelValue, (visible) => {
	if (visible) {
		resetForm()
		void loadSchemas()
	} else {
		schemaLoadVersion++
	}
}, { immediate: true })

function resetForm() {
  if (form.submitting) return
  const defaults = backupDefaults(props.defaults)
  form.name = ''
  form.threads = defaults.threads
  form.maxRateMBps = defaults.maxRateMBps
  form.keepLast = defaults.keepLast
	form.schemas = []
	schemas.value = []
	schemaError.value = false
}

async function loadSchemas() {
	const version = ++schemaLoadVersion
	schemasLoading.value = true
	schemaError.value = false
	try {
		const catalog = await discoverMySQLBackupSchemas(props.instanceId)
		if (version !== schemaLoadVersion || !props.modelValue) return
		schemas.value = catalog.schemas
		form.schemas = catalog.schemas.filter((schema) => schema.selectedByDefault).map((schema) => schema.name)
	} catch {
		if (version !== schemaLoadVersion || !props.modelValue) return
		schemas.value = []
		form.schemas = []
		schemaError.value = true
	} finally {
		if (version === schemaLoadVersion) schemasLoading.value = false
	}
}

function toggleSchema(name: string, checked: unknown) {
	const schema = schemas.value.find((candidate) => candidate.name === name)
	if (!schema?.selectable) return
	const selected = new Set(form.schemas)
	if (checked === true) selected.add(name)
	else selected.delete(name)
	form.schemas = schemas.value.filter((candidate) => selected.has(candidate.name)).map((candidate) => candidate.name)
}

async function submit() {
  if (!canSubmit.value) return
  if (props.beforeSubmit && !await props.beforeSubmit()) {
    ElMessage.warning(t('database.mysqlBackup.staleOperationBlocked'))
    return
  }
  form.submitting = true
  try {
    await startMySQLBackup(props.instanceId, {
      name: form.name,
      threads: form.threads,
      maxRateMBps: form.maxRateMBps,
	  schemas: [...form.schemas],
      ...(form.keepLast ? { keepLast: form.keepLast } : {})
    }, taskProgress, t('database.mysqlBackup.taskLabel'))
    ElMessage.success(t('database.mysqlBackup.accepted'))
    emit('submitted')
    emit('update:modelValue', false)
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : t('database.mysqlBackup.submitFailed'))
  } finally {
    form.submitting = false
  }
}
</script>

<style scoped>
.backup-form {
  margin-top: 24px;
}

.parameter-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.schema-section {
	margin-bottom: 24px;
	padding: 16px;
	border: 1px solid var(--aifar-border-soft);
	border-radius: 8px;
	background: var(--aifar-surface-subtle);
}

.schema-heading,
.schema-group-title {
	display: flex;
	align-items: flex-start;
	justify-content: space-between;
	gap: 8px;
}

.schema-heading p,
.schema-group-hint {
	margin: 4px 0 0;
	color: var(--aifar-text-tertiary);
	font-size: 12px;
	line-height: 20px;
}

.schema-groups {
	display: grid;
	gap: 12px;
	margin-top: 16px;
}

.schema-group {
	padding: 12px;
	border: 1px solid var(--aifar-border-soft);
	border-radius: 6px;
	background: var(--aifar-surface);
}

.schema-group-title span {
	font-weight: 600;
}

.schema-list {
	display: grid;
	grid-template-columns: repeat(2, minmax(0, 1fr));
	max-height: 144px;
	margin-top: 8px;
	overflow: auto;
}

.schema-empty,
.schema-required {
	display: block;
	margin-top: 8px;
	color: var(--aifar-text-tertiary);
	font-size: 12px;
}

.schema-required {
	color: var(--el-color-danger);
}

.parameter-grid :deep(.el-input-number) {
  width: 100%;
}

.form-note {
  margin: 0;
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  line-height: 20px;
}

@media (max-width: 767px) {
  .parameter-grid {
    grid-template-columns: 1fr;
  }

	.schema-list {
		grid-template-columns: 1fr;
	}
}
</style>
