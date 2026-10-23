import { describe, expect, it } from 'vitest'
import {
  isolatedEquity,
  isolatedMarginAfter,
  liquidationPrice,
  maintenanceMargin,
  maxOrderSize,
  orderMargin,
  positionMaintenanceRate,
  positionMargin,
  projectedLiquidationPrice,
  score,
  summarize,
  unrealizedPnl,
  type CoinState,
} from './margin'

// Same cases as TestMargin in server/internal/engine/math_test.go: the two must agree.
describe('margin mirrors the engine', () => {
  const base = { orders: [], isolated: false, isolatedMargin: 0, maintenanceRate: 0.0125 }
  const btc: CoinState = { ...base, coin: 'BTC', size: 0.01, entryPx: 80000, leverage: 20, markPx: 82000 }

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
    const eth: CoinState = { ...base, coin: 'ETH', size: -1, entryPx: 2500, leverage: 10, markPx: 2400 }
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

// Same cases as TestSummarizeWithIsolated and the liquidation replays in risk_test.go.
describe('isolated margin and liquidation', () => {
  const btc: CoinState = {
    coin: 'BTC', size: 0.01, entryPx: 80000, leverage: 20, markPx: 82000, orders: [],
    isolated: false, isolatedMargin: 0, maintenanceRate: 0.0125,
  }
  const eth: CoinState = {
    coin: 'ETH', size: -1, entryPx: 2500, leverage: 10, markPx: 2400, orders: [],
    isolated: true, isolatedMargin: 250, maintenanceRate: 0.02,
  }

  it('keeps isolated margin out of cross', () => {
    const s = summarize(1000, [btc, eth])
    expect(s.accountValue).toBeCloseTo(1120)
    expect(s.isolatedMargin).toBeCloseTo(250)
    expect(s.crossAccountValue).toBeCloseTo(770)
    expect(s.marginUsed).toBeCloseTo(41)
    expect(s.crossMaintenance).toBeCloseTo(10.25)
    expect(s.available).toBeCloseTo(729)
    expect(isolatedEquity(eth)).toBeCloseTo(350)
    expect(maintenanceMargin(eth)).toBeCloseTo(48)
  })

  it('finds the cross liquidation price of the 40x BTC long', () => {
    // 0.4 BTC at 82,000, balance 985.24 → liquidates below ≈ 80,543.70.
    const big: CoinState = { ...btc, size: 0.4, entryPx: 82000, leverage: 40, markPx: 81995 }
    expect(liquidationPrice(big, summarize(985.24, [big]))).toBeCloseTo(80543.696, 2)
  })

  it('finds the isolated liquidation price of the 25x ETH long', () => {
    const iso: CoinState = { ...eth, size: 4, entryPx: 2500, leverage: 25, markPx: 2499.95, isolatedMargin: 400 }
    expect(liquidationPrice(iso, summarize(995.5, [iso]))).toBeCloseTo(2448.98, 2)
  })

  it('finds a short liquidation price above entry', () => {
    // Isolated short 1 @ 2,500 with 250 margin at 2%: m = (2,500 + 250) / 1.02
    expect(liquidationPrice({ ...eth, markPx: 2500 }, summarize(1000, [eth]))).toBeCloseTo(2696.08, 2)
  })

  it('has no liquidation price without a position, or when fully collateralized', () => {
    expect(liquidationPrice({ ...btc, size: 0 }, summarize(1000, [btc]))).toBeNull()
    const tiny: CoinState = { ...btc, size: 0.001, entryPx: 80000, leverage: 1 }
    expect(liquidationPrice(tiny, summarize(100000, [tiny]))).toBeNull()
  })

  it('projects the liquidation price of a new order', () => {
    const flat: CoinState = { ...btc, size: 0, entryPx: 0, leverage: 40, markPx: 82000 }
    // Same trade as the replay: buy 0.4 at 82,000 paying 14.76.
    expect(projectedLiquidationPrice([flat], 1000, 'BTC', true, 0.4, 82000, 14.76)).toBeCloseTo(80543.696, 1)
  })

  it('mirrors IsolatedMarginAfter', () => {
    expect(isolatedMarginAfter(0, 0, 0.01, 82000, 20)).toBeCloseTo(41)
    expect(isolatedMarginAfter(0.01, 41, 0.03, 84000, 20)).toBeCloseTo(125)
    expect(isolatedMarginAfter(0.03, 125, 0.01, 85000, 20)).toBeCloseTo(41.666667, 5)
    expect(isolatedMarginAfter(0.01, 41, 0, 85000, 20)).toBe(0)
    expect(isolatedMarginAfter(0.01, 41, -0.02, 80000, 20)).toBeCloseTo(80)
  })
})

describe('score mirrors engine.Score', () => {
  it('keeps PnL from runs ended by resets', () => {
    // TestResetLocksPnLAndHasCooldown: after the reset, prior -10.419 on a fresh 1,000.
    expect(score(-10.419, 1000, 0, 1000)).toBeCloseTo(-10.419)
    expect(score(-10.419, 1030, 5, 1000)).toBeCloseTo(24.581)
  })
})

describe('positionMaintenanceRate', () => {
  it('uses the market cap up to it, and the position’s own leverage above it (degen mode)', () => {
    expect(positionMaintenanceRate(40, 20)).toBe(0.0125)
    expect(positionMaintenanceRate(40, 1000)).toBe(0.0005)
  })
})
