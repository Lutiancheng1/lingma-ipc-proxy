<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { GetModels, GetConfig, GetRequests, GetStatus, GetTokenStats, QuitApp, RefreshModels, StartProxy, StopProxy } from '../../wailsjs/go/main/App.js'
import { ClipboardSetText } from '../../wailsjs/runtime'
import { modelIcon } from '../modelIcons'

const props = defineProps({
  shellStatus: {
    type: Object,
    default: () => ({ running: false, addr: '', models: 0 })
  }
})

const emit = defineEmits(['log', 'status', 'notice', 'open-settings', 'open-requests', 'open-models'])

const status = ref(props.shellStatus)
const models = ref([])
const requests = ref([])
const tokenStats = ref({ totalRequests: 0, successRequests: 0, inputTokens: 0, outputTokens: 0, totalTokens: 0 })
const health = ref(null)
const config = ref({})
const proxyLoading = ref(false)
const modelsLoading = ref(false)
const testing = ref(false)
const now = ref(Date.now())
let interval = null
let clockInterval = null

const endpoint = computed(() => (status.value.addr ? `http://${status.value.addr}` : '未启动'))
const isRunning = computed(() => Boolean(status.value.running))
const isRemoteBackend = computed(() => status.value.backend === 'remote' || config.value.Backend === 'remote' || health.value?.state?.transport === 'remote')
const transportLabel = computed(() => {
  if (isRemoteBackend.value) return 'Remote API'
  return health.value?.state?.transport || config.value.Transport || 'auto'
})
const sessionLabel = computed(() => health.value?.state?.session_mode || config.value.SessionMode || 'auto')
const runningDuration = computed(() => {
  if (!isRunning.value || !status.value.startedAt) return '未运行'
  const startedAt = new Date(status.value.startedAt).getTime()
  if (!Number.isFinite(startedAt)) return '运行中'
  const seconds = Math.max(0, Math.floor((now.value - startedAt) / 1000))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = seconds % 60
  return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(rest).padStart(2, '0')}`
})
const parsedDurations = computed(() => requests.value.map((request) => parseDurationMs(request.duration)).filter((value) => value > 0))
const healthStats = computed(() => {
  const values = parsedDurations.value
  if (values.length === 0) {
    return { avg: 0, p50: 0, p95: 0, max: 0 }
  }
  const sorted = [...values].sort((a, b) => a - b)
  const avg = Math.round(values.reduce((sum, value) => sum + value, 0) / values.length)
  return {
    avg,
    p50: percentile(sorted, 0.5),
    p95: percentile(sorted, 0.95),
    max: Math.round(sorted[sorted.length - 1])
  }
})
const chartBars = computed(() => {
  const values = parsedDurations.value.slice(0, 36).reverse()
  if (values.length === 0) return []
  const max = Math.max(...values)
  return values.map((value) => Math.max(12, Math.round((value / max) * 100)))
})
const displayRequests = computed(() => {
  if (requests.value.length > 0) return requests.value.slice(0, 7)
  return []
})
const displayModels = computed(() => {
  if (models.value.length > 0) {
    return models.value.map((model) => ({ ...model, online: true }))
  }
  return []
})
const successRate = computed(() => {
  const total = Number(tokenStats.value.totalRequests || 0)
  if (!total) return '0%'
  return `${Math.round((Number(tokenStats.value.successRequests || 0) / total) * 100)}%`
})

function parseDurationMs(duration) {
  const text = String(duration || '').trim()
  if (!text) return 0
  if (text.endsWith('ms')) return Math.round(Number.parseFloat(text))
  if (text.endsWith('s')) return Math.round(Number.parseFloat(text) * 1000)
  return Math.round(Number.parseFloat(text) || 0)
}

function percentile(sorted, p) {
  if (sorted.length === 0) return 0
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil(sorted.length * p) - 1))
  return Math.round(sorted[index])
}

function formatNumber(value) {
  const n = Number(value || 0)
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`
  if (n >= 10000) return `${Math.round(n / 1000)}K`
  return n.toLocaleString('zh-CN')
}

async function refresh() {
  try {
    const nextStatus = await GetStatus()
    status.value = nextStatus
    emit('status', nextStatus)
    requests.value = await GetRequests()
    tokenStats.value = await GetTokenStats()
    config.value = await GetConfig()
    if (nextStatus.running) {
      models.value = await GetModels()
    }
  } catch (e) {
    emit('log', 'error', '刷新仪表盘失败：' + (e.message || String(e)))
  }
}

async function refreshModels() {
  modelsLoading.value = true
  try {
    models.value = await RefreshModels()
    emit('log', 'info', `模型探测完成：${models.value.length} 个`)
    await refresh()
  } catch (e) {
    emit('log', 'error', '模型探测失败：' + (e.message || String(e)) + '。请确认 Lingma 插件已启动并登录；自动探测失败时可到设置页手动填写 WebSocket：ws://127.0.0.1:36510/，或 Windows Named Pipe：\\\\.\\pipe\\lingma-xxxx。')
  } finally {
    modelsLoading.value = false
  }
}

