import { computed } from 'vue'
import { useSessionStore } from '../stores/session'
import type { Permission } from '../rbac'
import { useI18n } from '../i18n'

export function usePermissions() {
  const session = useSessionStore()
  const { t } = useI18n()
  const deniedText = computed(() => t('common.permissionDenied'))

  function can(permission: Permission) {
    return session.hasPermission(permission)
  }

  return {
    can,
    deniedText
  }
}
