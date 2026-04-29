<script setup>
import { computed, ref } from 'vue'
import { ClipboardSetText } from '../../wailsjs/runtime'

const props = defineProps({
  logs: {
    type: Array,
    default: () => [],
  },
})

const emit = defineEmits(['clear', 'notice'])

const filter = ref('all')
const search = ref('')

const filteredLogs = computed(() => {
  const q = search.value.trim().toLowerCase()
  return props.logs.filter((log) => {
    const matchesLevel = filter.value === 'all' || log.level === filter.value
    const matchesSearch = !q || `${log.time} ${log.level} ${log.message}`.toLowerCase().includes(q)
    return matchesLevel && matchesSearch
  })
})

function levelClass(level) {
  return {
    info: 'level-info',
    warn: 'level-warn',
    error: 'level-error',
  }[level] || 'level-info'
}

function levelLabel(level) {
  return {
    info: '信息',
    warn: '警告',
    error: '错误',
  }[level] || level
}

function serializeLogs() {
  return filteredLogs.value.map((log) => `[${log.time}] ${levelLabel(log.level)} ${log.message}`).join('\n')
}

async function copyLogs() {
  try {
    await ClipboardSetText(serializeLogs())
    emit('notice', `已复制 ${filteredLogs.value.length} 条日志`)
  } catch (e) {
    try {
      await navigator.clipboard?.writeText(serializeLogs())
      emit('notice', `已复制 ${filteredLogs.value.length} 条日志`)
    } catch (fallbackError) {
      console.debug('Copy logs failed:', fallbackError)
      emit('notice', '日志复制失败')
    }
  }
}
</script>

<template>
  <div class="page logs-page">
    <div class="page-title">
      <div>
        <h1>日志</h1>
        <p>记录代理启动、模型同步、健康检查和配置保存事件。</p>
      </div>
      <div class="toolbar">
        <button class="secondary-button" type="button" :disabled="filteredLogs.length === 0" @click="copyLogs">复制日志</button>
        <button class="danger-button" type="button" @click="emit('clear')">清空日志</button>
      </div>
    </div>

    <section class="table-panel logs-panel">
      <div class="table-toolbar">
        <div class="segmented">
          <button :class="{ active: filter === 'all' }" type="button" @click="filter = 'all'">全部</button>
          <button :class="{ active: filter === 'info' }" type="button" @click="filter = 'info'">信息</button>
          <button :class="{ active: filter === 'warn' }" type="button" @click="filter = 'warn'">警告</button>
          <button :class="{ active: filter === 'error' }" type="button" @click="filter = 'error'">错误</button>
        </div>
        <input v-model="search" class="search-input" type="search" placeholder="搜索日志内容" />
      </div>

      <div v-if="filteredLogs.length > 0" class="log-list hidden-scrollbar">
        <div v-for="(log, index) in filteredLogs" :key="index" class="log-row">
          <span class="muted">{{ log.time }}</span>
          <strong :class="levelClass(log.level)">{{ levelLabel(log.level) }}</strong>
          <span>{{ log.message }}</span>
        </div>
      </div>
      <div v-else class="empty-state">暂无日志。</div>
    </section>
  </div>
</template>