async function copyModelName(model) {
  if (!model?.id) return
  try {
    await ClipboardSetText(model.id)
    emit('notice', `已复制模型 ID：${model.id}`)
  } catch (e) {
    try {
      await navigator.clipboard?.writeText(model.id)
      emit('notice', `已复制模型 ID：${model.id}`)
    } catch (fallbackError) {
      emit('log', 'error', '模型 ID 复制失败：' + (fallbackError.message || String(fallbackError)))
    }
  }
}

async function toggleProxy() {
  proxyLoading.value = true
  try {
    if (isRunning.value) {
      await StopProxy()
      emit('log', 'info', '代理已停止')
    } else {
      await StartProxy()
      emit('log', 'info', '代理已启动')
    }
    await refresh()
  } catch (e) {
    emit('log', 'error', '代理切换失败：' + (e.message || String(e)))
  } finally {
    proxyLoading.value = false
  }
}

async function restartProxy() {
  if (!isRunning.value) return
  proxyLoading.value = true
  try {
    await StopProxy()
    await StartProxy()
    emit('log', 'info', '代理已重启')
    await refresh()
  } catch (e) {
    emit('log', 'error', '代理重启失败：' + (e.message || String(e)))
  } finally {
    proxyLoading.value = false
  }
}

async function testConnection() {
  if (!isRunning.value || !status.value.addr) {
    emit('log', 'warn', '代理未运行，无法测试连接')
    return
  }
  testing.value = true
  try {
    const resp = await fetch(`${endpoint.value}/health`)
    const data = await resp.json()
    health.value = data
    emit('log', data.ok ? 'info' : 'warn', data.ok ? '健康检查通过' : '健康检查返回异常')
  } catch (e) {
    health.value = { ok: false, error: e.message || String(e) }
    emit('log', 'error', '健康检查失败：' + (e.message || String(e)))
  } finally {
    testing.value = false
  }
}

async function quitApp() {
  if (confirm('确定退出应用？代理服务会一起停止。')) {
    await QuitApp()
  }
}

function statusClass(code) {
  if (code >= 200 && code < 300) return 'ok'
  if (code >= 400) return 'err'
  return 'warn'
}

onMounted(() => {
  refresh()
  interval = setInterval(refresh, 2500)
  clockInterval = setInterval(() => {
    now.value = Date.now()
  }, 1000)
})

onUnmounted(() => {
  clearInterval(interval)
  clearInterval(clockInterval)
})
</script>

