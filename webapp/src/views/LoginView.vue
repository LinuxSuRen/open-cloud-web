<script setup>
import { ref } from 'vue'
import { api } from '../api'

const emit = defineEmits(['logined', 'feishu'])
const username = ref('')
const password = ref('')
const busy = ref(false)
const err = ref('')

async function login() {
  busy.value = true
  err.value = ''
  try {
    const d = await api('POST', '/api/v1/auth/login', {
      username: username.value.trim(),
      password: password.value,
    })
    localStorage.setItem('ocw_token', d.token)
    emit('logined', d.user)
  } catch (e) {
    err.value = '登录失败：' + e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div id="login">
    <h1>OpenCloudLab</h1>
    <p>云主机快速创建平台 · 功能测试专用</p>
    <form @submit.prevent="login">
      <label>用户名 <input v-model="username" autocomplete="username" placeholder="admin" /></label>
      <label>密码 <input v-model="password" type="password" autocomplete="current-password" /></label>
      <button class="btn" type="submit" :disabled="busy" style="width:100%">
        {{ busy ? '登录中…' : '登录' }}
      </button>
    </form>
    <button class="ghost" style="width:100%;margin-top:8px" @click="emit('feishu')">飞书登录</button>
    <p v-if="err" style="color:var(--err);margin-top:10px">{{ err }}</p>
  </div>
</template>

<style scoped>
#login {
  max-width: 380px; margin: 12vh auto; background: var(--card);
  border: 1px solid var(--line); border-radius: 12px; padding: 32px;
}
#login h1 { font-size: 20px; margin-bottom: 4px; }
#login > p { color: var(--sub); margin-bottom: 20px; }
form { display: flex; flex-direction: column; gap: 12px; }
label { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--sub); }
</style>
