<template>
  <section class="terminal-page">
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('terminal.title') }}</h1>
        <p class="page-subtitle">{{ t('terminal.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-select v-model="serverId" :placeholder="t('terminal.server')" class="toolbar-control">
          <el-option v-for="server in servers" :key="server.id" :label="server.name" :value="server.id" />
        </el-select>
        <el-tooltip :content="deniedText" :disabled="canConnectTerminal" placement="top">
          <span><el-button type="primary" :disabled="!canConnectTerminal || !serverId" @click="connect">{{ t('terminal.connect') }}</el-button></span>
        </el-tooltip>
        <el-button @click="newTab">{{ t('terminal.newTab') }}</el-button>
        <el-button @click="$router.push('/servers')">{{ t('terminal.openServers') }}</el-button>
      </div>
    </div>

    <div class="workspace-card terminal-panel">
      <el-tabs v-model="activeTab">
        <el-tab-pane :label="tabLabel" name="main" />
      </el-tabs>
      <div class="terminal-head">
        <div>
          <strong>{{ currentServer?.name || t('terminal.notSelected') }}</strong>
          <span>{{ connected ? t('common.connected') : t('common.disconnected') }}</span>
        </div>
        <div class="head-actions">
          <el-tooltip :content="deniedText" :disabled="canConnectTerminal" placement="top">
            <span><el-button size="small" type="primary" :disabled="!canConnectTerminal || !serverId" @click="connect">{{ t('terminal.connect') }}</el-button></span>
          </el-tooltip>
          <el-button size="small" @click="disconnect">{{ t('terminal.disconnect') }}</el-button>
        </div>
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

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const servers = ref<any[]>([])
const serverId = ref('')
const terminalEl = ref<HTMLElement>()
const fallback = ref(t('terminal.fallback'))
const activeTab = ref('main')
const connected = ref(false)
let terminal: Terminal | null = null
let socket: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null
const currentServer = computed(() => servers.value.find((server) => server.id === serverId.value))
const tabLabel = computed(() => currentServer.value ? `${currentServer.value.name} - ${connected.value ? t('common.connected') : t('terminal.ready')}` : t('terminal.tab'))
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
  terminal = new Terminal({ cursorBlink: true, fontSize: 13 })
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
    window.setTimeout(fitTerminal, 50)
  })
}

function fitTerminal() {
  if (!terminal || !terminalEl.value) return
  const style = window.getComputedStyle(terminalEl.value)
  const width = terminalEl.value.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
  const height = terminalEl.value.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom)
  const measure = terminalEl.value.querySelector('.xterm-char-measure-element') as HTMLElement | null
  const rect = measure?.getBoundingClientRect()
  const cellWidth = rect && rect.width > 0 ? rect.width : 8
  const cellHeight = rect && rect.height > 0 ? rect.height : 17
  const cols = Math.max(20, Math.floor(width / cellWidth))
  const rows = Math.max(8, Math.floor(height / cellHeight))
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
}

.terminal-panel {
  padding: 12px;
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr);
  min-height: 0;
  overflow: hidden !important;
}

.terminal-panel :deep(.el-tabs__header) {
  margin-bottom: 10px;
}

.terminal-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  padding: 10px 12px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  background: #fbfdff;
}

.terminal-head strong,
.terminal-head span {
  display: block;
}

.terminal-head span {
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.terminal-box {
  min-height: 0;
  height: 100%;
  box-sizing: border-box;
  overflow: hidden;
  border-radius: var(--aifar-radius-lg);
}

.terminal-box :deep(.xterm) {
  height: 100%;
}

.terminal-box :deep(.xterm-viewport) {
  overflow-y: auto;
}

@media (max-width: 760px) {
  .terminal-head {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
