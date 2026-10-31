// 进度页的展示模型：把 /api/v1/public/cdk/result-by-code 返回的卡台订单 + 事件，
// 整理成固定 6 步 + 一句用户文案。事件流水本身不展示。纯函数，见 __tests__/progress.test.ts。

export type StepState = 'done' | 'active' | 'error' | 'todo'
export interface ProgressStep { text: string; state: StepState }

export interface ProgressView {
  found: boolean
  state: 'processing' | 'review' | 'success' | 'failed'
  statusText: string
  badge: string
  percent: number
  message: string
  steps: ProgressStep[]
  terminal: boolean
  canResubmit: boolean
  waitingForCard: boolean
  accountEmail: string
  plan: string
  product: string
  elapsedSeconds: number
  running: boolean
  invoiceUrl: string
}

const STEP_TEXTS = ['订单已创建', '验证账号信息', '创建充值订单', '正在填卡', '正在确认支付', '充值成功']
const STEP_PERCENT = [8, 22, 45, 68, 88, 100]
const STEP_VERB = ['处理', '验证账号信息', '创建充值订单', '填卡', '确认支付', '确认结果']

const CREATED = 0, VERIFY = 1, CHECKOUT = 2, PAYMENT = 3, CONFIRM = 4, SUCCESS = 5

const FAILED = new Set(['declined', 'failed_precharge', 'cancelled', 'failed'])

// 卡台事件 step → 展示步骤
const EVENT_STEP: Record<string, number> = {
  queued: VERIFY, credential_check: VERIFY,
  pricing: CHECKOUT, checkout: CHECKOUT,
  payment: PAYMENT,
  subscription: CONFIRM, invoice: CONFIRM, renewal: CONFIRM, reconcile: CONFIRM,
  completed: SUCCESS,
}

// 卡台订单 status → 至少到达的展示步骤
const STATUS_STEP: Record<string, number> = {
  queued: VERIFY, running: VERIFY,
  awaiting_card: CHECKOUT, funding_pending: CHECKOUT,
  dispatching: PAYMENT,
  pending: CONFIRM, requires_action: CONFIRM, plus_paid: CONFIRM,
  completed: SUCCESS,
}

function stageStep(stage: string): number {
  const s = stage.toLowerCase()
  if (!s) return CREATED
  if (/subscription|paid|invoice|reconcile/.test(s)) return CONFIRM
  if (/payment|spend|dispatch/.test(s)) return PAYMENT
  if (/checkout|card|fund|await|pricing/.test(s)) return CHECKOUT
  return VERIFY
}

const str = (v: unknown) => (v === null || v === undefined ? '' : String(v).trim())
const first = (...vals: unknown[]) => vals.map(str).find((v) => v && v !== 'null') || ''

/** 时间戳转毫秒：兼容 ISO 字符串、Unix 秒、Unix 毫秒（卡台 webhook 的 completed_at 是 Unix 秒）。 */
function time(v: unknown): number {
  const s = str(v)
  if (!s || s === 'null') return 0
  if (/^\d{9,13}$/.test(s)) {
    const n = Number(s)
    return s.length <= 10 ? n * 1000 : n
  }
  const t = Date.parse(s)
  return Number.isFinite(t) ? t : 0
}

/** 兼容 {code,msg,data:{order,events}}、{order,events} 与扁平结构。 */
export function splitPayload(payload: any): { order: any | null; events: any[] } {
  if (!payload || typeof payload !== 'object') return { order: null, events: [] }
  const root = payload.data && typeof payload.data === 'object' ? payload.data : payload
  let order = root.order && typeof root.order === 'object' ? root.order : payload.order && typeof payload.order === 'object' ? payload.order : null
  if (!order && str(root.status)) order = root
  let events = Array.isArray(root.events) ? root.events : Array.isArray(payload.events) ? payload.events : Array.isArray(order?.events) ? order.events : []
  events = events.filter((e: any) => e && typeof e === 'object').slice()
  events.sort((a: any, b: any) => time(a.created_at) - time(b.created_at))
  return { order, events }
}

function failureReason(order: any, events: any[]): string {
  const direct = first(order.user_message, order.message, order.failure_reason)
  if (direct) return direct
  for (let i = events.length - 1; i >= 0; i--) {
    const cat = str(events[i].category).toLowerCase()
    if ((cat === 'failed' || cat === 'error') && str(events[i].public_message)) return str(events[i].public_message)
  }
  for (let i = events.length - 1; i >= 0; i--) {
    if (str(events[i].public_message)) return str(events[i].public_message)
  }
  return '充值未成功，任务已结束'
}

