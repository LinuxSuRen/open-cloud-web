<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../api'

const form = reactive({ proxyURL: '' })
const saved = ref(false)
const busy = ref(false)

onMounted(async () => {
  const d = await api('GET', '/api/v1/admin/settings')
  form.proxyURL = d.proxyURL || ''
})

async function save() {
  busy.value = true
  try {
    const d = await api('PUT', '/api/v1/admin/settings', { proxyURL: form.proxyURL.trim() })
    form.proxyURL = d.proxyURL
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
      <div class="row">
        <label>
          Provider 下载代理（tofu init 走的 GitHub 下载）
          <input v-model="form.proxyURL" placeholder="http://127.0.0.1:7890（留空不使用）" style="width:320px" />
        </label>
        <button class="btn" :disabled="busy" @click="save">{{ busy ? '保存中…' : '保存' }}</button>
        <span v-if="saved" style="color:var(--ok)">已保存 ✓</span>
      </div>
      <p class="hint">
        网络不佳时 provider 二进制从 GitHub Releases 下载容易超时，配置代理后仅下载流量走代理；
        registry.opentofu.org 固定直连（经代理通常更慢）。保存后立即生效，无需重启。
        已下载的 provider 会缓存在 data/plugin-cache，之后创建不再重复下载。
      </p>
    </div>
  </section>
</template>
