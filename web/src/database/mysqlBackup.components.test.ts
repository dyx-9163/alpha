// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ElementPlus, { ElButton, ElCheckbox, ElInput } from 'element-plus'
import MySQLBackupDialog from './MySQLBackupDialog.vue'
import MySQLBackupDrawer from './MySQLBackupDrawer.vue'
import MySQLDisasterRebuildDialog from './MySQLDisasterRebuildDialog.vue'
import MySQLRestoreDialog from './MySQLRestoreDialog.vue'
import DatabaseView from '../views/DatabaseView.vue'
import type { MySQLBackupRecord } from './mysqlBackup'
import { setLocale } from '../i18n'

const { apiGet, apiPost, track } = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  track: vi.fn()
}))

vi.mock('../api/client', () => ({
  apiPost,
  apiGet,
  apiDelete: vi.fn(),
  asArray: (value: unknown) => Array.isArray(value) ? value : []
}))

vi.mock('../stores/taskProgress', () => ({
  useTaskProgressStore: () => ({ track })
}))

const instanceId = 'app_1234567890abcdef12345678'
const serverId = 'srv_1234567890abcdef12345678'
const clusterId = 'mysql_cluster_1234567890abcdef12345678'
const taskId = 'tsk_1234567890abcdef12345678'

const backup: MySQLBackupRecord = {
  id: 'backup_1234567890abcdef12345678',
  instanceId,
  serverId,
  backupType: 'logical-full',
  status: 'success',
  checksum: 'a'.repeat(64),
  size: 1024,
  metadata: {
    manifestVersion: 2,
    topology: 'standalone',
    mysqlVersion: '8.0.36',
    schemas: ['aifar'],
    verificationResult: 'success'
  },
  createdAt: '2026-07-28T01:02:03Z'
}

function mountingOptions() {
  return {
    global: {
      plugins: [createPinia(), ElementPlus],
      stubs: { teleport: true },
      mocks: { ResizeObserver: class { observe() {} unobserve() {} disconnect() {} } }
    }
  }
}

