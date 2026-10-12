import { useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { useAccount } from '../lib/account'
import { formatNumber, formatPct, formatUsd, formatUsdCompact, pxDecimals } from '../lib/format'
import { useTickers } from '../lib/market'
import { traderPath, useTraderHandle } from '../lib/route'
import { PNL_RANGES, pnlWindow, type PnlRange } from '../lib/pnlRange'
import { roe } from '../lib/share'
import type { TraderProfile } from '../lib/types'
import { PnlChart } from './PnlChart'
import { ShareButton } from './ShareButton'
import { TradeStats } from './TradeStats'

const tone = (n: number) => (n > 0 ? 'up' : n < 0 ? 'down' : '')

/** A trader's public profile: /u/<handle>. */
export function TraderPage() {
  const handle = useTraderHandle()
  const me = useAccount((s) => s.me)
  const tickers = useTickers()
  const [profile, setProfile] = useState<TraderProfile | null>(null)
  const [range, setRange] = useState<PnlRange>('30d')
  const [tab, setTab] = useState<'positions' | 'stats'>('positions')
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
  const best = Number(current.bestTrade)
  const win = pnlWindow(current.equity, pnl, range)
  const rangeLabel = PNL_RANGES.find((r) => r.id === range)?.label ?? ''

  return (
    <main className="page trader-page">
      <div className="trader-head">
        <div className="trader-id">
          <div>
            <h1 className="trader-name">
              {current.handle}
              {isYou && <span className="badge">you</span>}
            </h1>
            <p className="muted small">
              {current.rank > 0 ? <span className="trader-rank">#{current.rank} all time</span> : 'Not ranked yet'} · trading since {joined}
            </p>
          </div>
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

      <div className="trader-overview">
        <div className="trader-tiles">
          <section className="card trader-tile">
            <h2>Lifetime PnL</h2>
            <p className={`trader-big ${tone(pnl)}`}>{formatUsd(pnl, { signed: true })}</p>
          </section>
          <section className="card trader-tile">
            <h2>Volume</h2>
            <p className="trader-big">{formatUsdCompact(Number(current.volume))}</p>
            <p className="muted small">{current.trades.toLocaleString('en-US')} {current.trades === 1 ? 'trade' : 'trades'}</p>
          </section>
        </div>

        <section className="card trader-summary">
          <div className="trader-summary-head">
            <h2>Performance</h2>
            <div className="trader-ranges" role="tablist" aria-label="Time range">
              {PNL_RANGES.map((r) => (
                <button
                  key={r.id}
                  type="button"
                  role="tab"
                  aria-selected={range === r.id}
                  className={range === r.id ? 'active' : ''}
                  onClick={() => setRange(r.id)}
                >
                  {r.label}
                </button>
              ))}
            </div>
          </div>
          <dl className="trader-rows">
            <div>
              <dt>PnL ({rangeLabel})</dt>
              <dd className={tone(win.pnl)}>{formatUsd(win.pnl, { signed: true })}</dd>
            </div>
            <div>
              <dt>Max drawdown ({rangeLabel})</dt>
              <dd className={win.maxDrawdown > 0 ? 'down' : ''}>{win.maxDrawdown > 0 ? formatUsd(-win.maxDrawdown) : '—'}</dd>
            </div>
            <div>
              <dt>Win rate</dt>
              <dd>
                {winRate === null ? '—' : formatPct(winRate, 0).replace('+', '')}
                {winRate !== null && <span className="muted"> · {current.wins}/{current.closedTrades}</span>}
              </dd>
            </div>
            <div>
              <dt>Best trade</dt>
              <dd className={tone(best)}>{best > 0 ? formatUsd(best, { signed: true }) : '—'}</dd>
            </div>
            <div>
              <dt>Open positions</dt>
              <dd>{current.positions.length}</dd>
            </div>
            <div>
              <dt>Resets</dt>
              <dd>{current.resets}</dd>
            </div>
          </dl>
        </section>

        <section className="card trader-chart">
          <div className="tabs" role="tablist" aria-label="Chart">
            <button type="button" role="tab" aria-selected className="active">
              PnL · {rangeLabel}
            </button>
          </div>
          <PnlChart
            points={win.points}
            live={win.points.length > 0 ? win.pnl : null}
            empty={range === 'all' ? 'No history yet. Points are added every 5 minutes while trading.' : 'No activity in this range.'}
          />
        </section>
      </div>

      <section className="card trader-positions">
        <div className="tabs" role="tablist" aria-label="Account">
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'positions'}
            className={tab === 'positions' ? 'active' : ''}
            onClick={() => setTab('positions')}
          >
            Positions{current.positions.length > 0 && ` (${current.positions.length})`}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'stats'}
            className={tab === 'stats' ? 'active' : ''}
            onClick={() => setTab('stats')}
          >
            Trade stats
          </button>
        </div>
        {tab === 'stats' ? (
          <TradeStats perf={current.performance} />
        ) : current.positions.length === 0 ? (
          <div className="panel-empty">No open positions.</div>
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Coin</th>
                  <th className="num">Size</th>
                  <th className="num hide-sm">Position value</th>
                  <th className="num">Entry</th>
                  <th className="num hide-sm">Mark</th>
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
                    <tr key={p.coin} className={size > 0 ? 'pos-long' : 'pos-short'}>
                      <td>
                        <a className={tone(size)} href={`/trade/${encodeURIComponent(p.coin)}`}>
                          {p.coin}
                        </a>
                        <span className="badge">
                          {p.leverage}x {p.marginMode === 'isolated' ? 'iso' : 'cross'}
                        </span>
                      </td>
                      <td className={`num ${tone(size)}`}>
                        {size > 0 ? 'Long' : 'Short'} {formatNumber(Math.abs(size), t?.szDecimals ?? 4)}
                      </td>
                      <td className="num hide-sm">{mark === null ? '—' : formatUsd(Math.abs(size) * mark)}</td>
                      <td className="num">{formatNumber(entry, dec)}</td>
                      <td className="num hide-sm">{mark === null ? '—' : formatNumber(mark, dec)}</td>
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

