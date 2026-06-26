<template>
  <el-drawer v-model="visible" :title="title" size="460px">
    <el-form label-position="top">
      <el-form-item :label="t('servers.name')" required><el-input v-model="form.name" /></el-form-item>
      <el-form-item :label="t('servers.host')" required><el-input v-model="form.host" /></el-form-item>
      <el-form-item :label="t('servers.port')" required><el-input-number v-model="form.port" :min="1" :max="65535" /></el-form-item>
      <el-form-item :label="t('servers.username')" required><el-input v-model="form.username" /></el-form-item>
      <el-form-item :label="t('servers.authType')">
        <el-radio-group v-model="form.authType">
          <el-radio-button label="password">{{ t('servers.authPassword') }}</el-radio-button>
          <el-radio-button label="privateKey">{{ t('servers.authPrivateKey') }}</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="form.authType !== 'privateKey'" :label="t('servers.password')">
        <el-input v-model="form.password" show-password />
      </el-form-item>
      <el-form-item v-else :label="t('servers.privateKey')">
        <el-input v-model="form.privateKey" type="textarea" :rows="7" />
      </el-form-item>
      <el-form-item :label="t('servers.deployDir')"><el-input v-model="form.deployDir" /></el-form-item>
      <el-form-item :label="t('servers.tags')"><el-input v-model="form.tags" /></el-form-item>
      <el-form-item :label="t('servers.note')"><el-input v-model="form.note" type="textarea" :rows="3" /></el-form-item>
      <div class="drawer-actions">
        <el-button @click="visible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" @click="$emit('save')">{{ t('servers.save') }}</el-button>
      </div>
    </el-form>
  </el-drawer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '../../i18n'
import type { ServerFormModel } from '../types'

const visible = defineModel<boolean>('visible', { default: false })
const props = defineProps<{ form: ServerFormModel }>()
defineEmits<{ save: [] }>()
const { t } = useI18n()
const title = computed(() => props.form.id ? t('servers.editTitle') : t('servers.createTitle'))
</script>

<style scoped>
.drawer-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--aifar-border-soft);
}

:deep(.el-form-item__label) {
  color: var(--aifar-ink);
  font-weight: 750;
}
</style>
