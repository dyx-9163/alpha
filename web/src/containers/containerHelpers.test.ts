import { describe, expect, it } from 'vitest'

import {
  activeCollectionKind,
  collectionBackedKind,
  collectionCacheKey,
  containerCacheScope,
  runtimeCacheKey,
  runtimeLogCacheKey,
  summaryCacheKey
} from './cacheKeys'
import { imageReference, imageRowKey, uniqueValues } from './dockerImages'
import { mergeDockerSummarySnapshot } from './realtimeSummary'

describe('container cache keys', () => {
  it('uses an explicit none scope when no server is selected', () => {
    expect(containerCacheScope('')).toBe('none')
    expect(containerCacheScope('server-1')).toBe('server-1')
  })

  it('separates base and disk summary caches', () => {
    expect(summaryCacheKey('server-1', false)).toBe('server-1:summary:base')
    expect(summaryCacheKey('server-1', true)).toBe('server-1:summary:disk')
  })

  it('only selects the resource sub-tab while the images workspace is active', () => {
    expect(activeCollectionKind('images', 'networks')).toBe('networks')
    expect(activeCollectionKind('overview', 'images')).toBe('')
    expect(activeCollectionKind('aifar-runtime', 'volumes')).toBe('')
  })

  it('recognizes only API-backed Docker collections', () => {
    expect(collectionBackedKind('images')).toBe(true)
    expect(collectionBackedKind('networks')).toBe(true)
    expect(collectionBackedKind('volumes')).toBe(true)
    expect(collectionBackedKind('registry')).toBe(false)
    expect(collectionBackedKind('settings')).toBe(false)
    expect(collectionBackedKind('')).toBe(false)
    expect(collectionCacheKey('server-1', 'images')).toBe('server-1:collection:images')
  })

  it('separates runtime base, pod, and complete log cache scopes', () => {
    expect(runtimeCacheKey('server-1')).toBe('server-1:aifar-runtime:base')
    expect(runtimeCacheKey('server-1', 'pods')).toBe('server-1:aifar-runtime:pods')
    expect(runtimeLogCacheKey('server-1', 'instance-1', ['gateway', 'oauth'], ['pod-a'], 200, 60))
      .toBe('server-1:aifar-runtime:logs:instance-1:gateway,oauth:pod-a:200:60')
    expect(runtimeLogCacheKey('none', undefined, [], [], 50, -1))
      .toBe('none:aifar-runtime:logs:none:::50:-1')
  })
})

describe('Docker image helpers', () => {
  it('builds a trimmed repository tag reference', () => {
    expect(imageReference({ repository: ' registry.example/aifar ', tag: ' v1 ', id: 'sha256:fallback' }))
      .toBe('registry.example/aifar:v1')
  })

  it('falls back to the trimmed id for untagged images', () => {
    expect(imageReference({ repository: '<none>', tag: '<none>', id: ' sha256:one ' })).toBe('sha256:one')
    expect(imageReference({ repository: 'repo', tag: '<none>', id: 'sha256:two' })).toBe('sha256:two')
    expect(imageReference({ repository: '', tag: '', id: '' })).toBe('')
  })

  it('provides a stable fallback row key when no image reference exists', () => {
    expect(imageRowKey({ repository: ' repo ', tag: '', id: '' })).toBe('repo::')
    expect(imageRowKey({})).toBe('::')
  })

  it('trims, removes blanks, and deduplicates without changing first-seen order', () => {
    expect(uniqueValues([' gateway ', '', 'oauth', 'gateway', ' oauth ', 'system']))
      .toEqual(['gateway', 'oauth', 'system'])
  })
})

describe('Docker realtime summary merge', () => {
  it('merges the collector event envelope while retaining disk usage', () => {
    const current = {
      available: false,
      error: 'old error',
      summary: { running: 1, stopped: 2 },
      diskUsage: [{ type: 'Images', size: '1 GiB' }]
    }
    const event = {
      type: 'status.docker.summary.updated',
      resource: 'docker.summary',
      serverId: 'server-1',
      payload: {
        scope: 'docker.summary',
        resourceId: 'server-1',
        status: 'available',
        lastError: '',
        version: 9,
        payload: {
          available: true,
          summary: { running: 3, stopped: 0 }
        }
      }
    }

    expect(mergeDockerSummarySnapshot(current, event)).toEqual({
      available: true,
      error: '',
      summary: { running: 3, stopped: 0 },
      diskUsage: current.diskUsage
    })
  })

  it('merges unavailable status and last error without discarding the last summary', () => {
    const current = { available: true, summary: { running: 2 }, diskUsage: [{ type: 'Images' }] }
    const event = {
      payload: {
        lastError: 'docker daemon unavailable',
        payload: { available: false }
      }
    }

    expect(mergeDockerSummarySnapshot(current, event)).toEqual({
      available: false,
      error: 'docker daemon unavailable',
      summary: current.summary,
      diskUsage: current.diskUsage
    })
  })

  it.each([
    ['null event', null],
    ['missing outer payload', {}],
    ['array envelope', { payload: [] }],
    ['missing inner payload', { payload: { status: 'available' } }],
    ['array inner payload', { payload: { payload: [] } }]
  ])('rejects a malformed %s', (_name, event) => {
    expect(mergeDockerSummarySnapshot({ available: true }, event)).toBeNull()
  })
})
