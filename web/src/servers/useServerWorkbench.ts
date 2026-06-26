import { computed, reactive, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { deleteServer, getServerDefaults, listServers, probeServer, saveServer, waitTaskDone } from './api'
import type { ServerFormModel, ServerRecord } from './types'

type ServerDefaults = {
  defaultDeployDir: string
}

const fallbackServerDefaults: ServerDefaults = {
  defaultDeployDir: '/aifar/apps'
}

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
    status: row?.status ?? 'unknown'
  }
}

export function useServerWorkbench(t: (key: string, params?: Record<string, unknown>) => string) {
  const servers = ref<ServerRecord[]>([])
  const selectedId = ref('')
  const search = ref('')
  const drawer = ref(false)
  const activeTab = ref('overview')
  const probingIds = ref<Set<string>>(new Set())
  const defaultProbeDone = ref(false)
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
    unknown: servers.value.filter((server) => !server.status || server.status === 'unknown').length
  }))

  async function load() {
    servers.value = await listServers()
    if (!selectedId.value && servers.value.length) selectedId.value = servers.value[0].id
    if (selectedId.value && !servers.value.some((server) => server.id === selectedId.value)) {
      selectedId.value = servers.value[0]?.id ?? ''
    }
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
    const saved = await saveServer(form)
    selectedId.value = saved.id
    drawer.value = false
    await load()
  }

  async function remove(row: ServerRecord) {
    await ElMessageBox.confirm(t('servers.confirmDelete', { name: row.name }), t('servers.confirmDeleteTitle'))
    await deleteServer(row.id)
    await load()
  }

  async function probe(row: ServerRecord) {
    setProbing(row.id, true)
    patchServerStatus(row.id, 'running', '')
    activeTab.value = 'overview'
    try {
      const result = await probeServer(row.id)
      await waitTaskDone(result.taskId)
    } finally {
      setProbing(row.id, false)
      await load()
    }
  }

  async function probeSelectedOnce() {
    if (defaultProbeDone.value) {
      return
    }
    defaultProbeDone.value = true
    if (selectedServer.value) {
      await probe(selectedServer.value)
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
    probe,
    probeSelectedOnce
  }
}
