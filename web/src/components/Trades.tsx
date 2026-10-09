import { formatNumber, formatTime, pxDecimals } from '../lib/format'
import type { Trade } from '../lib/types'

const ROWS = 40

type Props = {
  trades: Trade[] | undefined
  szDecimals: number
  coin: string
}

export function Trades({ trades, szDecimals, coin }: Props) {
  if (!trades) return <div className="panel-empty">Loading trades…</div>
  if (trades.length === 0) return <div className="panel-empty">No recent trades</div>

  const pxDec = pxDecimals(Number(trades[0].px), szDecimals)
  return (
    <div className="trades">
      <div className="trade-row head">
        <span>Price</span>
        <span className="num">Size ({coin})</span>
        <span className="num">Time</span>
      </div>
      <div className="trades-list">
        {trades.slice(0, ROWS).map((t) => (
          <div className="trade-row" key={t.tid}>
            <span className={t.side === 'B' ? 'up' : 'down'}>{formatNumber(t.px, pxDec)}</span>
            <span className="num">{formatNumber(t.sz, szDecimals)}</span>
            <span className="num muted">{formatTime(t.time)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
