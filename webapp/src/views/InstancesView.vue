<script setup>
import { ref, reactive, onMounted, onUnmounted, watch } from 'vue'
import { api, arr, fmtDur, fmtTime, PROV_NAME, ST_NAME, connectWS } from '../api'

const instances = ref([])
const accounts = ref([])
const form = reactive({
  accountID: null, region: '', zone: '', imageID: '',
  instanceType: '', durationSec: null, name: '',
})
const catalog = reactive({ regions: [], zones: [], images: [], types: [], loading: '' })
const busy = ref(false)
const filter = ref('active') // active(默认=创建中+运行中) | all | 具体状态
const page = ref(1)
const pageSize = 20
const total = ref(0)
const pages = ref(0)
let timer = null

async function load() {
  try {
    const d = await api('GET', `/api/v1/instances?status=${filter.value}&page=${page.value}&limit=${pageSize}`)
    instances.value = arr(d.instances)
    total.value = d.total ?? 0
    pages.value = d.pages ?? 0
  } catch { /* 401 已全局处理 */ }
}

watch(filter, () => { page.value = 1; load() })

function goPage(p) {
  if (p < 1 || (pages.value && p > pages.value)) return
  page.value = p
  load()
}

function pageList() {
  const out = []
  const start = Math.max(1, page.value - 3)
  const end = Math.min(pages.value, start + 6)
  for (let i = start; i <= end; i++) out.push(i)
  return out
}

async function removeRecord(id) {
  if (!confirm('确认删除该已销毁的记录？仅清理数据库记录，不影响云资源。')) return
  try {
    await api('DELETE', `/api/v1/instances/${id}`)
    await load()
  } catch (e) { alert('删除失败：' + e.message) }
}

async function loadAccounts() {
  accounts.value = arr(await api('GET', '/api/v1/cloud-accounts'))
  if (!form.accountID && accounts.value.length) {
    form.accountID = accounts.value[0].id
    await loadRegions()
  }
}

async function loadRegions() {
  catalog.loading = 'regions'
  catalog.regions = []
  catalog.zones = []
  catalog.images = []
  catalog.types = []
  try {
    catalog.regions = arr(await api('GET', `/api/v1/cloud-accounts/${form.accountID}/regions`))
    form.region = catalog.regions[0] || ''
    if (form.region) await loadCatalog()
  } finally {
    catalog.loading = ''
  }
}

async function loadZones() {
  catalog.zones = []
  form.zone = ''
  if (!form.region) return
  try {
    catalog.zones = arr(await api('GET', `/api/v1/cloud-accounts/${form.accountID}/zones?region=${encodeURIComponent(form.region)}`))
    form.zone = catalog.zones[0] || ''
  } catch { /* 错误时留空，创建前的校验会提示 */ }
}

async function loadCatalog() {
  catalog.loading = 'catalog'
  catalog.images = []
  catalog.types = []
  loadZones()
  const base = `/api/v1/cloud-accounts/${form.accountID}`
  const r = encodeURIComponent(form.region)
  const [imgs, types] = await Promise.allSettled([
    api('GET', `${base}/images?region=${r}`),
    api('GET', `${base}/instance-types?region=${r}`),
  ])
  if (imgs.status === 'fulfilled') catalog.images = arr(imgs.value)
  if (types.status === 'fulfilled') catalog.types = arr(types.value)
  form.imageID = catalog.images[0]?.id || ''
  form.instanceType = catalog.types[0]?.id || ''
  catalog.loading = ''
}

async function create() {
  busy.value = true
  try {
    await api('POST', '/api/v1/instances', {
      cloudAccountID: form.accountID,
      region: form.region,
      zone: form.zone,
      imageID: form.imageID,
      instanceType: form.instanceType,
      durationSec: Number(form.durationSec) || 0,
      name: form.name.trim(),
    })
    await load()
  } catch (e) {
    alert('创建失败：' + e.message)
  } finally {
    busy.value = false
  }
}

async function renew(id) {
  try {
    await api('POST', `/api/v1/instances/${id}/renew`, {})
    await load()
  } catch (e) { alert('续用失败：' + e.message) }
}

async function cancelCreate(id) {
  if (!confirm('确认取消创建？已开始创建的云资源会在 apply 结束后自动回收。')) return
  try {
    await api('DELETE', `/api/v1/instances/${id}`)
    await load()
  } catch (e) { alert('取消失败：' + e.message) }
}

async function destroy(id) {
  if (!confirm('确认销毁该云主机？')) return
  try {
    await api('DELETE', `/api/v1/instances/${id}`)
    await load()
  } catch (e) { alert('销毁失败：' + e.message) }
}

const now = ref(Date.now())
onMounted(() => {
  load()
  loadAccounts()
  now.value = Date.now()
  // 服务端推送刷新（替代 15s 轮询）；断线自动重连，多次失败降级轮询。
  timer = connectWS(
    (ev) => {
      now.value = Date.now()
      if (ev.type === 'instances.updated') load()
    },
    () => load(), // fallback
  )
})
onUnmounted(() => timer && timer())

const left = (i) => Math.floor((new Date(i.expiresAt) - now.value) / 1000)
const canRenew = (i) => i.status === 'running' && !i.renewedAt && left(i) > 0
const accountName = (id) => accounts.value.find((a) => a.id === id)?.name || `#${id}`
</script>

