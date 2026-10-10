import { useMemo, useState } from 'react'
import { computeDepth, groupings, stepDecimals, type DepthRow } from '../lib/book'
import { formatNumber, formatPct, pxDecimals } from '../lib/format'
import { useBook, useTicker } from '../lib/market'
import { pickPrice } from '../lib/ticket'

const ROWS = 11

type Props = {
  szDecimals: number
  coin: string
}

export function OrderBook({ szDecimals, coin }: Props) {
  const ticker = useTicker(coin)
  const markPx = ticker ? Number(ticker.markPx) : 0
  const rawDec = pxDecimals(markPx, szDecimals)
  const options = useMemo(() => groupings(markPx, 10 ** -rawDec), [markPx, rawDec])
  // The chosen grouping belongs to a market; switching markets starts from the raw book.
  const [choice, setChoice] = useState<{ coin: string; sigFigs: number }>({ coin, sigFigs: 0 })
  const sigFigs = choice.coin === coin && options.some((o) => o.sigFigs === choice.sigFigs) ? choice.sigFigs : 0
  const grouping = options.find((o) => o.sigFigs === sigFigs) ?? options[0]

  const book = useBook(coin, sigFigs)
  const depth = useMemo(() => (book ? computeDepth(book, ROWS) : null), [book])
  if (!depth) return <div className="panel-empty">Loading order book…</div>

  const refPx = depth.bids[0]?.px ?? depth.asks[0]?.px ?? 0
  const pxDec = sigFigs ? stepDecimals(grouping.step) : pxDecimals(refPx, szDecimals)

  const row = (r: DepthRow, side: 'bid' | 'ask') => (
    <div
      className={`book-row ${side} clickable`}
      key={`${side}-${r.px}`}
      role="button"
      tabIndex={0}
      title="Use this price for a limit order"
      onClick={() => pickPrice(r.px.toFixed(pxDec))}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') pickPrice(r.px.toFixed(pxDec))
      }}
    >
      <span
        className="depth"
        style={{ transform: `scaleX(${r.depth})` }}
        aria-hidden
      />
      <span className="px">{formatNumber(r.px, pxDec)}</span>
      <span className="num">{formatNumber(r.sz, szDecimals)}</span>
      <span className="num">{formatNumber(r.total, szDecimals)}</span>
    </div>
  )

  return (
    <div className="book">
      {options.length > 1 && (
        <div className="book-tools">
          <label>
            <span className="muted">Group</span>
            <select
              value={sigFigs}
              onChange={(e) => setChoice({ coin, sigFigs: Number(e.target.value) })}
              aria-label="Group prices by"
            >
              {options.map((o) => (
                <option key={o.sigFigs} value={o.sigFigs}>
                  {formatNumber(o.step, stepDecimals(o.step))}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}
      <div className="book-row head">
        <span>Price</span>
        <span className="num">Size ({coin})</span>
        <span className="num">Total ({coin})</span>
      </div>
      {/* Asks render highest first so the best ask sits on the spread. */}
      <div className="book-side asks">{[...depth.asks].reverse().map((r) => row(r, 'ask'))}</div>
      <div className="book-spread">
        <span>Spread</span>
        <span className="num">{depth.spread === null ? '—' : formatNumber(depth.spread, pxDec)}</span>
        <span className="num">
          {depth.spreadRatio === null ? '—' : formatPct(depth.spreadRatio, 3).replace('+', '')}
        </span>
      </div>
      <div className="book-side bids">{depth.bids.map((r) => row(r, 'bid'))}</div>
    </div>
  )
}