function steps(active: number, allDone: boolean): ProgressStep[] {
  return STEP_TEXTS.map((text, i) => ({ text, state: allDone || i < active ? 'done' : i === active ? 'active' : 'todo' }))
}

/** 第一条把订单带进终态的事件时间（成功看 completed，失败看失败类事件）。 */
function terminalEventTime(events: any[], status: string): number {
  const hit = events.find((e) => {
    const to = str(e.to_status).toLowerCase()
    if (status === 'completed') return to === 'completed' || str(e.step).toLowerCase() === 'completed'
    const cat = str(e.category).toLowerCase()
    return FAILED.has(to) || cat === 'failed' || cat === 'error'
  })
  return hit ? time(hit.created_at) : 0
}

/** 去掉句末标点再接下一句，避免出现「。。」 */
const sentence = (s: string) => s.replace(/[。.！!]+$/, '')

export function buildProgressView(payload: any, now = Date.now()): ProgressView {
  const { order, events } = splitPayload(payload)
  const v: ProgressView = {
    found: !!order, state: 'processing', statusText: '', badge: '', percent: 0, message: '', steps: [],
    terminal: false, canResubmit: false, waitingForCard: false, accountEmail: '', plan: '', product: '',
    elapsedSeconds: 0, running: false, invoiceUrl: '',
  }
  if (!order) return v

  const status = str(order.status).toLowerCase()
  const cdkStatus = str(order.cdk_status).toLowerCase()
  v.accountEmail = first(order.account_email, order.email, payload?.account_email)
  v.plan = first(order.plan, order.plan_type)
  v.product = first(order.product)
  const invoice = first(payload?.invoice_url)
  if (invoice.startsWith('https://')) v.invoiceUrl = invoice

  // 当前步骤：订单状态、阶段、历史事件取最远，订单「重新进入支付队列」时进度不倒退。
  let idx = Math.max(VERIFY, STATUS_STEP[status] ?? 0, stageStep(str(order.stage)))
  for (const e of events) idx = Math.max(idx, EVENT_STEP[str(e.step).toLowerCase()] ?? 0)
  if (status !== 'completed' && idx >= SUCCESS) idx = CONFIRM // 没到 completed 不显示成功

  if (status === 'completed') {
    Object.assign(v, { state: 'success', statusText: '充值成功', badge: '100%', percent: 100, terminal: true, steps: steps(SUCCESS, true) })
    v.message = '已充值成功，请到账号中确认套餐。'
  } else if (FAILED.has(status)) {
    const reason = failureReason(order, events)
    Object.assign(v, { state: 'failed', statusText: '充值未成功', badge: status === 'cancelled' ? '已取消' : '未完成', terminal: true })
    v.steps = [{ text: '订单已创建', state: 'done' }, { text: reason, state: 'error' }]
    if (cdkStatus === 'unused') {
      v.canResubmit = true
      v.message = sentence(reason) + '。原卡密仍可使用，可以重新提交。'
    } else if (cdkStatus === 'review' || cdkStatus === 'reserved') {
      v.message = sentence(reason) + '。正在核实扣款情况，请勿重复提交。'
    } else {
      v.message = sentence(reason) + '。如需帮助请联系发码方。'
    }
  } else if (status.includes('review')) {
    const msg = '正在核对支付结果，请勿重复提交或付款。'
    Object.assign(v, { state: 'review', statusText: '核对中', badge: '待核对', percent: STEP_PERCENT[CONFIRM], message: msg })
    v.steps = [{ text: '订单已创建', state: 'done' }, { text: msg, state: 'active' }]
  } else {
    v.statusText = '处理中'
    v.percent = STEP_PERCENT[idx]
    v.badge = v.percent + '%'
    v.steps = steps(idx, false)
    v.waitingForCard = status === 'awaiting_card' || status === 'funding_pending'
    v.message = v.waitingForCard ? '正在等待可用支付资源，准备好后会自动继续。' : `正在${STEP_VERB[idx]}，请保持页面打开。`
  }

  // 计时：从订单创建算起，结束态停在「订单进入终态」的那一刻。
  // ★不能用最后一条事件★：成功后卡台还会追加取消自动续费、对账等事件，
  // 刷新页面时耗时会被拉长（实测 33 秒的单刷新后显示 1 分多钟）。
  const start = time(order.created_at) || (events.length ? time(events[0].created_at) : 0)
  let end = now
  if (v.terminal) {
    end = time(order.completed_at) || time(order.finished_at) || terminalEventTime(events, status) ||
      (events.length ? time(events[events.length - 1].created_at) : 0) || time(order.updated_at)
  } else {
    v.running = true
  }
  if (start && end > start) v.elapsedSeconds = Math.floor((end - start) / 1000)
  return v
}
