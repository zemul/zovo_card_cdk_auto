<template>
  <div class="pb-2">
    <div class="w-full space-y-6">
      <div class="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
        <div>
          <h1 class="mt-3 text-3xl font-bold text-ink">自有卡密</h1>
          <p class="text-sm text-muted mt-2">
            本站自己发的码，生成时不花钱。用户兑换时才现场向卡台购买一张卡台码（扣服务费）再充值。
            Plus 码在账号有半价资格时自动按半价购买，用户端只显示 Plus。
          </p>
        </div>
        <button class="btn-secondary" :disabled="loading" @click="load">刷新</button>
      </div>

      <!-- 生成 -->
      <div class="card space-y-4">
        <h2 class="text-lg font-semibold text-ink">生成</h2>
        <div class="grid gap-3 md:grid-cols-5">
          <label class="space-y-1">
            <span class="text-sm text-muted">套餐</span>
            <select v-model="form.plan" class="input w-full">
              <option v-for="p in PLANS" :key="p.key" :value="p.key">{{ p.label }}</option>
            </select>
          </label>
          <label class="space-y-1">
            <span class="text-sm text-muted">付款地区</span>
            <select v-model="form.region" class="input w-full">
              <option v-for="r in REGIONS" :key="r.key" :value="r.key">{{ r.label }}</option>
            </select>
          </label>
          <label class="space-y-1">
            <span class="text-sm text-muted">数量（1–200）</span>
            <input v-model.number="form.count" type="number" min="1" max="200" class="input w-full" />
          </label>
          <label class="space-y-1 md:col-span-2">
            <span class="text-sm text-muted">备注（可选）</span>
            <input v-model="form.note" class="input w-full" placeholder="例如：发给谁、哪一批" />
          </label>
        </div>
        <p v-if="form.region === 'auto'" class="text-xs text-muted">「自动」暂按菲律宾付款，接入价格表后会按当时最便宜的地区。</p>
        <div class="flex flex-wrap items-center gap-3">
          <button class="btn-primary" :disabled="creating" @click="create">{{ creating ? '生成中…' : '生成' }}</button>
          <template v-if="created.length">
            <span class="text-sm text-muted">已生成 {{ created.length }} 张</span>
            <button class="btn-secondary" @click="copyLines(created)">复制全部</button>
            <button class="btn-secondary" @click="download(created)">导出 .txt</button>
          </template>
        </div>
        <textarea v-if="created.length" class="input w-full h-32 font-mono text-xs" readonly :value="created.join('\n')" />
      </div>

      <!-- 列表 -->
      <div class="card space-y-4">
        <div class="flex flex-wrap items-center gap-3">
          <select v-model="filter.status" class="input" @change="reload">
            <option value="">全部状态</option>
            <option value="unused">未使用</option>
            <option value="issued">已兑换/处理中</option>
            <option value="issuing">购买中</option>
            <option value="disabled">已停用</option>
          </select>
          <input v-model="filter.q" class="input flex-1 min-w-[200px]" placeholder="搜索卡密 / 卡台码 / 备注" @keyup.enter="reload" />
          <button class="btn-secondary" @click="reload">搜索</button>
          <span class="text-sm text-muted">共 {{ total }} 张</span>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-sm text-muted">已选 {{ selected.length }} 张</span>
          <button class="btn-secondary" :disabled="!selected.length" @click="copyLines(selectedCodes)">复制</button>
          <button class="btn-secondary" :disabled="!selected.length" @click="setStatus(true)">停用</button>
          <button class="btn-secondary" :disabled="!selected.length" @click="setStatus(false)">启用</button>
          <button class="btn-secondary" :disabled="!selected.length" @click="setNote">改备注</button>
        </div>

        <div class="overflow-x-auto">
          <table class="data-table">
            <thead>
              <tr>
                <th><input type="checkbox" :checked="allChecked" @change="toggleAll" /></th>
                <th>卡密</th>
                <th>套餐</th>
                <th>地区</th>
                <th>状态</th>
                <th>卡台码（实际购买）</th>
                <th>备注</th>
                <th>生成时间</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="loading"><td colspan="8" class="py-8 text-center text-muted">加载中…</td></tr>
              <tr v-else-if="!list.length"><td colspan="8" class="py-8 text-center text-muted">暂无卡密</td></tr>
              <tr v-for="it in list" :key="it.id">
                <td><input v-model="selected" type="checkbox" :value="it.id" /></td>
                <td class="font-mono whitespace-nowrap">
                  {{ it.code }}
                  <button class="text-xs text-muted ml-1" @click="copyLines([it.code])">复制</button>
                </td>
                <td class="whitespace-nowrap">{{ planName(it.plan) }}</td>
                <td class="whitespace-nowrap">{{ regionName(it.region) }}</td>
                <td><span :class="statusClass(it.status)">{{ statusName(it.status) }}</span></td>
                <td class="text-xs whitespace-nowrap">
                  <template v-if="it.platform_code">
                    <span class="font-mono">{{ it.platform_code }}</span>
                    <span class="text-muted ml-1">{{ planName(it.platform_plan) }} · {{ it.platform_region }}</span>
                    <div v-if="it.spare_codes" class="text-muted mt-1">闲置（可再用）：<span class="font-mono">{{ it.spare_codes }}</span></div>
                  </template>
                  <span v-else class="text-muted">—</span>
                </td>
                <td class="text-muted">{{ it.note || '—' }}</td>
                <td class="text-muted whitespace-nowrap">{{ fmt(it.created_at) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="total > pageSize" class="flex items-center justify-end gap-2">
          <button class="btn-secondary" :disabled="page <= 1" @click="go(page - 1)">上一页</button>
          <span class="text-sm text-muted">{{ page }} / {{ pages }}</span>
          <button class="btn-secondary" :disabled="page >= pages" @click="go(page + 1)">下一页</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { authFetch } from '../../lib/api'
import { copyToClipboard } from '../../lib/clipboard'
import { dialog } from '../../lib/dialog'

interface OwnCDK {
  id: number
  code: string
  plan: string
  region: string
  status: string
  platform_code?: string
  platform_plan?: string
  platform_region?: string
  spare_codes?: string
  note?: string
  created_at: string
}

const PLANS = [
  { key: 'plus', label: 'Plus' },
  { key: 'pro_5x', label: 'Pro 5x' },
  { key: 'pro_20x', label: 'Pro 20x' },
  { key: 'pro_50x', label: 'Pro 50x' },
  { key: 'go', label: 'Go' },
]
const REGIONS = [
  { key: 'PH', label: '菲律宾 PH' },
  { key: 'US', label: '美国 US' },
  { key: 'JP', label: '日本 JP' },
  { key: 'CL', label: '智利 CL' },
  { key: 'EG', label: '埃及 EG' },
  { key: 'auto', label: '自动' },
]
// 后台可见实际购买档位，所以这里保留 plus_half 的名字
const PLAN_NAMES: Record<string, string> = { plus_half: 'Plus 半价', ...Object.fromEntries(PLANS.map((p) => [p.key, p.label])) }

const form = reactive({ plan: 'plus', region: 'PH', count: 1, note: '' })
const creating = ref(false)
const created = ref<string[]>([])

const list = ref<OwnCDK[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const loading = ref(false)
const filter = reactive({ status: '', q: '' })
const selected = ref<number[]>([])

const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
const allChecked = computed(() => list.value.length > 0 && list.value.every((it) => selected.value.includes(it.id)))
const selectedCodes = computed(() => list.value.filter((it) => selected.value.includes(it.id)).map((it) => it.code))

const planName = (k?: string) => (k ? PLAN_NAMES[k] || k : '—')
const regionName = (k: string) => REGIONS.find((r) => r.key.toLowerCase() === String(k).toLowerCase())?.label || k
const statusName = (s: string) => ({ unused: '未使用', issuing: '购买中', issued: '已兑换/处理中', disabled: '已停用' }[s] || s)
const statusClass = (s: string) => ({ unused: 'pill pill-good', issuing: 'pill pill-warn', issued: 'pill pill-info', disabled: 'pill' }[s] || 'pill')
const fmt = (v: string) => (v ? new Date(v.replace(' ', 'T') + (v.includes('Z') ? '' : 'Z')).toLocaleString('zh-CN') : '—')

async function readJSON(r: Response) {
  try { return await r.json() } catch { return {} }
}

async function load() {
  loading.value = true
  try {
    const q = new URLSearchParams({ page: String(page.value), page_size: String(pageSize) })
    if (filter.status) q.set('status', filter.status)
    if (filter.q.trim()) q.set('q', filter.q.trim())
    const r = await authFetch('/api/v1/admin/own-cdks?' + q.toString())
    const d = await readJSON(r)
    if (!r.ok) throw new Error(d.error || '加载失败')
    list.value = d.list || []
    total.value = d.total || 0
    selected.value = selected.value.filter((id) => list.value.some((it) => it.id === id))
  } catch (e: any) {
    dialog.toast(e.message || '加载失败', 'err')
  } finally {
    loading.value = false
  }
}

function reload() {
  page.value = 1
  void load()
}

function go(p: number) {
  page.value = p
  void load()
}

async function create() {
  const count = Math.floor(Number(form.count) || 0)
  if (count < 1 || count > 200) {
    dialog.toast('数量须在 1–200 之间', 'warn')
    return
  }
  creating.value = true
  try {
    const r = await authFetch('/api/v1/admin/own-cdks', {
      method: 'POST',
      body: JSON.stringify({ plan: form.plan, region: form.region, count, note: form.note }),
    })
    const d = await readJSON(r)
    if (!r.ok) throw new Error(d.error || '生成失败')
    created.value = d.codes || []
    dialog.toast(`已生成 ${created.value.length} 张`, 'ok')
    reload()
  } catch (e: any) {
    dialog.toast(e.message || '生成失败', 'err')
  } finally {
    creating.value = false
  }
}

function toggleAll() {
  selected.value = allChecked.value ? [] : list.value.map((it) => it.id)
}

async function copyLines(codes: string[]) {
  if (!codes.length) return
  const ok = await copyToClipboard(codes.join('\n'))
  dialog.toast(ok ? `已复制 ${codes.length} 张` : '复制失败', ok ? 'ok' : 'err')
}

function download(codes: string[]) {
  const blob = new Blob([codes.join('\n') + '\n'], { type: 'text/plain;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `own-cdk-${form.plan}-${new Date().toISOString().slice(0, 10)}.txt`
  a.click()
  URL.revokeObjectURL(a.href)
}

async function setStatus(disabled: boolean) {
  const r = await authFetch('/api/v1/admin/own-cdks/status', { method: 'POST', body: JSON.stringify({ ids: selected.value, disabled }) })
  const d = await readJSON(r)
  if (!r.ok) {
    dialog.toast(d.error || '操作失败', 'err')
    return
  }
  const skipped = selected.value.length - (d.updated || 0)
  dialog.toast(`已${disabled ? '停用' : '启用'} ${d.updated || 0} 张${skipped ? `，${skipped} 张已兑换或状态不符，未改` : ''}`, 'ok')
  void load()
}

async function setNote() {
  const note = await dialog.prompt('给选中的卡密设置备注（留空即清除）', { title: '改备注' })
  if (note === null || note === undefined) return
  const r = await authFetch('/api/v1/admin/own-cdks/note', { method: 'POST', body: JSON.stringify({ ids: selected.value, note }) })
  const d = await readJSON(r)
  if (!r.ok) {
    dialog.toast(d.error || '操作失败', 'err')
    return
  }
  dialog.toast(`已更新 ${d.updated || 0} 张`, 'ok')
  void load()
}

onMounted(load)
</script>
