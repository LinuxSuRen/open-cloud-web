<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api, arr, fmtTime } from '../api'

const pats = ref([])
const newToken = ref('')
const form = reactive({ name: '', days: 30 })

async function load() {
  pats.value = arr(await api('GET', '/api/v1/auth/pats'))
}
onMounted(load)

async function create() {
  try {
    const d = await api('POST', '/api/v1/auth/pats', {
      name: form.name.trim() || 'cli',
      expiresInDays: Number(form.days) || 0,
    })
    newToken.value = d.token
    await load()
  } catch (e) {
    alert('生成失败：' + e.message)
  }
}

async function remove(p) {
  if (!confirm('确认删除该令牌？使用它的 CLI 将立即失效。')) return
  try {
    await api('DELETE', `/api/v1/auth/pats/${p.id}`)
    await load()
  } catch (e) { alert(e.message) }
}
</script>

<template>
  <section>
    <div class="card">
      <h2>创建访问令牌（CLI 用）</h2>
      <div class="row">
        <label>名称 <input v-model="form.name" placeholder="my-cli" /></label>
        <label>有效期（天，0=不过期）
          <input v-model.number="form.days" type="number" min="0" style="width:120px" />
        </label>
        <button class="btn" @click="create">生成</button>
      </div>
      <div v-if="newToken" style="margin-top:12px">
        <p style="color:var(--warn);font-size:12px">明文仅此一次显示，请立即保存：</p>
        <div class="mono" style="background:#f9fafb;padding:10px;border-radius:8px;word-break:break-all">
          {{ newToken }}
        </div>
      </div>
    </div>
    <div class="card">
      <h2>我的令牌</h2>
      <table>
        <thead><tr><th>名称</th><th>创建时间</th><th>过期时间</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-if="!pats.length"><td colspan="4" class="empty">暂无令牌</td></tr>
          <tr v-for="p in pats" :key="p.id">
            <td>{{ p.name }}</td>
            <td>{{ fmtTime(p.createdAt) }}</td>
            <td>{{ p.expiresAt && p.expiresAt > '0001' ? fmtTime(p.expiresAt) : '永不过期' }}</td>
            <td><button class="ghost mini danger" @click="remove(p)">删除</button></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
