import type { AifarRuntimePod } from './types'

function runtimePodKey(pod: AifarRuntimePod) {
  return `${pod.instanceId}\u0000${pod.containerName}`
}

export function mergeRuntimePodMetrics(previous: AifarRuntimePod[], incoming: AifarRuntimePod[]) {
  const previousByKey = new Map(previous.map((pod) => [runtimePodKey(pod), pod]))
  return incoming.map((pod) => {
    const prior = previousByKey.get(runtimePodKey(pod))
    if (!prior) return pod
    return {
      ...pod,
      cpuPercent: pod.cpuPercent ?? prior.cpuPercent,
      memoryPercent: pod.memoryPercent ?? prior.memoryPercent,
      memoryUsage: pod.memoryUsage ?? prior.memoryUsage
    }
  })
}
