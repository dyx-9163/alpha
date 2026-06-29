<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('tasks.title') }}</h1>
        <p class="page-subtitle">{{ t('tasks.subtitle') }}</p>
      </div>
    </div>

    <TaskLogPane :task-id="selectedTaskId" :can-manage="canManageTasks" :disabled-reason="deniedText" />
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import TaskLogPane from '../components/TaskLogPane.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'

const route = useRoute()
const { t } = useI18n()
const { can, deniedText } = usePermissions()
const selectedTaskId = computed(() => typeof route.query.taskId === 'string' ? route.query.taskId : '')
const canManageTasks = computed(() => can(permissions.tasksManage))
</script>
