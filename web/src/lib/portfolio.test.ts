import { describe, expect, it } from 'vitest'
import { activityInWindow, equityWindow } from './portfolio'
import type { Fill, FundingPayment } from './types'

const now = Date.parse('2026-10-11T12:00:00Z')
const ago = (hours: number) => new Date(now - hours * 3600_000).toISOString()

const fill = (hours: number, sz: number, px: number, fee: number): Fill => ({
  id: String(hours),
  orderId: null,
  coin: 'BTC',
  side: 'buy',
  px: String(px),
  sz: String(sz),
  fee: String(fee),
  liquidity: 'taker',
  closedPnl: '0',
  isLiquidation: false,
  createdAt: ago(hours),
  note: '',
})

const payment = (hours: number, amount: number): FundingPayment => ({
  coin: 'BTC',
  fundingHour: ago(hours),
  size: '1',
  rate: '0.0001',
  oraclePx: '100',
  amount: String(amount),
})

describe('equityWindow', () => {
  const history = [
    { t: ago(72), equity: '1000', cumulativePnl: '0' },
    { t: ago(30), equity: '1100', cumulativePnl: '100' },
    { t: ago(2), equity: '1050', cumulativePnl: '50' },
  ]

  it('starts a range at the value before it', () => {
    const points = equityWindow(history, '24h', now)
    expect(points.map((p) => Number(p.cumulativePnl))).toEqual([1100, 1050])
    expect(points[0].t).toBe(ago(24))
  })

  it('keeps everything for all time', () => {
    expect(equityWindow(history, 'all', now)).toHaveLength(3)
  })
})

describe('activityInWindow', () => {
  const fills = [fill(1, 2, 100, 0.5), fill(10, 1, 50, 0.25), fill(48, 5, 10, 1)]
  const funding = [payment(3, -0.1), payment(30, 2)]

  it('sums the rows inside the range', () => {
    expect(activityInWindow(fills, funding, '24h', 200, now)).toEqual({
      trades: 2,
      volume: 250,
      fees: 0.75,
      funding: -0.1,
      partial: false,
    })
  })

  it('flags a range the history may not fully cover', () => {
    expect(activityInWindow(fills, funding, 'all', 3, now).partial).toBe(true)
    expect(activityInWindow(fills, funding, '24h', 3, now).partial).toBe(false)
  })
})
