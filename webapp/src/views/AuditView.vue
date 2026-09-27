<script setup>
import { ref, onMounted } from 'vue'
import { api, arr, fmtTime } from '../api'

const logs = ref([])
onMounted(async () => {
  logs.value = arr(await api('GET', '/api/v1/admin/audit-logs?limit=100'))
})
</script>

<template>
  <section>
    <div class="card">
      <h2>审计日志（最近 100 条）</h2>
      <table>
        <thead><tr><th>时间</th><th>用户ID</th><th>动作</th><th>详情</th></tr></thead>
        <tbody>
          <tr v-if="!logs.length"><td colspan="4" class="empty">暂无日志</td></tr>
          <tr v-for="l in logs" :key="l.id">
            <td style="white-space:nowrap">{{ fmtTime(l.createdAt) }}</td>
            <td>#{{ l.userID }}</td>
            <td class="mono">{{ l.action }}</td>
            <td>{{ l.detail }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
