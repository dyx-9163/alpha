import { describe, expect, it } from 'vitest'

import containersViewSource from './ContainersView.vue?raw'
import databaseViewSource from './DatabaseView.vue?raw'
import nacosViewSource from './NacosView.vue?raw'
import storageViewSource from './StorageView.vue?raw'

describe('runtime status presentation policy', () => {
  it('keeps database cards to one group status plus node-level statuses', () => {
    expect(databaseViewSource).toContain('<StatusTag :status="group.status" />')
    expect(databaseViewSource).not.toContain('<div><span>{{ t(\'common.status\') }}</span><StatusTag :status="group.status" /></div>')
    expect(databaseViewSource).not.toContain('databaseServiceUnavailableText(group)')
    expect(databaseViewSource).not.toContain("t('database.routerServiceUnavailable')")
    expect(databaseViewSource).not.toContain('endpoint: isUnavailable(nodeStatus)')
  })

  it('presents database resources as a split list and detail workbench', () => {
    expect(databaseViewSource).toContain('class="db-resource-shell"')
    expect(databaseViewSource).toContain('class="db-resource-list"')
    expect(databaseViewSource).toContain('class="db-resource-row"')
    expect(databaseViewSource).toContain('@click="selectDatabaseResource(group)"')
    expect(databaseViewSource).toContain('activeDatabaseWorkbenchGroup')
    expect(databaseViewSource).toContain('class="resource-inline-detail db-inline-detail"')
    expect(databaseViewSource).toContain('v-model="databaseDetailVisible"')
    expect(databaseViewSource).toContain('activeDatabaseGroup')
    expect(databaseViewSource).toContain('.db-resource-shell {\n  display: grid;')
    expect(databaseViewSource).toContain('.db-resource-list {\n  display: grid;')
    expect(databaseViewSource).toContain('.db-resource-row {\n  display: grid;\n  grid-template-columns: minmax(0, 1fr) auto;')
    expect(databaseViewSource).not.toContain('class="db-card-grid"')
    expect(databaseViewSource).not.toContain('class="db-card"')
  })

  it('centers resource card status badges in the card header actions', () => {
    for (const [source, className] of [
      [databaseViewSource, '.db-resource-actions'],
      [nacosViewSource, '.nacos-head-actions'],
      [storageViewSource, '.storage-head-actions']
    ] as const) {
      expect(source).toContain(`${className} {\n  display: flex;\n  align-items: center;`)
      expect(source).toContain(`${className} :deep(.el-tag) {\n  min-height: 22px;`)
      expect(source).toContain('align-items: center;')
      expect(source).toContain('justify-content: center;')
    }
  })

  it('presents Nacos resources as a split list and detail workbench', () => {
    expect(nacosViewSource).toContain('<StatusTag :status="group.status" />')
    expect(nacosViewSource).toContain('class="nacos-resource-shell"')
    expect(nacosViewSource).toContain('class="nacos-resource-list"')
    expect(nacosViewSource).toContain('class="nacos-resource-row"')
    expect(nacosViewSource).toContain('@click="selectNacosResource(group)"')
    expect(nacosViewSource).toContain('activeNacosWorkbenchGroup')
    expect(nacosViewSource).toContain('class="resource-inline-detail nacos-inline-detail"')
    expect(nacosViewSource).toContain('v-model="nacosDetailVisible"')
    expect(nacosViewSource).toContain('activeNacosGroup')
    expect(nacosViewSource).toContain('.nacos-resource-shell {\n  display: grid;')
    expect(nacosViewSource).toContain('.nacos-resource-list {\n  display: grid;')
    expect(nacosViewSource).toContain('.nacos-resource-row {\n  display: grid;\n  grid-template-columns: minmax(0, 1fr) auto;')
    expect(nacosViewSource).not.toContain('class="nacos-card-grid"')
    expect(nacosViewSource).not.toContain('class="nacos-card"')
    expect(nacosViewSource).not.toContain("t('nacos.serviceUnavailable')")
    expect(nacosViewSource).not.toContain('class="nacos-info-grid"')
    expect(nacosViewSource).not.toContain('isUnavailable(group.status)')
  })

  it('presents object storage resources as a split list and detail workbench', () => {
    expect(storageViewSource).toContain('<StatusTag :status="group.status" />')
    expect(storageViewSource).toContain('class="storage-resource-shell"')
    expect(storageViewSource).toContain('class="storage-resource-list"')
    expect(storageViewSource).toContain('class="storage-resource-row"')
    expect(storageViewSource).toContain('@click="selectStorageResource(group)"')
    expect(storageViewSource).toContain('activeStorageWorkbenchGroup')
    expect(storageViewSource).toContain('class="resource-inline-detail storage-inline-detail"')
    expect(storageViewSource).toContain('v-model="storageDetailVisible"')
    expect(storageViewSource).toContain('activeStorageGroup')
    expect(storageViewSource).toContain('.storage-resource-shell {\n  display: grid;')
    expect(storageViewSource).toContain('.storage-resource-list {\n  display: grid;')
    expect(storageViewSource).toContain('.storage-resource-row {\n  display: grid;\n  grid-template-columns: minmax(0, 1fr) auto;')
    expect(storageViewSource).not.toContain('class="storage-card-grid"')
    expect(storageViewSource).not.toContain('class="storage-card"')
    expect(storageViewSource).not.toContain("t('common.status')")
    expect(storageViewSource).not.toContain('class="storage-info-grid"')
    expect(storageViewSource).not.toContain('isUnavailable(group.status)')
  })

  it('does not expose manual Docker host check and refresh buttons in the container header', () => {
    expect(containersViewSource).not.toContain("{{ t('containers.checkHost') }}")
    expect(containersViewSource).not.toContain('@click="load(true)"')
    expect(containersViewSource).not.toContain('@click="loadActive(true)"')
  })

  it('uses persisted 15s status snapshots instead of live probe refresh on container page entry', () => {
    expect(containersViewSource).toContain('applyPersistedContainerSnapshots')
    expect(containersViewSource).toContain('realtime.statusRevision')
    expect(containersViewSource).not.toMatch(/onMounted\(async \(\) => \{[\s\S]*?await load\(\)/)
    expect(containersViewSource).not.toMatch(/watch\(tab,[\s\S]*?loadActive\(false\)/)
    expect(containersViewSource).not.toMatch(/watch\(resourceTab,[\s\S]*?loadActive\(false\)/)
    expect(containersViewSource).not.toContain('runtimeStatusRefresh.request()')
  })

  it('does not expose a manual refresh button inside the AIFAR runtime workspace', () => {
    expect(containersViewSource).not.toContain('loadAifarRuntime,')
    expect(containersViewSource).not.toContain('runtimeStatusRefresh.dispose()')
  })
})

