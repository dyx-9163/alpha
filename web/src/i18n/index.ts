import { computed, readonly, ref } from 'vue'
import { messages, type I18nValue, type LocaleCode } from './messages'

const STORAGE_KEY = 'aifar-language'

function normalizeLocale(value?: string | null): LocaleCode {
  const source = value || localStorage.getItem(STORAGE_KEY) || navigator.language || 'zh'
  return source.toLowerCase().startsWith('en') ? 'en' : 'zh'
}

const locale = ref<LocaleCode>(normalizeLocale())

export function resolveLocale(value?: string | null): LocaleCode {
  return normalizeLocale(value)
}

export function getCurrentLocale(): LocaleCode {
  return locale.value
}

export function setLocale(value: string) {
  locale.value = normalizeLocale(value)
  localStorage.setItem(STORAGE_KEY, locale.value)
  document.documentElement.lang = locale.value === 'en' ? 'en' : 'zh-CN'
}

export function translate(key: string, params?: Record<string, unknown>): string {
  const value = readMessage(locale.value, key) ?? readMessage('en', key) ?? key
  if (typeof value === 'function') {
    return value(params)
  }
  return interpolate(value, params)
}

export function useI18n() {
  return {
    locale: readonly(locale),
    isZh: computed(() => locale.value === 'zh'),
    t: translate,
    setLocale
  }
}

function readMessage(lang: LocaleCode, key: string): I18nValue | undefined {
  return messages[lang]?.[key]
}

function interpolate(value: string, params?: Record<string, unknown>) {
  if (!params) {
    return value
  }
  return value.replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? ''))
}

setLocale(locale.value)
