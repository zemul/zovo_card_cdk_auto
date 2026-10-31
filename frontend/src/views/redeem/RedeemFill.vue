<template>
  <RedeemShell :title="'填写账号信息'" :subtitle="subtitle" :step="confirmOpen ? 3 : 2" compact>
    <div v-if="loading" class="rx-loading"><span class="rx-spin" aria-hidden="true" />正在验证卡密…</div>

    <form v-else class="rx-form" novalidate @submit.prevent="doPreflight">
      <p v-if="planText" class="rx-banner" role="status">充值产品：{{ planText }}</p>

      <label class="rx-field">
        <span>卡密</span>
        <input :value="card" readonly autocomplete="off" />
      </label>

      <!-- ChatGPT：两步获取完整 Session JSON -->
      <div v-if="kind === 'gpt'" class="rx-guide">
        <p class="rx-alert is-warn">请按顺序完成以下两步，登录错账号会导致充错账号。</p>
        <div class="rx-guide-step">
          <span class="rx-guide-index">1</span>
          <div><strong>确认目标 GPT 账号</strong><small>先确认网页端登录的是需要充值的账号</small></div>
          <a class="rx-btn is-secondary" href="https://chatgpt.com/" target="_blank" rel="noopener">打开网页端 →</a>
        </div>
        <div class="rx-guide-step">
          <span class="rx-guide-index">2</span>
          <div><strong>复制完整 JSON</strong><small>从第一个 { 复制到最后一个 }，不要遗漏</small></div>
          <a class="rx-btn is-secondary" href="https://chatgpt.com/api/auth/session" target="_blank" rel="noopener">打开获取页面 →</a>
        </div>
      </div>
      <div v-else class="rx-guide">
        <p class="rx-alert is-warn">{{ kind === 'grok' ? t('grok.hint') : t('xPremium.hint') }}</p>
      </div>

      <label class="rx-field">
        <span>{{ fieldLabel }}</span>
        <textarea v-model="sessionRaw" name="session" rows="8" autocomplete="off" spellcheck="false" :placeholder="placeholder" />
      </label>

      <details v-if="kind === 'gpt'" class="rx-more">
        <summary><span>其他导入方式</span><small>可选</small></summary>
        <div class="rx-more-body">
          <p>仅在 Session 太长、手机复制不完整时使用 TXT 导入。</p>
          <div class="rx-file" :class="{ 'is-over': dragOver }" @dragover.prevent="dragOver = true" @dragleave="dragOver = false" @drop.prevent="onDrop">
            <input id="rx-session-file" type="file" accept=".txt,text/plain" @change="onFile" />
            <label class="rx-btn is-secondary" for="rx-session-file">选择 TXT</label>
            <span>{{ fileStatus }}</span>
          </div>
        </div>
      </details>

      <p v-if="error" class="rx-alert" role="alert">{{ error }}</p>

      <button class="rx-btn is-wide" type="submit" :disabled="busy">
        <span v-if="busy" class="rx-spin" aria-hidden="true" />{{ busy ? '正在验证账号…' : '确认提交充值' }}
      </button>
      <router-link class="rx-back" :to="RX_HOME">返回上一步</router-link>
    </form>

    <template #outside>
      <div v-if="confirmOpen" class="rx-modal" role="dialog" aria-modal="true" aria-labelledby="rx-confirm-title" @click.self="closeConfirm">
        <div class="rx-dialog">
          <p class="rx-eyebrow">提交前确认</p>
          <h2 id="rx-confirm-title">请核对充值账号</h2>
          <div class="rx-copy-row">
            <strong>{{ account.email || '账号已验证' }}</strong>
            <button v-if="account.email" class="rx-copy" type="button" @click="copyEmail">{{ copied ? '已复制' : '复制' }}</button>
          </div>
          <dl>
            <dt>充值产品</dt><dd>{{ planText || '—' }}</dd>
            <dt>当前套餐</dt><dd>{{ currentPlanText }}</dd>
            <template v-if="account.activeUntil"><dt>套餐到期</dt><dd>{{ fmtTime(account.activeUntil) }}</dd></template>
          </dl>

          <p v-if="account.delinquent" class="rx-alert is-warn" style="margin-top: 14px">该账号有未结清账单（欠费）。仍可兑换：卡台会先取消原订阅再重新开通，但成功率低于正常账号。</p>
          <p v-if="blockReason" class="rx-alert is-warn" style="margin-top: 14px">{{ blockReason }}</p>
          <div v-else-if="needsRecovery || recoveryPending" class="rx-alert is-warn" style="margin-top: 14px">
            <!-- 二次确认放在本弹窗里：站点全局确认框会被这个弹窗盖住 -->
            <template v-if="graceAsk">
              <p style="margin: 0 0 10px">{{ t('grace.confirm') }}</p>
              <div class="rx-inline-actions">
                <button class="rx-btn is-danger" type="button" :disabled="recovering" @click="recoverSubscription">
                  {{ recovering ? t('grace.processing') : t('grace.confirmAction') }}
                </button>
                <button class="rx-btn is-secondary" type="button" :disabled="recovering" @click="graceAsk = false">{{ t('grace.dismiss') }}</button>
              </div>
            </template>
            <template v-else>
              <p style="margin: 0 0 10px">{{ t(recoveryPending ? 'grace.pending' : 'grace.hint') }}</p>
              <button v-if="!recoveryPending" class="rx-btn is-secondary" type="button" @click="graceAsk = true">{{ t('grace.action') }}</button>
              <button v-else class="rx-btn is-secondary" type="button" :disabled="busy" @click="recheck">{{ t('grace.recheck') }}</button>
            </template>
          </div>

          <p class="rx-help">请确认账号无误。提交后系统将按此账号创建充值任务，提交后请勿重复提交。</p>
          <p v-if="redeemError" class="rx-alert" role="alert" style="margin: -6px 0 14px">{{ redeemError }}</p>
          <div class="rx-dialog-actions">
            <button class="rx-btn is-secondary" type="button" :disabled="submitting" @click="closeConfirm">返回修改</button>
            <button class="rx-btn" type="button" :disabled="!canRedeem || submitting" @click="doRedeem">
              <span v-if="submitting" class="rx-spin" aria-hidden="true" />{{ submitting ? (own ? '正在提交，约需 20 秒…' : '提交中…') : '确认提交' }}
            </button>
          </div>
        </div>
      </div>
    </template>
  </RedeemShell>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import RedeemShell from './RedeemShell.vue'
