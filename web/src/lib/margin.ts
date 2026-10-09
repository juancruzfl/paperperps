// Cross-margin math, mirrored from server/internal/engine/math.go so the UI can update
// equity and available margin live as marks move. The server stays authoritative.

import type { Me, Ticker } from './types'

export type RestingOrder = { buy: boolean; remaining: number; px: number }

export type CoinState = {
  coin: string
  size: number // signed
  entryPx: number
  leverage: number
  markPx: number
  orders: RestingOrder[]
}

export type Summary = {
  balance: number
  unrealizedPnl: number
  accountValue: number
  marginUsed: number
  orderMargin: number
  available: number
}

export const unrealizedPnl = (c: CoinState) => c.size * (c.markPx - c.entryPx)

export const positionMargin = (c: CoinState) => (Math.abs(c.size) * c.markPx) / c.leverage

/** Margin for resting orders: only the part that would grow the position counts. */
export function orderMargin(c: CoinState): number {
  let buyQty = 0
  let buyNtl = 0
  let sellQty = 0
  let sellNtl = 0
  for (const o of c.orders) {
    if (o.buy) {
      buyQty += o.remaining
      buyNtl += o.remaining * o.px
    } else {
      sellQty += o.remaining
      sellNtl += o.remaining * o.px
    }
  }
  const short = Math.max(0, -c.size)
  const long = Math.max(0, c.size)
  let m = 0
  if (buyQty > 0) m += (Math.max(0, buyQty - short) * (buyNtl / buyQty)) / c.leverage
  if (sellQty > 0) m += (Math.max(0, sellQty - long) * (sellNtl / sellQty)) / c.leverage
  return m
}

export function summarize(balance: number, coins: CoinState[]): Summary {
  let upnl = 0
  let used = 0
  let reserved = 0
  for (const c of coins) {
    upnl += unrealizedPnl(c)
    used += positionMargin(c)
    reserved += orderMargin(c)
  }
  const accountValue = balance + upnl
  return {
    balance,
    unrealizedPnl: upnl,
    accountValue,
    marginUsed: used,
    orderMargin: reserved,
    available: accountValue - used - reserved,
  }
}

/** Builds margin inputs from the account and live marks. */
export function coinStates(me: Me, tickers: Ticker[]): CoinState[] {
  const marks = new Map(tickers.map((t) => [t.coin, Number(t.markPx)]))
  const coins = new Map<string, CoinState>()
  const get = (coin: string): CoinState => {
    let c = coins.get(coin)
    if (!c) {
      c = { coin, size: 0, entryPx: 0, leverage: 1, markPx: 0, orders: [] }
      coins.set(coin, c)
    }
    return c
  }
  for (const p of me.positions) {
    const c = get(p.coin)
    c.size = Number(p.size)
    c.entryPx = Number(p.entryPx)
    c.leverage = Math.max(1, p.leverage)
  }
  for (const o of me.openOrders) {
    get(o.coin).orders.push({
      buy: o.side === 'buy',
      remaining: Number(o.sz) - Number(o.filledSz),
      px: Number(o.px),
    })
  }
  for (const c of coins.values()) c.markPx = marks.get(c.coin) || c.entryPx
  return [...coins.values()]
}

/**
 * Largest size (in coin units) an order can have: closing any opposite position is free,
 * and the rest needs initial margin plus the taker fee out of available margin.
 */
export function maxOrderSize(opts: {
  buy: boolean
  px: number
  leverage: number
  available: number
  positionSize: number
  feeRate: number
  szDecimals: number
}): number {
  const { buy, px, leverage, available, positionSize, feeRate, szDecimals } = opts
  if (px <= 0) return 0
  const closable = buy ? Math.max(0, -positionSize) : Math.max(0, positionSize)
  const opening = Math.max(0, available) / (px / leverage + px * feeRate)
  const step = 10 ** szDecimals
  return Math.floor((closable + opening) * step) / step
}
