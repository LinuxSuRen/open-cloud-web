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

export const PROV_NAME = { alicloud: '阿里云', volcengine: '火山引擎' }
export const ST_NAME = { creating: '创建中', running: '运行中', destroying: '销毁中', destroyed: '已销毁', failed: '失败' }
