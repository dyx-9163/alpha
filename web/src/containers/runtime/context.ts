import { inject, type InjectionKey } from 'vue'

export type AifarRuntimeContext = Record<string, any>

export const aifarRuntimeContextKey: InjectionKey<AifarRuntimeContext> = Symbol('AifarRuntimeContext')
export const aifarRuntimeDialogContextKey = aifarRuntimeContextKey

export function useAifarRuntimeContext() {
  const context = inject(aifarRuntimeContextKey)
  if (!context) {
    throw new Error('AIFAR runtime context is not provided')
  }
  return context
}

export const useAifarRuntimeDialogContext = useAifarRuntimeContext
