// @vitest-environment happy-dom

import { createPinia } from 'pinia'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiGetMock, routeQuery, routerReplaceMock } = vi.hoisted(() => ({
  apiGetMock: vi.fn(),
  routeQuery: {} as Record<string, unknown>,
  routerReplaceMock: vi.fn()
}))

vi.mock('../api/client', () => ({
  apiGet: apiGetMock,
  asArray: <T>(value: unknown): T[] => Array.isArray(value) ? value as T[] : []
}))

vi.mock('../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: routeQuery }),
  useRouter: () => ({ replace: routerReplaceMock })
}))

import { normalizeDashboardRuntimeStatus, normalizeDashboardServerStatus } from '../dashboard/serverStatus'
import { useRealtimeStore } from '../stores/realtime'
import DashboardView from './DashboardView.vue'
import dashboardViewSource from './DashboardView.vue?raw'

const normalizedDashboardViewSource = dashboardViewSource.replace(/\r\n/g, '\n')

const server = {
  id: 'srv-1',
  name: 'one',
  host: '10.0.0.1',
  dockerHost: 'unix:///var/run/docker.sock',
  deployDir: '/aifar/apps',
  status: 'unknown'
}

function mountDashboard() {
  return shallowMount(DashboardView, {
    global: {
      plugins: [createPinia()],
      stubs: {
        'el-button': {
          props: ['loading'],
          emits: ['click'],
          template: '<button :data-loading="String(Boolean(loading))" @click="$emit(\'click\')"><slot /></button>'
        },
        MetricGrid: {
          props: ['items'],
          template: '<div class="metric-grid-stub">{{ JSON.stringify(items) }}</div>'
        },
        'el-table': true,
        'el-table-column': true,
        'el-empty': true,
        'el-tag': true,
        RouterLink: {
          props: ['to'],
          template: '<a :href="to"><slot /></a>'
        }
      }
    }
  })
}

function databaseFixture() {
  return [
    { id: 'db-router', app: 'mysql-router', version: '8.0.36', topology: 'router', status: 'failed' },
    { id: 'db-mysql', app: 'mysql', version: '8.0.36', topology: 'innodb-cluster', status: 'failed' },
    { id: 'db-redis', app: 'redis', version: '7.2.14', topology: 'standalone', status: 'failed' }
  ]
}

function componentFixture() {
  return [
    { id: 'db-router', app: 'mysql-router', version: '8.0.36', topology: 'router', status: 'failed' },
    { id: 'db-mysql', app: 'mysql', version: '8.0.36', topology: 'innodb-cluster', status: 'running' },
    { id: 'db-redis', app: 'redis', version: '7.2.14', topology: 'standalone', status: 'failed' }
  ]
}

function databaseClusterFixture() {
  return [
    {
      id: 'mysql-1',
      app: 'mysql',
      version: '8.0.36',
      topology: 'innodb-cluster',
      serverId: 'srv-1',
      status: 'running',
      metadata: JSON.stringify({ clusterId: 'cluster-a', topology: 'innodb-cluster' })
    },
    {
      id: 'mysql-2',
      app: 'mysql',
      version: '8.0.36',
      topology: 'innodb-cluster',
      serverId: 'srv-2',
      status: 'failed',
      metadata: JSON.stringify({ clusterId: 'cluster-a', topology: 'innodb-cluster' })
    },
    {
      id: 'mysql-3',
      app: 'mysql',
      version: '8.0.36',
      topology: 'innodb-cluster',
      serverId: 'srv-3',
      status: 'running',
      metadata: JSON.stringify({ clusterId: 'cluster-a', topology: 'innodb-cluster' })
    },
    {
      id: 'redis-single',
      app: 'redis',
      version: '7.2.14',
      topology: 'standalone',
      serverId: 'srv-1',
      status: 'failed'
    }
  ]
}

