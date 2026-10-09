// Margin math, mirrored from server/internal/engine/math.go so the UI can update equity,
// available margin and liquidation prices live as marks move. The server stays
// authoritative; margin.test.ts uses the same cases as the Go tests.

import type { Me, Ticker } from './types'

export type RestingOrder = { buy: boolean; remaining: number; px: number }

export type CoinState = {
  coin: string
  size: number // signed
  entryPx: number
  leverage: number
  markPx: number
  orders: RestingOrder[]
  isolated: boolean
  isolatedMargin: number
  maintenanceRate: number // 1 / (2 · max leverage)
}

export type Summary = {
  balance: number
  unrealizedPnl: number
  accountValue: number
  crossAccountValue: number
  marginUsed: number // cross positions' initial margin
  crossMaintenance: number
  isolatedMargin: number
  orderMargin: number
  available: number
}

export const maintenanceRate = (maxLeverage: number) => 1 / (2 * Math.max(1, maxLeverage))

export const maintenanceMargin = (c: CoinState) => Math.abs(c.size) * c.markPx * c.maintenanceRate

export const isolatedEquity = (c: CoinState) => c.isolatedMargin + unrealizedPnl(c)

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
  let crossUpnl = 0
  let used = 0
  let maintenance = 0
  let isolated = 0
  let reserved = 0
  for (const c of coins) {
    const p = unrealizedPnl(c)
    upnl += p
    reserved += orderMargin(c)
    if (c.isolated) {
      isolated += c.isolatedMargin
      continue
    }
    crossUpnl += p
    used += positionMargin(c)
    maintenance += maintenanceMargin(c)
  }
  const crossAccountValue = balance - isolated + crossUpnl
  return {
    balance,
    unrealizedPnl: upnl,
    accountValue: balance + upnl,
    crossAccountValue,
    marginUsed: used,
    crossMaintenance: maintenance,
    isolatedMargin: isolated,
    orderMargin: reserved,
    available: crossAccountValue - used - reserved,
  }
}

/**
 * Mark price at which the position on c would be liquidated, holding every other position's
 * mark fixed. Solves equity(m) = maintenance(m), where the margin behind the position (M) is
 * its isolated margin, or for cross the cross equity without this position's PnL minus the
 * other cross positions' maintenance:
 *
 *   m = (size·entry − M) / (size − |size|·rate)
 *
 * Returns null when there's no position or no positive solution (it can't be liquidated).
 */
export function liquidationPrice(c: CoinState, s: Summary): number | null {
  if (c.size === 0) return null
  const backing = c.isolated
    ? c.isolatedMargin
    : s.crossAccountValue - unrealizedPnl(c) - (s.crossMaintenance - maintenanceMargin(c))
  const denom = c.size - Math.abs(c.size) * c.maintenanceRate
  if (denom === 0) return null
  const m = (c.size * c.entryPx - backing) / denom
  return m > 0 && Number.isFinite(m) ? m : null
}

/** Mirrors IsolatedMarginAfter in Go. */
export function isolatedMarginAfter(oldSize: number, oldMargin: number, newSize: number, px: number, leverage: number): number {
  if (newSize === 0) return 0
  if (oldSize === 0 || Math.sign(newSize) !== Math.sign(oldSize)) return (Math.abs(newSize) * px) / leverage
  if (Math.abs(newSize) > Math.abs(oldSize)) return oldMargin + ((Math.abs(newSize) - Math.abs(oldSize)) * px) / leverage
  return (oldMargin * Math.abs(newSize)) / Math.abs(oldSize)
}

/**
 * Estimated liquidation price if an order for sz (buy or sell) filled at px on coin,
 * paying fee. Realized PnL from a reducing fill is ignored; it's an estimate.
 */
export function projectedLiquidationPrice(
  coins: CoinState[],
  balance: number,
  coin: string,
  buy: boolean,
  sz: number,
  px: number,
  fee: number,
): number | null {
  const states = coins.map((c) => ({ ...c, orders: [...c.orders] }))
  const c = states.find((x) => x.coin === coin)
  if (!c || sz <= 0 || px <= 0) return null
  const signed = buy ? sz : -sz
  const newSize = c.size + signed
  let entry = c.entryPx
  if (c.size === 0 || Math.sign(c.size) === Math.sign(signed)) {
    entry = (Math.abs(c.size) * c.entryPx + sz * px) / Math.abs(newSize)
  } else if (newSize !== 0 && Math.sign(newSize) === Math.sign(signed)) {
    entry = px
  }
  if (c.isolated) c.isolatedMargin = isolatedMarginAfter(c.size, c.isolatedMargin, newSize, px, c.leverage)
  c.size = newSize
  c.entryPx = entry
  return liquidationPrice(c, summarize(balance - fee, states))
}

/** Builds margin inputs from the account and live marks. */
export function coinStates(me: Me, tickers: Ticker[]): CoinState[] {
  const byCoin = new Map(tickers.map((t) => [t.coin, t]))
  const coins = new Map<string, CoinState>()
  const get = (coin: string): CoinState => {
    let c = coins.get(coin)
    if (!c) {
      c = { coin, size: 0, entryPx: 0, leverage: 1, markPx: 0, orders: [], isolated: false, isolatedMargin: 0, maintenanceRate: 0 }
      coins.set(coin, c)
    }
    return c
  }
  for (const p of me.positions) {
    const c = get(p.coin)
    c.size = Number(p.size)
    c.entryPx = Number(p.entryPx)
    c.leverage = Math.max(1, p.leverage)
    c.isolated = p.marginMode === 'isolated'
    c.isolatedMargin = Number(p.isolatedMargin)
  }
  for (const o of me.openOrders) {
    get(o.coin).orders.push({
      buy: o.side === 'buy',
      remaining: Number(o.sz) - Number(o.filledSz),
      px: Number(o.px),
    })
  }
  for (const c of coins.values()) {
    const t = byCoin.get(c.coin)
    c.markPx = Number(t?.markPx) || c.entryPx
    c.maintenanceRate = maintenanceRate(t?.maxLeverage ?? c.leverage)
  }
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
