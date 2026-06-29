<template>
  <el-dialog
    :model-value="modelValue"
    :title="title"
    width="420px"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
    @closed="secret = ''"
  >
    <p v-if="message" class="secret-confirm-message">{{ message }}</p>
    <el-input
      v-model="secret"
      :type="inputType"
      :placeholder="placeholder"
      show-password
      autofocus
      @keyup.enter="confirm"
    />
    <template #footer>
      <el-button :disabled="loading" @click="emit('update:modelValue', false)">{{ cancelText }}</el-button>
      <el-button type="danger" :loading="loading" @click="confirm">{{ confirmText }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'

const props = withDefaults(defineProps<{
  modelValue: boolean
  title: string
  message?: string
  placeholder?: string
  confirmText: string
  cancelText: string
  loading?: boolean
  inputType?: string
}>(), {
  inputType: 'password'
})

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  confirm: [secret: string]
}>()

const secret = ref('')

watch(() => props.modelValue, (visible) => {
  if (!visible) {
    secret.value = ''
  }
})

function confirm() {
  emit('confirm', secret.value)
}
</script>

<style scoped>
.secret-confirm-message {
  margin: 0 0 12px;
  color: var(--aifar-text-secondary);
  font-size: 14px;
  line-height: 22px;
}
</style>
