import { inject, type InjectionKey } from 'vue'

export type AifarRuntimeDialogContext = Record<string, any>

export const aifarRuntimeDialogContextKey: InjectionKey<AifarRuntimeDialogContext> = Symbol('AifarRuntimeDialogContext')

export function useAifarRuntimeDialogContext() {
  const context = inject(aifarRuntimeDialogContextKey)
  if (!context) {
    throw new Error('AIFAR runtime dialog context is not provided')
  }
  return context
}