function serverFixture(count: number) {
  return Array.from({ length: count }, (_, index) => ({
    ...server,
    id: `srv-${index + 1}`,
    name: `${index + 1}`,
    host: `10.0.0.${index + 1}`,
    dockerHost: index % 3 === 0 ? '' : 'unix:///var/run/docker.sock',
    status: index < 6 ? 'failed' : 'available'
  }))
}

function requestedPaths() {
  return apiGetMock.mock.calls.map((call) => String(call[0]))
}

describe('DashboardView refresh behavior', () => {
  beforeEach(() => {
    localStorage.setItem('aifar-permissions', JSON.stringify(['alerts.view']))
    apiGetMock.mockReset()
    routerReplaceMock.mockReset()
    for (const key of Object.keys(routeQuery)) {
      delete routeQuery[key]
    }
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      if (path === '/servers/srv-1/telemetry') return { sampledAt: '2026-08-12T07:31:00Z', cpu: 12, cpuText: '12%' }
      return null
    })
  })

  it('does not probe server telemetry when the dashboard is opened', async () => {
    mountDashboard()

    await flushPromises()

    expect(requestedPaths()).toEqual(expect.arrayContaining([
      '/servers',
      '/tasks',
      '/database/instances',
      '/storage/instances'
    ]))
    expect(requestedPaths()).not.toContain('/servers/srv-1/telemetry')
  })

  it('does not render a manual refresh button for persisted dashboard snapshots', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.text()).not.toContain('common.refresh')
    expect(requestedPaths()).not.toContain('/servers/srv-1/telemetry')
  })

  it('uses wrapping dashboard lists instead of fixed-width tables', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.findAll('el-table-stub')).toHaveLength(0)
    expect(wrapper.find('.dashboard-component-health').exists()).toBe(true)
    expect(wrapper.find('.dashboard-health-grid').exists()).toBe(false)
    expect(wrapper.find('.dashboard-server-list').exists()).toBe(false)
    expect(wrapper.find('.dashboard-server-card').exists()).toBe(false)
  })

  it('shows aggregate resource counts without rendering all as a resource type tab', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server, { ...server, id: 'srv-2', name: 'two', host: '10.0.0.2', dockerHost: '', status: 'available' }]
      if (path === '/tasks') return []
      if (path === '/database/instances') return componentFixture()
      if (path === '/nacos/instances') return [{ id: 'nacos-1', app: 'nacos', version: '2.4.3', topology: 'standalone', status: 'running', serverId: 'srv-1' }]
      if (path === '/storage/instances') return [{ id: 'minio-1', app: 'minio', version: '2025', topology: 'standalone', status: 'failed', serverId: 'srv-1' }]
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.find('[data-dashboard-component-tab="all"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-dashboard-resource-summary-tile]')).toHaveLength(5)
    expect(wrapper.find('[data-dashboard-resource-overview]').text()).toContain('dashboard.allResources8')
    expect(wrapper.find('[data-dashboard-resource-overview]').text()).toContain('common.running 3')
    expect(wrapper.find('[data-dashboard-resource-overview]').text()).toContain('common.unavailable 5')
    expect(wrapper.text()).toContain('nav.servers')
    expect(wrapper.text()).toContain('Docker')
    expect(wrapper.text()).toContain('nav.database')
    expect(wrapper.text()).toContain('Nacos')
    expect(wrapper.text()).toContain('nav.storage')
    expect(wrapper.text()).toContain('common.unavailable 5')
    expect(wrapper.text()).toContain('one')
    expect(wrapper.text()).toContain('two')
    expect(wrapper.text()).not.toContain('mysql-router')
    expect(wrapper.text()).not.toContain('redis')
  })

  it('opens the dashboard on the server resource tab by default', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server, { ...server, id: 'srv-2', name: 'two', host: '10.0.0.2', status: 'available' }]
      if (path === '/tasks') return []
      if (path === '/database/instances') return componentFixture()
      if (path === '/nacos/instances') return [{ id: 'nacos-1', app: 'nacos', version: '2.4.3', topology: 'standalone', status: 'running', serverId: 'srv-1' }]
      if (path === '/storage/instances') return [{ id: 'minio-1', app: 'minio', version: '2025', topology: 'standalone', status: 'failed', serverId: 'srv-1' }]
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.find('[data-dashboard-component-tab="servers"]').classes()).toContain('active')
    expect(wrapper.find('.dashboard-section-link').attributes('href')).toBe('/servers')
    expect(wrapper.find('[data-dashboard-health-filter="all"]').classes()).toContain('active')
    expect(wrapper.findAll('.dashboard-entity-row')).toHaveLength(2)
    expect(wrapper.text()).toContain('one')
    expect(wrapper.text()).toContain('two')
    expect(wrapper.text()).not.toContain('mysql-router')
  })

  it('keeps dashboard resource cards in the same order as the left navigation modules', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.findAll('[data-dashboard-component-tab]').map((tab) => tab.attributes('data-dashboard-component-tab'))).toEqual([
      'servers',
      'docker',
      'database',
      'nacos',
      'storage'
    ])
  })

  it('keeps server rows in the server workbench order instead of sorting failures first', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [
        { ...server, id: 'srv-1', name: '1', host: '10.0.0.1', dockerHost: 'unix:///var/run/docker.sock', status: 'available' },
        { ...server, id: 'srv-2', name: '2', host: '10.0.0.2', dockerHost: 'unix:///var/run/docker.sock', status: 'failed' },
        { ...server, id: 'srv-3', name: '3', host: '10.0.0.3', dockerHost: '', status: 'failed' },
        { ...server, id: 'srv-4', name: '4', host: '10.0.0.4', dockerHost: '', status: 'available' }
      ]
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/nacos/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.find('[data-dashboard-component-tab="servers"]').classes()).toContain('active')
    expect(wrapper.findAll('.dashboard-entity-main strong').map((title) => title.text())).toEqual(['1', '2', '3', '4'])
  })

  it('filters the unified health preview by resource type and health state', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return componentFixture()
      if (path === '/nacos/instances') return [{ id: 'nacos-1', app: 'nacos', version: '2.4.3', topology: 'standalone', status: 'running', serverId: 'srv-1' }]
      if (path === '/storage/instances') return [{ id: 'minio-1', app: 'minio', version: '2025', topology: 'standalone', status: 'failed', serverId: 'srv-1' }]
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.findAll('[data-dashboard-component-tab]')).toHaveLength(5)
    expect(wrapper.text()).toContain('dashboard.componentHealth')

    await wrapper.find('[data-dashboard-component-tab="database"]').trigger('click')

    expect(wrapper.findAll('.dashboard-health-filter button')).toHaveLength(3)
    expect(wrapper.text()).toContain('common.all 3')
    expect(wrapper.text()).toContain('common.running 1')
    expect(wrapper.text()).toContain('common.unavailable 2')
    expect(wrapper.text()).toContain('mysql-router')
    expect(wrapper.text()).toContain('mysql')
    expect(wrapper.text()).toContain('redis')

    await wrapper.find('[data-dashboard-health-filter="unavailable"]').trigger('click')

    expect(wrapper.text()).toContain('mysql-router')
    expect(wrapper.text()).not.toContain('innodb-cluster')
    expect(wrapper.text()).toContain('redis')

    await wrapper.find('[data-dashboard-component-tab="nacos"]').trigger('click')

    expect(wrapper.text()).toContain('common.all 1')
    expect(wrapper.text()).toContain('common.running 1')
    expect(wrapper.text()).toContain('standalone')
    expect(wrapper.text()).toContain('nacos')
  })

  it('groups clustered runtime resources while standalone instances stay separate', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [
        { ...server, id: 'srv-1', name: 'db-1', host: '10.0.0.1', status: 'available' },
        { ...server, id: 'srv-2', name: 'db-2', host: '10.0.0.2', status: 'available' },
        { ...server, id: 'srv-3', name: 'db-3', host: '10.0.0.3', status: 'available' }
      ]
      if (path === '/tasks') return []
      if (path === '/database/instances') return databaseClusterFixture()
      if (path === '/nacos/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    await wrapper.find('[data-dashboard-component-tab="database"]').trigger('click')

    expect(wrapper.text()).toContain('common.all 2')
    expect(wrapper.text()).toContain('common.running 0')
    expect(wrapper.text()).toContain('common.unavailable 2')
    expect(wrapper.findAll('.dashboard-entity-row')).toHaveLength(2)
    expect(wrapper.text()).toContain('mysql')
    expect(wrapper.text()).toContain('innodb-cluster')
    expect(wrapper.text()).toContain('dashboard.clusterNodes 3')
    expect(wrapper.text()).toContain('dashboard.unavailableNodes 1')
    expect(wrapper.text()).toContain('db-1 (10.0.0.1), db-2 (10.0.0.2), db-3 (10.0.0.3)')
    expect(wrapper.text()).toContain('redis')
    expect(wrapper.text()).toContain('standalone')
    expect(wrapper.text()).not.toContain('mysql-1')
    expect(wrapper.text()).not.toContain('mysql-2')
    expect(wrapper.text()).not.toContain('mysql-3')
  })

  it('keeps MySQL data nodes and routers in the same installed cluster group', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [
        { ...server, id: 'srv-1', name: 'db-1', host: '10.0.0.1', status: 'available' },
        { ...server, id: 'srv-2', name: 'db-2', host: '10.0.0.2', status: 'available' },
        { ...server, id: 'srv-3', name: 'db-3', host: '10.0.0.3', status: 'available' },
        { ...server, id: 'srv-router', name: 'router-1', host: '10.0.0.9', status: 'available' }
      ]
      if (path === '/tasks') return []
      if (path === '/database/instances') return [
        {
          id: 'mysql-1',
          app: 'mysql',
          version: '8.0.36',
          topology: 'innodb-cluster',
          serverId: 'srv-1',
          status: 'running',
          metadata: JSON.stringify({ clusterId: 'cluster-a', topology: 'innodb-cluster' })
        },
        {
          id: 'mysql-2',
          app: 'mysql',
          version: '8.0.36',
          topology: 'innodb-cluster',
          serverId: 'srv-2',
          status: 'running',
          metadata: JSON.stringify({ clusterId: 'cluster-a', topology: 'innodb-cluster' })
        },
        {
          id: 'mysql-3',
          app: 'mysql',
          version: '8.0.36',
          topology: 'innodb-cluster',
          serverId: 'srv-3',
          status: 'running',
          metadata: JSON.stringify({ clusterId: 'cluster-a', topology: 'innodb-cluster' })
        },
        {
          id: 'router-1',
          app: 'mysql-router',
          version: '8.0.36',
          topology: 'router',
          serverId: 'srv-router',
          status: 'failed',
          metadata: JSON.stringify({ clusterId: 'cluster-a', topology: 'router' })
        }
      ]
      if (path === '/nacos/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    await wrapper.find('[data-dashboard-component-tab="database"]').trigger('click')

    expect(wrapper.text()).toContain('common.all 1')
    expect(wrapper.text()).toContain('common.running 0')
    expect(wrapper.text()).toContain('common.unavailable 1')
    expect(wrapper.findAll('.dashboard-entity-row')).toHaveLength(1)
    expect(wrapper.text()).toContain('mysql')
    expect(wrapper.text()).toContain('mysql-router')
    expect(wrapper.text()).toContain('dashboard.clusterNodes 4')
    expect(wrapper.text()).toContain('dashboard.unavailableNodes 1')
    expect(wrapper.text()).toContain('router-1 (10.0.0.9)')
  })

  it('does not merge cluster-shaped instances without an explicit shared group identity', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [
        { ...server, id: 'srv-1', name: 'db-1', host: '10.0.0.1', status: 'available' },
        { ...server, id: 'srv-2', name: 'db-2', host: '10.0.0.2', status: 'available' }
      ]
      if (path === '/tasks') return []
      if (path === '/database/instances') return [
        { id: 'mysql-a', app: 'mysql', version: '8.0.36', topology: 'innodb-cluster', serverId: 'srv-1', status: 'failed' },
        { id: 'mysql-b', app: 'mysql', version: '8.0.36', topology: 'innodb-cluster', serverId: 'srv-2', status: 'failed' }
      ]
      if (path === '/nacos/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    await wrapper.find('[data-dashboard-component-tab="database"]').trigger('click')

    expect(wrapper.text()).toContain('common.all 2')
    expect(wrapper.findAll('.dashboard-entity-row')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('dashboard.clusterNodes 2')
  })

  it('does not show a full-resource link on the aggregate view and restores the selected tab from the URL', async () => {
    routeQuery.runtime = 'database'
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return componentFixture()
      if (path === '/nacos/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.find('[data-dashboard-component-tab="database"]').classes()).toContain('active')
    const links = wrapper.findAll('.dashboard-section-link')
    expect(links).toHaveLength(1)
    expect(links[0].text()).toBe('dashboard.viewAllResources')
    expect(links[0].attributes('href')).toBe('/database')

    await wrapper.find('[data-dashboard-component-tab="storage"]').trigger('click')

    expect(wrapper.find('.dashboard-section-link').text()).toBe('dashboard.viewAllResources')
    expect(wrapper.find('.dashboard-section-link').attributes('href')).toBe('/storage')
    expect(routerReplaceMock).toHaveBeenLastCalledWith({ query: { runtime: 'storage' } })
  })

  it('keeps the aggregate entry read-only and defaults the actionable list to servers', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return componentFixture()
      if (path === '/nacos/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.find('[data-dashboard-component-tab="all"]').exists()).toBe(false)
    expect(wrapper.find('[data-dashboard-resource-overview]').exists()).toBe(true)
    expect(wrapper.find('.dashboard-section-link').attributes('href')).toBe('/servers')
    expect(wrapper.find('[data-dashboard-component-tab="servers"]').classes()).toContain('active')
    expect(wrapper.text()).toContain('one')
    expect(wrapper.text()).not.toContain('mysql-router')
    expect(wrapper.text()).not.toContain('redis')
  })

  it('caps server metric rows on the dashboard and links to the server workbench for the full list', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return serverFixture(7)
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/nacos/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.find('[data-dashboard-component-tab="servers"]').exists()).toBe(true)
    await wrapper.find('[data-dashboard-component-tab="servers"]').trigger('click')
    await wrapper.find('[data-dashboard-health-filter="all"]').trigger('click')

    expect(wrapper.findAll('.dashboard-entity-row')).toHaveLength(5)
    expect(wrapper.text()).toContain('common.all 7')
    expect(wrapper.text()).toContain('common.running 1')
    expect(wrapper.text()).toContain('common.unavailable 6')
    expect(wrapper.text()).toContain('2')
    expect(wrapper.find('a[href="/servers"]').exists()).toBe(true)
    expect(wrapper.find('a[href="/servers"]').text()).toBe('dashboard.viewAllResources')
  })

  it('keeps dashboard runtime summaries compact when the card stretches to fill the viewport', () => {
    expect(normalizedDashboardViewSource).toContain('.dashboard-component-health {\n  display: grid;\n  align-content: start;\n  gap: 0;')
    expect(normalizedDashboardViewSource).toContain('.dashboard-section-head {\n  padding: 14px 14px 12px;')
    expect(normalizedDashboardViewSource).toContain('.dashboard-command-shell {\n  display: grid;\n  grid-template-columns: minmax(620px, 1.35fr) minmax(380px, .65fr);')
    expect(normalizedDashboardViewSource).toContain('.dashboard-component-tabs {\n  display: flex;\n  flex-wrap: wrap;')
    expect(normalizedDashboardViewSource).toContain('.dashboard-component-tabs button {\n  min-height: 30px;')
    expect(normalizedDashboardViewSource).toContain('.dashboard-health-filter {\n  display: flex;\n  align-items: center;')
    expect(normalizedDashboardViewSource).toContain('.dashboard-health-filter button {\n  height: 28px;')
    expect(normalizedDashboardViewSource).toContain('.dashboard-entity-table-head {\n  display: grid;\n  grid-template-columns: minmax(180px, .85fr) minmax(260px, 1.45fr) auto;')
    expect(normalizedDashboardViewSource).toContain('.dashboard-entity-list {\n  max-height: min(430px, 45vh);')
    expect(normalizedDashboardViewSource).toContain('.dashboard-entity-row {\n  display: grid;\n  grid-template-columns: minmax(180px, .85fr) minmax(260px, 1.45fr) auto;')
  })

  it('keeps alert details out of the dashboard and leaves alert counts in the KPI bar', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/status/snapshots') return { items: [] }
      if (path === '/alerts?status=open') {
        return {
          items: [{
            id: 'alt-1',
            fingerprint: 'mysql-down',
            severity: 'critical',
            scope: 'database',
            app: 'mysql',
            status: 'open',
            title: 'MYSQL instance is unavailable',
            lastSeenAt: '2026-08-13T07:12:45Z'
          }]
        }
      }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    const kpiText = wrapper.find('.metric-grid-stub').text()
    expect(kpiText).toContain('"label":"alerts.title","value":1')
    expect(wrapper.find('.dashboard-alerts').exists()).toBe(false)
    expect(wrapper.find('.dashboard-alert-row').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('MYSQL instance is unavailable')
  })

  it.each(['available', 'running', 'success', 'ok'])('keeps server inventory status %s aligned with the server workbench', (status) => {
    expect(normalizeDashboardServerStatus(status)).toBe('available')
  })

  it.each(['failed', 'error', 'unavailable', 'unhealthy', 'no-endpoints', 'down', 'offline', 'missing', 'stopped'])('renders runtime health status %s as service unavailable on the dashboard', (status) => {
    expect(normalizeDashboardRuntimeStatus(status)).toBe('unavailable')
  })

  it.each(['running', 'available', 'success', 'ok'])('renders runtime health status %s as running on the dashboard', (status) => {
    expect(normalizeDashboardRuntimeStatus(status)).toBe('running')
  })

  it('hydrates persisted status snapshots on entry so server KPI matches the server workbench', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') {
        return {
          items: [{
            scope: 'server',
            resourceId: 'srv-1',
            status: 'available',
            version: 7,
            collectedAt: '2026-08-12T14:11:00Z',
            updatedAt: '2026-08-12T14:11:01Z'
          }]
        }
      }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(requestedPaths()).toContain('/status/snapshots')
    expect(useRealtimeStore().serverSnapshot('srv-1')?.status).toBe('available')
    await vi.waitFor(() => {
      expect(wrapper.find('.metric-grid-stub').text()).toContain('common.available 1')
    })
  })

  it('uses centralized runtime health when counting Docker hosts', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') {
        return {
          items: [{
            scope: 'docker.summary',
            resourceId: 'srv-1',
            status: 'error',
            payload: {
              available: true,
              summary: {
                containers: 2,
                images: 4,
                endpoint: 'unix:///var/run/docker.sock'
              }
            },
            version: 3,
            collectedAt: '2026-08-12T14:12:00Z',
            updatedAt: '2026-08-12T14:12:01Z'
          }]
        }
      }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    await vi.waitFor(() => {
      expect(wrapper.find('.metric-grid-stub').text()).toContain('"label":"Docker","value":1,"note":"common.running 0"')
    })
  })
})
