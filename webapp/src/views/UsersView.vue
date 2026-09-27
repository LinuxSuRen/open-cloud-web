<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api, arr, fmtDur, fmtTime } from '../api'

const users = ref([])
const form = reactive({ username: '', displayName: '', password: '', role: 'user', maxDurationSec: null })

async function load() {
  users.value = arr(await api('GET', '/api/v1/users'))
}
onMounted(load)

async function create() {
  try {
    await api('POST', '/api/v1/users', {
      username: form.username.trim(),
      displayName: form.displayName.trim(),
      password: form.password,
      role: form.role,
      maxDurationSec: Number(form.maxDurationSec) || 0,
    })
    Object.assign(form, { username: '', displayName: '', password: '' })
    await load()
  } catch (e) {
    alert('创建失败：' + e.message)
  }
}

async function toggle(u) {
  try {
    await api('PATCH', `/api/v1/users/${u.id}`, { status: u.status === 'active' ? 'disabled' : 'active' })
    await load()
  } catch (e) { alert(e.message) }
}

async function setQuota(u) {
  const v = prompt('单次最长使用时长（秒，0=用全局默认）', u.maxDurationSec || 0)
  if (v === null) return
  try {
    await api('PATCH', `/api/v1/users/${u.id}`, { maxDurationSec: parseInt(v) || 0 })
    await load()
  } catch (e) { alert(e.message) }
}

async function remove(u) {
  if (!confirm(`确认删除用户 ${u.username}？其名下实例记录将一并删除。`)) return
  try {
    await api('DELETE', `/api/v1/users/${u.id}`)
    await load()
  } catch (e) { alert(e.message) }
}
</script>

<template>
  <section>
    <div class="card">
      <h2>创建用户</h2>
      <div class="row">
        <label>用户名 <input v-model="form.username" /></label>
        <label>显示名 <input v-model="form.displayName" /></label>
        <label>密码 <input v-model="form.password" type="password" placeholder="≥8 位" /></label>
        <label>角色
          <select v-model="form.role">
            <option value="user">普通用户</option>
            <option value="admin">管理员</option>
          </select>
        </label>
        <label>单次最长时长（秒，0=默认）
          <input v-model.number="form.maxDurationSec" type="number" min="0" style="width:150px" />
        </label>
        <button class="btn" @click="create">创建</button>
      </div>
    </div>
    <div class="card">
      <h2>用户列表</h2>
      <table>
        <thead>
          <tr><th>用户名</th><th>角色</th><th>状态</th><th>单次最长时长</th><th>来源</th><th>操作</th></tr>
        </thead>
        <tbody>
          <tr v-if="!users.length"><td colspan="6" class="empty">暂无用户</td></tr>
          <tr v-for="u in users" :key="u.id">
            <td><b>{{ u.username }}</b><br /><span style="color:var(--sub)">{{ u.displayName }}</span></td>
            <td>{{ u.role === 'admin' ? '管理员' : '用户' }}</td>
            <td>{{ u.status === 'active' ? '✅ 正常' : '⛔ 禁用' }}</td>
            <td>{{ u.maxDurationSec > 0 ? fmtDur(u.maxDurationSec) : '全局默认' }}</td>
            <td>{{ u.provider }}</td>
            <td style="white-space:nowrap">
              <button class="ghost mini" @click="toggle(u)">{{ u.status === 'active' ? '禁用' : '启用' }}</button>
              <button class="ghost mini" @click="setQuota(u)">配额</button>
              <button class="ghost mini danger" @click="remove(u)">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
