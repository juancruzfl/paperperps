import type { Candle, Trade } from './types'

/** A chart bar. time is the bar's open time in unix seconds, as lightweight-charts wants. */
export type Bar = {
  time: number
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export const INTERVALS = {
  '1m': 60_000,
  '5m': 300_000,
  '15m': 900_000,
  '1h': 3_600_000,
  '4h': 14_400_000,
  '1d': 86_400_000,
} as const

export type Interval = keyof typeof INTERVALS

export function toBar(c: Candle): Bar {
  return {
    time: c.t / 1000,
    open: Number(c.o),
    high: Number(c.h),
    low: Number(c.l),
    close: Number(c.c),
    volume: Number(c.v),
  }
}

/**
 * Folds a trade into the last bar, or starts a new bar if the trade falls in a later
 * interval. Returns null for trades older than the last bar, which history already covers.
 * Mirrors how Hyperliquid builds candles from trades.
 */
export function applyTrade(last: Bar | null, trade: Trade, intervalMs: number): Bar | null {
  const px = Number(trade.px)
  const sz = Number(trade.sz)
  const open = (Math.floor(trade.time / intervalMs) * intervalMs) / 1000
  if (last && open < last.time) return null
  if (last && open === last.time) {
    return {
      ...last,
      high: Math.max(last.high, px),
      low: Math.min(last.low, px),
      close: px,
      volume: last.volume + sz,
    }
  }
  return { time: open, open: px, high: px, low: px, close: px, volume: sz }
}
