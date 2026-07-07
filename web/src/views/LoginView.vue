<template>
  <main class="login-screen">
    <section class="login-panel">
      <div class="login-brand">
        <div class="brand-mark">A</div>
        <div>
          <h1>{{ t('login.title') }}</h1>
          <span>AIFAR Deployment Console</span>
        </div>
      </div>
      <p class="page-subtitle">{{ t('login.subtitle') }}</p>
      <el-form @submit.prevent="submit">
        <el-form-item>
          <el-input v-model="username" :placeholder="t('login.username')" />
        </el-form-item>
        <el-form-item>
          <el-input v-model="password" :placeholder="t('login.password')" show-password type="password" />
        </el-form-item>
        <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" />
        <el-button class="login-button" type="primary" :loading="loading" @click="submit">{{ t('login.submit') }}</el-button>
      </el-form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useSessionStore } from '../stores/session'
import { useI18n } from '../i18n'

const router = useRouter()
const session = useSessionStore()
const { t } = useI18n()
const username = ref('admin')
const password = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  loading.value = true
  error.value = ''
  try {
    await session.login(username.value, password.value)
    router.push('/dashboard')
  } catch (err) {
    error.value = err instanceof Error ? err.message : t('login.failed')
  } finally {
    password.value = ''
    loading.value = false
  }
}
</script>

<style scoped>
.login-screen {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
  background:
    linear-gradient(90deg, rgba(22, 119, 255, .07) 1px, transparent 1px),
    linear-gradient(180deg, rgba(19, 194, 194, .06) 1px, transparent 1px),
    radial-gradient(circle at 50% 0, rgba(22, 119, 255, .16), transparent 34%),
    var(--aifar-page);
  background-size: 32px 32px, 32px 32px, auto;
}

.login-panel {
  width: min(420px, calc(100vw - 32px));
  position: relative;
  overflow: hidden;
  padding: 28px;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: rgba(255, 255, 255, .96);
  box-shadow: 0 18px 50px rgba(15, 35, 68, .16);
}

.login-panel::before {
  content: "";
  position: absolute;
  inset: 0 0 auto;
  height: 3px;
  background: linear-gradient(90deg, var(--aifar-primary), var(--aifar-cyan));
}

.login-brand {
  display: flex;
  align-items: center;
  gap: 12px;
}

.login-brand h1 {
  margin: 0;
  font-size: 22px;
  line-height: 28px;
  color: var(--aifar-ink);
  letter-spacing: 0;
}

.login-brand span {
  display: block;
  margin-top: 2px;
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.login-panel :deep(.el-form-item) {
  margin-bottom: 16px;
}

.login-panel :deep(.el-alert) {
  margin-bottom: 14px;
}

.login-button {
  width: 100%;
  margin-top: 12px;
  height: 38px;
}
</style>
