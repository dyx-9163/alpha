<template>
  <el-dropdown-item
    :command="command"
    class="runtime-overflow-action-item"
    :class="{ 'is-aria-disabled': isDisabled }"
    :aria-describedby="isDisabled ? descriptionId : undefined"
    @focus="reasonTooltipVisible = isDisabled"
    @blur="reasonTooltipVisible = false"
  >
    <el-tooltip v-model:visible="reasonTooltipVisible" :content="disabledReason" :disabled="!isDisabled" :trigger="['hover', 'focus']" placement="right">
      <span ref="triggerRef" class="runtime-overflow-action-trigger">
        {{ label }}
      </span>
    </el-tooltip>
    <span v-if="isDisabled" :id="descriptionId" class="runtime-screen-reader-only">{{ disabledReason }}</span>
  </el-dropdown-item>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import {
  runtimeOverflowReasonId,
  syncRuntimeOverflowMenuItemState,
  type RuntimeOverflowCommand
} from './runtimeToolbar'

const props = defineProps<{
  command: RuntimeOverflowCommand
  label: string
  disabledReason: string
}>()

const isDisabled = computed(() => Boolean(props.disabledReason))
const descriptionId = computed(() => runtimeOverflowReasonId(props.command))
const triggerRef = ref<HTMLElement | null>(null)
const reasonTooltipVisible = ref(false)

function syncMenuItemState() {
  syncRuntimeOverflowMenuItemState(triggerRef.value, isDisabled.value, descriptionId.value)
}

onMounted(syncMenuItemState)
watch([isDisabled, descriptionId], () => {
  void nextTick(syncMenuItemState)
})
</script>
