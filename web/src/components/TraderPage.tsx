import { useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { useAccount } from '../lib/account'
import { formatNumber, formatPct, formatUsd, formatUsdCompact, pxDecimals } from '../lib/format'
import { useTickers } from '../lib/market'
import { traderPath, useTraderHandle } from '../lib/route'
import { roe } from '../lib/share'
import type { TraderProfile } from '../lib/types'
import { PnlChart } from './PnlChart'
import { ShareButton } from './ShareButton'

const tone = (n: number) => (n > 0 ? 'up' : n < 0 ? 'down' : '')

/** A trader's public profile: /u/<handle>. */
export function TraderPage() {
  const handle = useTraderHandle()
  const me = useAccount((s) => s.me)
  const tickers = useTickers()
  const [profile, setProfile] = useState<TraderProfile | null>(null)
  const [error, setError] = useState<{ handle: string; notFound: boolean; message: string } | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = () =>
      api.trader(handle).then(
        (p) => {
          if (cancelled) return
          setProfile(p)
          setError(null)
        },
        (err: unknown) => {
          if (cancelled) return
          const notFound = err instanceof ApiError && err.status === 404
          setError({ handle, notFound, message: err instanceof ApiError ? err.message : 'Could not reach the server' })
        },
      )
    void load()
    const id = setInterval(load, 30_000)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [handle])

  useEffect(() => {
    document.title = `${handle} · PaperPerps`
  }, [handle])

  const current = profile?.handle === handle ? profile : null
  const isYou = me?.handle === handle

  if (error?.handle === handle && (error.notFound || !current)) {
    return (
      <main className="page">
        <section className="card trader-missing">
          <h1>{error.notFound ? 'No public profile' : 'Couldn’t load this profile'}</h1>
          <p className="muted">
            {error.notFound
              ? isYou
                ? 'Your profile is hidden because you’re not shown on the leaderboard. Turn on “Show me on the leaderboard” to make it public.'
                : `There’s no trader called “${handle}” on the leaderboard. They may have changed their handle or hidden themselves.`
              : error.message}
          </p>
          <a className="primary-link" href="/leaderboard">
            Back to the leaderboard
          </a>
        </section>
      </main>
    )
  }
  if (!current) return <main className="page"><div className="panel-empty">Loading…</div></main>

  const pnl = Number(current.lifetimePnl)
  const winRate = current.closedTrades > 0 ? current.wins / current.closedTrades : null
  const joined = new Date(current.createdAt).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })

  return (
    <main className="page">
      <div className="page-head">
        <div>
          <h1 className="trader-name">
            {current.handle}
            {isYou && <span className="badge">you</span>}
          </h1>
          <p className="muted">
            {current.rank > 0 ? `#${current.rank} all time` : 'Not ranked yet'} · trading since {joined}
          </p>
        </div>
        <ShareButton
          className="ghost"
          label="Share profile"
          build={() => ({
            kind: 'profile',
            handle: current.handle,
            pnl,
            winRate,
            trades: current.trades,
            rank: current.rank,
            link: `${location.host}${traderPath(current.handle)}`,
          })}
        />
      </div>

      <dl className="trader-stats card">
        <div>
          <dt>Lifetime PnL</dt>
          <dd className={tone(pnl)}>{formatUsd(pnl, { signed: true })}</dd>
        </div>
        <div>
          <dt>Win rate</dt>
          <dd>{winRate === null ? '—' : formatPct(winRate, 0).replace('+', '')}</dd>
        </div>
        <div>
          <dt>Best trade</dt>
          <dd className={tone(Number(current.bestTrade))}>
            {Number(current.bestTrade) > 0 ? formatUsd(Number(current.bestTrade), { signed: true }) : '—'}
          </dd>
        </div>
        <div>
          <dt>Trades</dt>
          <dd>{current.trades.toLocaleString('en-US')}</dd>
        </div>
        <div>
          <dt>Volume</dt>
          <dd>{formatUsdCompact(Number(current.volume))}</dd>
        </div>
        <div>
          <dt>Resets</dt>
          <dd>{current.resets}</dd>
        </div>
      </dl>

      <section className="card trader-section">
        <h2 className="panel-title">PnL over time</h2>
        <PnlChart points={current.equity} live={null} empty="No history yet. Points are added every 5 minutes while trading." />
      </section>

      <section className="card trader-section">
        <h2 className="panel-title">Open positions</h2>
        {current.positions.length === 0 ? (
          <div className="panel-empty">No open positions.</div>
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Coin</th>
                  <th className="num">Size</th>
                  <th className="num">Entry</th>
                  <th className="num">Mark</th>
                  <th className="num">PnL (ROE)</th>
                </tr>
              </thead>
              <tbody>
                {current.positions.map((p) => {
                  const t = tickers.find((x) => x.coin === p.coin)
                  const size = Number(p.size)
                  const entry = Number(p.entryPx)
                  const mark = t ? Number(t.markPx) : null
                  const upnl = mark === null ? null : size * (mark - entry)
                  const dec = pxDecimals(entry, t?.szDecimals ?? 4)
                  const r = upnl === null || mark === null ? null : roe(upnl, size, mark, p.leverage)
                  return (
                    <tr key={p.coin}>
                      <td>
                        <a href={`/trade/${encodeURIComponent(p.coin)}`}>{p.coin}</a>
                        <span className="badge">{p.leverage}x</span>
                      </td>
                      <td className={`num ${tone(size)}`}>
                        {size > 0 ? 'Long' : 'Short'} {formatNumber(Math.abs(size), t?.szDecimals ?? 4)}
                      </td>
                      <td className="num">{formatNumber(entry, dec)}</td>
                      <td className="num">{mark === null ? '—' : formatNumber(mark, dec)}</td>
                      <td className={`num ${tone(upnl ?? 0)}`}>
                        {upnl === null ? '—' : `${formatUsd(upnl, { signed: true })}${r === null ? '' : ` (${formatPct(r)})`}`}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <p className="muted small">
        Paper trading: every balance and trade on PaperPerps is simulated, with no real money involved.
      </p>
    </main>
  )
}
