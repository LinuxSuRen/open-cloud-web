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
          HTTP 代理（云 API 调用与 provider 下载都走它）
          <input v-model="form.proxyURL" placeholder="http://127.0.0.1:7890（留空不使用）" style="width:320px" />
        </label>
        <button class="btn" :disabled="busy" @click="save">{{ busy ? '保存中…' : '保存' }}</button>
        <span v-if="saved" style="color:var(--ok)">已保存 ✓</span>
      </div>
      <p class="hint">
        作用于：① 云厂商 API 调用（查询镜像/规格/创建实例）；② tofu 的 provider 下载
        （GitHub Releases，registry.opentofu.org 固定直连避免变慢）。保存后立即生效，无需重启。
        已下载的 provider 缓存在 data/plugin-cache，后续创建不再重复下载。
      </p>
    </div>
  </section>
</template>
