<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api, arr, fmtTime } from '../api'

const accounts = ref([])
const form = reactive({ name: '', provider: 'alicloud', accessKey: '', secretKey: '', sessionToken: '' })
const busy = ref(false)
const testing = ref(0) // 正在测试的账号 ID
const editing = ref(0) // 正在编辑的账号 ID
const editForm = reactive({ name: '', accessKey: '', secretKey: '', sessionToken: '' })
const results = reactive({}) // id -> { status, message }

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

function startEdit(a) {
  editing.value = a.id
  editForm.name = a.name
  editForm.accessKey = a.accessKey
  editForm.secretKey = ''
  editForm.sessionToken = a.hasSessionToken ? '（已设置）' : ''
}

async function saveEdit(a) {
  const body = { name: editForm.name.trim() || a.name, accessKey: editForm.accessKey || a.accessKey }
  if (editForm.secretKey && !editForm.secretKey.startsWith('（')) body.secretKey = editForm.secretKey
  if (editForm.sessionToken === '') body.sessionToken = ''
  else if (!editForm.sessionToken.startsWith('（')) body.sessionToken = editForm.sessionToken
  try {
    await api('PATCH', `/api/v1/cloud-accounts/${a.id}`, body)
    editing.value = 0
    await load()
  } catch (e) { alert('保存失败：' + e.message) }
}

async function test(a) {
  testing.value = a.id
  delete results[a.id]
  try {
    const d = await api('POST', `/api/v1/cloud-accounts/${a.id}/test`)
    results[a.id] = d
  } catch (e) {
    results[a.id] = { status: 'error', message: e.message }
  } finally {
    testing.value = 0
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
        <label>SessionToken（STS 临时密钥必填，长期密钥留空） <input v-model="form.sessionToken" type="password" style="width:220px" /></label>
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
            <td>
              <b>{{ a.name }}</b>
              <div v-if="editing === a.id" class="row" style="margin-top:8px;flex-direction:column;align-items:stretch;min-width:260px">
                <label>名称 <input v-model="editForm.name" /></label>
                <label>AccessKey ID <input v-model="editForm.accessKey" /></label>
                <label>AccessKey Secret（留空则不修改） <input v-model="editForm.secretKey" type="password" /></label>
                <label>SessionToken（STS 临时密钥用；留空=清除） <input v-model="editForm.sessionToken" type="password" /></label>
                <div class="row">
                  <button class="btn mini" @click="saveEdit(a)">保存</button>
                  <button class="ghost mini" @click="editing = 0">取消</button>
                </div>
              </div>
            </td>
            <td>{{ PROV[a.provider] || a.provider }}</td>
            <td class="mono">{{ a.accessKey }}</td>
            <td>{{ fmtTime(a.createdAt) }}</td>
            <td style="white-space:nowrap">
              <button class="ghost mini" @click="startEdit(a)">编辑</button>
              <button class="ghost mini" :disabled="testing === a.id" @click="test(a)">
                {{ testing === a.id ? '测试中…' : '测试' }}
              </button>
              <button class="ghost mini danger" @click="remove(a)">删除</button>
              <div
                v-if="results[a.id]"
                :style="{
                  marginTop: '6px',
                  fontSize: '12px',
                  whiteSpace: 'normal',
                  maxWidth: '360px',
                  color: results[a.id].status === 'ok' ? 'var(--ok)' : 'var(--err)',
                }"
              >
                {{ results[a.id].status === 'ok' ? '✅ ' : '❌ ' }}{{ results[a.id].message }}
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
