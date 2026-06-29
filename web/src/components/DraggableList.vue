<template>
  <div class="draggable-list" :class="{ 'is-disabled': disabled }">
    <div
      v-for="(item, index) in items"
      :key="itemKeyValue(item, index)"
      class="draggable-list__item"
      :class="{ 'is-dragging': draggingIndex === index, 'is-drop-target': dropIndex === index && draggingIndex !== index }"
      :draggable="!disabled && items.length > 1"
      @dragstart="onDragStart($event, index)"
      @dragenter.prevent="onDragOver(index)"
      @dragover.prevent="onDragOver(index)"
      @drop.prevent="onDrop(index)"
      @dragend="resetDrag"
    >
      <slot :item="item" :index="index" :dragging="draggingIndex === index" :drop-target="dropIndex === index" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

type KeyValue = string | number
type ItemKey = string | ((item: any, index: number) => KeyValue)

type DraggableListReorderPayload = {
  items: any[]
  keys: KeyValue[]
  fromIndex: number
  toIndex: number
}

const props = withDefaults(defineProps<{
  items: any[]
  itemKey?: ItemKey
  disabled?: boolean
}>(), {
  itemKey: 'id',
  disabled: false
})

const emit = defineEmits<{
  'update:items': [items: any[]]
  reorder: [payload: DraggableListReorderPayload]
}>()

defineSlots<{
  default(props: { item: any; index: number; dragging: boolean; dropTarget: boolean }): any
}>()

const draggingIndex = ref<number | null>(null)
const dropIndex = ref<number | null>(null)

function itemKeyValue(item: any, index: number): KeyValue {
  if (typeof props.itemKey === 'function') {
    return props.itemKey(item, index)
  }
  if (item && typeof item === 'object' && props.itemKey in item) {
    const value = (item as Record<string, unknown>)[props.itemKey]
    if (typeof value === 'string' || typeof value === 'number') {
      return value
    }
  }
  return index
}

function onDragStart(event: DragEvent, index: number) {
  if (props.disabled) {
    return
  }
  draggingIndex.value = index
  dropIndex.value = index
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', String(itemKeyValue(props.items[index], index)))
  }
}

function onDragOver(index: number) {
  if (!props.disabled && draggingIndex.value !== null) {
    dropIndex.value = index
  }
}

function onDrop(toIndex: number) {
  const fromIndex = draggingIndex.value
  if (props.disabled || fromIndex === null) {
    resetDrag()
    return
  }
  if (fromIndex === toIndex) {
    resetDrag()
    return
  }
  const next = props.items.slice()
  const [moved] = next.splice(fromIndex, 1)
  next.splice(toIndex, 0, moved)
  emit('update:items', next)
  emit('reorder', {
    items: next,
    keys: next.map((item, index) => itemKeyValue(item, index)),
    fromIndex,
    toIndex
  })
  resetDrag()
}

function resetDrag() {
  draggingIndex.value = null
  dropIndex.value = null
}
</script>

<style scoped>
.draggable-list {
  display: grid;
  gap: 8px;
}

.draggable-list__item {
  border-radius: var(--aifar-radius-lg);
  transition: opacity .16s ease, outline-color .16s ease, transform .16s ease;
}

.draggable-list__item[draggable="true"] {
  cursor: grab;
}

.draggable-list__item.is-dragging {
  opacity: .58;
}

.draggable-list__item.is-drop-target {
  outline: 2px solid #1677ff;
  outline-offset: 2px;
}

.draggable-list.is-disabled .draggable-list__item {
  cursor: default;
}
</style>
