import type { AppFrontendModule } from '../registry/contract'
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import { redisFrontendAppForLocale } from './catalog'
import { redisInstallDialogProps } from './i18n'

const redisModule: AppFrontendModule = {
  name: 'redis',
  manifest: redisFrontendAppForLocale,
  installDialog: AppInstallDialog,
  installDialogProps: redisInstallDialogProps
}

export default redisModule
