<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import Dashboard from './views/Dashboard.vue'
import Logs from './views/Logs.vue'
import Models from './views/Models.vue'
import Requests from './views/Requests.vue'
import Settings from './views/Settings.vue'
import { EventsOff, EventsOn } from '../wailsjs/runtime'
import { GetStatus, HideWindow, MinimizeWindow } from '../wailsjs/go/main/App.js'
import lingmaIcon from './assets/images/lingma-icon.png'

const currentTab = ref('dashboard')
const logs = ref([])
const status = ref({ running: false, addr: '', models: 0 })
const toast = ref('')
const themeMode = ref(localStorage.getItem('lingma-theme-mode') || 'system')
const appliedTheme = ref('light')
let systemThemeQuery = null
let toastTimer = null

const navigation = [
  { key: 'dashboard', label: '仪表盘', icon: 'bi-house-door' },
  { key: 'requests', label: '请求流', icon: 'bi-file-earmark-text' },
  { key: 'models', label: '模型', icon: 'bi-box' },
  { key: 'settings', label: '设置', icon: 'bi-gear' },
  { key: 'logs', label: '日志', icon: 'bi-terminal' },
]

function addLog(level, message) {
  const time = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  logs.value.unshift({ time, level, message })
  if (logs.value.length > 500) {
    logs.value = logs.value.slice(0, 500)
  }
}

function showToast(message) {
  toast.value = message
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    toast.value = ''
  }, 2200)
}

function clearLocalLogs() {
  logs.value = []
}

function setStatus(nextStatus) {
  status.value = nextStatus
}

function handleNotice(message) {
  showToast(message)
  addLog('info', message)
}