describe('MySQL backup and restore surfaces', () => {
  beforeEach(() => {
    apiPost.mockReset()
    apiGet.mockReset()
    track.mockReset()
    localStorage.clear()
    setLocale('en')
  })

  it('rechecks live state immediately before creating a backup', async () => {
    const beforeSubmit = vi.fn().mockResolvedValue(false)
    const wrapper = mount(MySQLBackupDialog, {
      ...mountingOptions(),
      props: {
        modelValue: true,
        instanceId,
        sourceLabel: 'mysql-primary',
        defaults: { threads: 4, maxRateMBps: 0 },
        beforeSubmit
      }
    })
    await flushPromises()
    await wrapper.findComponent(ElInput).setValue('nightly')
    await wrapper.findAllComponents(ElButton).at(-1)!.trigger('click')
    await flushPromises()
    expect(beforeSubmit).toHaveBeenCalledOnce()
    expect(apiPost).not.toHaveBeenCalled()
  })

  it('submits the exact backup body and tracks the returned task', async () => {
    apiPost.mockResolvedValue({ taskId })
    const wrapper = mount(MySQLBackupDialog, {
      ...mountingOptions(),
      props: {
        modelValue: true,
        instanceId,
        sourceLabel: 'mysql-primary',
        defaults: { threads: 6, maxRateMBps: 12, keepLast: 5 }
      }
    })
    await flushPromises()
    await wrapper.findComponent(ElInput).setValue('nightly')
    await wrapper.findAllComponents(ElButton).at(-1)!.trigger('click')
    await flushPromises()
    expect(apiPost).toHaveBeenCalledWith(`/apps/instances/${instanceId}/backup`, {
      name: 'nightly', threads: 6, maxRateMBps: 12, keepLast: 5
    })
    expect(track).toHaveBeenCalledWith(taskId, expect.any(String))
  })

  it('allows disabling the default pre-restore backup and submits the exact typed body', async () => {
    apiPost.mockResolvedValue({ taskId })
    const wrapper = mount(MySQLRestoreDialog, {
      ...mountingOptions(),
      props: {
        modelValue: true,
        backup,
        target: { topology: 'standalone', mysqlVersion: '8.0.36', instanceId, serverId, label: 'mysql-primary' },
        defaultThreads: 4
      }
    })
    await flushPromises()
    const checks = wrapper.findAllComponents(ElCheckbox)
    checks[0].vm.$emit('update:modelValue', false)
    checks[1].vm.$emit('update:modelValue', true)
    await wrapper.vm.$nextTick()
    await wrapper.findAllComponents(ElButton).at(-1)!.trigger('click')
    await flushPromises()
    expect(apiPost).toHaveBeenCalledWith(`/apps/instances/${instanceId}/restore`, {
      backupId: backup.id,
      mode: 'standalone',
      maintenanceConfirmed: true,
      createPreRestoreBackup: false,
      disasterConfirmed: false,
      threads: 4
    })
    expect(track).toHaveBeenCalledWith(taskId, expect.any(String))
  })

  it('disables drawer restore for a full-version mismatch and exposes the localized reason', async () => {
    const wrapper = mount(MySQLBackupDrawer, {
      ...mountingOptions(),
      props: {
        modelValue: true,
        sourceLabel: 'mysql-primary',
        version: '8.0.37',
        topology: 'standalone',
        target: { topology: 'standalone', mysqlVersion: '8.0.37', instanceId, serverId },
        records: [backup],
        canVerify: true,
        canRestore: true
      }
    })
    await flushPromises()
    const restore = wrapper.findAllComponents(ElButton).at(-1)
    expect(restore?.props('disabled')).toBe(true)
    expect(wrapper.find('.drawer-summary').attributes('aria-label')).toBe('MySQL backup source summary')
    expect(wrapper.text()).toContain('Standalone')
  })

  it('blocks a stale disaster submit and clears all password fields when closed', async () => {
    const clusterBackup: MySQLBackupRecord = {
      ...backup,
      metadata: { ...backup.metadata, topology: 'innodb-cluster', clusterId }
    }
    const nodes = [0, 1, 2].map((index) => ({
      instanceId: `app_${String(index + 1).repeat(24)}`,
      instanceLabel: `mysql-${index + 1}`,
      serverId: `srv_${String(index + 1).repeat(24)}`,
      serverLabel: `server-${index + 1}`
    }))
    const beforeSubmit = vi.fn().mockResolvedValue(false)
    const wrapper = mount(MySQLDisasterRebuildDialog, {
      ...mountingOptions(),
      props: {
        modelValue: true,
        instanceId,
        clusterId,
        mysqlVersion: '8.0.36',
        backup: clusterBackup,
        nodes,
        defaultThreads: 4,
        beforeSubmit
      }
    })
    await flushPromises()
    const inputs = wrapper.findAllComponents(ElInput)
    for (const input of inputs) await input.setValue('one-use-only')
    const checks = wrapper.findAllComponents(ElCheckbox)
    checks[0].vm.$emit('update:modelValue', true)
    checks[1].vm.$emit('update:modelValue', true)
    await wrapper.vm.$nextTick()
    await wrapper.findAllComponents(ElButton).at(-1)!.trigger('click')
    await flushPromises()
    expect(beforeSubmit).toHaveBeenCalledOnce()
    expect(apiPost).not.toHaveBeenCalled()
    await wrapper.setProps({ modelValue: false })
    await flushPromises()
    expect(wrapper.findAll('input[type="password"]').every((input) => (input.element as HTMLInputElement).value === '')).toBe(true)
  })

  it('submits the exact disaster mapping, tracks the task, and clears one-use passwords', async () => {
    apiPost.mockResolvedValue({ taskId })
    const clusterBackup: MySQLBackupRecord = {
      ...backup,
      metadata: { ...backup.metadata, topology: 'innodb-cluster', clusterId }
    }
    const nodes = [0, 1, 2].map((index) => ({
      instanceId: `app_${String(index + 1).repeat(24)}`,
      instanceLabel: `mysql-${index + 1}`,
      serverId: `srv_${String(index + 1).repeat(24)}`,
      serverLabel: `server-${index + 1}`
    }))
    const wrapper = mount(MySQLDisasterRebuildDialog, {
      ...mountingOptions(),
      props: { modelValue: true, instanceId, clusterId, mysqlVersion: '8.0.36', backup: clusterBackup, nodes, defaultThreads: 4 }
    })
    await flushPromises()
    for (const input of wrapper.findAllComponents(ElInput)) await input.setValue('one-use-only')
    const checks = wrapper.findAllComponents(ElCheckbox)
    checks[0].vm.$emit('update:modelValue', true)
    checks[1].vm.$emit('update:modelValue', true)
    await wrapper.vm.$nextTick()
    await wrapper.findAllComponents(ElButton).at(-1)!.trigger('click')
    await flushPromises()
    expect(apiPost).toHaveBeenCalledWith(`/apps/instances/${instanceId}/restore`, {
      backupId: backup.id,
      mode: 'disaster-rebuild',
      maintenanceConfirmed: true,
      createPreRestoreBackup: false,
      disasterConfirmed: true,
      threads: 4,
      targetMapping: Object.fromEntries(nodes.map((node) => [node.instanceId, node.serverId])),
      serverPasswords: Object.fromEntries(nodes.map((node) => [node.serverId, 'one-use-only']))
    })
    expect(track).toHaveBeenCalledWith(taskId, expect.any(String))
    expect(wrapper.findAll('input[type="password"]').every((input) => (input.element as HTMLInputElement).value === '')).toBe(true)
  })

  it('keeps DatabaseView actions wired to the live group and closes submission after maintenance appears', async () => {
    localStorage.setItem('aifar-session-token', 'test-token')
    localStorage.setItem('aifar-role', 'owner')
    let metadata = JSON.stringify({ topology: 'standalone', endpoint: '10.0.0.8:3306', lastCheck: { status: 'success' } })
    apiGet.mockImplementation(async (path: string) => {
      if (path === '/database/instances') return [{
        id: instanceId,
        app: 'mysql',
        version: '8.0.36',
        serverId,
        status: 'running',
        topology: 'standalone',
        metadata,
        createdAt: '2026-07-28T01:00:00Z'
      }]
      if (path === '/servers') return [{ id: serverId, name: 'db-1', status: 'available' }]
      if (path === '/tasks') return []
      if (path === `/apps/instances/${instanceId}/backups`) return { instanceId, items: [backup], defaults: { threads: 4, maxRateMBps: 0 } }
      return []
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div />' } }, { path: '/tasks', component: { template: '<div />' } }]
    })
    await router.push('/')
    await router.isReady()
    const wrapper = mount(DatabaseView, {
      global: {
        plugins: [createPinia(), router, ElementPlus],
        stubs: { teleport: true, StatusTag: true, RunRecordTable: true, KeyValueGrid: true }
      }
    })
    await flushPromises()
    const action = (label: string) => wrapper.findAllComponents(ElButton).find((button) => button.text() === label)!
    await action('Back up now').trigger('click')
    await flushPromises()
    expect(wrapper.findComponent(MySQLBackupDialog).props('modelValue')).toBe(true)
    await action('Backup records').trigger('click')
    await flushPromises()
    expect(wrapper.findComponent(MySQLBackupDrawer).props('modelValue')).toBe(true)
    const drawer = wrapper.findComponent(MySQLBackupDrawer)
    const drawerRestore = drawer.findAllComponents(ElButton).find((button) => button.text() === 'Restore data')!
    await drawerRestore.trigger('click')
    await flushPromises()
    expect(wrapper.findComponent(MySQLRestoreDialog).props('backup')).toMatchObject({ id: backup.id })

    metadata = JSON.stringify({
      topology: 'standalone',
      endpoint: '10.0.0.8:3306',
      mysqlMaintenance: {
        version: 1,
        state: 'required',
        reason: 'restore_incomplete',
        scope: 'standalone',
        backupId: backup.id,
        taskId,
        restorePhase: 'schema_mutation_started',
        recordedAt: '2026-07-28T02:00:00Z'
      }
    })
    await action('Refresh').trigger('click')
    await flushPromises()
    const backupDialog = wrapper.findComponent(MySQLBackupDialog)
    expect(backupDialog.props('submissionAllowed')).toBe(false)
    const beforeSubmit = backupDialog.props('beforeSubmit')
    expect(beforeSubmit).toBeTypeOf('function')
    expect(await beforeSubmit!()).toBe(false)
    expect(wrapper.findComponent(MySQLRestoreDialog).props('submissionAllowed')).toBe(false)
  })

  it('opens disaster rebuild with the exact marker backup even when a newer compatible backup is first', async () => {
    localStorage.setItem('aifar-session-token', 'test-token')
    localStorage.setItem('aifar-role', 'owner')
    const controlledClusterId = 'cluster_1234567890abcdef12345678'
    const nodeIds = [instanceId, 'app_222222222222222222222222', 'app_333333333333333333333333']
    const serverIds = [serverId, 'srv_222222222222222222222222', 'srv_333333333333333333333333']
    const marker = {
      version: 1, state: 'required', reason: 'restore_incomplete', scope: 'cluster', clusterId: controlledClusterId,
      backupId: backup.id, taskId, restorePhase: 'schema_mutation_started', recordedAt: '2026-07-28T02:00:00Z'
    }
    const clusterBackup: MySQLBackupRecord = {
      ...backup,
      metadata: { ...backup.metadata, topology: 'innodb-cluster', clusterId: controlledClusterId, verificationResult: 'success' }
    }
    const newer = { ...clusterBackup, id: 'backup_aaaaaaaaaaaaaaaaaaaaaaaa', createdAt: '2026-07-28T03:00:00Z' }
    apiGet.mockImplementation(async (path: string) => {
      if (path === '/database/instances') return nodeIds.map((id, index) => ({
        id,
        app: 'mysql',
        version: '8.0.36',
        serverId: serverIds[index],
        status: 'failed',
        topology: 'innodb-cluster',
        metadata: JSON.stringify({ topology: 'innodb-cluster', clusterId: controlledClusterId, role: index === 0 ? 'primary' : 'secondary', mysqlMaintenance: marker }),
        createdAt: `2026-07-28T01:00:0${index}Z`
      }))
      if (path === '/servers') return serverIds.map((id, index) => ({ id, name: `db-${index + 1}`, status: 'available' }))
      if (path === '/tasks') return []
      if (path === `/apps/instances/${instanceId}/backups`) return { instanceId, items: [newer, clusterBackup], defaults: { threads: 4, maxRateMBps: 0 } }
      return []
    })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
    await router.push('/')
    await router.isReady()
    const wrapper = mount(DatabaseView, {
      global: {
        plugins: [createPinia(), router, ElementPlus],
        stubs: { teleport: true, StatusTag: true, RunRecordTable: true, KeyValueGrid: true }
      }
    })
    await flushPromises()
    const disaster = wrapper.findAllComponents(ElButton).find((button) => button.text() === 'Disaster rebuild')!
    await disaster.trigger('click')
    await flushPromises()
    expect(wrapper.findComponent(MySQLDisasterRebuildDialog).props('modelValue')).toBe(true)
    expect(wrapper.findComponent(MySQLDisasterRebuildDialog).props('backup')).toMatchObject({ id: backup.id })
  })
})
