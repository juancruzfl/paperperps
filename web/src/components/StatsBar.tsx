import { useEffect, useState, type ReactNode } from 'react'
import {
  change24h,
  formatNumber,
  formatPct,
  formatUsdCompact,
  fundingCountdown,
  pxDecimals,
} from '../lib/format'
import type { Ticker } from '../lib/types'
import { PriceAlertControl } from './PriceAlerts'

function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

export function StatsBar({ ticker }: { ticker: Ticker | undefined }) {
  const now = useNow(1000)
  if (!ticker) return <div className="stats-bar muted">Loading market…</div>

  const dec = pxDecimals(Number(ticker.markPx), ticker.szDecimals)
  const ch = change24h(ticker.markPx, ticker.prevDayPx)
  const funding = Number(ticker.funding)
  const oiUsd = Number(ticker.openInterest) * Number(ticker.markPx)

  return (
    <div className="stats-bar">
      <Stat label="Mark" value={formatNumber(ticker.markPx, dec)} big />
      <Stat label="Oracle" value={formatNumber(ticker.oraclePx, dec)} />
      <Stat
        label="24h Change"
        value={`${formatNumber(ch.abs, dec)} / ${formatPct(ch.ratio)}`}
        tone={ch.ratio >= 0 ? 'up' : 'down'}
      />
      <Stat label="24h Volume" value={formatUsdCompact(Number(ticker.dayNtlVlm))} />
      <Stat label="Open Interest" value={formatUsdCompact(oiUsd)} />
      <Stat
        label="Funding / Countdown"
        value={
          <>
            <span className={funding >= 0 ? 'up' : 'down'}>{formatPct(funding, 4)}</span>{' '}
            <span className="muted">{fundingCountdown(now)}</span>
          </>
        }
      />
      {ticker.stale && (
        <span className="stale" title="No fresh data from Hyperliquid. Prices may be out of date.">
          Stale data
        </span>
      )}
      <PriceAlertControl ticker={ticker} />
    </div>
  )
}

function Stat({
  label,
  value,
  tone,
  big,
}: {
  label: string
  value: ReactNode
  tone?: 'up' | 'down'
  big?: boolean
}) {
  return (
    <div className={`stat${big ? ' big' : ''}`}>
      <span className="stat-label">{label}</span>
      <span className={`stat-value ${tone ?? ''}`}>{value}</span>
    </div>
  )
}
