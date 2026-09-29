<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api, arr, fmtTime } from '../api'

const form = reactive({ tofuProxyURL: '', cloudProxyURL: '' })
const providers = ref([])
const loading = ref(false)
const preloading = ref('')

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
  } finally {
    loading.value = false
  }
}

async function preload(provider) {
  preloading.value = provider
  try {
    const d = await api('POST', '/api/v1/admin/providers/preload', { provider })
    providers.value = arr(d.providers)
  } catch (e) {
    alert('下载失败：' + e.message)
  } finally {
    preloading.value = ''
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
    </div>
  </section>
</template>
