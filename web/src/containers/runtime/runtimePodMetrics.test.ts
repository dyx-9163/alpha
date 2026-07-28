import { describe, expect, it } from 'vitest'
import { mergeRuntimePodMetrics } from './runtimePodMetrics'
import type { AifarRuntimePod } from './types'

describe('Runtime Pod metric merging', () => {
  it('retains previous metrics while incoming base state stays authoritative', () => {
    const previous: AifarRuntimePod[] = [{
      instanceId: 'instance-1',
      containerName: 'pod-1',
      serviceName: 'gateway',
      status: 'starting',
      cpuPercent: 1.5,
      memoryPercent: 25,
      memoryUsage: '512 MiB / 2 GiB'
    }]
    const incoming: AifarRuntimePod[] = [{
      instanceId: 'instance-1',
      containerName: 'pod-1',
      serviceName: 'gateway',
      status: 'ready',
      revision: 'revision-2'
    }]

    expect(mergeRuntimePodMetrics(previous, incoming)).toEqual([{
      ...incoming[0],
      cpuPercent: 1.5,
      memoryPercent: 25,
      memoryUsage: '512 MiB / 2 GiB'
    }])
  })

  it('uses fresh metrics and does not copy metrics between different instances', () => {
    const previous: AifarRuntimePod[] = [{
      instanceId: 'instance-1',
      containerName: 'pod-1',
      serviceName: 'gateway',
      cpuPercent: 9,
      memoryUsage: 'old'
    }]
    const incoming: AifarRuntimePod[] = [
      {
        instanceId: 'instance-1',
        containerName: 'pod-1',
        serviceName: 'gateway',
        cpuPercent: 2,
        memoryUsage: 'new'
      },
      {
        instanceId: 'instance-2',
        containerName: 'pod-1',
        serviceName: 'gateway'
      }
    ]

    expect(mergeRuntimePodMetrics(previous, incoming)).toEqual([
      incoming[0],
      incoming[1]
    ])
  })
})
