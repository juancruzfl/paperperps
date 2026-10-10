import { useEffect, useMemo, useState } from 'react'
import { api } from '../lib/api'
import { useAccount } from '../lib/account'
import { formatUsd } from '../lib/format'
import { coinStates, score, summarize } from '../lib/margin'
import { useTickers } from '../lib/market'
import type { EquityPoint } from '../lib/types'
import { PnlChart } from './PnlChart'

/** Lifetime PnL over time (from equity snapshots), with the live value as the last point. */
export function Performance() {
  const me = useAccount((s) => s.me)
  const secret = useAccount((s) => s.secret)
  const tickers = useTickers()
  const [points, setPoints] = useState<EquityPoint[] | null>(null)

  // Live lifetime PnL, mirroring engine.Score.
  const live = useMemo(() => {
    if (!me) return null
    const s = summarize(Number(me.balance), coinStates(me, tickers))
    return score(Number(me.priorPnl), Number(me.balance), s.unrealizedPnl, Number(me.startingBalance))
  }, [me, tickers])
  // This run's PnL is the lifetime figure minus what earlier runs (before resets) made.
  const runPnl = me && live !== null ? live - Number(me.priorPnl) : 0

  useEffect(() => {
    if (!secret) return
    let cancelled = false
    const load = () => api.equity(secret).then((p) => !cancelled && setPoints(p), () => {})
    void load()
    const id = setInterval(load, 5 * 60_000) // snapshots are taken every 5 minutes
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [secret])

  if (!me) return null
  const tone = (n: number) => (n > 0 ? 'up' : n < 0 ? 'down' : '')
  return (
    <div className="performance">
      <dl className="perf-stats">
        <div>
          <dt>Lifetime PnL</dt>
          <dd className={tone(live ?? 0)}>{formatUsd(live ?? 0, { signed: true })}</dd>
        </div>
        <div>
          <dt>This run</dt>
          <dd className={tone(runPnl)}>{formatUsd(runPnl, { signed: true })}</dd>
        </div>
        <div>
          <dt>Resets</dt>
          <dd>{me.resets}</dd>
        </div>
      </dl>
      <PnlChart
        points={points}
        live={live}
        empty="Your PnL history starts with your first trade. Points are added every 5 minutes."
      />
    </div>
  )
}
