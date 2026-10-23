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

const RANGE_NAMES: Record<PnlRange, string> = {
  '24h': 'Last 24 hours',
  '7d': 'Last 7 days',
  '30d': 'Last 30 days',
  all: 'All time',
}

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
  // Win rate over closed positions, as in Trade stats; older servers only send fill counts.
  const perf = current.performance
  const wins = perf ? perf.wins : current.wins
  const closed = perf ? perf.roundTrips : current.closedTrades
  const winRate = closed > 0 ? wins / closed : null
  const joined = new Date(current.createdAt).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
  const best = Number(current.bestTrade)
  const win = pnlWindow(current.equity, pnl, range)
  const rangeName = RANGE_NAMES[range]

  return (
    <main className="page trader-page">
      <div className="trader-head">
        <div className="trader-id">
          <div>
            <h1 className="trader-name">
              {current.handle}
              {isYou && <span className="badge">you</span>}
              {current.degen && <span className="badge degen" title="Has traded above a market’s real leverage cap (degen mode)">degen</span>}
            </h1>
            <p className="muted small">
              {current.rank > 0 ? (
                <a className="trader-rank" href="/leaderboard">
                  #{current.rank} on the all-time leaderboard
                </a>
              ) : (
                'Not on the leaderboard yet'
              )}{' '}
              · trading since {joined}
            </p>
          </div>
        </div>
        <ShareButton
          className="ghost share-profile"
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

      {isYou && (
        <p className="trader-own muted small">
          This is your public profile: what other traders see. Change your handle or hide it on the{' '}
          <a href="/leaderboard">leaderboard</a>.
        </p>
      )}

      <dl className="trader-hero">
        <div className="trader-hero-main">
          <dt title="Profit and loss since they started, across resets, counting open positions at live prices">Lifetime PnL</dt>
          <dd className={tone(pnl)}>{formatUsd(pnl, { signed: true })}</dd>
        </div>
        <div>
          <dt title="Total value of everything they’ve traded">Volume</dt>
          <dd>{formatUsdCompact(Number(current.volume))}</dd>
        </div>
        <div>
          <dt title="Share of closed positions that made money, after fees">Win rate</dt>
          <dd>
            {winRate === null ? '—' : formatPct(winRate, 0).replace('+', '')}
            {winRate !== null && (
              <span className="muted small">
                {' '}
                {wins} of {closed}
              </span>
            )}
          </dd>
        </div>
        <div>
          <dt>Trades</dt>
          <dd>{current.trades.toLocaleString('en-US')}</dd>
        </div>
      </dl>

      <div className="trader-section-head">
        <h2>Performance</h2>
        <div className="trader-ranges" role="tablist" aria-label="Time range for PnL and the chart">
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

      <div className="trader-overview">
        <section className="trader-chart">
          <PnlChart
            points={win.points}
            live={win.points.length > 0 ? win.pnl : null}
            empty={
              current.equity.length === 0
                ? 'No history yet. The chart fills in every 5 minutes while they trade.'
                : 'Nothing recorded in this range yet.'
            }
          />
        </section>

        <aside className="trader-summary">
          <h3 className="trader-summary-head">{rangeName}</h3>
          <dl className="trader-rows">
            <div>
              <dt>PnL</dt>
              <dd className={tone(win.pnl)}>{formatUsd(win.pnl, { signed: true })}</dd>
            </div>
            <div>
              <dt title="The biggest drop from a high point to a later low in this range">Max drawdown</dt>
              <dd className={win.maxDrawdown > 0 ? 'down' : ''}>{win.maxDrawdown > 0 ? formatUsd(-win.maxDrawdown) : '—'}</dd>
            </div>
          </dl>
          <h3 className="trader-summary-head">Trading record</h3>
          <dl className="trader-rows">
            <div>
              <dt title="The largest profit from a single closing trade">Best trade</dt>
              <dd className={tone(best)}>{best > 0 ? formatUsd(best, { signed: true }) : '—'}</dd>
            </div>
            <div>
              <dt title="Times they started over with $1,000. Losses from before a reset still count">Resets</dt>
              <dd>{current.resets}</dd>
            </div>
          </dl>
        </aside>
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
          <div className="panel-empty">No open positions right now.</div>
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Coin</th>
                  <th className="num">Size</th>
                  <th className="num hide-sm" title="Size × the current mark price">
                    Position value
                  </th>
                  <th className="num" title="The average price they got in at">
                    Entry
                  </th>
                  <th className="num hide-sm" title="The current price used for profit and loss">
                    Mark
                  </th>
                  <th className="num" title="Profit or loss if closed now. ROE is that as a share of the margin behind it">
                    PnL (ROE)
                  </th>
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

