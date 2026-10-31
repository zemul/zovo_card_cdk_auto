<template>
  <RedeemShell title="卡密查询" subtitle="输入卡密或粘贴完整进度链接，查看当前处理状态。" compact>
    <form class="rx-form" novalidate @submit.prevent="submit">
      <label class="rx-field">
        <span>卡密</span>
        <input v-model="input" autocomplete="off" autocapitalize="characters" spellcheck="false" placeholder="请输入卡密或完整进度链接" />
      </label>
      <div class="rx-query-actions">
        <router-link :to="RX_HOME">返回充值</router-link>
        <button class="rx-btn" type="submit" :disabled="loading">
          <span v-if="loading && !view" class="rx-spin" aria-hidden="true" />查询进度
        </button>
      </div>
    </form>

    <p v-if="error" class="rx-alert" role="alert" style="margin-top: 14px">{{ error }}</p>

    <section v-if="view" class="rx-status" :class="'is-' + view.state" aria-live="polite">
      <p v-if="productText" class="rx-banner">充值产品：{{ productText }}</p>
      <p v-if="view.accountEmail" class="rx-account">
        充值账号：<strong>{{ view.accountEmail }}</strong>
        <button class="rx-copy" type="button" @click="copyEmail">{{ copied ? '已复制' : '复制' }}</button>
      </p>

      <div class="rx-status-top">
        <div>
          <p class="rx-eyebrow">当前状态</p>
          <h2>{{ view.statusText }}</h2>
        </div>
        <span class="rx-badge">{{ view.badge }}</span>
      </div>

      <div v-if="elapsed > 0" class="rx-timer" aria-live="off">
        <span>{{ view.state === 'success' ? '充值成功总耗时' : view.terminal ? '本次处理耗时' : '本次充值已用时' }}</span>
        <strong>{{ formatElapsed(elapsed) }}</strong>
        <small>从订单进入充值流程开始计算</small>
      </div>

      <p class="rx-message">{{ view.message }}</p>

      <div v-if="view.state !== 'failed'" class="rx-meter" aria-label="充值处理进度"><span :style="{ width: view.percent + '%' }" /></div>

      <ol class="rx-progress">
        <li v-for="(s, i) in view.steps" :key="i" :class="s.state !== 'todo' ? 'is-' + s.state : ''">{{ s.text }}</li>
      </ol>

      <div v-if="view.state === 'failed'" class="rx-next">
        <template v-if="view.canResubmit">
          <strong>原卡密仍可使用</strong>
          <p>请确认账号状态正常后，用原卡密重新提交。如之前登录过期，请重新获取 Session。</p>
          <router-link class="rx-btn is-wide" :to="{ path: RX_FILL, query: { card: card, retry: '1' } }">重新提交充值</router-link>
        </template>
        <template v-else>
          <strong>本次充值未完成</strong>
          <p>卡密暂时不能重新使用，请联系发码方处理，不要重复付款。</p>
        </template>
      </div>

      <div v-if="SHOW_INVOICE && view.state === 'success'" class="rx-invoice">
        <div class="rx-invoice-head">
          <div>
            <p class="rx-eyebrow">账单记录</p>
            <h3>支付账单</h3>
          </div>
          <strong v-if="view.invoiceUrl">100%</strong>
        </div>
        <template v-if="view.invoiceUrl">
          <p>账单已生成，你可以打开账单页面保存记录。</p>
          <a class="rx-btn" :href="view.invoiceUrl" target="_blank" rel="noopener noreferrer">打开账单</a>
        </template>
        <p v-else>账单暂未生成，可稍后回到本页查询，或在账号的账单设置中查看。</p>
      </div>
    </section>

    <section v-else-if="pendingSubmit" class="rx-status" aria-live="polite">
      <div class="rx-status-top">
        <div>
          <p class="rx-eyebrow">当前状态</p>
          <h2>已提交</h2>
        </div>
        <span class="rx-badge">等待</span>
      </div>
      <p class="rx-message">订单已提交，正在等待系统处理，请保持页面打开，不要重复提交。</p>
    </section>
  </RedeemShell>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import RedeemShell from './RedeemShell.vue'
import { RX_HOME, RX_FILL, RX_QUERY, api, cardFromQuery, rememberedPlan, errorText, extractCardInput, formatElapsed, recentlySubmitted } from './api'
import { planLabel } from '../../lib/plan'
import { isGrokPlan } from '../../lib/grok'
import { isXPremiumPlan } from '../../lib/x-premium'
import { copyToClipboard } from '../../lib/clipboard'
import { buildProgressView, type ProgressView } from './progress'

/**
 * 成功页的「账单记录 / 打开账单」区块，暂时关闭。
 * 账单链接来自 result-by-code 里的 invoice_url，后端要靠 accounthub 服务才查得到，目前没有部署。
 * 恢复方法：部署 accounthub 后改成 true；或者改用 /api/v1/public/billing/check（凭卡密用兑换时保存的 Session 查）。
 */
const SHOW_INVOICE = false

const route = useRoute()
const router = useRouter()
const input = ref('')
const card = ref('')
const view = ref<ProgressView | null>(null)
const loading = ref(false)
const error = ref('')
const copied = ref(false)
const pendingSubmit = ref(false)
const elapsed = ref(0)