<template>
  <section>
    <div class="card">
      <h2>创建云主机</h2>
      <div v-if="!accounts.length" class="empty">
        还没有云账号——请先到「云提供商」页签添加账号（AccessKey 认证信息）。
      </div>
      <div v-else class="row">
        <label>云账号
          <select v-model="form.accountID" @change="loadRegions">
            <option v-for="a in accounts" :key="a.id" :value="a.id">
              {{ a.name }}（{{ PROV_NAME[a.provider] || a.provider }}）
            </option>
          </select>
        </label>
        <label>地域
          <select v-model="form.region" @change="loadCatalog">
            <option v-if="catalog.loading === 'regions'" value="">加载中…</option>
            <option v-for="r in catalog.regions" :key="r" :value="r">{{ r }}</option>
            <option v-if="!catalog.loading && !catalog.regions.length" value="">（加载失败，请检查账号凭据）</option>
          </select>
        </label>
        <label>可用区
          <select v-model="form.zone">
            <option v-if="catalog.loading" value="">加载中…</option>
            <option v-for="z in catalog.zones" :key="z" :value="z">{{ z }}</option>
            <option v-if="!catalog.loading && !catalog.zones.length" value="">（该地域无可用区）</option>
          </select>
        </label>
        <label>镜像
          <select v-model="form.imageID" style="max-width:260px">
            <option v-if="catalog.loading === 'catalog'" value="">加载中…</option>
            <option v-for="i in catalog.images" :key="i.id" :value="i.id">
              {{ i.name || i.id }} {{ i.osType || '' }}
            </option>
          </select>
        </label>
        <label>规格
          <select v-model="form.instanceType">
            <option v-for="t in catalog.types" :key="t.id" :value="t.id">
              {{ t.id }}（{{ t.cpu }}C/{{ Math.round(t.memoryMB / 1024) }}G）
            </option>
          </select>
        </label>
        <label>时长（秒，默认 3600）
          <input v-model.number="form.durationSec" type="number" min="60" placeholder="3600" style="width:120px" />
        </label>
        <label>名称（可选）
          <input v-model="form.name" placeholder="自动生成" style="width:130px" />
        </label>
        <button class="btn" :disabled="busy || !form.imageID || !form.instanceType || !form.zone" @click="create">
          {{ busy ? '创建中…' : '创建' }}
        </button>
      </div>
      <p v-if="accounts.length" class="hint">镜像与规格实时来自云厂商 API（使用所选云账号凭据）；到期自动销毁，到期前可续用一次。</p>
    </div>

    <div class="card">
      <h2>
        我的云主机 <span class="hint" style="margin-left:4px">共 {{ total }} 条</span>
        <select v-model="filter" style="margin-left:8px;font-size:12px;padding:3px 8px">
          <option value="active">有效（创建中+运行中，默认）</option>
          <option value="all">全部</option>
          <option value="creating">创建中</option>
          <option value="running">运行中</option>
          <option value="destroying">销毁中</option>
          <option value="destroyed">已销毁</option>
          <option value="failed">失败</option>
        </select>
        <button class="ghost mini" style="margin-left:8px" @click="load">刷新</button>
      </h2>
      <table>
        <thead>
          <tr>
            <th>名称/ID</th><th>云账号/地域</th><th>规格/镜像</th><th>状态</th>
            <th>公网 IP</th><th>到期</th><th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!instances.length">
            <td colspan="7" class="empty">暂无云主机</td>
          </tr>
          <tr v-for="i in instances" :key="i.id">
            <td>
              <b>{{ i.name }}</b><br />
              <span class="mono" style="color:var(--sub)">#{{ i.id }}</span>
            </td>
            <td>{{ accountName(i.cloudAccountID) }}<br />{{ i.region }} {{ i.zone }}</td>
            <td class="mono">{{ i.instanceType }}<br /><span style="color:var(--sub)">{{ i.imageID }}</span></td>
            <td>
              <span class="tag" :class="'t-' + i.status">{{ ST_NAME[i.status] || i.status }}</span>
              <div v-if="i.errorMessage" style="color:var(--err);font-size:12px">{{ i.errorMessage }}</div>
            </td>
            <td class="mono">
              <a v-if="i.publicIP" :href="'http://' + i.publicIP" target="_blank">{{ i.publicIP }}</a>
              <template v-else>—</template>
            </td>
            <td>
              <template v-if="i.status === 'creating' || !i.expiresAt || i.expiresAt < '2000-'">
                <span style="color:var(--sub)">创建完成后起算</span>
              </template>
              <template v-else>
                {{ fmtTime(i.expiresAt) }}
                <div v-if="i.status === 'running'" :style="{ color: left(i) < 600 ? 'var(--err)' : 'var(--sub)', fontSize: '12px' }">
                  剩 {{ fmtDur(left(i)) }}
                </div>
                <div v-if="i.renewedAt" style="color:var(--sub);font-size:12px">已续用</div>
              </template>
            </td>
            <td style="white-space:nowrap">
              <button v-if="canRenew(i)" class="ghost mini" @click="renew(i.id)">续用 1 小时</button>
              <button
                v-if="i.status === 'running' || i.status === 'failed'"
                class="ghost mini danger" @click="destroy(i.id)">销毁</button>
              <button
                v-if="i.status === 'creating'"
                class="ghost mini danger" @click="cancelCreate(i.id)">取消</button>
              <button
                v-if="i.status === 'destroyed'"
                class="ghost mini" @click="removeRecord(i.id)">删除记录</button>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-if="pages > 1" class="pager">
        <button class="ghost mini" :disabled="page <= 1" @click="goPage(page - 1)">‹ 上一页</button>
        <button v-for="p in pageList()" :key="p" class="ghost mini" :class="{ cur: p === page }" @click="goPage(p)">{{ p }}</button>
        <button class="ghost mini" :disabled="page >= pages" @click="goPage(page + 1)">下一页 ›</button>
        <span class="hint" style="margin-left:8px">{{ page }} / {{ pages }} 页</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.pager { margin-top: 14px; display: flex; align-items: center; gap: 6px; }
.pager .cur { color: var(--pri); font-weight: 700; border-color: var(--pri); }
</style>
