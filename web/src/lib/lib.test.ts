import { describe, expect, it } from 'vitest'
import { computeDepth } from './book'
import { applyTrade, toBar } from './candles'
import { change24h, formatPct, formatUsd, fundingCountdown, pxDecimals } from './format'
import type { Trade } from './types'

describe('pxDecimals', () => {
  it('shows 5 significant figures', () => {
    expect(pxDecimals(82079, 5)).toBe(0) // BTC
    expect(pxDecimals(2472.5, 4)).toBe(1) // ETH
    expect(pxDecimals(108.6, 2)).toBe(2) // SOL
  })
  it('caps decimals at 6 - szDecimals', () => {
    expect(pxDecimals(0.21234, 0)).toBe(5) // DOGE
    expect(pxDecimals(0.0012345, 0)).toBe(6)
    expect(pxDecimals(0.0012345, 2)).toBe(4)
  })
  it('handles bad input', () => {
    expect(pxDecimals(0, 2)).toBe(4)
    expect(pxDecimals(NaN, 5)).toBe(1)
  })
})

describe('formatting', () => {
  it('formats signed percentages', () => {
    expect(formatPct(0.0123)).toBe('+1.23%')
    expect(formatPct(-0.0005739396, 4)).toBe('-0.0574%')
    expect(formatPct(0)).toBe('0.00%')
  })
  it('formats dollars with the sign in front', () => {
    expect(formatUsd(1234.567)).toBe('$1,234.57')
    expect(formatUsd(-10)).toBe('-$10.00')
    expect(formatUsd(10, { signed: true })).toBe('+$10.00')
    expect(formatUsd(-0.002, { signed: true })).toBe('$0.00')
  })
  it('computes 24h change', () => {
    const { abs, ratio } = change24h('82137', '82564')
    expect(abs).toBe(-427)
    expect(ratio).toBeCloseTo(-0.005172, 5)
    expect(change24h('1', '0')).toEqual({ abs: 0, ratio: 0 })
  })
  it('counts down to the next hour', () => {
    expect(fundingCountdown(Date.UTC(2026, 9, 9, 12, 59, 30))).toBe('00:30')
    expect(fundingCountdown(Date.UTC(2026, 9, 9, 12, 0, 0))).toBe('60:00')
  })
})

describe('computeDepth', () => {
  const book = {
    coin: 'BTC',
    time: 0,
    bids: [
      { px: '100', sz: '1', n: 1 },
      { px: '99', sz: '3', n: 1 },
      { px: '98', sz: '5', n: 1 },
    ],
    asks: [
      { px: '101', sz: '2', n: 1 },
      { px: '102', sz: '2', n: 1 },
    ],
  }

  it('accumulates outward from the spread and scales against both sides', () => {
    const d = computeDepth(book, 2)
    expect(d.bids.map((r) => r.total)).toEqual([1, 4])
    expect(d.asks.map((r) => r.total)).toEqual([2, 4])
    expect(d.bids[1].depth).toBe(1)
    expect(d.asks[0].depth).toBe(0.5)
    expect(d.spread).toBe(1)
    expect(d.spreadRatio).toBeCloseTo(1 / 100.5)
  })

  it('handles an empty side', () => {
    const d = computeDepth({ ...book, asks: [] }, 10)
    expect(d.spread).toBeNull()
    expect(d.bids).toHaveLength(3)
  })
})

describe('applyTrade', () => {
  const minute = 60_000
  const trade = (time: number, px: string, sz = '1'): Trade => ({
    coin: 'BTC',
    side: 'B',
    px,
    sz,
    time,
    tid: time,
    hash: '',
  })
  const last = toBar({ t: 600_000, T: 659_999, o: '100', h: '105', l: '99', c: '101', v: '2', n: 3 })

  it('updates the current bar', () => {
    expect(applyTrade(last, trade(630_000, '106', '0.5'), minute)).toEqual({
      time: 600,
      open: 100,
      high: 106,
      low: 99,
      close: 106,
      volume: 2.5,
    })
  })

  it('starts a new bar in the next interval', () => {
    expect(applyTrade(last, trade(661_000, '98'), minute)).toEqual({
      time: 660,
      open: 98,
      high: 98,
      low: 98,
      close: 98,
      volume: 1,
    })
  })

  it('ignores trades older than the last bar', () => {
    expect(applyTrade(last, trade(500_000, '1'), minute)).toBeNull()
  })

  it('starts the first bar when there is no history', () => {
    expect(applyTrade(null, trade(61_000, '5'), minute)?.time).toBe(60)
  })
})

describe('trigger direction mirrors the engine', () => {
  it('fires stops against the position and take-profits with it', async () => {
    const { triggerDirection } = await import('./orders')
    expect(triggerDirection('stop_market', 'sell')).toBe('≤') // stop-loss on a long
    expect(triggerDirection('stop_market', 'buy')).toBe('≥') // stop-loss on a short
    expect(triggerDirection('take_profit_market', 'sell')).toBe('≥') // take-profit on a long
    expect(triggerDirection('take_profit_limit', 'buy')).toBe('≤') // take-profit on a short
  })
})

describe('chart lines', () => {
  it('marks the position, its liquidation price and orders on the coin', async () => {
    const { chartLines } = await import('./chartLines')
    const me = {
      balance: '985.24',
      positions: [{ coin: 'BTC', size: '0.4', entryPx: '82000', leverage: 40, marginMode: 'cross', isolatedMargin: '0' }],
      openOrders: [
        { id: 'a', coin: 'BTC', side: 'sell', type: 'take_profit_market', px: null, triggerPx: '85000', sz: '0.4', filledSz: '0', reduceOnly: true, parentOrderId: null, status: 'open' },
        { id: 'b', coin: 'BTC', side: 'buy', type: 'limit', px: '80000', triggerPx: null, sz: '0.01', filledSz: '0', reduceOnly: false, parentOrderId: null, status: 'open' },
        { id: 'c', coin: 'ETH', side: 'buy', type: 'limit', px: '2000', triggerPx: null, sz: '1', filledSz: '0', reduceOnly: false, parentOrderId: null, status: 'open' },
      ],
    } as unknown as import('./types').Me
    const tickers = [{ coin: 'BTC', markPx: '81995', szDecimals: 5, maxLeverage: 40 }] as unknown as import('./types').Ticker[]
    const lines = chartLines(me, tickers, 'BTC')
    expect(lines.map((l) => l.title)).toEqual(['Long 0.4', 'Liq.', 'TP', 'Buy 0.01'])
    expect(lines[1].price).toBe(80544) // ≈ 80,543.70, rounded to BTC's whole-dollar ticks
    expect(chartLines(null, tickers, 'BTC')).toEqual([])
  })
})