<template>
  <div class="page">
    <section class="glass-panel status-strip">
      <div class="strip-cell">
        <span class="strip-dot" :class="{ stopped: !isRunning }"></span>
        <div>
          <strong>{{ isRunning ? 'Proxy Running' : 'Proxy Stopped' }}</strong>
          <span>{{ isRunning ? `运行 ${runningDuration}` : runningDuration }}</span>
        </div>
      </div>
      <div class="strip-cell">
        <label>Endpoint</label>
        <a href="#" @click.prevent>{{ endpoint }}</a>
      </div>
      <div class="strip-cell">
        <label>Transport</label>
        <strong>{{ transportLabel }}</strong>
      </div>
      <div class="strip-cell">
        <label>Session</label>
        <strong>{{ sessionLabel }}</strong>
      </div>
      <div class="strip-actions">
        <button :class="{ active: !isRunning }" type="button" :disabled="proxyLoading || isRunning" @click="toggleProxy">启动</button>
        <button :class="{ active: isRunning }" type="button" :disabled="proxyLoading || !isRunning" @click="toggleProxy">停止</button>
        <button type="button" :disabled="proxyLoading || !isRunning" @click="restartProxy">重启</button>
      </div>
    </section>

    <section class="dashboard-grid">
      <div class="glass-panel area-health">
        <div class="panel-header">
          <div>
            <h2>Health <span class="muted">(Last 60s)</span></h2>
            <p>Latency (ms)</p>
          </div>
          <span class="status-chip ok">Healthy</span>
        </div>
        <div class="activity-chart" aria-label="延迟趋势图">
          <span v-for="(height, index) in chartBars" :key="index" class="bar" :style="{ height: `${height}%`, opacity: 0.55 + index / 45 }"></span>
          <span v-if="chartBars.length === 0" class="chart-empty">暂无请求</span>
        </div>
        <div class="health-stats">
          <div><strong>{{ healthStats.avg }}</strong><span>Avg (ms)</span></div>
          <div><strong>{{ healthStats.p50 }}</strong><span>P50 (ms)</span></div>
          <div><strong>{{ healthStats.p95 }}</strong><span>P95 (ms)</span></div>
          <div><strong style="color: #d97706">{{ healthStats.max }}</strong><span>Max (ms)</span></div>
        </div>
      </div>

      <div class="glass-panel area-models model-card">
        <div class="panel-header">
          <div>
            <h2>Models</h2>
          </div>
          <button class="btn-sm-outline" type="button" :disabled="modelsLoading || !isRunning" @click="refreshModels">
            {{ modelsLoading ? '探测中...' : '探测模型' }}
          </button>
        </div>
        <div class="model-card-list hidden-scrollbar">
          <button v-for="model in displayModels" :key="model.id" class="model-row model-choice" type="button" :title="`复制模型 ID：${model.id}`" @click="copyModelName(model)">
            <span class="model-brand-icon" :style="{ '--model-icon': `url(${modelIcon(model).src})`, '--model-icon-color': modelIcon(model).color }" aria-hidden="true"></span>
            <div>
              <div class="model-name">{{ model.name || model.id }}</div>
            </div>
            <span class="status-chip" :class="model.online ? 'ok' : 'warn'">{{ model.online ? 'Online' : 'Offline' }}</span>
          </button>
        </div>
        <div v-if="displayModels.length === 0" class="empty-state compact">暂无模型，启动代理后点击探测模型。</div>
      </div>

      <div class="glass-panel area-config">
        <div class="panel-header compact-header">
          <div>
            <h2>Configuration</h2>
            <p>首页只展示关键配置，完整项在设置页查看。</p>
          </div>
          <span class="status-chip ok">Valid</span>
        </div>
        <div class="config-summary">
          <div class="config-summary-item">
            <label>监听地址</label>
            <strong>{{ config.Host || '127.0.0.1' }}:{{ config.Port || 8095 }}</strong>
          </div>
          <div class="config-summary-item">
            <label>传输方式</label>
            <strong>{{ transportLabel }}</strong>
          </div>
          <div class="config-summary-item">
            <label>会话策略</label>
            <strong>{{ config.SessionMode || 'Reuse' }}</strong>
          </div>
          <div class="config-summary-item">
            <label>超时</label>
            <strong>{{ config.Timeout || 120 }} 秒</strong>
          </div>
          <div class="config-summary-item span-2">
            <label>工作目录</label>
            <strong :title="config.Cwd || '未配置'">{{ config.Cwd || '未配置' }}</strong>
          </div>
          <div v-if="config.CurrentFilePath" class="config-summary-item span-2">
            <label>当前文件</label>
            <strong :title="config.CurrentFilePath">{{ config.CurrentFilePath }}</strong>
          </div>
        </div>
      </div>

      <div class="glass-panel area-usage">
        <div class="panel-header">
          <div>
            <h2>Token 统计</h2>
            <p>按代理返回的 usage 累计，流式缺失字段时只统计可获得部分。</p>
          </div>
          <span class="status-chip ok">Persisted</span>
        </div>
        <div class="usage-grid">
          <div>
            <label>总 Token</label>
            <strong>{{ formatNumber(tokenStats.totalTokens) }}</strong>
          </div>
          <div>
            <label>输入</label>
            <strong>{{ formatNumber(tokenStats.inputTokens) }}</strong>
          </div>
          <div>
            <label>输出</label>
            <strong>{{ formatNumber(tokenStats.outputTokens) }}</strong>
          </div>
          <div>
            <label>成功率</label>
            <strong>{{ successRate }}</strong>
          </div>
        </div>
        <div class="usage-foot">
          <span>累计请求 {{ formatNumber(tokenStats.totalRequests) }} 次</span>
          <span v-if="tokenStats.lastModel">最近模型 {{ tokenStats.lastModel }}</span>
        </div>
      </div>

      <div class="table-panel area-requests">
        <div class="table-toolbar">
          <div class="panel-header toolbar-header">
            <h2>Recent Requests</h2>
          </div>
          <button type="button" class="btn-sm-outline" @click="emit('open-requests')">
            查看全部请求 <i class="bi bi-chevron-right"></i>
          </button>
        </div>
        <div v-if="displayRequests.length > 0" class="table-scroll hidden-scrollbar">
          <table class="data-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Method</th>
                <th>Path</th>
                <th>Model</th>
                <th>Status</th>
                <th>Duration</th>
                <th>Size</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(request, index) in displayRequests" :key="index">
                <td>{{ request.time }}</td>
                <td>{{ request.method }}</td>
                <td>{{ request.path }}</td>
                <td>{{ request.model || '-' }}</td>
                <td><span class="status-chip" :class="statusClass(request.statusCode)">{{ request.statusCode }}</span></td>
                <td>{{ request.duration }}</td>
                <td>{{ request.size || '-' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="empty-state compact">暂无请求记录。连接客户端后会显示真实调用。</div>
      </div>
    </section>
  </div>
</template>
