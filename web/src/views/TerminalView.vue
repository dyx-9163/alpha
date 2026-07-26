<template>
  <section class="terminal-page">
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('terminal.title') }}</h1>
        <p class="page-subtitle">{{ t('terminal.subtitle') }}</p>
      </div>
      <el-button @click="$router.push('/servers')">{{ t('terminal.openServers') }}</el-button>
    </div>

    <div class="aifar-panel terminal-toolbar">
      <div class="terminal-control-group">
        <span class="terminal-control-label">{{ t('terminal.server') }}</span>
        <el-select v-model="serverId" :placeholder="t('terminal.server')" class="toolbar-control is-lg">
          <el-option v-for="server in servers" :key="server.id" :label="server.name" :value="server.id" />
        </el-select>
      </div>
      <div class="head-actions terminal-toolbar-actions">
        <el-tooltip :content="deniedText" :disabled="canConnectTerminal" placement="top">
          <span>
            <el-button
              type="primary"
              :disabled="!canConnectTerminal || !serverId"
              @click="newConnection"
            >
              {{ t('terminal.newConnection') }}
            </el-button>
          </span>
        </el-tooltip>
        <el-button :disabled="!canDisconnectFocused" @click="disconnectFocused">
          {{ t('terminal.disconnect') }}
        </el-button>
        <el-button :disabled="!canReconnectFocused" @click="reconnectFocused">
          {{ t('terminal.reconnect') }}
        </el-button>
      </div>
    </div>

    <div class="workspace-card terminal-panel">
      <nav v-if="workspace.sessions.length" class="terminal-tabs" :aria-label="t('terminal.sessions')">
        <div
          v-for="session in workspace.sessions"
          :key="session.id"
          class="terminal-tab"
          :class="{ 'is-active': session.id === workspace.focusedId }"
          role="tab"
          :aria-selected="session.id === workspace.focusedId"
          tabindex="0"
          @click="selectSession(session.id)"
          @keydown.enter="selectSession(session.id)"
          @keydown.space.prevent="selectSession(session.id)"
        >
          <span class="terminal-status-dot" :class="statusClass(session.status)" />
          <span class="terminal-tab-label">{{ session.label }}</span>
          <button
            class="terminal-tab-action"
            type="button"
            :disabled="workspace.visibleIds.includes(session.id) && workspace.visibleIds.length === 1"
            :title="workspace.visibleIds.includes(session.id) ? t('terminal.removeFromSplit') : t('terminal.addToSplit')"
            :aria-label="workspace.visibleIds.includes(session.id) ? t('terminal.removeFromSplit') : t('terminal.addToSplit')"
            @click.stop="toggleSplit(session.id)"
          >
            {{ workspace.visibleIds.includes(session.id) ? '−' : '+' }}
          </button>
          <button
            class="terminal-tab-action"
            type="button"
            :title="t('terminal.closeSession')"
            :aria-label="t('terminal.closeSession')"
            @click.stop="requestClose(session)"
          >
            ×
          </button>
        </div>
      </nav>

      <div
        v-if="workspace.visibleIds.length"
        class="terminal-grid"
        :data-pane-count="workspace.visibleIds.length"
      >
        <TerminalSessionPane
          v-for="session in workspace.sessions"
          v-show="workspace.visibleIds.includes(session.id)"
          :key="session.id"
          :ref="(component) => setPaneRef(session.id, component)"
          :session="session"
          :server-name="serverName(session.serverId)"
          :server-meta="serverMeta(session.serverId)"
          :visible="workspace.visibleIds.includes(session.id)"
          :focused="workspace.focusedId === session.id"
          @status="updateStatus"
          @focus="selectSession"
        />
      </div>
      <div v-else class="terminal-empty">
        {{ workspace.sessions.length ? t('terminal.selectTab') : t('terminal.fallback') }}
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiGet, asArray } from '../api/client'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import TerminalSessionPane from '../terminal/TerminalSessionPane.vue'
import {
  addSession,
  addToSplit,
  closeSession,
  emptyTerminalWorkspace,
  focusSession,
  nextSessionSequence,
  removeFromSplit,
  updateSessionStatus,
  type TerminalConnectionState,
  type TerminalSessionMeta
} from '../terminal/sessions'

defineOptions({ name: 'TerminalView' })

interface ServerOption {
  id: string
  name: string
  host?: string
  port?: number
}

