import { aifarFrontendAppForLocale } from './catalog'
import { aifarInstallDialogProps } from './i18n'
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import type { AppFrontendModule } from '../registry/contract'

export const aifarFrontendModule: AppFrontendModule = {
  name: 'aifar',
  manifest: aifarFrontendAppForLocale,
  installDialog: AppInstallDialog,
  installDialogProps: aifarInstallDialogProps,
  supportsMultiTarget: false
}

export default aifarFrontendModule
