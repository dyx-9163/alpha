import type { AppFrontendModule } from '../registry/contract'
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import { nacosFrontendAppForLocale } from './catalog'
import { nacosInstallDialogProps } from './i18n'

const nacosModule: AppFrontendModule = {
  name: 'nacos',
  manifest: nacosFrontendAppForLocale,
  installDialog: AppInstallDialog,
  installDialogProps: nacosInstallDialogProps
}

export default nacosModule
