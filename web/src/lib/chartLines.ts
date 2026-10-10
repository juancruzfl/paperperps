// Price lines drawn on the chart for the account's position and orders on one coin.

import { pxDecimals } from './format'
import { coinStates, liquidationPrice, summarize } from './margin'
import { isTrigger } from './orders'
import type { Me, Ticker } from './types'

export type PriceLine = {
  price: number
  title: string
  color: string
  style: 'solid' | 'dashed' | 'dotted'
}

const GREEN = '#3ddc97'
const RED = '#ff5c7a'
const ORANGE = '#f5b942'
const GREY = '#c9d3dc'

export function chartLines(me: Me | null, tickers: Ticker[], coin: string): PriceLine[] {
  if (!me) return []
  const lines: PriceLine[] = []
  const states = coinStates(me, tickers)
  const c = states.find((s) => s.coin === coin)
  const szDec = tickers.find((t) => t.coin === coin)?.szDecimals ?? 4
  const round = (px: number) => Number(px.toFixed(pxDecimals(px, szDec)))

  if (c && c.size !== 0) {
    const long = c.size > 0
    lines.push({ price: round(c.entryPx), title: `${long ? 'Long' : 'Short'} ${Math.abs(c.size)}`, color: GREY, style: 'solid' })
    const liq = liquidationPrice(c, summarize(Number(me.balance), states))
    if (liq !== null) lines.push({ price: round(liq), title: 'Liq.', color: ORANGE, style: 'dashed' })
  }

  const pendingParents = new Set(me.openOrders.map((o) => o.id))
  for (const o of me.openOrders) {
    if (o.coin !== coin) continue
    const remaining = Number(o.sz) - Number(o.filledSz)
    if (isTrigger(o.type) && o.status === 'open' && o.triggerPx) {
      const tp = o.type.startsWith('take_profit')
      const waiting = o.parentOrderId !== null && pendingParents.has(o.parentOrderId)
      lines.push({
        price: Number(o.triggerPx),
        title: `${tp ? 'TP' : o.reduceOnly ? 'SL' : 'Stop'}${waiting ? ' (after entry)' : ''}`,
        color: tp ? GREEN : RED,
        style: 'dashed',
      })
    } else if (o.px) {
      lines.push({
        price: Number(o.px),
        title: `${o.side === 'buy' ? 'Buy' : 'Sell'} ${remaining}`,
        color: o.side === 'buy' ? GREEN : RED,
        style: 'dotted',
      })
    }
  }
  return lines
}
