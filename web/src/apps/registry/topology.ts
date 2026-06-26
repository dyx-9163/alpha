import type { AppInstallField } from './contract'
import type { AppTargetMode, AppTopologyDefinition } from './model'

export function defaultTopology(topologies: AppTopologyDefinition[]) {
  return topologies.find((item) => item.default) ?? topologies[0]
}

export function targetModeResolver(topologies: AppTopologyDefinition[], fallback: AppTargetMode = 'single') {
  const defaultName = defaultTopology(topologies)?.name
  return (values: Record<string, unknown>): AppTargetMode => {
    const selected = String(values.topology ?? defaultName ?? '')
    return topologies.find((item) => item.name === selected)?.targetMode ?? fallback
  }
}

export function topologySelectField(label: string, topologies: AppTopologyDefinition[]): AppInstallField {
  const selected = defaultTopology(topologies)
  return {
    name: 'topology',
    label,
    type: 'select',
    defaultValue: selected?.name,
    required: true,
    options: topologies.map((item) => ({
      label: item.label,
      value: item.name
    }))
  }
}
