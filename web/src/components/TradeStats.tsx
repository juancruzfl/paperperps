import { formatDuration, formatPct, formatUsd } from '../lib/format'
import type { SideStats, TradePerformance } from '../lib/types'

const tone = (n: number) => (n > 0 ? 'up' : n < 0 ? 'down' : '')
const rate = (s: SideStats) => (s.trips > 0 ? formatPct(s.wins / s.trips, 0).replace('+', '') : '—')

/** How a trader's closed positions went: averages, streaks, and splits by side and coin. */
export function TradeStats({ perf }: { perf: TradePerformance }) {
  if (perf.roundTrips === 0) {
    return <div className="panel-empty">No closed positions yet. Stats appear once a position is closed.</div>
  }
  const avgWin = Number(perf.avgWin)
  const avgLoss = Number(perf.avgLoss)
  const pf = perf.profitFactor === null ? null : Number(perf.profitFactor)
  const sides: [string, SideStats][] = [
    ['Long', perf.long],
    ['Short', perf.short],
  ]

  return (
    <div className="trade-stats">
      <dl className="trade-stats-grid">
        <div>
          <dt>Closed positions</dt>
          <dd>
            {perf.roundTrips}
            <span className="muted">
              {' '}
              · {perf.wins}W {perf.losses}L
            </span>
          </dd>
        </div>
        <div>
          <dt>Avg win</dt>
          <dd className={tone(avgWin)}>{perf.wins > 0 ? formatUsd(avgWin, { signed: true }) : '—'}</dd>
        </div>
        <div>
          <dt>Avg loss</dt>
          <dd className={tone(avgLoss)}>{perf.losses > 0 ? formatUsd(avgLoss, { signed: true }) : '—'}</dd>
        </div>
        <div>
          <dt title="Total won divided by total lost. Above 1 means winners outweigh losers.">Profit factor</dt>
          <dd className={pf === null ? 'up' : pf >= 1 ? 'up' : 'down'}>{pf === null ? 'No losses' : pf.toFixed(2)}</dd>
        </div>
        <div>
          <dt>Avg hold time</dt>
          <dd>{formatDuration(perf.avgHoldSeconds)}</dd>
        </div>
        <div>
          <dt>Longest losing streak</dt>
          <dd>{perf.longestLosingStreak}</dd>
        </div>
      </dl>

      <div className="trade-stats-tables">
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Side</th>
                <th className="num">Positions</th>
                <th className="num">Win rate</th>
                <th className="num">PnL</th>
              </tr>
            </thead>
            <tbody>
              {sides.map(([label, s]) => (
                <tr key={label}>
                  <td className={label === 'Long' ? 'up' : 'down'}>{label}</td>
                  <td className="num">{s.trips}</td>
                  <td className="num">{rate(s)}</td>
                  <td className={`num ${tone(Number(s.pnl))}`}>{formatUsd(Number(s.pnl), { signed: true })}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Coin</th>
                <th className="num">Positions</th>
                <th className="num">Win rate</th>
                <th className="num">PnL</th>
              </tr>
            </thead>
            <tbody>
              {perf.byCoin.map((c) => (
                <tr key={c.coin}>
                  <td>
                    <a href={`/trade/${encodeURIComponent(c.coin)}`}>{c.coin}</a>
                  </td>
                  <td className="num">{c.trips}</td>
                  <td className="num">{rate(c)}</td>
                  <td className={`num ${tone(Number(c.pnl))}`}>{formatUsd(Number(c.pnl), { signed: true })}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <p className="muted small trade-stats-note">
        A position counts once it’s fully closed. PnL here is after fees; funding isn’t included.
      </p>
    </div>
  )
}