import { buildProgressView } from './progress'
import { RX_HOME, RX_QUERY, api, cardFromQuery, ownPreview, clearPreview, errorText, extractSession, looksUsed, markSubmitted, planFromPreview, stashPreview, takePreview } from './api'
import { planLabel, planSatisfied } from '../../lib/plan'
import { grokCredential, isGrokPlan } from '../../lib/grok'
import { isXPremiumPlan, xPremiumCredential } from '../../lib/x-premium'
import { copyToClipboard } from '../../lib/clipboard'
import { dialog } from '../../lib/dialog'

const { t } = useI18n({ useScope: 'global' })
const route = useRoute()
const router = useRouter()

const card = ref('')
const token = ref('')
const previewInfo = ref<any>({})
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const sessionRaw = ref('')
const fileStatus = ref('点击选择，或把一个 TXT 拖到这里。文件只在当前浏览器中读取。')
const dragOver = ref(false)

const confirmOpen = ref(false)
const preflightToken = ref('')
const grokSession = ref('')
const blockReason = ref('')
const needsRecovery = ref(false)
const recoveryPending = ref(false)
const recovering = ref(false)
const graceAsk = ref(false)
const submitting = ref(false)
const redeemError = ref('')
const copied = ref(false)
let clientRequestId = ''
const account = ref({ email: '', currentPlan: '', activeUntil: '', canPurchaseAt: '', willRenew: null as boolean | null, hasActive: null as boolean | null, delinquent: false })
/** 自有卡密：预检 / 兑换都走 /api/v1/public/own-cdk/*，由后端现场购买卡台码 */
const own = ref(false)
const ownChecked = ref(false)

const plan = computed(() => planFromPreview(previewInfo.value))
const kind = computed<'gpt' | 'grok' | 'x'>(() => {
  if (isXPremiumPlan(plan.value)) return 'x'
  if (isGrokPlan(plan.value)) return 'grok'
  return 'gpt'
})
const planText = computed(() => {
  if (!plan.value) return ''
  const label = planLabel(plan.value)
  return kind.value === 'gpt' ? 'ChatGPT ' + label : label
})
const subtitle = computed(() => kind.value === 'gpt' ? '卡密验证通过，按下面两步获取完整信息' : '卡密验证通过，请填写账号登录信息')
const fieldLabel = computed(() => kind.value === 'gpt' ? '完整 JSON 信息' : kind.value === 'grok' ? t('grok.credential') : t('xPremium.credential'))
const placeholder = computed(() => kind.value === 'gpt'
  ? '请粘贴获取页面显示的完整 JSON 内容\n\n内容通常很长，请从第一个 { 复制到最后一个 }，不要遗漏。'
  : kind.value === 'grok' ? t('grok.placeholder') : t('xPremium.placeholder'))
