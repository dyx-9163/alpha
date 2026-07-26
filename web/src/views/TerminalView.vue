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
        <span class="status-pill" :class="connected ? 'success' : ''">{{ connectionStatus }}</span>
      </div>
      <div class="head-actions terminal-toolbar-actions">
        <el-tooltip :content="deniedText" :disabled="canConnectTerminal" placement="top">
          <span><el-button type="primary" :disabled="!canConnectTerminal || !serverId" @click="connect">{{ t('terminal.connect') }}</el-button></span>
        </el-tooltip>
        <el-button :disabled="!connected" @click="disconnect">{{ t('terminal.disconnect') }}</el-button>
        <el-button @click="newTab">{{ t('terminal.newTab') }}</el-button>
      </div>
    </div>

    <div class="workspace-card terminal-panel">
      <div class="terminal-session-head">
        <div>
          <strong>{{ currentServer?.name || t('terminal.notSelected') }}</strong>
          <span>{{ currentServerMeta }}</span>
        </div>
        <span class="status-pill" :class="connected ? 'success' : ''">{{ connectionStatus }}</span>
      </div>
      <div ref="terminalEl" class="terminal-box">{{ fallback }}</div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Terminal } from '@xterm/xterm'
import { apiGet, asArray, terminalProtocols, terminalUrl } from '../api/client'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { calculateTerminalGrid } from '../terminal/grid'

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const servers = ref<any[]>([])
const serverId = ref('')
const terminalEl = ref<HTMLElement>()
const fallback = ref(t('terminal.fallback'))
const connected = ref(false)
let terminal: Terminal | null = null
let socket: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null
const currentServer = computed(() => servers.value.find((server) => server.id === serverId.value))
const connectionStatus = computed(() => connected.value ? t('common.connected') : t('common.disconnected'))
const currentServerMeta = computed(() => {
  const server = currentServer.value
  if (!server) return t('terminal.fallback')
  const host = server.host || server.id
  const port = server.port || 22
  return `${host}:${port}`
})
const canConnectTerminal = computed(() => can(permissions.terminalConnect))

async function load() {
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
  if (!serverId.value && servers.value.length) serverId.value = servers.value[0].id
}
function connect() {
  if (!canConnectTerminal.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  if (!terminalEl.value || !serverId.value) return
  terminalEl.value.textContent = ''
  terminal?.dispose()
  terminal = new Terminal({
    cursorBlink: true,
    fontFamily: '"Cascadia Mono", Consolas, "SFMono-Regular", monospace',
    fontSize: 13,
    lineHeight: 1.2,
    scrollback: 10000
  })
  terminal.open(terminalEl.value)
  observeTerminalSize()
  scheduleTerminalFit()
  socket?.close()
  socket = new WebSocket(terminalUrl(serverId.value), terminalProtocols())
  socket.binaryType = 'arraybuffer'
  socket.onopen = () => {
    connected.value = true
    fitTerminal()
    terminal?.write(`${t('terminal.connecting', { target: currentServer.value?.name ?? serverId.value })}\r\n`)
  }
  socket.onmessage = (event) => writeTerminalData(event.data)
  socket.onerror = () => {
    terminal?.write(`\r\n${t('terminal.connectionFailed')}\r\n`)
    connected.value = false
  }
  socket.onclose = () => {
    if (connected.value) {
      terminal?.write(`\r\n${t('terminal.disconnectedMessage')}\r\n`)
    }
    connected.value = false
  }
  terminal.onData((data) => socket?.send(data))
}
function disconnect() {
  socket?.close()
  connected.value = false
}
function newTab() {
  fallback.value = t('terminal.fallback')
  disconnect()
  terminal?.dispose()
  terminal = null
  if (terminalEl.value) terminalEl.value.textContent = fallback.value
}
function writeTerminalData(data: unknown) {
  if (data instanceof ArrayBuffer) {
    terminal?.write(new Uint8Array(data))
    return
  }
  if (data instanceof Blob) {
    void data.arrayBuffer().then((buffer) => terminal?.write(new Uint8Array(buffer)))
    return
  }
  terminal?.write(String(data))
}

function observeTerminalSize() {
  if (!terminalEl.value || resizeObserver) return
  resizeObserver = new ResizeObserver(() => fitTerminal())
  resizeObserver.observe(terminalEl.value)
}

function scheduleTerminalFit() {
  void nextTick(() => {
    fitTerminal()
    window.requestAnimationFrame(fitTerminal)
    window.setTimeout(fitTerminal, 50)
    window.setTimeout(fitTerminal, 150)
  })
}

function fitTerminal() {
  if (!terminal || !terminalEl.value) return
  const style = window.getComputedStyle(terminalEl.value)
  const width = terminalEl.value.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
  const height = terminalEl.value.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom)
  const measure = terminalEl.value.querySelector('.xterm-char-measure-element') as HTMLElement | null
  const rect = measure?.getBoundingClientRect()
  const measuredCellWidth = rect?.width ?? 0
  const measuredCharHeight = rect?.height ?? 0
  const { cols, rows } = calculateTerminalGrid({
    width,
    height,
    measuredCellWidth,
    measuredCharHeight,
    lineHeight: Number(terminal.options.lineHeight)
  })
  if (cols !== terminal.cols || rows !== terminal.rows) {
    terminal.resize(cols, rows)
  }
}

onMounted(load)
onBeforeUnmount(() => {
  socket?.close()
  resizeObserver?.disconnect()
  terminal?.dispose()
})
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
  padding: 14px;
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  gap: 12px;
  min-height: 360px;
  height: auto;
  overflow: hidden !important;
}

.terminal-session-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  background: #fbfdff;
}

.terminal-session-head strong,
.terminal-session-head span {
  display: block;
}

.terminal-session-head span {
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.terminal-box {
  min-height: 0;
  height: 100%;
  box-sizing: border-box;
  overflow: hidden;
  border-radius: var(--aifar-radius-lg);
  padding: 8px 10px;
  background: var(--aifar-code-bg);
  border: 1px solid rgba(68, 112, 173, .35);
}

.terminal-box :deep(.xterm) {
  height: 100%;
  width: 100%;
}

.terminal-box :deep(.xterm-viewport) {
  overflow-y: auto;
}

.terminal-box :deep(.xterm-screen) {
  padding-top: 2px;
  padding-left: 2px;
}

@media (max-width: 760px) {
  .terminal-page {
    overflow: visible;
  }

  .terminal-toolbar,
  .terminal-session-head {
    align-items: flex-start;
    flex-direction: column;
  }

  .terminal-control-group {
    align-items: stretch;
    flex-direction: column;
    width: 100%;
  }

  .terminal-toolbar-actions {
    width: 100%;
  }

  .terminal-panel {
    height: 560px;
  }
}
</style>
