import { reactive } from 'vue'

export const store = reactive({
  me: null,
  token: () => localStorage.getItem('ocw_token'),
})

export async function api(method, path, body) {
  const token = store.token()
  const r = await fetch(path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: 'Bearer ' + token } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (r.status === 401) {
    localStorage.removeItem('ocw_token')
    location.reload()
    throw new Error('未认证')
  }
  const data = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error(data.error || 'HTTP ' + r.status)
  // 列表端点返回裸数组（空时为 null）
  return data
}

export const arr = (d) => (Array.isArray(d) ? d : []) || []

export function fmtDur(sec) {
  if (sec <= 0) return '已到期'
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (h > 0) return h + ' 时' + (m > 0 ? ' ' + m + ' 分' : '')
  return m > 0 ? m + ' 分' : sec + ' 秒'
}

export function fmtTime(t) {
  if (!t) return '—'
  const d = new Date(t)
  return isNaN(d) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}

export const PROV_NAME = { alicloud: '阿里云', volcengine: '火山引擎', tencentcloud: '腾讯云', huaweicloud: '华为云' }
export const ST_NAME = { creating: '创建中', running: '运行中', destroying: '销毁中', destroyed: '已销毁', failed: '失败' }

// connectWS 建立 /api/v1/ws 推送连接（token 走查询参数）。
// 返回清理函数。onMessage 收到事件对象；断线自动重连，重连失败超过阈值
// 时调用 onFallback（调用方据此降级为轮询）。
export function connectWS(onMessage, onFallback) {
  let ws = null
  let retries = 0
  let closed = false
  let fallbackTimer = null
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const token = localStorage.getItem('ocw_token') || ''

  function open() {
    if (closed) return
    ws = new WebSocket(`${proto}://${location.host}/api/v1/ws?token=${encodeURIComponent(token)}`)
    ws.onmessage = (ev) => {
      retries = 0
      try { onMessage(JSON.parse(ev.data)) } catch { /* 非 JSON 忽略 */ }
    }
    ws.onclose = () => {
      if (closed) return
      retries++
      if (retries > 5 && onFallback && !fallbackTimer) {
        // 多次重连失败：降级为 15s 轮询，同时保留低频重连尝试。
        fallbackTimer = setInterval(onFallback, 15000)
        onFallback()
      }
      setTimeout(open, Math.min(1000 * 2 ** retries, 15000))
    }
    ws.onerror = () => ws.close()
  }
  open()
  return () => {
    closed = true
    if (fallbackTimer) clearInterval(fallbackTimer)
    if (ws) ws.close()
  }
}
