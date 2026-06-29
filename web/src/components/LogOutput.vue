<template>
  <div ref="root" class="log-output" :style="{ minHeight: normalizedMinHeight }">
    <div v-if="normalizedLines.length" class="log-lines">
      <div v-for="line in normalizedLines" :key="lineKey(line)" class="log-line" :class="line.level">
        <span v-if="line.createdAt" class="log-time">{{ formatTime(line.createdAt) }}</span>
        <span v-if="line.level" class="log-level">[{{ line.level }}]</span>
        <span class="log-message">{{ line.message }}</span>
      </div>
    </div>
    <pre v-else-if="displayText" class="log-pre">{{ displayText }}</pre>
    <span v-else class="empty-log">{{ emptyText }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

type LogLine = {
  id?: string | number
  level?: string
  message: string
  createdAt?: string
}

const props = withDefaults(defineProps<{
  lines?: LogLine[]
  text?: string
  emptyText: string
  minHeight?: string
  autoScroll?: boolean
}>(), {
  lines: () => [],
  text: '',
  minHeight: '180px'
})

const root = ref<HTMLElement | null>(null)
const normalizedMinHeight = computed(() => props.minHeight || '180px')
const displayText = computed(() => props.text.trim())
const normalizedLines = computed(() => props.lines.map((line) => ({
  ...line,
  level: String(line.level || '').toLowerCase()
})))

watch(
  () => [normalizedLines.value.length, displayText.value],
  async () => {
    if (!props.autoScroll) {
      return
    }
    await nextTick()
    if (root.value) {
      root.value.scrollTop = root.value.scrollHeight
    }
  },
  { flush: 'post' }
)

function lineKey(line: LogLine) {
  return line.id ?? `${line.createdAt ?? ''}-${line.level ?? ''}-${line.message}`
}

function formatTime(value?: string) {
  if (!value) {
    return '-'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString()
}
</script>

<style scoped>
.log-output {
  overflow: auto;
  background: var(--aifar-code-bg);
  color: #dbeafe;
  font-family: Consolas, "SFMono-Regular", monospace;
  font-size: 12px;
  line-height: 20px;
  padding: 12px;
  white-space: pre-wrap;
  border-radius: var(--aifar-radius-lg);
}

.log-lines {
  display: grid;
  gap: 0;
}

.log-line {
  display: grid;
  grid-template-columns: minmax(132px, 168px) 56px minmax(0, 1fr);
  gap: 6px;
}

.log-line.error {
  color: #fecaca;
}

.log-line.warn,
.log-line.warning {
  color: #fde68a;
}

.log-time {
  color: #93a4bc;
}

.log-level {
  color: #bfdbfe;
  font-weight: 850;
}

.log-message {
  word-break: break-word;
}

.log-pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font: inherit;
}

.empty-log {
  color: #93a4bc;
}

@media (max-width: 980px) {
  .log-line {
    grid-template-columns: 1fr;
  }
}
</style>
