import { useEffect, useMemo, useState } from 'react'
import { api } from '../lib/api'
import { loadHistory, useAccount } from '../lib/account'
import { formatPct, formatUsd, formatUsdCompact } from '../lib/format'
import { coinStates, score, summarize } from '../lib/margin'
import { useTickers } from '../lib/market'
import { PNL_RANGES, pnlWindow, type PnlRange } from '../lib/pnlRange'
import { activityInWindow, equityWindow } from '../lib/portfolio'
import { navigate, traderPath } from '../lib/route'
import type { EquityPoint } from '../lib/types'
import { AccountPanel, type Tab } from './AccountPanel'
import { PnlChart } from './PnlChart'

// The server sends at most this many rows of trade or funding history.
const HISTORY_LIMIT = 200

// The chart already covers performance, so the table skips that tab.
const TABLE_TABS: readonly Tab[] = ['Positions', 'Open Orders', 'Trade History', 'Order History', 'Funding History']

const RANGE_NAMES: Record<PnlRange, string> = {
  '24h': 'Last 24 hours',
  '7d': 'Last 7 days',
  '30d': 'Last 30 days',
  all: 'All time',
}

type View = 'value' | 'pnl'

const tone = (n: number) => (n > 0 ? 'up' : n < 0 ? 'down' : '')

