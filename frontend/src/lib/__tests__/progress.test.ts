import { describe, expect, it } from 'vitest'

import { buildProgressView } from '../../views/redeem/progress'

const states = (v: ReturnType<typeof buildProgressView>) => v.steps.map((s) => s.state[0]).join('')

describe('buildProgressView', () => {
  it('已完成：6 步全勾、计时停在最后一条事件', () => {
    const v = buildProgressView({
      code: 0,
      data: {
        order: { status: 'completed', account_email: 'a@b.com', created_at: '2026-10-26T11:58:15Z' },
        events: [
          { step: 'completed', created_at: '2026-10-26T11:58:43Z' },
          { step: 'queued', created_at: '2026-10-26T11:58:15Z' },
        ],
      },
      invoice_url: 'https://invoice.stripe.com/i/x',
    })
    expect(v.state).toBe('success')
    expect(states(v)).toBe('dddddd')
    expect(v.elapsedSeconds).toBe(28)
    expect(v.running).toBe(false)
    expect(v.accountEmail).toBe('a@b.com')
    expect(v.invoiceUrl).toBe('https://invoice.stripe.com/i/x')
  })

  it('重新进入支付队列时进度不倒退', () => {
    const v = buildProgressView(
      { data: { order: { status: 'awaiting_card', stage: 'cdk_accepted', created_at: '2026-10-26T11:58:15Z' }, events: [{ step: 'payment' }] } },
      Date.parse('2026-10-26T11:59:15Z'),
    )
    expect(v.state).toBe('processing')
    expect(states(v)).toBe('dddatt')
    expect(v.waitingForCard).toBe(true)
    expect(v.running).toBe(true)
    expect(v.elapsedSeconds).toBe(60)
  })

  it('没到 completed 不显示成功', () => {
    const v = buildProgressView({ order: { status: 'running' }, events: [{ step: 'completed' }] })
    expect(v.state).toBe('processing')
    expect(v.steps[5].state).toBe('todo')
  })

  it('失败且卡密可用：给出原因与重新提交', () => {
    const v = buildProgressView({
      data: {
        order: { status: 'failed', cdk_status: 'unused' },
        events: [{ step: 'payment', category: 'failed', public_message: '套餐和账单均未显示支付成功，订单已失败。' }],
      },
    })
    expect(v.state).toBe('failed')
    expect(v.canResubmit).toBe(true)
    expect(v.steps).toHaveLength(2)
    expect(v.steps[1]).toEqual({ text: '套餐和账单均未显示支付成功，订单已失败。', state: 'error' })
    expect(v.message).toContain('原卡密仍可使用')
    expect(v.message).not.toContain('。。')
  })

  it('失败且卡密待核验：不可重新提交', () => {
    const v = buildProgressView({ order: { status: 'declined', cdk_status: 'review', message: '银行拒付' } })
    expect(v.canResubmit).toBe(false)
    expect(v.steps[1].text).toBe('银行拒付')
    expect(v.message).toContain('请勿重复提交')
  })

  it('review 状态', () => {
    const v = buildProgressView({ order: { status: 'card_recharge_review' } })
    expect(v.state).toBe('review')
    expect(v.terminal).toBe(false)
  })

  it('没有订单', () => {
    expect(buildProgressView({ code: 0, data: {} }).found).toBe(false)
    expect(buildProgressView(null).found).toBe(false)
  })
})
