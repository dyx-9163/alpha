import { provide } from 'vue'
import { aifarRuntimeContextKey, type AifarRuntimeContext } from './context'

export function useAifarRuntimeProvider(context: AifarRuntimeContext) {
  provide(aifarRuntimeContextKey, context)
  return context
}
