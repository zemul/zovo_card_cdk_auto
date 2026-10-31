// 新版兑换前台共用：路径、设备标识、请求封装、卡密参数解析、凭证校验。

/** 新版页面的路径前缀；改这里时同步改 router/index.ts 里的三条路由。 */
export const RX_BASE = '/v2'
export const RX_HOME = RX_BASE
export const RX_FILL = RX_BASE + '/redeem'
export const RX_QUERY = RX_BASE + '/query'

/** 与旧版兑换页共用同一个设备标识，卡台四步要求同一设备。 */
export const deviceId = (() => {
  const k = 'cdk_device_id'
  let v = localStorage.getItem(k)
  if (!v) {
    v = 'web-' + Math.random().toString(36).slice(2) + Date.now().toString(36)
    localStorage.setItem(k, v)
  }
  return v
})()

export async function api(path: string, init: RequestInit = {}) {
  const headers = new Headers(init.headers || {})
  headers.set('X-Redemption-Device', deviceId)
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  let r: Response
  try {
    r = await fetch(path, { ...init, headers, credentials: 'include', cache: 'no-store' })
  } catch {
    return { ok: false, status: 0, data: { error: '网络异常，请检查网络后重试' } as any }
  }
  const text = await r.text()
  let data: any = null
  try { data = text ? JSON.parse(text) : null } catch { data = { raw: text } }
  // 卡台 envelope：HTTP 200 也可能带非零 code
  const ok = r.ok && !(data && typeof data.code === 'number' && data.code !== 0)
  return { ok, status: r.status, data }
}

export function errorText(data: any, fallback: string) {
  return String(data?.error || data?.msg || data?.message || fallback)
}

const CARD_PARAMS = ['card', 'cdk', 'code', 'card_key']

/** 从地址栏 query 取卡密。 */
export function cardFromQuery(query: Record<string, any>): string {
  for (const k of CARD_PARAMS) {
    const v = query[k]
    const s = Array.isArray(v) ? v[0] : v
    if (s && String(s).trim()) return String(s).trim().toUpperCase()
  }
  return ''
}

/** 输入框里既可以填卡密，也可以粘贴整条进度链接。 */
export function extractCardInput(value: string): string {
  const text = String(value || '').trim()
  if (!text) return ''
  if (/^https?:\/\/|[?&](card|cdk|code|card_key)=/i.test(text)) {
    try {
      const url = new URL(text.startsWith('?') ? location.origin + '/' + text : text, location.origin)
      for (const k of CARD_PARAMS) {
        const v = url.searchParams.get(k)
        if (v && v.trim()) return v.trim().toUpperCase()
      }
    } catch { /* 当作卡密 */ }
  }
  return text.toUpperCase()
}

/** 首页 preview 成功后暂存兑换会话，填写页直接用，不再重复 preview（卡台会话 15 分钟有效）。 */
const PREVIEW_PREFIX = 'rx_preview:'
const PREVIEW_MAX_AGE = 12 * 60 * 1000

export function stashPreview(code: string, token: string, info: any) {
  try {
    sessionStorage.setItem(PREVIEW_PREFIX + code, JSON.stringify({ token, info, at: Date.now() }))
    const plan = planFromPreview(info)
    if (plan) localStorage.setItem(PLAN_PREFIX + code, plan)
  } catch { /* ignore */ }
}

/** 进度页「充值产品」：卡台结果不一定带档位，用本浏览器 preview 时记下的。 */
const PLAN_PREFIX = 'rx_plan:'
export function rememberedPlan(code: string): string {
  try { return localStorage.getItem(PLAN_PREFIX + code) || '' } catch { return '' }
}

/** 进度页「充值账号」：卡台公开结果不带邮箱，用本浏览器预检时看到的（与旧兑换页同一做法）。 */
const EMAIL_PREFIX = 'rx_email:'
export function rememberEmail(code: string, email: string) {
  try { if (email) localStorage.setItem(EMAIL_PREFIX + code, email) } catch { /* ignore */ }
}
export function rememberedEmail(code: string): string {
  try { return localStorage.getItem(EMAIL_PREFIX + code) || '' } catch { return '' }
}

export function takePreview(code: string): { token: string; info: any } | null {
  try {
    const raw = sessionStorage.getItem(PREVIEW_PREFIX + code)
    if (!raw) return null
    const p = JSON.parse(raw)
    if (!p?.token || Date.now() - Number(p.at || 0) > PREVIEW_MAX_AGE) return null
    return { token: String(p.token), info: p.info || {} }
  } catch {
    return null
  }
}

export function clearPreview(code: string) {
  try { sessionStorage.removeItem(PREVIEW_PREFIX + code) } catch { /* ignore */ }
}

/** 刚提交兑换的卡密：进度页在卡台订单可查之前先显示「已提交」，不报「未找到」。 */
const SUBMITTED_PREFIX = 'rx_submitted:'
export function markSubmitted(code: string) {
  try { sessionStorage.setItem(SUBMITTED_PREFIX + code, String(Date.now())) } catch { /* ignore */ }
}
export function recentlySubmitted(code: string, withinMs = 60_000): boolean {
  try { return Date.now() - Number(sessionStorage.getItem(SUBMITTED_PREFIX + code) || 0) < withinMs } catch { return false }
}

/** preview 结果 → 档位键（Grok / X 码补产品前缀，与旧版兑换页一致）。 */
export function planFromPreview(info: any): string {
  const plan = String(info?.plan || info?.plan_type || '').toLowerCase()
  const product = String(info?.product || '').toLowerCase()
  if (plan && product === 'grok' && !plan.startsWith('grok_')) return 'grok_' + plan
  if (plan && product === 'x' && !plan.startsWith('x_')) return 'x_' + plan
  return plan
}

/** preview 失败文案是否表示卡密已经用过 / 在处理中。 */
export function looksUsed(msg: string): boolean {
  return /已兑换|已使用|已消耗|处理中|reserved|consumed|redeemed|used/i.test(msg)
}

/** ChatGPT 完整 Session：必须含 sessionToken；纯 Access Token 拒绝。与旧版兑换页同一规则。 */
export function extractSession(raw: string): string {
  const s = raw.trim()
  if (!s) return ''
  if (!s.startsWith('{') && s.split('.').length >= 5) return s
  if (!s.startsWith('{') && s.startsWith('eyJ') && s.split('.').length === 3) return ''
  if (s.startsWith('{')) {
    try {
      const o = JSON.parse(s)
      const st = String(o.sessionToken || o.session_token || o.token?.sessionToken || '').trim()
      return st ? s : ''
    } catch {
      return ''
    }
  }
  return s.length > 40 ? s : ''
}

export function formatElapsed(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${pad(m)}:${pad(sec)}`
}
