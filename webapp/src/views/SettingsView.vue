<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api, arr, fmtTime } from '../api'

const form = reactive({ tofuProxyURL: '', cloudProxyURL: '' })
const providers = ref([])
const jobs = ref({})
const loading = ref(false)
const preloading = ref('')
let pollTimer = null

function fmtSize(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(2) + ' GB'
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB'
  return (n / (1 << 10)).toFixed(1) + ' KB'
}

async function loadProviders() {
  loading.value = true
  try {
    const d = await api('GET', '/api/v1/admin/providers')
    providers.value = arr(d.providers)
    jobs.value = d.jobs || {}
    schedulePoll()
  } finally {
    loading.value = false
  }
}

// 有下载任务在跑时每 2s 轮询日志
function schedulePoll() {
  const running = Object.values(jobs.value).some((j) => j.status === 'running')
  if (running && !pollTimer) {
    pollTimer = setInterval(async () => {
      try {
        const d = await api('GET', '/api/v1/admin/providers')
        providers.value = arr(d.providers)
        jobs.value = d.jobs || {}
        if (!Object.values(jobs.value).some((j) => j.status === 'running')) {
          clearInterval(pollTimer)
          pollTimer = null
        }
      } catch { /* 下轮重试 */ }
    }, 2000)
  } else if (!running && pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function preload(provider) {
  try {
    await api('POST', '/api/v1/admin/providers/preload', { provider })
    loadProviders() // 立即开始轮询日志
  } catch (e) {
    alert('启动下载失败：' + e.message)
  }
}
const saved = ref(false)
const busy = ref(false)

onMounted(async () => {
  loadProviders()
  const d = await api('GET', '/api/v1/admin/settings')
  form.tofuProxyURL = d.tofuProxyURL || ''
  form.cloudProxyURL = d.cloudProxyURL || ''
})

async function save() {
  busy.value = true
  try {
    const d = await api('PUT', '/api/v1/admin/settings', {
      tofuProxyURL: form.tofuProxyURL.trim(),
      cloudProxyURL: form.cloudProxyURL.trim(),
    })
    form.tofuProxyURL = d.tofuProxyURL
    form.cloudProxyURL = d.cloudProxyURL
    saved.value = true
    setTimeout(() => (saved.value = false), 2000)
  } catch (e) {
    alert('保存失败：' + e.message)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section>
    <div class="card">
      <h2>系统设置</h2>
      <div class="row" style="flex-direction:column;align-items:stretch;max-width:560px">
        <label>
          Provider 下载代理（tofu init 下载 GitHub Releases 的 provider 二进制）
          <input v-model="form.tofuProxyURL" placeholder="http://127.0.0.1:7890（留空=直连）" style="width:100%" />
        </label>
        <label>
          云厂商 API 代理（阿里云/火山引擎的镜像查询、实例创建等 OpenAPI 调用）
          <input v-model="form.cloudProxyURL" placeholder="http://127.0.0.1:7890（留空=直连）" style="width:100%" />
        </label>
        <div class="row">
          <button class="btn" :disabled="busy" @click="save">{{ busy ? '保存中…' : '保存' }}</button>
          <span v-if="saved" style="color:var(--ok)">已保存 ✓</span>
        </div>
      </div>
      <p class="hint">
        两条链路独立配置，按需只开其一。保存后立即生效，无需重启。
        provider 下载固定对 registry.opentofu.org 直连（经代理通常更慢）；
        已下载的 provider 缓存在 data/plugin-cache，后续创建不再重复下载。
      </p>
    </div>

    <div class="card">
      <h2>
        Provider 插件缓存
        <button class="ghost mini" style="margin-left:8px" :disabled="loading" @click="loadProviders">
          {{ loading ? '加载中…' : '刷新' }}
        </button>
        <button class="ghost mini" :disabled="!!preloading" @click="preload('alicloud')">
          {{ preloading === 'alicloud' ? '下载中…' : '预下载 阿里云' }}
        </button>
        <button class="ghost mini" :disabled="!!preloading" @click="preload('volcengine')">
          {{ preloading === 'volcengine' ? '下载中…（包较大）' : '预下载 火山引擎' }}
        </button>
      </h2>
      <table>
        <thead><tr><th>Provider</th><th>版本</th><th>平台</th><th>大小</th><th>下载时间</th></tr></thead>
        <tbody>
          <tr v-if="!providers.length && !loading"><td colspan="5" class="empty">尚未下载任何 provider（可点击上方预下载，或等首次创建实例时自动下载）</td></tr>
          <tr v-for="p in providers" :key="p.namespace + p.name + p.version + p.platform">
            <td class="mono">{{ p.namespace }}/{{ p.name }}</td>
            <td>{{ p.version }}</td>
            <td>{{ p.platform }}</td>
            <td>{{ fmtSize(p.size) }}</td>
            <td>{{ fmtTime(p.modTime) }}</td>
          </tr>
        </tbody>
      </table>
      <p class="hint">预下载/创建时下载走「Provider 下载代理」设置；init 不需要云凭据。下载完成的 provider 全局共享，后续创建不再重复下载。</p>

      <div v-for="(job, name) in jobs" :key="name" style="margin-top:14px">
        <h3 style="font-size:13px;margin-bottom:6px">
          {{ { alicloud: '阿里云', volcengine: '火山引擎' }[name] || name }} 下载任务
          <span class="tag" :class="job.status === 'running' ? 't-creating' : job.status === 'success' ? 't-running' : 't-failed'">
            {{ { running: '下载中…', success: '成功', failed: '失败' }[job.status] || job.status }}
          </span>
          <span style="color:var(--sub);font-size:12px;margin-left:8px">
            {{ fmtTime(job.startedAt) }}{{ job.endedAt ? ' → ' + fmtTime(job.endedAt) : '' }}
          </span>
        </h3>
        <pre class="mono" style="background:#0f172a;color:#e2e8f0;padding:12px;border-radius:8px;max-height:220px;overflow:auto;white-space:pre-wrap">{{ job.log || '（等待输出…）' }}</pre>
        <p v-if="job.error" style="color:var(--err);font-size:12px;margin-top:4px">{{ job.error }}</p>
      </div>
    </div>
  </section>
</template>
