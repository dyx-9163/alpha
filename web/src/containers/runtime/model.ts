export const AIFAR_SERVICE_CONTROLLER_MODEL = 'agent-service-controller-v1'

export function isAifarServiceControllerModel(model?: string) {
  return String(model || '').trim() === AIFAR_SERVICE_CONTROLLER_MODEL
}
