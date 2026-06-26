<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('tasks.title') }}</h1>
        <p class="page-subtitle">{{ t('tasks.subtitle') }}</p>
      </div>
    </div>

    <TaskLogPane :task-id="selectedTaskId" />
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import TaskLogPane from '../components/TaskLogPane.vue'
import { useI18n } from '../i18n'

const route = useRoute()
const { t } = useI18n()
const selectedTaskId = computed(() => typeof route.query.taskId === 'string' ? route.query.taskId : '')
</script>