let pollTimer: ReturnType<typeof setTimeout> | null = null
let tickTimer: ReturnType<typeof setInterval> | null = null
let generation = 0
let failures = 0
let elapsedBase = { seconds: 0, at: 0 }

document.title = '卡密查询'

const productText = computed(() => {
  const p = String(view.value?.plan || rememberedPlan(card.value) || '')
  if (!p) return ''
  const product = String(view.value?.product || '').toLowerCase()
  const key = product === 'grok' && !p.startsWith('grok_') ? 'grok_' + p : product === 'x' && !p.startsWith('x_') ? 'x_' + p : p
  const label = planLabel(key)
  return isGrokPlan(key) || isXPremiumPlan(key) ? label : 'ChatGPT ' + label
})

function stopPolling() {
  generation++
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
}

// 卡台公开结果不带充值邮箱；用本站现成的 /api/v1/lookup/cdk（从兑换时保存的 Session 取邮箱）补上。
// 该接口内部也会请求卡台，所以每张卡密只查一次，结果留在页面内存里。
const emailCache = new Map<string, string>()
const emailPending = new Set<string>()
async function fillEmail(code: string, gen: number) {
  if (emailCache.has(code) || emailPending.has(code)) return
  emailPending.add(code)
  try {
    const r = await api('/api/v1/lookup/cdk?code=' + encodeURIComponent(code))
    const email = r.ok ? String(r.data?.account_email || '').trim() : ''
    emailCache.set(code, email)
    if (email && gen === generation && card.value === code && view.value && !view.value.accountEmail) {
      view.value = { ...view.value, accountEmail: email }
    }
  } finally {
    emailPending.delete(code)
  }
}

function schedule(ms: number, gen: number) {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = setTimeout(() => { void load(gen) }, ms)
}

async function load(gen = generation) {
  if (gen !== generation || !card.value) return
  loading.value = true
  const r = await api('/api/v1/public/cdk/result-by-code?code=' + encodeURIComponent(card.value))
  if (gen !== generation) return
  loading.value = false

  const next = r.ok ? buildProgressView(r.data) : null
  if (next?.found) {
    failures = 0
    error.value = ''
    pendingSubmit.value = false
    if (!next.accountEmail) next.accountEmail = emailCache.get(card.value) || ''
    view.value = next
    if (!next.accountEmail) void fillEmail(card.value, gen)
    elapsedBase = { seconds: next.elapsedSeconds, at: Date.now() }
    elapsed.value = elapsedBase.seconds
    if (!next.terminal) {
      schedule(next.state === 'review' ? 5000 : 3000, gen)
    } else if (SHOW_INVOICE && next.state === 'success' && !next.invoiceUrl) {
      schedule(15000, gen) // 账单生成通常稍晚
    }
    return
  }

  if (r.status === 404 || (r.ok && !next?.found)) {
    view.value = null
    // 刚提交时卡台订单可能还没建好，先显示「已提交」并继续查
    if (recentlySubmitted(card.value)) {
      pendingSubmit.value = true
      schedule(3000, gen)
      return
    }
    pendingSubmit.value = false
    error.value = r.status === 404 ? '未找到这张卡密的兑换记录。请确认卡密正确；需在兑换时使用的同一浏览器中查询。' : errorText(r.data, '未找到这张卡密的兑换记录')
    return
  }

  // 其它 4xx（如换了浏览器导致设备不匹配）重试也不会变：停止轮询，显示原因
  if (r.status >= 400 && r.status < 500 && r.status !== 429) {
    if (!view.value) error.value = errorText(r.data, '暂时无法查询这张卡密，请在兑换时使用的浏览器中打开')
    return
  }

  // 网络 / 限流 / 上游暂时不可用：保留上次结果，退避重试
  failures++
  if (!view.value) error.value = errorText(r.data, '进度暂时未同步，正在重试…')
  schedule(Math.min(15000, 3000 * 2 ** Math.min(failures - 1, 3)), gen)
}

function start(code: string) {
  stopPolling()
  card.value = code
  input.value = code
  view.value = null
  error.value = ''
  copied.value = false
  failures = 0
  void load(generation)
}

async function submit() {
  const code = extractCardInput(input.value)
  if (!code) {
    error.value = '请输入卡密'
    return
  }
  if (code === cardFromQuery(route.query)) start(code)
  else await router.replace({ path: RX_QUERY, query: { card: code } })
}

async function copyEmail() {
  if (view.value?.accountEmail) copied.value = await copyToClipboard(view.value.accountEmail)
}

function onVisible() {
  // 切回页面时立刻刷新一次（浏览器后台会节流定时器）
  if (document.visibilityState === 'visible' && card.value && view.value && !view.value.terminal) {
    void load(generation)
  }
}

watch(() => route.query, (q) => {
  const code = cardFromQuery(q)
  if (code && code !== card.value) start(code)
})

onMounted(() => {
  tickTimer = setInterval(() => {
    if (view.value?.running) elapsed.value = elapsedBase.seconds + Math.floor((Date.now() - elapsedBase.at) / 1000)
  }, 1000)
  document.addEventListener('visibilitychange', onVisible)
  const code = cardFromQuery(route.query)
  if (code) start(code)
})

onUnmounted(() => {
  stopPolling()
  if (tickTimer) clearInterval(tickTimer)
  document.removeEventListener('visibilitychange', onVisible)
})
</script>
