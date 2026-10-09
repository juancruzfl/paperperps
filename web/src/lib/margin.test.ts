import { describe, expect, it } from 'vitest'
import { maxOrderSize, orderMargin, positionMargin, summarize, unrealizedPnl, type CoinState } from './margin'

// Same cases as TestMargin in server/internal/engine/math_test.go: the two must agree.
describe('margin mirrors the engine', () => {
  const btc: CoinState = { coin: 'BTC', size: 0.01, entryPx: 80000, leverage: 20, markPx: 82000, orders: [] }

  it('computes position PnL and margin', () => {
    expect(unrealizedPnl(btc)).toBeCloseTo(20)
    expect(positionMargin(btc)).toBeCloseTo(41)
  })

  it('reserves margin only for the part of an order that grows the position', () => {
    expect(orderMargin({ ...btc, orders: [{ buy: false, remaining: 0.03, px: 85000 }] })).toBeCloseTo(85)
    expect(
      orderMargin({
        ...btc,
        orders: [
          { buy: true, remaining: 0.01, px: 78000 },
          { buy: true, remaining: 0.01, px: 76000 },
        ],
      }),
    ).toBeCloseTo(77)
  })

  it('summarizes cross margin', () => {
    const withOrders: CoinState = {
      ...btc,
      orders: [
        { buy: true, remaining: 0.01, px: 78000 },
        { buy: true, remaining: 0.01, px: 76000 },
      ],
    }
    const eth: CoinState = { coin: 'ETH', size: -1, entryPx: 2500, leverage: 10, markPx: 2400, orders: [] }
    const s = summarize(1000, [withOrders, eth])
    expect(s.unrealizedPnl).toBeCloseTo(120)
    expect(s.accountValue).toBeCloseTo(1120)
    expect(s.marginUsed).toBeCloseTo(281)
    expect(s.orderMargin).toBeCloseTo(77)
    expect(s.available).toBeCloseTo(762)
  })
})

describe('maxOrderSize', () => {
  const base = { px: 80000, leverage: 20, available: 1000, positionSize: 0, feeRate: 0.00045, szDecimals: 5 }

  it('spends available margin on initial margin plus fee', () => {
    // 1000 / (4000 + 36) = 0.247770...
    expect(maxOrderSize({ ...base, buy: true })).toBe(0.24777)
  })

  it('lets an opposite position close for free on top', () => {
    expect(maxOrderSize({ ...base, buy: false, positionSize: 0.1 })).toBe(0.34777)
    expect(maxOrderSize({ ...base, buy: true, positionSize: 0.1 })).toBe(0.24777)
  })

  it('never goes negative', () => {
    expect(maxOrderSize({ ...base, buy: true, available: -50 })).toBe(0)
    expect(maxOrderSize({ ...base, buy: true, px: 0 })).toBe(0)
  })
})
