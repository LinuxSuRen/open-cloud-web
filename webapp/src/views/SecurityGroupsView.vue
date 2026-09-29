<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api, arr, fmtTime } from '../api'

const groups = ref([])
const commonPorts = ref([])
const commonUdpPorts = ref([])
const busy = ref(false)
const form = reactive({ name: '', ports: [], custom: '', udpPorts: [], udpCustom: '' })

async function load() {
  const d = await api('GET', '/api/v1/security-groups')
  groups.value = arr(d.groups)
  commonPorts.value = arr(d.commonPorts)
  commonUdpPorts.value = arr(d.commonUdpPorts)
}

onMounted(load)

function toggleUdpPort(p) {
  const i = form.udpPorts.indexOf(p)
  if (i >= 0) form.udpPorts.splice(i, 1)
  else form.udpPorts.push(p)
}

function togglePort(p) {
  const i = form.ports.indexOf(p)
  if (i >= 0) form.ports.splice(i, 1)
  else form.ports.push(p)
}

function parseCustom() {
  return form.custom
    .split(/[,，\s]+/)
    .map((s) => parseInt(s, 10))
.filter((n) => n >= 1 && n <= 65535)
}

function parseUdpCustom() {
  return form.udpCustom
    .split(/[,\s]+/)
    .map((x) => parseInt(x, 10))
    .filter((n) => n >= 1 && n <= 65535)
}

async function create() {
  const ports = [...new Set([...form.ports, ...parseCustom()])]
  const udpPorts = [...new Set([...form.udpPorts, ...parseUdpCustom()])]
  if (!form.name.trim() || (ports.length === 0 && udpPorts.length === 0)) {
    alert('请填写名称并至少选择一个端口（TCP 或 UDP）')
    return
  }
  busy.value = true
  try {
    await api('POST', '/api/v1/security-groups', { name: form.name.trim(), ports, udpPorts })
    form.name = ''
    form.ports = []
    form.custom = ''
    form.udpPorts = []
    form.udpCustom = ''
    await load()
  } catch (e) {
    alert('创建失败：' + e.message)
  } finally {
    busy.value = false
  }
}

async function remove(g) {
  if (!confirm(`确认删除安全组「${g.name}」？已有实例不受影响（云上规则已创建）。`)) return
  try {
    await api('DELETE', `/api/v1/security-groups/${g.id}`)
    await load()
  } catch (e) { alert(e.message) }
}

function portNames(rawPorts, rawUdpPorts) {
  const ports = rawPorts || []
  const udpPorts = rawUdpPorts || []
  const m = Object.fromEntries([...commonPorts.value, ...commonUdpPorts.value].map((p) => [p.port, p.name]))
  const parts = []
  if (ports.length) parts.push('TCP: ' + ports.map((p) => `${p}${m[p] ? '(' + m[p] + ')' : ''}`).join('、'))
  if (udpPorts.length) parts.push('UDP: ' + udpPorts.map((p) => `${p}${m[p] ? '(' + m[p] + ')' : ''}`).join('、'))
  return parts.join('　')
}
</script>

<template>
  <section>
    <div class="card">
      <h2>创建安全组（端口集合）</h2>
      <div class="row" style="flex-direction:column;align-items:stretch;max-width:640px">
        <label>名称 <input v-model="form.name" placeholder="如：物联网测试" style="width:260px" /></label>
        <div>
          <span style="font-size:12px;color:var(--sub)">常见服务端口：</span>
          <span
            v-for="p in commonPorts"
            :key="p.port"
            class="port-chip"
            :class="{ on: form.ports.includes(p.port) }"
            @click="togglePort(p.port)"
          >{{ p.port }} {{ p.name }}</span>
        </div>
        <div>
          <span style="font-size:12px;color:var(--sub)">常见 UDP 端口（流媒体/监控常用）：</span>
          <span
            v-for="p in commonUdpPorts"
            :key="'u'+p.port"
            class="port-chip"
            :class="{ on: form.udpPorts.includes(p.port) }"
            @click="toggleUdpPort(p.port)"
          >{{ p.port }} {{ p.name }}</span>
        </div>
        <label>自定义 TCP 端口（逗号/空格分隔，可多选叠加）
          <input v-model="form.custom" placeholder="如：9000, 9092" style="width:260px" />
        </label>
        <label>自定义 UDP 端口
          <input v-model="form.udpCustom" placeholder="如：8189, 8890" style="width:260px" />
        </label>
        <div class="row">
          <button class="btn" :disabled="busy" @click="create">{{ busy ? '创建中…' : '创建' }}</button>
        </div>
      </div>
    </div>
    <div class="card">
      <h2>安全组列表（预置 + 我的）</h2>
      <table>
        <thead><tr><th>名称</th><th>端口</th><th>类型</th><th>创建时间</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-if="!groups.length"><td colspan="5" class="empty">暂无安全组</td></tr>
          <tr v-for="g in groups" :key="g.id">
            <td><b>{{ g.name }}</b><br /><span v-if="g.remark" style="color:var(--sub);font-size:12px">{{ g.remark }}</span></td>
            <td class="mono" style="white-space:normal;max-width:480px">{{ portNames(g.ports, g.udpPorts) }}</td>
            <td>{{ g.userID === 0 ? '预置' : '自建' }}</td>
            <td>{{ fmtTime(g.createdAt) }}</td>
            <td><button class="ghost mini danger" @click="remove(g)">删除</button></td>
          </tr>
        </tbody>
      </table>
      <p class="hint">创建云主机时选择安全组，平台会在云上按端口集合创建对应的安全组规则（TCP 入方向，0.0.0.0/0）。</p>
    </div>
  </section>
</template>

<style scoped>
.port-chip {
  display: inline-block;
  margin: 3px 4px 3px 0;
  padding: 2px 10px;
  border: 1px solid var(--line);
  border-radius: 999px;
  font-size: 12px;
  cursor: pointer;
  user-select: none;
}
.port-chip.on {
  background: #eff6ff;
  color: var(--pri);
  border-color: var(--pri);
}
</style>
