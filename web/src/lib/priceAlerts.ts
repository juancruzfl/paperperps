// Price alerts: "tell me when BTC reaches $X". They're checked against the mark price in
// this browser while PaperPerps is open, fire once, and then disappear. Kept in
// localStorage, like the other alert preferences.

import { create } from 'zustand'
import { raiseAlert } from './alerts'
import type { PriceLine } from './chartLines'
import { formatNumber, pxDecimals } from './format'
import type { Ticker } from './types'

const KEY = 'paperperps.priceAlerts'
export const MAX_PRICE_ALERTS = 20

export type PriceAlert = {
  id: string
  coin: string
  px: number
  /** Fires when the mark rises to px (true) or falls to it (false). */
  above: boolean
}

function load(): PriceAlert[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(KEY) ?? '[]')
    return Array.isArray(parsed)
      ? parsed.filter(
          (a): a is PriceAlert =>
            typeof a?.id === 'string' && typeof a.coin === 'string' && Number.isFinite(a.px) && typeof a.above === 'boolean',
        )
      : []
  } catch {
    return []
  }
}

function save(alerts: PriceAlert[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(alerts))
  } catch {
    // Private mode: alerts last for this tab.
  }
}

export const usePriceAlerts = create<{ alerts: PriceAlert[] }>(() => ({ alerts: load() }))

function setAlerts(alerts: PriceAlert[]) {
  save(alerts)
  usePriceAlerts.setState({ alerts })
}

/** Adds an alert for coin at px, on whichever side of the current mark px is. */
export function addPriceAlert(coin: string, px: number, mark: number): string | null {
  const { alerts } = usePriceAlerts.getState()
  if (!(px > 0)) return 'Enter a price above zero.'
  if (px === mark) return 'That’s the current price. Pick a price above or below it.'
  if (alerts.length >= MAX_PRICE_ALERTS) return `You can have up to ${MAX_PRICE_ALERTS} alerts. Remove one first.`
  if (alerts.some((a) => a.coin === coin && a.px === px)) return 'You already have an alert at that price.'
  setAlerts([...alerts, { id: crypto.randomUUID(), coin, px, above: px > mark }])
  return null
}

export function removePriceAlert(id: string) {
  setAlerts(usePriceAlerts.getState().alerts.filter((a) => a.id !== id))
}

/** The alerts that the current marks have reached. */
export function reached(alerts: PriceAlert[], tickers: Ticker[]): PriceAlert[] {
  const marks = new Map(tickers.map((t) => [t.coin, Number(t.markPx)]))
  return alerts.filter((a) => {
    const mark = marks.get(a.coin)
    if (mark === undefined || !Number.isFinite(mark)) return false
    return a.above ? mark >= a.px : mark <= a.px
  })
}

export function alertLines(alerts: PriceAlert[], coin: string): PriceLine[] {
  return alerts.filter((a) => a.coin === coin).map((a) => ({ price: a.px, title: 'Alert', color: '#8ab4ff', style: 'dotted' }))
}

/** Fires and removes the alerts the latest marks have reached. The app calls this on every ticker update. */
export function checkPriceAlerts(tickers: Ticker[]) {
  const { alerts } = usePriceAlerts.getState()
  if (alerts.length === 0) return
  const hit = reached(alerts, tickers)
  if (hit.length === 0) return
  const ids = new Set(hit.map((a) => a.id))
  setAlerts(alerts.filter((a) => !ids.has(a.id)))
  for (const a of hit) {
    const t = tickers.find((x) => x.coin === a.coin)
    const dec = pxDecimals(a.px, t?.szDecimals ?? 4)
    const what = `${a.coin} ${a.above ? 'rose' : 'fell'} to ${formatNumber(a.px, dec)}`
    raiseAlert('price', what, `Price alert: ${what}`)
  }
}
