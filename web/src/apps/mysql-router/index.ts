import type { AppFrontendModule } from '../registry/contract'
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import { mysqlRouterFrontendAppForLocale } from './catalog'
import { mysqlRouterDeployDisabledReason, mysqlRouterInstallDialogProps } from './i18n'

const mysqlRouterModule: AppFrontendModule = {
  name: 'mysql-router',
  manifest: mysqlRouterFrontendAppForLocale,
  installDialog: AppInstallDialog,
  installDialogProps: mysqlRouterInstallDialogProps,
  deployDisabledReason: mysqlRouterDeployDisabledReason,
  supportsMultiTarget: true
}

export default mysqlRouterModule
