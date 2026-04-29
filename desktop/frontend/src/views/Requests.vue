<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ClearRequests, GetRequests } from '../../wailsjs/go/main/App.js'
import { ClipboardSetText, EventsOff, EventsOn } from '../../wailsjs/runtime'
import JsonViewer from '../components/JsonViewer.vue'

const emit = defineEmits(['notice'])

const requests = ref([])
const selected = ref(null)
const query = ref('')
const activeStatus = ref('all')

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return requests.value.filter((request) => {
    const matchesQuery = !q || `${request.method} ${request.path} ${request.statusCode}`.toLowerCase().includes(q)
    const code = Number(request.statusCode)
    const matchesStatus =
      activeStatus.value === 'all' ||
      (activeStatus.value === 'ok' && code >= 200 && code < 300) ||
      (activeStatus.value === 'err' && code >= 400) ||
      (activeStatus.value === 'warn' && code >= 300 && code < 400)
    return matchesQuery && matchesStatus
  })
})

async function refresh() {
  try {
    requests.value = await GetRequests()
  } catch (e) {
    console.debug('Wails GetRequests unavailable in browser preview')
  }
}

async function clear() {
  try {
    await ClearRequests()
  } catch (e) {
    console.debug('Wails ClearRequests unavailable in browser preview')
  }
  requests.value = []
  selected.value = null
}

function statusClass(code) {
  if (code >= 200 && code < 300) return 'ok'
  if (code >= 400) return 'err'
  return 'warn'
}

function selectRow(index) {
  selected.value = selected.value === index ? null : index
}

async function writeClipboard(text) {
  const value = text || ''
  try {
    await ClipboardSetText(value)
    return true
  } catch (e) {
    await navigator.clipboard?.writeText(value)
    return true
  }
}

async function copyText(text, label) {
  try {
    await writeClipboard(text)
    emit('notice', `已复制${label}`)
  } catch (e) {
    console.debug('Copy failed:', e)
    emit('notice', `${label}复制失败`)
  }
}

function safeEventsOn(name, handler) {
  try {
    EventsOn(name, handler)
  } catch (e) {
    console.debug('Wails runtime event unavailable:', name)
  }
}

function safeEventsOff(name) {
  try {
    EventsOff(name)
  } catch (e) {
    console.debug('Wails runtime event unavailable:', name)
  }
}

onMounted(() => {
  refresh()
  safeEventsOn('requests:updated', (data) => {
    requests.value = data || []
  })
})

onUnmounted(() => {
  safeEventsOff('requests:updated')
})
</script>

<template>
  <div class="page requests-page">
    <div class="page-title">
      <div>
        <h1>请求流</h1>
        <p>查看客户端调用 OpenAI / Anthropic 兼容接口的请求与响应。</p>
      </div>
      <div class="toolbar">
        <button class="secondary-button" type="button" @click="refresh">刷新</button>
        <button class="danger-button" type="button" @click="clear">清空</button>
      </div>
    </div>

    <section class="table-panel requests-panel">
      <div class="table-toolbar">
        <input v-model="query" class="search-input" type="search" placeholder="搜索路径、方法或状态码" />
        <div class="segmented">
          <button :class="{ active: activeStatus === 'all' }" type="button" @click="activeStatus = 'all'">全部</button>
          <button :class="{ active: activeStatus === 'ok' }" type="button" @click="activeStatus = 'ok'">成功</button>
          <button :class="{ active: activeStatus === 'warn' }" type="button" @click="activeStatus = 'warn'">跳转</button>
          <button :class="{ active: activeStatus === 'err' }" type="button" @click="activeStatus = 'err'">错误</button>
        </div>
      </div>

      <div v-if="filtered.length > 0" class="table-scroll hidden-scrollbar">
        <table class="data-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>方法</th>
              <th>路径</th>
              <th>状态</th>
              <th>耗时</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(request, index) in filtered"
              :key="index"
              :class="{ selected: selected === index }"
              @click="selectRow(index)"
            >
              <td>{{ request.time }}</td>
              <td><span class="method-chip">{{ request.method }}</span></td>
              <td>
                <div class="cell-main">{{ request.path }}</div>
                <div class="cell-sub">{{ request.reqBody ? '包含请求体' : '无请求体' }}</div>
              </td>
              <td><span class="status-chip" :class="statusClass(request.statusCode)">{{ request.statusCode }}</span></td>
              <td>{{ request.duration }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty-state">暂无匹配请求。</div>

      <div v-if="selected !== null && filtered[selected]" class="detail-panel hidden-scrollbar">
        <div class="detail-section">
          <div class="detail-toolbar">
            <h3>请求内容</h3>
            <div class="detail-actions">
              <button type="button" class="ghost-button" @click="copyText(filtered[selected].reqBody, '请求内容')">
                复制
              </button>
            </div>
          </div>
          <JsonViewer :body="filtered[selected].reqBody" empty-text="空请求体" />
        </div>
        <div class="detail-section">
          <div class="detail-toolbar">
            <h3>响应内容</h3>
            <div class="detail-actions">
              <button type="button" class="ghost-button" @click="copyText(filtered[selected].respBody, '响应内容')">
                复制
              </button>
            </div>
          </div>
          <JsonViewer :body="filtered[selected].respBody" empty-text="空响应体" />
        </div>
      </div>
    </section>
  </div>
</template>
