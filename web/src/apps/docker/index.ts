import { dockerFrontendAppForLocale } from './catalog'
import { dockerInstallDialogProps } from './i18n'
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import type { AppFrontendModule } from '../registry/contract'

export const dockerFrontendModule: AppFrontendModule = {
  name: 'docker',
  manifest: dockerFrontendAppForLocale,
  installDialog: AppInstallDialog,
  installDialogProps: dockerInstallDialogProps,
  supportsMultiTarget: true
}

export default dockerFrontendModule
