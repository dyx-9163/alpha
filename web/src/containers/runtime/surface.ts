import type { RuntimeResourceTab } from './context'

export const runtimeResourceTabOrder = [
  'deployments',
  'services',
  'pods',
  'logs',
  'ingress',
  'releases'
] as const satisfies readonly RuntimeResourceTab[]

export const runtimeResourceTabLabels: Record<RuntimeResourceTab, string> = {
  deployments: 'containers.deployments',
  services: 'containers.services',
  pods: 'containers.pods',
  logs: 'containers.logs',
  ingress: 'containers.ingressAndNacos',
  releases: 'containers.releases'
}

export const runtimeIngressColumns = ['service', 'app', 'discoveryTarget', 'endpoint'] as const
