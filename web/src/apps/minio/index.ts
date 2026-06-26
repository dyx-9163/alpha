import type { AppFrontendModule } from '../registry/contract'
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import { minioFrontendAppForLocale } from './catalog'
import { minioInstallDialogProps } from './i18n'

const minioModule: AppFrontendModule = {
  name: 'minio',
  manifest: minioFrontendAppForLocale,
  installDialog: AppInstallDialog,
  installDialogProps: minioInstallDialogProps
}

export default minioModule
