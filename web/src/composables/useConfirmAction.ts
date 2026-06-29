import { ElMessageBox } from 'element-plus'

export type ConfirmActionOptions = {
  message: string
  title?: string
  confirmText?: string
  cancelText?: string
  type?: 'success' | 'warning' | 'info' | 'error'
}

export async function confirmAction(options: ConfirmActionOptions) {
  await ElMessageBox.confirm(options.message, options.title, {
    type: options.type ?? 'warning',
    confirmButtonText: options.confirmText,
    cancelButtonText: options.cancelText
  })
}
