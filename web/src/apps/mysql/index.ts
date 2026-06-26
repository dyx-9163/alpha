import type { AppFrontendModule } from '../registry/contract'
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import { mysqlFrontendAppForLocale } from './catalog'
import { mysqlInstallDialogProps } from './i18n'

const mysqlModule: AppFrontendModule = {
  name: 'mysql',
  manifest: mysqlFrontendAppForLocale,
  installDialog: AppInstallDialog,
  installDialogProps: mysqlInstallDialogProps
}

export default mysqlModule
