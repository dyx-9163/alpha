import { getCurrentLocale, resolveLocale } from '../../i18n'

export type AppLocale = 'zh' | 'en'

export function resolveAppLocale(locale?: string): AppLocale {
  return resolveLocale(locale || getCurrentLocale())
}