interface TerminalPaneHandle {
  disconnect(): void
  reconnect(): void
  refit(): void
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const servers = ref<ServerOption[]>([])
const serverId = ref('')
const workspace = ref(emptyTerminalWorkspace())
const paneRefs = new Map<string, TerminalPaneHandle>()
const canConnectTerminal = computed(() => can(permissions.terminalConnect))
const focusedSession = computed(() => (
  workspace.value.sessions.find((session) => session.id === workspace.value.focusedId) ?? null
))
const canDisconnectFocused = computed(() => {
  const status = focusedSession.value?.status
  return canConnectTerminal.value && (status === 'connected' || status === 'connecting')
})
const canReconnectFocused = computed(() => {
  const status = focusedSession.value?.status
  return canConnectTerminal.value && !!status && status !== 'connected' && status !== 'connecting'
})

async function load() {
  servers.value = asArray(await apiGet<ServerOption[] | null>('/servers').catch(() => []))
  if (!serverId.value && servers.value.length) serverId.value = servers.value[0].id
}

function serverById(id: string) {
  return servers.value.find((server) => server.id === id)
}

function serverName(id: string) {
  return serverById(id)?.name || id
}

function serverMeta(id: string) {
  const server = serverById(id)
  if (!server) return id
  return `${server.host || server.id}:${server.port || 22}`
}

function createSessionId() {
  return typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `terminal-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function newConnection() {
  if (!canConnectTerminal.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const server = serverById(serverId.value)
  if (!server) return
  const sequence = nextSessionSequence(workspace.value.sessions, server.id)
  workspace.value = addSession(workspace.value, {
    id: createSessionId(),
    serverId: server.id,
    label: `${server.name} · ${sequence}`,
    sequence,
    status: 'connecting'
  })
}

function selectSession(id: string) {
  workspace.value = focusSession(workspace.value, id)
}

function toggleSplit(id: string) {
  if (workspace.value.visibleIds.includes(id)) {
    if (workspace.value.visibleIds.length === 1) return
    workspace.value = removeFromSplit(workspace.value, id)
    return
  }
  const result = addToSplit(workspace.value, id)
  workspace.value = result.state
  if (result.limitReached) ElMessage.warning(t('terminal.splitLimit'))
}

async function requestClose(session: TerminalSessionMeta) {
  if (session.status === 'connected' || session.status === 'connecting') {
    try {
      await ElMessageBox.confirm(
        t('terminal.closeConfirm', { target: session.label }),
        t('terminal.closeConfirmTitle'),
        { type: 'warning' }
      )
    } catch {
      return
    }
  }
  workspace.value = closeSession(workspace.value, session.id)
}

function setPaneRef(id: string, component: unknown) {
  if (component) paneRefs.set(id, component as TerminalPaneHandle)
  else paneRefs.delete(id)
}

function focusedPane() {
  const id = workspace.value.focusedId
  return id ? paneRefs.get(id) : undefined
}

function disconnectFocused() {
  focusedPane()?.disconnect()
}

function reconnectFocused() {
  focusedPane()?.reconnect()
}

function updateStatus(id: string, status: TerminalConnectionState) {
  workspace.value = updateSessionStatus(workspace.value, id, status)
}

function statusClass(status: TerminalConnectionState) {
  return {
    'is-connected': status === 'connected',
    'is-connecting': status === 'connecting',
    'is-error': status === 'error'
  }
}

onMounted(load)
</script>

<style scoped>
.terminal-page {
  min-height: 0;
  overflow: hidden;
}

.terminal-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px 16px;
  padding: 12px 14px;
}

.terminal-control-group {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1 1 auto;
}

.terminal-control-label {
  flex: 0 0 auto;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 750;
}

.terminal-toolbar-actions {
  justify-content: flex-end;
}

.terminal-panel {
  flex: 1 1 auto;
  min-height: 360px;
  padding: 12px;
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  gap: 10px;
  overflow: hidden !important;
}

.terminal-tabs {
  display: flex;
  gap: 6px;
  min-width: 0;
  padding-bottom: 2px;
  overflow-x: auto;
}

.terminal-tab {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 148px;
  max-width: 260px;
  padding: 8px 9px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-md);
  background: #f7faff;
  color: var(--aifar-text-secondary);
  cursor: pointer;
  outline: none;
}

.terminal-tab:hover,
.terminal-tab:focus-visible {
  border-color: color-mix(in srgb, var(--aifar-primary) 48%, var(--aifar-border-soft));
}

.terminal-tab.is-active {
  border-color: var(--aifar-primary);
  background: color-mix(in srgb, var(--aifar-primary) 8%, #fff);
  color: var(--aifar-text-primary);
}

.terminal-status-dot {
  width: 8px;
  height: 8px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: #9ba9ba;
}

.terminal-status-dot.is-connected {
  background: var(--el-color-success);
}

.terminal-status-dot.is-connecting {
  background: var(--el-color-warning);
}

.terminal-status-dot.is-error {
  background: var(--el-color-danger);
}

.terminal-tab-label {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  font-weight: 700;
}

.terminal-tab-action {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  flex: 0 0 auto;
  padding: 0;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.terminal-tab-action:hover:not(:disabled) {
  background: rgba(46, 94, 151, .12);
}

.terminal-tab-action:disabled {
  opacity: .35;
  cursor: not-allowed;
}

.terminal-grid {
  min-width: 0;
  min-height: 0;
  display: grid;
  gap: 10px;
  overflow: hidden;
}

.terminal-grid[data-pane-count='2'],
.terminal-grid[data-pane-count='3'],
.terminal-grid[data-pane-count='4'] {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.terminal-grid[data-pane-count='3'],
.terminal-grid[data-pane-count='4'] {
  grid-template-rows: repeat(2, minmax(0, 1fr));
}

.terminal-empty {
  min-height: 0;
  display: grid;
  place-items: start;
  padding: 12px 14px;
  border: 1px solid rgba(68, 112, 173, .35);
  border-radius: var(--aifar-radius-lg);
  background: var(--aifar-code-bg);
  color: #c7d3e3;
  font-family: "Cascadia Mono", Consolas, "SFMono-Regular", monospace;
}

@media (max-width: 760px) {
  .terminal-page {
    overflow: visible;
  }

  .terminal-toolbar,
  .terminal-control-group {
    align-items: stretch;
    flex-direction: column;
  }

  .terminal-control-group,
  .terminal-toolbar-actions {
    width: 100%;
  }

  .terminal-panel {
    height: auto;
    min-height: 640px;
  }

  .terminal-grid[data-pane-count] {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: none;
    grid-auto-rows: minmax(360px, 55vh);
    overflow: visible;
  }
}
</style>
