import { createApp, reactive } from 'vue'
import App from './App.vue'
import { api, store } from './api'

// OAuth 回调：/#token=<jwt>
if (location.hash.startsWith('#token=')) {
  localStorage.setItem('ocw_token', location.hash.slice(7))
  history.replaceState(null, '', '/')
}

async function boot() {
  const token = localStorage.getItem('ocw_token')
  if (token) {
    try {
      store.me = await api('GET', '/api/v1/me')
    } catch {
      localStorage.removeItem('ocw_token')
    }
  }
  createApp(App).mount('#app')
}
boot()
