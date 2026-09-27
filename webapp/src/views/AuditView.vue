<script setup>
import { ref, onMounted } from 'vue'
import { arr, fmtTime } from '../api'
import { api } from '../api'

const logs = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const pages = ref(0)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const d = await api('GET', `/api/v1/admin/audit-logs?page=${page.value}&limit=${pageSize}`)
    logs.value = arr(d.logs)
    total.value = d.total ?? 0
    pages.value = d.pages ?? 0
  } finally {
    loading.value = false
  }
}

function go(p) {
  if (p < 1 || (pages.value && p > pages.value)) return
  page.value = p
  load()
}

onMounted(load)

// 简易页码列表：当前页附近最多 7 个
function pageList() {
  const out = []
  const start = Math.max(1, page.value - 3)
  const end = Math.min(pages.value, start + 6)
  for (let i = start; i <= end; i++) out.push(i)
  return out
}
</script>

<template>
  <section>
    <div class="card">
      <h2>审计日志 <span class="hint" style="margin-left:8px">共 {{ total }} 条</span></h2>
      <table>
        <thead><tr><th>时间</th><th>用户ID</th><th>动作</th><th>详情</th></tr></thead>
        <tbody>
          <tr v-if="!loading && !logs.length"><td colspan="4" class="empty">暂无日志</td></tr>
          <tr v-if="loading"><td colspan="4" class="empty">加载中…</td></tr>
          <tr v-for="l in logs" :key="l.id">
            <td style="white-space:nowrap">{{ fmtTime(l.createdAt) }}</td>
            <td>#{{ l.userID }}</td>
            <td class="mono">{{ l.action }}</td>
            <td>{{ l.detail }}</td>
          </tr>
        </tbody>
      </table>
      <div v-if="pages > 1" class="pager">
        <button class="ghost mini" :disabled="page <= 1" @click="go(page - 1)">‹ 上一页</button>
        <button
          v-for="p in pageList()" :key="p"
          class="ghost mini" :class="{ cur: p === page }" @click="go(p)">{{ p }}</button>
        <button class="ghost mini" :disabled="page >= pages" @click="go(page + 1)">下一页 ›</button>
        <span class="hint" style="margin-left:8px">{{ page }} / {{ pages }} 页</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.pager { margin-top: 14px; display: flex; align-items: center; gap: 6px; }
.pager .cur { color: var(--pri); font-weight: 700; border-color: var(--pri); }
</style>
