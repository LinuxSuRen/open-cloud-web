<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../api'

const form = reactive({ tofuProxyURL: '', cloudProxyURL: '' })
const saved = ref(false)
const busy = ref(false)

onMounted(async () => {
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
  </section>
</template>
