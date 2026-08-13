import { runtimeHealthDisplayStatus, serverReachabilityDisplayStatus } from '../status/semantics'

export function normalizeDashboardServerStatus(status: unknown) {
  return serverReachabilityDisplayStatus(status)
}

export function normalizeDashboardRuntimeStatus(status: unknown) {
  return runtimeHealthDisplayStatus(status)
}