const currentPlanText = computed(() => {
  const p = account.value.currentPlan
  if (!p) return '—'
  if (kind.value === 'grok') return planLabel('grok_' + String(p).replace(/^grok_/i, ''))
  return planLabel(p)
})
const alreadySatisfied = computed(() => {
  if (!account.value.currentPlan || account.value.hasActive === false) return false
  return planSatisfied(account.value.currentPlan, plan.value, previewInfo.value?.plan_flow)
})
const canRedeem = computed(() => {
  if (blockReason.value || needsRecovery.value || recoveryPending.value) return false
  if (own.value) return ownChecked.value
  return kind.value === 'grok' ? !!grokSession.value : !!preflightToken.value
})

// 改了凭证就作废上一次预检结果
watch(sessionRaw, () => { preflightToken.value = ''; grokSession.value = ''; ownChecked.value = false })

/** 账号已有该套餐：告诉用户什么时候能再充（与旧兑换页同一文案）。 */
function alreadyActiveHint(): string {
  const label = planLabel(plan.value)
  const until = account.value.canPurchaseAt || account.value.activeUntil
  if (until && account.value.willRenew === true) {
    return `账号已有 ${label} 或更高套餐，当前周期至 ${fmtTime(until)} 并会自动续费。请先在 ChatGPT 取消自动续费，到期后再兑换。`
  }
  if (until && account.value.willRenew === false) {
    return `账号已有 ${label} 或更高套餐，最早可在 ${fmtTime(until)} 后兑换。卡密仍可使用，到期后再来即可。`
  }
  if (until) return `账号已有 ${label} 或更高套餐，当前周期至 ${fmtTime(until)}，到期后再兑换。`
  return `账号已有 ${label} 或更高套餐，暂不能重复充值。`
}

function fmtTime(v: string) {
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? v : d.toLocaleString()
}

/** 重新 preview：首页暂存的会话过期，或直接打开 /redeem?card= 时。 */
async function runPreview(): Promise<boolean> {
  const r = await api('/api/v1/public/cdk/preview', { method: 'POST', body: JSON.stringify({ code: card.value }) })
  const body = r.data?.data && typeof r.data.data === 'object' ? r.data.data : r.data || {}
  const tok = body.redemption_token || r.data?.redemption_token || body.token || ''
  if (r.ok && tok) {
    token.value = tok
    previewInfo.value = body
    stashPreview(card.value, tok, body)
    return true
  }
  const msg = errorText(r.data, '卡密无效或不可用')
  const progress = await api('/api/v1/public/cdk/result-by-code?code=' + encodeURIComponent(card.value))
  if (progress.ok && buildProgressView(progress.data).found) {
    await router.replace({ path: RX_QUERY, query: { card: card.value } })
    return false
  }
  error.value = looksUsed(msg) ? '该卡密已使用。' : msg
  return false
}

function credential(): { mode: string; session: string } | null {
  const raw = sessionRaw.value
  const s = kind.value === 'grok' ? grokCredential(raw) : kind.value === 'x' ? xPremiumCredential(raw) : extractSession(raw)
  return s ? { mode: 'session', session: s } : null
}

function applyPreflight(body: any) {
  account.value = {
    email: String(body.email || body.account_email || ''),
    currentPlan: String(body.currentPlan || body.current_plan || body.plan || ''),
    activeUntil: String(body.subscription_active_until || ''),
    canPurchaseAt: String(body.can_purchase_at || body.subscription_active_until || ''),
    willRenew: typeof body.subscription_will_renew === 'boolean' ? body.subscription_will_renew : null,
    hasActive: typeof body.subscription_has_active === 'boolean' ? body.subscription_has_active : null,
    delinquent: body.subscription_is_delinquent === true,
  }
  // 与旧兑换页 needsSubscriptionRecovery 同一条件
  needsRecovery.value = body.subscription_recovery_required === true || body.subscription_is_delinquent === true
  if (kind.value !== 'grok') preflightToken.value = String(body.preflight_token || '')
}

