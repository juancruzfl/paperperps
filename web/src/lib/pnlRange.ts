import type { PnlPoint } from '../components/PnlChart'

export type PnlRange = '24h' | '7d' | '30d' | 'all'

export const PNL_RANGES: { id: PnlRange; label: string; ms: number }[] = [
  { id: '24h', label: '24H', ms: 24 * 3600_000 },
  { id: '7d', label: '7D', ms: 7 * 24 * 3600_000 },
  { id: '30d', label: '30D', ms: 30 * 24 * 3600_000 },
  { id: 'all', label: 'All', ms: Infinity },
]

export type PnlWindow = {
  /** Snapshots inside the window, rebased so the window starts at zero. */
  points: PnlPoint[]
  /** PnL made inside the window, at live marks. */
  pnl: number
  /** Largest peak-to-trough fall inside the window, as a positive number. */
  maxDrawdown: number
}

/**
 * Cuts a cumulative-PnL history down to a time window and rebases it on the last
 * snapshot before the window, so the chart and totals show only what the window earned.
 */
export function pnlWindow(history: PnlPoint[], lifetimePnl: number, range: PnlRange, now = Date.now()): PnlWindow {
  const span = PNL_RANGES.find((r) => r.id === range)?.ms ?? Infinity
  const cutoff = now - span
  let base = 0
  let hasBase = false
  const inside: PnlPoint[] = []
  for (const p of history) {
    if (Date.parse(p.t) < cutoff) {
      base = Number(p.cumulativePnl)
      hasBase = true
    } else {
      inside.push(p)
    }
  }
  const points = inside.map((p) => ({ t: p.t, cumulativePnl: String(Number(p.cumulativePnl) - base) }))
  if (hasBase) points.unshift({ t: new Date(cutoff).toISOString(), cumulativePnl: '0' })

  const pnl = lifetimePnl - base
  let peak = hasBase ? 0 : -Infinity
  let maxDrawdown = 0
  for (const v of [...points.map((p) => Number(p.cumulativePnl)), pnl]) {
    peak = Math.max(peak, v)
    maxDrawdown = Math.max(maxDrawdown, peak - v)
  }
  return { points, pnl, maxDrawdown }
}
