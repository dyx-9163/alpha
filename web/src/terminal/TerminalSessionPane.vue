<template>
  <section
    class="terminal-session-pane"
    :class="{ 'is-focused': focused }"
    @mousedown="emit('focus', session.id)"
  >
    <header class="terminal-session-head">
      <div class="terminal-session-identity">
        <strong>{{ serverName }}</strong>
        <span>{{ serverMeta }}</span>
      </div>
      <span class="status-pill" :class="statusClass">
        {{ t(`terminal.status.${session.status}`) }}
      </span>
    </header>
    <div ref="terminalEl" class="terminal-box" />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onActivated, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { terminalProtocols, terminalUrl } from '../api/client'
import { useI18n } from '../i18n'
import { createTerminalFitScheduler } from './fitScheduler'
import { calculateTerminalGrid } from './grid'
import { createTerminalSessionController } from './sessionController'
import type { TerminalConnectionState, TerminalSessionMeta } from './sessions'

const props = defineProps<{
  session: TerminalSessionMeta
  serverName: string
  serverMeta: string
  visible: boolean
  focused: boolean
}>()
const emit = defineEmits<{
  status: [id: string, status: TerminalConnectionState]
  focus: [id: string]
}>()
const { t } = useI18n()
const terminalEl = ref<HTMLElement>()
const statusClass = computed(() => ({
  success: props.session.status === 'connected',
  warning: props.session.status === 'connecting',
  danger: props.session.status === 'error'
}))
let terminal: Terminal | null = null
let controller: ReturnType<typeof createTerminalSessionController> | null = null
let resizeObserver: ResizeObserver | null = null
let inputDisposable: { dispose(): void } | null = null

const fitScheduler = createTerminalFitScheduler({
  isVisible: () => props.visible,
  fit: fitTerminal,
  afterRender: (callback) => { void nextTick(callback) },
  requestFrame: (callback) => window.requestAnimationFrame(callback),
  cancelFrame: (id) => window.cancelAnimationFrame(id)
})

function mountSession() {
  if (!terminalEl.value) return
  terminal = new Terminal({
    cursorBlink: true,
    fontFamily: '"Cascadia Mono", Consolas, "SFMono-Regular", monospace',
    fontSize: 13,
    lineHeight: 1.2,
    scrollback: 10000
  })
  terminal.open(terminalEl.value)
  controller = createTerminalSessionController({
    terminal,
    createSocket: () => new WebSocket(terminalUrl(props.session.serverId), terminalProtocols()),
    onState: (status) => emit('status', props.session.id, status),
    connectionFailedText: t('terminal.connectionFailed'),
    disconnectedText: t('terminal.disconnectedMessage')
  })
  inputDisposable = terminal.onData((data) => controller?.send(data))
  resizeObserver = new ResizeObserver(fitScheduler.schedule)
  resizeObserver.observe(terminalEl.value)
  terminal.write(`${t('terminal.connecting', { target: props.serverName })}\r\n`)
  controller.connect()
  fitScheduler.schedule()
}

function disconnect() {
  controller?.disconnect()
}

function reconnect() {
  terminal?.write(`\r\n${t('terminal.connecting', { target: props.serverName })}\r\n`)
  controller?.connect()
  fitScheduler.schedule()
}

function fitTerminal() {
  if (!terminal || !terminalEl.value || !props.visible) return
  const style = window.getComputedStyle(terminalEl.value)
  const width = terminalEl.value.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
  const height = terminalEl.value.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom)
  const measure = terminalEl.value.querySelector('.xterm-char-measure-element') as HTMLElement | null
  const rect = measure?.getBoundingClientRect()
  const size = calculateTerminalGrid({
    width,
    height,
    measuredCellWidth: rect?.width ?? 0,
    measuredCharHeight: rect?.height ?? 0,
    lineHeight: Number(terminal.options.lineHeight)
  })
  if (size.cols !== terminal.cols || size.rows !== terminal.rows) {
    terminal.resize(size.cols, size.rows)
  }
}

function disposeSession() {
  fitScheduler.dispose()
  controller?.dispose()
  resizeObserver?.disconnect()
  inputDisposable?.dispose()
  terminal?.dispose()
  controller = null
  resizeObserver = null
  inputDisposable = null
  terminal = null
}

watch(() => props.visible, (visible) => {
  if (visible) fitScheduler.schedule()
})
onMounted(mountSession)
onActivated(fitScheduler.schedule)
onBeforeUnmount(disposeSession)
defineExpose({ disconnect, reconnect, refit: fitScheduler.schedule })
</script>

<style scoped>
.terminal-session-pane {
  min-width: 0;
  min-height: 0;
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  gap: 8px;
  padding: 8px;
  overflow: hidden;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
}

.terminal-session-pane.is-focused {
  border-color: var(--aifar-primary);
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--aifar-primary) 28%, transparent);
}

.terminal-session-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;
  padding: 4px 6px;
}

.terminal-session-identity {
  min-width: 0;
}

.terminal-session-identity strong,
.terminal-session-identity span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.terminal-session-identity span {
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
  width: 100%;
  height: 100%;
}

.terminal-box :deep(.xterm-viewport) {
  overflow-y: auto;
}

.terminal-box :deep(.xterm-screen) {
  padding-top: 2px;
  padding-left: 2px;
}
</style>