async function doPreflight(retried = false) {
  if (busy.value) return
  error.value = ''
  const cred = credential()
  if (!cred) {
    error.value = kind.value === 'grok' ? t('grok.invalid')
      : kind.value === 'x' ? t('xPremium.invalid')
      : '请粘贴完整 JSON（必须含 sessionToken），不能只粘贴 Access Token'
    return
  }
  busy.value = true
  try {
    if (own.value) {
      await ownCheck(cred)
      return
    }
    if (!token.value && !(await runPreview())) return
    const r = await api('/api/v1/public/cdk/preflight', {
      method: 'POST',
      body: JSON.stringify({ code: card.value, redemption_token: token.value, credential: cred }),
    })
    if (!r.ok) {
      const msg = errorText(r.data, '账号验证失败')
      // 兑换会话过期：静默重新 preview 后再试一次
      if (!retried && (r.status === 401 || r.status === 410 || /过期|失效|expired|invalid.*token/i.test(msg))) {
        token.value = ''
        busy.value = false
        return doPreflight(true)
      }
      error.value = msg
      return
    }
    const body = r.data?.data && typeof r.data.data === 'object' ? r.data.data : r.data || {}
    blockReason.value = ''
    recoveryPending.value = false
    graceAsk.value = false
    applyPreflight(body)
    if (kind.value === 'grok') {
      grokSession.value = cred.session
      blockReason.value = String(body.blocking_reason || '')
    } else if (!preflightToken.value) {
      error.value = '账号验证未完成，请稍后重试'
      return
    }
    // 宽限期账号订阅还「有效」，会被误判成已有套餐；先让用户处理宽限期（与旧页面一致）
    if (!blockReason.value && !needsRecovery.value && alreadySatisfied.value) {
      blockReason.value = alreadyActiveHint()
    }
    clientRequestId = 'web-' + Math.random().toString(36).slice(2, 10) + '-' + Date.now()
    redeemError.value = ''
    copied.value = false
    confirmOpen.value = true
  } finally {
    busy.value = false
  }
}

/** 自有卡密的账号检查：后端调直充预检，不发码、不扣钱。 */
async function ownCheck(cred: { mode: string; session: string }) {
  const r = await api('/api/v1/public/own-cdk/check', { method: 'POST', body: JSON.stringify({ code: card.value, credential: cred }) })
  if (!r.ok) {
    error.value = errorText(r.data, '账号验证失败')
    return
  }
  const body = r.data?.data && typeof r.data.data === 'object' ? r.data.data : r.data || {}
  blockReason.value = String(body.blocking_reason || '')
  recoveryPending.value = false
  graceAsk.value = false
  applyPreflight(body)
  ownChecked.value = true
  // 宽限期：和卡台码一样显示「取消原订阅并重新检测」，后端先买好卡台码再取消
  if (!blockReason.value && !needsRecovery.value && alreadySatisfied.value) {
    blockReason.value = alreadyActiveHint()
  }
  redeemError.value = ''
  copied.value = false
  confirmOpen.value = true
}

async function recoverSubscription() {
  if (own.value) return ownRecover()
  if (recovering.value || !preflightToken.value) return
  const pf = preflightToken.value
  recovering.value = true
  redeemError.value = ''
  // 旧预检票用过即作废：无论结果如何，都必须重新检测账号才能兑换
  preflightToken.value = ''
  recoveryPending.value = true
  try {
    const r = await api('/api/v1/public/cdk/recover-subscription', {
      method: 'POST',
      body: JSON.stringify({ redemption_token: token.value, preflight_token: pf, confirmed: true }),
    })
    if (!r.ok) throw new Error('recovery_unconfirmed')
    const result = r.data?.data || r.data || {}
    if (result.preflight) applyPreflight(result.preflight)
    recoveryPending.value = result.status !== 'cleared' || !preflightToken.value
    if (!recoveryPending.value) needsRecovery.value = false
    dialog.toast(t(recoveryPending.value ? 'grace.pending' : 'grace.done'), recoveryPending.value ? 'warn' : 'ok')
  } catch {
    redeemError.value = t('grace.refreshFailed')
  } finally {
    recovering.value = false
    graceAsk.value = false
  }
}