function resolveTheme() {
  if (themeMode.value === 'system') {
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  return themeMode.value
}

function applyTheme() {
  appliedTheme.value = resolveTheme()
  document.documentElement.dataset.theme = appliedTheme.value
  localStorage.setItem('lingma-theme-mode', themeMode.value)
}

function toggleTheme() {
  const modes = ['system', 'light', 'dark']
  const index = modes.indexOf(themeMode.value)
  themeMode.value = modes[(index + 1) % modes.length]
  applyTheme()
}

function themeTitle() {
  if (themeMode.value === 'system') return `跟随系统（当前${appliedTheme.value === 'dark' ? '夜间' : '日间'}）`
  return themeMode.value === 'dark' ? '夜间模式' : '日间模式'
}

function themeIcon() {
  if (themeMode.value === 'system') return 'bi-circle-half'
  return themeMode.value === 'dark' ? 'bi-moon-stars' : 'bi-sun'
}

async function refreshStatus() {
  try {
    status.value = await GetStatus()
  } catch (e) {
    addLog('error', '状态刷新失败：' + (e.message || String(e)))
  }
}

async function copyEndpoint() {
  if (!status.value.addr) return
  const value = `http://${status.value.addr}`
  await navigator.clipboard?.writeText(value)
  handleNotice('已复制接口地址：' + value)
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

function handleAppShortcut(event) {
  const key = event.key.toLowerCase()
  if ((event.metaKey || event.ctrlKey) && key === 'w') {
    event.preventDefault()
    HideWindow()
  }
  if ((event.metaKey || event.ctrlKey) && key === 'm') {
    event.preventDefault()
    MinimizeWindow()
  }
  // Fallback copy for WebView where native Edit menu is unavailable
  if ((event.metaKey || event.ctrlKey) && key === 'c') {
    const selection = window.getSelection()?.toString()
    if (selection) {
      event.preventDefault()
      navigator.clipboard?.writeText(selection).catch(() => {})
    }
  }
  // Fallback select-all for log/request content areas
  if ((event.metaKey || event.ctrlKey) && key === 'a') {
    const active = document.activeElement
    const isEditable = active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA' || active.isContentEditable)
    if (!isEditable) {
      const panel = document.querySelector('.log-list, .request-list, .detail-panel')
      if (panel) {
        event.preventDefault()
        const range = document.createRange()
        range.selectNodeContents(panel)
        const sel = window.getSelection()
        sel?.removeAllRanges()
        sel?.addRange(range)
      }
    }
  }
}

onMounted(() => {
  window.addEventListener('keydown', handleAppShortcut, true)
  systemThemeQuery = window.matchMedia?.('(prefers-color-scheme: dark)')
  systemThemeQuery?.addEventListener?.('change', applyTheme)
  applyTheme()
  refreshStatus()
  safeEventsOn('models:updated', (data) => {
    status.value.models = Array.isArray(data) ? data.length : status.value.models
    addLog('info', `模型列表已更新：${status.value.models} 个模型`)
  })
  safeEventsOn('log', (data) => {
    addLog(data.level || 'info', data.message || '')
    refreshStatus()
  })
  safeEventsOn('quit:confirm', (message) => {
    showToast(message || '再按一次退出快捷键将停止代理并退出应用')
  })
  safeEventsOn('status:updated', (nextStatus) => {
    status.value = nextStatus
  })
  safeEventsOn('requests:updated', () => {
    refreshStatus()
  })
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleAppShortcut, true)
  clearTimeout(toastTimer)
  systemThemeQuery?.removeEventListener?.('change', applyTheme)
  safeEventsOff('models:updated')
  safeEventsOff('log')
  safeEventsOff('quit:confirm')
  safeEventsOff('status:updated')
  safeEventsOff('requests:updated')
})
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <button class="brand" type="button" @click="currentTab = 'dashboard'">
        <span class="brand-mark">
          <img :src="lingmaIcon" alt="" />
        </span>
        <span>
          <strong>灵码代理</strong>
          <small>IPC Proxy</small>
        </span>
      </button>

      <nav class="nav-list" aria-label="主导航">
        <button
          v-for="item in navigation"
          :key="item.key"
          class="nav-item"
          :class="{ active: currentTab === item.key }"
          type="button"
          @click="currentTab = item.key"
        >
          <span class="nav-icon">
            <i class="bi" :class="item.icon" aria-hidden="true"></i>
          </span>
          <span>{{ item.label }}</span>
        </button>
      </nav>

      <div class="sidebar-status">
        <span class="status-dot" :class="{ running: status.running }"></span>
        <div>
          <strong>{{ status.running ? 'Proxy Running' : 'Proxy Stopped' }}</strong>
          <small>v1.4.0</small>
        </div>
      </div>
    </aside>

    <section class="workspace">
      <header class="topbar">
        <span class="topbar-spacer" aria-hidden="true"></span>
        <div class="topbar-actions">
          <button class="icon-button" type="button" title="刷新状态" @click="refreshStatus">
            <i class="bi bi-arrow-clockwise" aria-hidden="true"></i>
          </button>
          <button class="icon-button" type="button" title="复制接口地址" @click="copyEndpoint">
            <i class="bi bi-copy" aria-hidden="true"></i>
          </button>
          <button class="icon-button" type="button" title="设置" @click="currentTab = 'settings'">
            <i class="bi bi-gear" aria-hidden="true"></i>
          </button>
          <button class="icon-button" type="button" :title="themeTitle()" @click="toggleTheme">
            <i class="bi" :class="themeIcon()" aria-hidden="true"></i>
          </button>
        </div>
      </header>

      <main class="view-stage">
        <Dashboard
          v-if="currentTab === 'dashboard'"
          :shell-status="status"
          @log="addLog"
          @status="setStatus"
          @notice="handleNotice"
          @open-settings="currentTab = 'settings'"
          @open-requests="currentTab = 'requests'"
          @open-models="currentTab = 'models'"
        />
        <Requests v-else-if="currentTab === 'requests'" @notice="handleNotice" />
        <Models v-else-if="currentTab === 'models'" @log="addLog" @status="setStatus" @notice="handleNotice" />
        <Settings v-else-if="currentTab === 'settings'" @log="addLog" @status-refresh="refreshStatus" />
        <Logs v-else-if="currentTab === 'logs'" :logs="logs" @clear="clearLocalLogs" @notice="handleNotice" />
      </main>
    </section>
    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>
