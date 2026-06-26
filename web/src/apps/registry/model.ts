export type AppTargetMode = 'single' | 'multiple'

export interface AppTopologyDefinition {
  name: string
  label: string
  targetMode: AppTargetMode
  minTargets?: number
  default?: boolean
}