/** 自有卡密的宽限期处理：后端先买卡台码（之后兑换继续用它），再调卡台取消原订阅。 */
async function ownRecover() {
  if (recovering.value) return
  const cred = credential()
  if (!cred) {
    redeemError.value = '请粘贴完整 JSON（必须含 sessionToken）'
    return
  }
  recovering.value = true
  redeemError.value = ''
  recoveryPending.value = true
  try {
    const r = await api('/api/v1/public/own-cdk/recover', { method: 'POST', body: JSON.stringify({ code: card.value, credential: cred, confirmed: true }) })
    if (!r.ok) {
      redeemError.value = errorText(r.data, t('grace.refreshFailed'))
      return
    }
    const result = r.data?.data || r.data || {}
    if (result.preflight) applyPreflight(result.preflight)
    recoveryPending.value = result.status !== 'cleared'
    if (!recoveryPending.value) needsRecovery.value = false
    dialog.toast(t(recoveryPending.value ? 'grace.pending' : 'grace.done'), recoveryPending.value ? 'warn' : 'ok')
  } catch {
    redeemError.value = t('grace.refreshFailed')
  } finally {
    recovering.value = false
    graceAsk.value = false
  }
}

async function recheck() {
  confirmOpen.value = false
  await doPreflight()
}

async function doRedeem() {
  if (submitting.value || !canRedeem.value) return
  submitting.value = true
  redeemError.value = ''
  try {
    if (own.value) {
      const cred = credential()
      if (!cred) {
        redeemError.value = '请粘贴完整 JSON（必须含 sessionToken）'
        return
      }
      // 后端要现场购买卡台码再兑换，通常 10–20 秒
      const r = await api('/api/v1/public/own-cdk/redeem', { method: 'POST', body: JSON.stringify({ code: card.value, credential: cred }) })
      if (!r.ok && r.status !== 202) {
        redeemError.value = errorText(r.data, '提交失败，请稍后用同一张卡密重试')
        return
      }
      markSubmitted(card.value)
      await router.replace({ path: RX_QUERY, query: { card: card.value } })
      return
    }
    const body = kind.value === 'grok'
      ? { redemption_token: token.value, credential: { mode: 'session', session: grokSession.value }, client_request_id: clientRequestId }
      : { redemption_token: token.value, preflight_token: preflightToken.value, client_request_id: clientRequestId }
    const r = await api('/api/v1/public/cdk/redeem', { method: 'POST', body: JSON.stringify(body) })
    if (!r.ok && r.status !== 202) {
      redeemError.value = errorText(r.data, '提交失败，请稍后重试')
      return
    }
    markSubmitted(card.value)
    clearPreview(card.value)
    await router.replace({ path: RX_QUERY, query: { card: card.value } })
  } finally {
    submitting.value = false
  }
}

function closeConfirm() {
  if (submitting.value) return
  confirmOpen.value = false
}

async function copyEmail() {
  copied.value = await copyToClipboard(account.value.email)
}

function readFile(file?: File | null) {
  if (!file) return
  if (file.size > 256 * 1024) {
    fileStatus.value = '文件太大，请确认选择的是保存 Session 的 TXT。'
    return
  }
  const reader = new FileReader()
  reader.onload = () => {
    sessionRaw.value = String(reader.result || '').trim()
    fileStatus.value = `已读取 ${file.name}`
  }
  reader.onerror = () => { fileStatus.value = '读取失败，请重新选择。' }
  reader.readAsText(file)
}
function onFile(e: Event) {
  readFile((e.target as HTMLInputElement).files?.[0])
}
function onDrop(e: DragEvent) {
  dragOver.value = false
  readFile(e.dataTransfer?.files?.[0])
}

onMounted(async () => {
  card.value = cardFromQuery(route.query)
  if (!card.value) {
    await router.replace(RX_HOME)
    return
  }
  document.title = '填写账号信息'
  const o = await ownPreview(card.value)
  if (o) {
    // 已提交过的码去看进度；从失败页点「重新提交」带 retry=1，留在填写页（后端确认卡台码可再用才会重新兑换）
    if (o.started && route.query.retry !== '1') {
      await router.replace({ path: RX_QUERY, query: { card: card.value } })
      return
    }
    own.value = true
    previewInfo.value = { plan: o.plan }
    if (o.error) error.value = o.error
    loading.value = false
    return
  }
  const stash = takePreview(card.value)
  if (stash) {
    token.value = stash.token
    previewInfo.value = stash.info
  } else {
    await runPreview()
  }
  loading.value = false
})
</script>
