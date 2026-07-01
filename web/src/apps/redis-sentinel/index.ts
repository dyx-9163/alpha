import type { AppFrontendModule } from '../registry/contract'
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import { redisSentinelFrontendAppForLocale } from './catalog'
import { redisSentinelDeployDisabledReason, redisSentinelInstallDialogProps } from './i18n'

const redisSentinelModule: AppFrontendModule = {
  name: 'redis-sentinel',
  manifest: redisSentinelFrontendAppForLocale,
  installDialog: AppInstallDialog,
  installDialogProps: redisSentinelInstallDialogProps,
  deployDisabledReason: redisSentinelDeployDisabledReason,
  supportsMultiTarget: true
}

export default redisSentinelModule
