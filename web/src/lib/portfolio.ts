// Numbers for the portfolio page: account value over a time range, and what the account
// did in that range according to its trade and funding history.

import type { PnlPoint } from '../components/PnlChart'
import { PNL_RANGES, type PnlRange } from './pnlRange'
import type { EquityPoint, Fill, FundingPayment } from './types'

const cutoffFor = (range: PnlRange, now: number) => now - (PNL_RANGES.find((r) => r.id === range)?.ms ?? Infinity)

/**
 * Account value snapshots inside a range, starting from the last snapshot before it so the
 * line begins at the range's opening value. Shaped for PnlChart.
 */
export function equityWindow(history: EquityPoint[], range: PnlRange, now = Date.now()): PnlPoint[] {
  const cutoff = cutoffFor(range, now)
  let before: EquityPoint | null = null
  const points: PnlPoint[] = []
  for (const p of history) {
    if (Date.parse(p.t) < cutoff) before = p
    else points.push({ t: p.t, cumulativePnl: p.equity })
  }
  if (before) points.unshift({ t: new Date(cutoff).toISOString(), cumulativePnl: before.equity })
  return points
}

export type Activity = {
  trades: number
  volume: number
  fees: number // paid, as a positive number
  funding: number // net: positive means received
  /** The history ends inside the range, so older rows the server didn't send may be missing. */
  partial: boolean
}

/** Totals of the fills and funding payments inside a range. Both lists are newest first. */
export function activityInWindow(
  fills: Fill[],
  funding: FundingPayment[],
  range: PnlRange,
  limit: number,
  now = Date.now(),
): Activity {
  const cutoff = cutoffFor(range, now)
  const a: Activity = { trades: 0, volume: 0, fees: 0, funding: 0, partial: false }
  for (const f of fills) {
    if (Date.parse(f.createdAt) < cutoff) continue
    a.trades++
    a.volume += Number(f.sz) * Number(f.px)
    a.fees += Number(f.fee)
  }
  for (const f of funding) {
    if (Date.parse(f.fundingHour) >= cutoff) a.funding += Number(f.amount)
  }
  const reachesBack = (rows: { length: number }, oldest: string | undefined) =>
    rows.length >= limit && oldest !== undefined && Date.parse(oldest) >= cutoff
  a.partial = reachesBack(fills, fills.at(-1)?.createdAt) || reachesBack(funding, funding.at(-1)?.fundingHour)
  return a
}

/** A local calendar day as YYYY-MM-DD. */
export const dayKey = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

/**
 * PnL made on each local day: the day's last cumulative PnL minus the previous day's (or
 * zero before the first snapshot). Today ends at the live figure. history is oldest first.
 */
export function dailyPnl(history: PnlPoint[], live: number, now = Date.now()): Map<string, number> {
  const closes = new Map<string, number>() // insertion order follows the history
  for (const p of history) closes.set(dayKey(new Date(p.t)), Number(p.cumulativePnl))
  if (history.length > 0) closes.set(dayKey(new Date(now)), live)
  const out = new Map<string, number>()
  let prev = 0
  for (const [day, close] of closes) {
    out.set(day, close - prev)
    prev = close
  }
  return out
}
