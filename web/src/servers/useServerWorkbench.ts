import { computed, reactive, ref } from 'vue'
import { confirmAction } from '../composables/useConfirmAction'
import type { StatusSnapshot } from '../stores/realtime'
import { deleteServer, getServerDefaults, listServers, probeServer, reorderServers, saveServer, waitTaskDone } from './api'
import { applyRealtimeStatusToServer } from './realtimeStatus'
import type { ServerFormModel, ServerRecord } from './types'

type ServerDefaults = {
  defaultDeployDir: string
}

const fallbackServerDefaults: ServerDefaults = {
  defaultDeployDir: '/aifar/apps'
}

export type ServerSnapshotResolver = (serverId: string) => StatusSnapshot | undefined

export function createServerForm(row?: Partial<ServerRecord>, defaults: ServerDefaults = fallbackServerDefaults): ServerFormModel {
  return {
    id: row?.id ?? '',
    name: row?.name ?? '',
    host: row?.host ?? '',
    port: row?.port ?? 22,
    username: row?.username ?? 'root',
    authType: row?.authType ?? 'password',
    password: '',
    privateKey: '',
    tags: row?.tags ?? '',
    note: row?.note ?? '',
    deployDir: row?.deployDir ?? defaults.defaultDeployDir,
    status: row?.status ?? 'unknown',
    sortOrder: row?.sortOrder
  }
}

export function useServerWorkbench(
  t: (key: string, params?: Record<string, unknown>) => string,
  resolveSnapshot: ServerSnapshotResolver = () => undefined
) {
  const servers = ref<ServerRecord[]>([])
  const selectedId = ref('')
  const search = ref('')
  const drawer = ref(false)
  const activeTab = ref('overview')
  const probingIds = ref<Set<string>>(new Set())
  const serverDefaults = ref<ServerDefaults>({ ...fallbackServerDefaults })
  const form = reactive<ServerFormModel>(createServerForm(undefined, serverDefaults.value))

  const filteredServers = computed(() => {
    const q = search.value.trim().toLowerCase()
    if (!q) return servers.value
    return servers.value.filter((server) => `${server.name} ${server.host} ${server.username} ${server.tags ?? ''}`.toLowerCase().includes(q))
  })

  const selectedServer = computed(() => servers.value.find((server) => server.id === selectedId.value) ?? null)

  const summary = computed(() => ({
    total: servers.value.length,
    available: servers.value.filter((server) => server.status === 'available').length,
    probing: servers.value.filter((server) => server.status === 'probing').length,
    unknown: servers.value.filter((server) => !server.status || server.status === 'unknown').length
  }))

  function mergeLiveStatus(server: ServerRecord) {
    return probingIds.value.has(server.id)
      ? { ...server, status: 'probing', lastError: '' }
      : applyRealtimeStatusToServer(server, resolveSnapshot(server.id))
  }

  async function load() {
    servers.value = (await listServers()).map(mergeLiveStatus)
    if (!selectedId.value && servers.value.length) selectedId.value = servers.value[0].id
    if (selectedId.value && !servers.value.some((server) => server.id === selectedId.value)) {
      selectedId.value = servers.value[0]?.id ?? ''
    }
  }

  function applyStatusSnapshots() {
    servers.value = servers.value.map(mergeLiveStatus)
  }

  async function loadDefaults() {
    try {
      serverDefaults.value = await getServerDefaults()
    } catch {
      serverDefaults.value = { ...fallbackServerDefaults }
    }
  }

  function open(row?: ServerRecord) {
    Object.assign(form, createServerForm(row, serverDefaults.value))
    drawer.value = true
  }

  async function save() {
    try {
      const saved = await saveServer(form)
      selectedId.value = saved.id
      drawer.value = false
      await load()
    } finally {
      form.password = ''
      form.privateKey = ''
    }
  }

  async function remove(row: ServerRecord) {
    await confirmAction({
      message: t('servers.confirmDelete', { name: row.name }),
      title: t('servers.confirmDeleteTitle'),
      confirmText: t('common.delete'),
      cancelText: t('common.cancel')
    })
    await deleteServer(row.id)
    await load()
  }

  function applyServerOrder(ids: string[]) {
    const byId = new Map(servers.value.map((server) => [server.id, server]))
    const selected = ids.map((id) => byId.get(id)).filter((server): server is ServerRecord => Boolean(server))
    const selectedIds = new Set(selected.map((server) => server.id))
    const rest = servers.value.filter((server) => !selectedIds.has(server.id))
    servers.value = [...selected, ...rest]
  }

  async function reorder(ids: string[]) {
    if (!ids.length) {
      return
    }
    const previous = servers.value.slice()
    applyServerOrder(ids)
    try {
      await reorderServers(ids)
      await load()
    } catch (err) {
      servers.value = previous
      throw err
    }
  }

  async function probe(row: ServerRecord) {
    setProbing(row.id, true)
    patchServerStatus(row.id, 'probing', '')
    activeTab.value = 'overview'
    try {
      const result = await probeServer(row.id)
      await waitTaskDone(result.taskId)
    } finally {
      setProbing(row.id, false)
      await load()
    }
  }

  function setProbing(id: string, probing: boolean) {
    const next = new Set(probingIds.value)
    if (probing) {
      next.add(id)
    } else {
      next.delete(id)
    }
    probingIds.value = next
  }

  function patchServerStatus(id: string, status: string, lastError: string) {
    servers.value = servers.value.map((server) => server.id === id ? { ...server, status, lastError } : server)
  }

  return {
    servers,
    filteredServers,
    selectedServer,
    selectedId,
    search,
    drawer,
    activeTab,
    probingIds,
    form,
    summary,
    loadDefaults,
    load,
    open,
    save,
    remove,
    reorder,
    probe,
    applyStatusSnapshots
  }
}
