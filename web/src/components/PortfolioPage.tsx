import { useEffect, useMemo, useState } from 'react'
import { api } from '../lib/api'
import { loadHistory, useAccount } from '../lib/account'
import { formatPct, formatUsd, formatUsdCompact } from '../lib/format'
import { coinStates, score, summarize } from '../lib/margin'
import { useTickers } from '../lib/market'
import { PNL_RANGES, pnlWindow, type PnlRange } from '../lib/pnlRange'
import { activityInWindow, dailyPnl, equityWindow } from '../lib/portfolio'
import { navigate, traderPath } from '../lib/route'
import type { EquityPoint } from '../lib/types'
import { AccountPanel, type Tab } from './AccountPanel'
import { PnlCalendar } from './PnlCalendar'
import { PnlChart } from './PnlChart'

// The server sends at most this many rows of trade or funding history.
const HISTORY_LIMIT = 200

// The chart already covers performance, so the table skips that tab.
const TABLE_TABS: readonly Tab[] = ['Positions', 'Open Orders', 'Trade History', 'Order History', 'Funding History']

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

  const states = useMemo(() => (me ? coinStates(me, tickers) : []), [me, tickers])
  const summary = useMemo(() => (me ? summarize(Number(me.balance), states) : null), [me, states])

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
  const days = dailyPnl(history, lifetime)
  const rangeLabel = PNL_RANGES.find((r) => r.id === range)?.label ?? ''

  const open = states.filter((c) => c.size !== 0)
  const notional = open.reduce((sum, c) => sum + Math.abs(c.size) * c.markPx, 0)
  const leverage = summary.accountValue > 0 ? notional / summary.accountValue : 0
  const marginInUse = summary.marginUsed + summary.isolatedMargin + summary.orderMargin
  // How close cross positions are to liquidation: 100% is liquidated.
  const health = summary.crossAccountValue > 0 ? summary.crossMaintenance / summary.crossAccountValue : 0
  const healthTone = health >= 0.8 ? 'danger' : health >= 0.5 ? 'warn' : 'ok'
  const healthLabel = health === 0 ? 'No risk' : healthTone === 'ok' ? 'Healthy' : healthTone === 'warn' ? 'Watch it' : 'Near liquidation'
  const joined = new Date(me.createdAt).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
  const onBoard = me.leaderboardOptIn && !me.leaderboardHidden
  const lifetimeRatio = Number(me.startingBalance) > 0 ? lifetime / Number(me.startingBalance) : 0

  return (
    <main className="page portfolio-page">
      <div className="portfolio-head">
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

      <div className="portfolio-grid">
        <section className="pcard pcard-value" aria-label="Account value">
          <div>
            <h2 className="pcard-label" title="Cash balance plus the profit or loss on open positions at live prices">
              Account value
            </h2>
            <p className="pcard-big">{formatUsd(summary.accountValue)}</p>
            <p className="pcard-sub">
              <span className={tone(lifetime)}>
                {formatUsd(lifetime, { signed: true })} ({formatPct(lifetimeRatio)})
              </span>{' '}
              <span className="muted" title="Profit and loss since you started, across resets">
                lifetime
              </span>
            </p>
          </div>
          <Sparkline points={equityWindow(history, '30d')} live={summary.accountValue} />
        </section>

        <section className="pcard pcard-risk" aria-label="Margin health">
          <div className="pcard-row">
            <h2 className="pcard-label" title="Maintenance margin as a share of cross account value. At 100% cross positions are liquidated">
              Margin ratio
            </h2>
            <span className={`pcard-chip ${healthTone}`}>{healthLabel}</span>
          </div>
          <p className={`pcard-big portfolio-health-value ${healthTone}`}>{formatPct(health).replace('+', '')}</p>
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
          <dl className="pcard-rows">
            <div>
              <dt title="Total position value divided by account value">Leverage</dt>
              <dd>{leverage.toFixed(2)}x</dd>
            </div>
            <div>
              <dt title="Margin behind open positions and resting orders">Margin in use</dt>
              <dd>{formatUsd(marginInUse)}</dd>
            </div>
            <div>
              <dt>Cash balance</dt>
              <dd>{formatUsd(Number(me.balance))}</dd>
            </div>
          </dl>
        </section>

        <section className="pcard pcard-stat" aria-label="Unrealized PnL">
          <h2 className="pcard-label" title="Profit or loss on open positions if you closed them now">
            Unrealized PnL
          </h2>
          <p className={`pcard-mid ${tone(summary.unrealizedPnl)}`}>{formatUsd(summary.unrealizedPnl, { signed: true })}</p>
          <p className="pcard-sub muted">
            {open.length === 0 ? 'No open positions' : `${open.length} open position${open.length === 1 ? '' : 's'}`}
          </p>
        </section>
        <section className="pcard pcard-stat" aria-label="Available">
          <h2 className="pcard-label" title="Margin free for new orders">
            Available to trade
          </h2>
          <p className="pcard-mid">{formatUsd(Math.max(0, summary.available))}</p>
          <p className="pcard-sub muted">
            {summary.accountValue > 0 ? `${formatPct(Math.max(0, summary.available) / summary.accountValue, 0).replace('+', '')} of account` : '—'}
          </p>
        </section>
        <section className="pcard pcard-stat" aria-label="Exposure">
          <h2 className="pcard-label" title="Total value of open positions at mark prices">
            Exposure
          </h2>
          <p className="pcard-mid">{formatUsdCompact(notional)}</p>
          <p className="pcard-sub muted">{open.length > 0 ? open.map((c) => c.coin).join(' · ') : 'Flat'}</p>
        </section>

        <section className="pcard pcard-chart" aria-label="Performance">
          <div className="pcard-row">
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
            <div className="trader-ranges" role="tablist" aria-label="Time range for the chart and stats">
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
          <div className="pcard-chart-body">
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
          </div>
          <dl className="pcard-strip" aria-label={`${rangeLabel} stats`}>
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
        </section>

        <section className="pcard pcard-calendar" aria-label="Daily PnL">
          <h2 className="pcard-label">Daily PnL</h2>
          {history.length === 0 ? (
            <p className="muted small">Your days fill in once you start trading.</p>
          ) : (
            <PnlCalendar days={days} />
          )}
        </section>

        <section className="pcard pcard-table">
          <AccountPanel tabs={TABLE_TABS} onSelectCoin={(coin) => navigate(`/trade/${encodeURIComponent(coin)}`)} />
        </section>
      </div>

      <p className="muted small">Paper trading: every balance and trade on PaperPerps is simulated, with no real money involved.</p>
    </main>
  )
}

/** A small trend line of account value, green if it ended higher than it started. */
function Sparkline({ points, live }: { points: { cumulativePnl: string }[]; live: number }) {
  const values = [...points.map((p) => Number(p.cumulativePnl)), live]
  if (values.length < 3) return null
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const w = 160
  const h = 48
  const xy = values.map((v, i) => [(i / (values.length - 1)) * w, h - 2 - ((v - min) / span) * (h - 4)])
  const line = xy.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`).join('')
  const color = live >= values[0] ? 'var(--long)' : 'var(--short)'
  return (
    <svg className="sparkline" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" aria-hidden focusable={false}>
      <path d={`${line}L${w},${h}L0,${h}Z`} fill={color} opacity={0.12} />
      <path d={line} fill="none" stroke={color} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
    </svg>
  )
}