/** The visitor's own account at a glance: /portfolio. */
export function PortfolioPage() {
  const me = useAccount((s) => s.me)
  const secret = useAccount((s) => s.secret)
  const status = useAccount((s) => s.status)
  const error = useAccount((s) => s.error)
  const fills = useAccount((s) => s.history.fills)
  const funding = useAccount((s) => s.history.funding)
  const tickers = useTickers()
  const [points, setPoints] = useState<EquityPoint[] | null>(null)
  const [range, setRange] = useState<PnlRange>('30d')
  const [view, setView] = useState<View>('value')

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

  // Account events reload whichever histories are loaded, so these stay current.
  useEffect(() => {
    if (status !== 'ready') return
    loadHistory('fills').catch(() => {})
    loadHistory('funding').catch(() => {})
  }, [status])

  const summary = useMemo(() => (me ? summarize(Number(me.balance), coinStates(me, tickers)) : null), [me, tickers])

  if (!me || !summary) {
    return (
      <main className="page">
        <div className="panel-empty">{status === 'error' ? `Couldn’t load your account: ${error}` : 'Loading…'}</div>
      </main>
    )
  }

  const lifetime = score(Number(me.priorPnl), Number(me.balance), summary.unrealizedPnl, Number(me.startingBalance))
  const history = points ?? []
  const pnl = pnlWindow(history, lifetime, range)
  const value = equityWindow(history, range)
  const valueChange = value.length > 0 ? summary.accountValue - Number(value[0].cumulativePnl) : 0
  const activity = fills && funding ? activityInWindow(fills, funding, range, HISTORY_LIMIT) : null

  const states = coinStates(me, tickers)
  const notional = states.reduce((sum, c) => sum + Math.abs(c.size) * c.markPx, 0)
  const leverage = summary.accountValue > 0 ? notional / summary.accountValue : 0
  const marginInUse = summary.marginUsed + summary.isolatedMargin + summary.orderMargin
  // How close cross positions are to liquidation: 100% is liquidated.
  const health = summary.crossAccountValue > 0 ? summary.crossMaintenance / summary.crossAccountValue : 0
  const healthTone = health >= 0.8 ? 'danger' : health >= 0.5 ? 'warn' : 'ok'
  const joined = new Date(me.createdAt).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
  const onBoard = me.leaderboardOptIn && !me.leaderboardHidden

  return (
    <main className="page trader-page portfolio-page">
      <div className="trader-head">
        <div className="trader-id">
          <div>
            <h1 className="trader-name">
              Portfolio
              {me.degenAt && <span className="badge degen" title="Has traded above a market’s real leverage cap (degen mode)">degen</span>}
            </h1>
            <p className="muted small">
              {me.handle} · trading since {joined}
              {me.resets > 0 && ` · ${me.resets} reset${me.resets === 1 ? '' : 's'}`}
            </p>
          </div>
        </div>
        <div className="portfolio-actions">
          {onBoard && (
            <a className="portfolio-btn" href={traderPath(me.handle)}>
              Public profile
            </a>
          )}
          <a className="portfolio-btn primary" href="/">
            Trade
          </a>
        </div>
      </div>

      <dl className="trader-hero">
        <div className="trader-hero-main">
          <dt title="Cash balance plus the profit or loss on open positions at live prices">Account value</dt>
          <dd>{formatUsd(summary.accountValue)}</dd>
        </div>
        <div>
          <dt title="Profit and loss since you started, across resets, counting open positions at live prices">Lifetime PnL</dt>
          <dd className={tone(lifetime)}>{formatUsd(lifetime, { signed: true })}</dd>
        </div>
        <div>
          <dt title="Profit or loss on open positions if you closed them now">Unrealized PnL</dt>
          <dd className={tone(summary.unrealizedPnl)}>{formatUsd(summary.unrealizedPnl, { signed: true })}</dd>
        </div>
        <div>
          <dt title="Margin free for new orders">Available</dt>
          <dd>{formatUsd(Math.max(0, summary.available))}</dd>
        </div>
      </dl>

      <section className="portfolio-health" aria-label="Margin health">
        <div className="portfolio-health-text">
          <span className="muted small" title="Maintenance margin as a share of cross account value. At 100% cross positions are liquidated">
            Margin ratio
          </span>
          <strong className={`portfolio-health-value ${healthTone}`}>{formatPct(health).replace('+', '')}</strong>
        </div>
        <div
          className="portfolio-meter"
          role="meter"
          aria-label="Margin ratio"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(Math.min(1, health) * 100)}
        >
          <div className={`portfolio-meter-fill ${healthTone}`} style={{ width: `${Math.min(1, health) * 100}%` }} />
        </div>
        <dl className="portfolio-health-stats">
          <div>
            <dt title="Margin behind open positions and resting orders">Margin in use</dt>
            <dd>{formatUsd(marginInUse)}</dd>
          </div>
          <div>
            <dt title="Total position value divided by account value">Leverage</dt>
            <dd>{leverage.toFixed(2)}x</dd>
          </div>
          <div>
            <dt>Cash balance</dt>
            <dd>{formatUsd(Number(me.balance))}</dd>
          </div>
        </dl>
      </section>

      <div className="trader-section-head">
        <div className="portfolio-views" role="tablist" aria-label="Chart">
          {(
            [
              ['value', 'Account value'],
              ['pnl', 'PnL'],
            ] as const
          ).map(([id, label]) => (
            <button
              key={id}
              type="button"
              role="tab"
              aria-selected={view === id}
              className={view === id ? 'active' : ''}
              onClick={() => setView(id)}
            >
              {label}
            </button>
          ))}
        </div>
        <div className="trader-ranges" role="tablist" aria-label="Time range for the chart and summary">
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
          {/* Keyed so each view starts with a fresh price scale. */}
          <PnlChart
            key={view}
            points={points === null ? null : view === 'value' ? value : pnl.points}
            live={view === 'value' ? (value.length > 0 ? summary.accountValue : null) : pnl.points.length > 0 ? pnl.pnl : null}
            trend={view === 'value' ? valueChange : undefined}
            empty={
              history.length === 0
                ? 'Your history starts with your first trade. Points are added every 5 minutes.'
                : 'Nothing recorded in this range yet.'
            }
          />
        </section>

        <aside className="trader-summary">
          <h3 className="trader-summary-head">{RANGE_NAMES[range]}</h3>
          <dl className="trader-rows">
            <div>
              <dt>PnL</dt>
              <dd className={tone(pnl.pnl)}>{formatUsd(pnl.pnl, { signed: true })}</dd>
            </div>
            <div>
              <dt title="The biggest drop from a high point to a later low in this range">Max drawdown</dt>
              <dd className={pnl.maxDrawdown > 0 ? 'down' : ''}>{pnl.maxDrawdown > 0 ? formatUsd(-pnl.maxDrawdown) : '—'}</dd>
            </div>
            <div>
              <dt title="Total value of your fills in this range">Volume</dt>
              <dd>{activity ? formatUsdCompact(activity.volume) : '…'}</dd>
            </div>
            <div>
              <dt>Trades</dt>
              <dd>{activity ? activity.trades.toLocaleString('en-US') : '…'}</dd>
            </div>
            <div>
              <dt>Fees paid</dt>
              <dd>{activity ? formatUsd(activity.fees) : '…'}</dd>
            </div>
            <div>
              <dt title="Funding received minus funding paid">Net funding</dt>
              <dd className={tone(activity?.funding ?? 0)}>{activity ? formatUsd(activity.funding, { signed: true }) : '…'}</dd>
            </div>
          </dl>
          {activity?.partial && (
            <p className="muted small portfolio-note">Volume, trades, fees and funding count your latest {HISTORY_LIMIT} entries.</p>
          )}
        </aside>
      </div>

      <section className="card portfolio-table">
        <AccountPanel tabs={TABLE_TABS} onSelectCoin={(coin) => navigate(`/trade/${encodeURIComponent(coin)}`)} />
      </section>

      <p className="muted small">Paper trading: every balance and trade on PaperPerps is simulated, with no real money involved.</p>
    </main>
  )
}
