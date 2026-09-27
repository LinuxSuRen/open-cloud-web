<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api, arr, fmtTime } from '../api'

const accounts = ref([])
const form = reactive({ name: '', provider: 'alicloud', accessKey: '', secretKey: '' })
const busy = ref(false)

async function load() {
  accounts.value = arr(await api('GET', '/api/v1/cloud-accounts'))
}
onMounted(load)

async function create() {
  busy.value = true
  try {
    await api('POST', '/api/v1/cloud-accounts', { ...form, name: form.name.trim() })
    Object.assign(form, { name: '', accessKey: '', secretKey: '' })
    await load()
  } catch (e) {
    alert('添加失败：' + e.message)
  } finally {
    busy.value = false
  }
}

async function remove(a) {
  if (!confirm(`确认删除云账号「${a.name}」？使用它创建的存量实例将无法自动销毁。`)) return
  try {
    await api('DELETE', `/api/v1/cloud-accounts/${a.id}`)
    await load()
  } catch (e) { alert(e.message) }
}

const PROV = { alicloud: '阿里云', volcengine: '火山引擎' }
</script>

<template>
  <section>
    <div class="card">
      <h2>添加云提供商账号</h2>
      <div class="row">
        <label>名称 <input v-model="form.name" placeholder="如：我的阿里云主账号" /></label>
        <label>云提供商
          <select v-model="form.provider">
            <option value="alicloud">阿里云</option>
            <option value="volcengine">火山引擎</option>
          </select>
        </label>
        <label>AccessKey ID <input v-model="form.accessKey" style="width:220px" /></label>
        <label>AccessKey Secret <input v-model="form.secretKey" type="password" style="width:220px" /></label>
        <button class="btn" :disabled="busy || !form.name || !form.accessKey || !form.secretKey" @click="create">
          {{ busy ? '添加中…' : '添加' }}
        </button>
      </div>
      <p class="hint">Secret 经 AES-256-GCM 加密存储，不会明文回显；创建实例、查询镜像均使用你自己的账号凭据。</p>
    </div>
    <div class="card">
      <h2>我的云账号</h2>
      <table>
        <thead><tr><th>名称</th><th>云提供商</th><th>AccessKey</th><th>添加时间</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-if="!accounts.length"><td colspan="5" class="empty">还没有云账号，先添加一个再创建云主机</td></tr>
          <tr v-for="a in accounts" :key="a.id">
            <td><b>{{ a.name }}</b></td>
            <td>{{ PROV[a.provider] || a.provider }}</td>
            <td class="mono">{{ a.accessKey }}</td>
            <td>{{ fmtTime(a.createdAt) }}</td>
            <td><button class="ghost mini danger" @click="remove(a)">删除</button></td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
