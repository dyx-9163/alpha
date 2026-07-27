<template>
  <el-dropdown-item :command="command" :disabled="isDisabled">
    <el-tooltip :content="disabledReason" :disabled="!isDisabled" :trigger="['hover', 'focus']" placement="right">
      <span
        class="runtime-overflow-action-trigger"
        :class="{ 'is-disabled': isDisabled }"
        :tabindex="isDisabled ? 0 : undefined"
        :aria-disabled="isDisabled || undefined"
        :aria-describedby="isDisabled ? descriptionId : undefined"
      >
        {{ label }}
      </span>
    </el-tooltip>
    <span v-if="isDisabled" :id="descriptionId" class="runtime-screen-reader-only">{{ disabledReason }}</span>
  </el-dropdown-item>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { runtimeOverflowReasonId, type RuntimeOverflowCommand } from './runtimeToolbar'

const props = defineProps<{
  command: RuntimeOverflowCommand
  label: string
  disabledReason: string
}>()

const isDisabled = computed(() => Boolean(props.disabledReason))
const descriptionId = computed(() => runtimeOverflowReasonId(props.command))
</script>
