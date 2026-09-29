<script setup>
import { ref, computed } from 'vue'
import { store, api } from './api'
import LoginView from './views/LoginView.vue'
import InstancesView from './views/InstancesView.vue'
import CloudAccountsView from './views/CloudAccountsView.vue'
import SecurityGroupsView from './views/SecurityGroupsView.vue'
import UsersView from './views/UsersView.vue'
import PatsView from './views/PatsView.vue'
import AuditView from './views/AuditView.vue'
import SettingsView from './views/SettingsView.vue'

const view = ref('instances')
const isAdmin = computed(() => store.me?.role === 'admin')
const tabs = computed(() => [
  { id: 'instances', label: '云主机' },
  { id: 'accounts', label: '云提供商' },
  { id: 'sgs', label: '安全组' },
  ...(isAdmin.value ? [{ id: 'users', label: '用户管理' }] : []),
  { id: 'pats', label: '访问令牌' },
  ...(isAdmin.value ? [{ id: 'audit', label: '审计日志' }] : []),
  ...(isAdmin.value ? [{ id: 'settings', label: '系统设置' }] : []),
])

function logout() {
  localStorage.removeItem('ocw_token')
  location.reload()
}
async function goFeishu() {
  const d = await api('POST', '/api/v1/auth/feishu/login')
  const sep = d.authorize_url.includes('?') ? '&' : '?'
  location.href = d.authorize_url + sep + 'ua=web'
}
</script>

<template>
  <LoginView v-if="!store.me" @logined="store.me = $event" @feishu="goFeishu" />
  <div v-else id="app-root">
    <header>
      <span class="logo">☁️ OpenCloudLab</span>
      <span class="sp"></span>
      <span class="who">{{ store.me.displayName || store.me.username }} · {{ store.me.role }}</span>
      <button class="ghost" @click="logout">退出</button>
    </header>
    <main>
      <nav>
        <button v-for="t in tabs" :key="t.id" :class="{ on: view === t.id }" @click="view = t.id">
          {{ t.label }}
        </button>
      </nav>
      <InstancesView v-show="view === 'instances'" />
      <CloudAccountsView v-show="view === 'accounts'" />
      <SecurityGroupsView v-show="view === 'sgs'" />
      <UsersView v-if="isAdmin" v-show="view === 'users'" />
      <PatsView v-show="view === 'pats'" />
      <AuditView v-if="isAdmin" v-show="view === 'audit'" />
      <SettingsView v-if="isAdmin" v-show="view === 'settings'" />
    </main>
  </div>
</template>

<style>
:root {
  --bg: #f5f7fa; --card: #fff; --line: #e4e8ef; --txt: #1f2937;
  --sub: #6b7280; --pri: #2563eb; --ok: #059669; --warn: #d97706; --err: #dc2626;
}
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  font: 14px/1.6 -apple-system, 'PingFang SC', 'Microsoft YaHei', sans-serif;
  background: var(--bg); color: var(--txt);
}
a { color: var(--pri); text-decoration: none; }
header {
  background: var(--card); border-bottom: 1px solid var(--line);
  padding: 0 24px; height: 56px; display: flex; align-items: center; gap: 16px;
}
header .logo { font-weight: 700; font-size: 16px; }
header .sp { flex: 1; }
header .who { color: var(--sub); font-size: 13px; }
nav { display: flex; gap: 4px; margin-top: 16px; }
nav button {
  border: none; background: none; padding: 8px 14px; border-radius: 8px;
  cursor: pointer; font-size: 14px; color: var(--sub);
}
nav button.on {
  background: var(--card); color: var(--pri); font-weight: 600;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
}
main { max-width: 1100px; margin: 0 auto; padding: 16px 24px 60px; }
.card {
  background: var(--card); border: 1px solid var(--line);
  border-radius: 12px; padding: 20px; margin-top: 16px;
}
.card h2 { font-size: 15px; margin-bottom: 14px; }
table { width: 100%; border-collapse: collapse; font-size: 13px; }
th, td { padding: 9px 10px; border-bottom: 1px solid var(--line); text-align: left; vertical-align: top; }
th { color: var(--sub); font-weight: 500; white-space: nowrap; }
tr:last-child td { border-bottom: none; }
input, select { font: inherit; border: 1px solid var(--line); border-radius: 8px; padding: 7px 10px; background: #fff; min-width: 0; }
button.btn { background: var(--pri); color: #fff; border: none; border-radius: 8px; padding: 7px 16px; cursor: pointer; }
button.btn:disabled { opacity: 0.5; cursor: not-allowed; }
button.ghost { background: #fff; color: var(--txt); border: 1px solid var(--line); border-radius: 8px; padding: 6px 12px; cursor: pointer; }
button.danger { color: var(--err); }
button.mini { padding: 3px 10px; font-size: 12px; border-radius: 6px; }
.row { display: flex; gap: 12px; flex-wrap: wrap; align-items: flex-end; }
.row label { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--sub); }
.tag { display: inline-block; padding: 1px 10px; border-radius: 999px; font-size: 12px; white-space: nowrap; }
.t-creating { background: #eff6ff; color: #2563eb; }
.t-running { background: #ecfdf5; color: var(--ok); }
.t-destroying { background: #fffbeb; color: var(--warn); }
.t-destroyed { background: #f3f4f6; color: var(--sub); }
.t-failed { background: #fef2f2; color: var(--err); }
.mono { font-family: ui-monospace, Menlo, monospace; font-size: 12px; }
.empty { color: var(--sub); text-align: center; padding: 26px 0; }
.hint { color: var(--sub); font-size: 12px; margin-top: 10px; }
</style>
