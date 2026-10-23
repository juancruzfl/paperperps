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
import { MarketSelector } from './MarketSelector'
import { PriceAlertControl } from './PriceAlerts'

function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

type Props = {
  coin: string
  tickers: Ticker[]
  onSelect: (coin: string) => void
}

/** The market picker, then the selected market's prices and funding. */
export function StatsBar({ coin, tickers, onSelect }: Props) {
  const ticker = tickers.find((t) => t.coin === coin)
  return (
    <div className="stats-bar">
      <MarketSelector coin={coin} tickers={tickers} onSelect={onSelect} />
      {/* Only the stats scroll sideways; the selector's dropdown would be clipped inside it. */}
      <div className="stats-scroll">{ticker ? <Stats ticker={ticker} /> : <span className="muted">Loading market…</span>}</div>
    </div>
  )
}

function Stats({ ticker }: { ticker: Ticker }) {
  const now = useNow(1000)

  const dec = pxDecimals(Number(ticker.markPx), ticker.szDecimals)
  const ch = change24h(ticker.markPx, ticker.prevDayPx)
  const funding = Number(ticker.funding)
  const oiUsd = Number(ticker.openInterest) * Number(ticker.markPx)

  return (
    <>
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
    </>
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
