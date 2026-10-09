import { useMemo } from 'react'
import { computeDepth, type DepthRow } from '../lib/book'
import { formatNumber, formatPct, pxDecimals } from '../lib/format'
import type { Book } from '../lib/types'

const ROWS = 11

type Props = {
  book: Book | undefined
  szDecimals: number
  coin: string
}

export function OrderBook({ book, szDecimals, coin }: Props) {
  const depth = useMemo(() => (book ? computeDepth(book, ROWS) : null), [book])
  if (!depth) return <div className="panel-empty">Loading order book…</div>

  const refPx = depth.bids[0]?.px ?? depth.asks[0]?.px ?? 0
  const pxDec = pxDecimals(refPx, szDecimals)

  const row = (r: DepthRow, side: 'bid' | 'ask') => (
    <div className={`book-row ${side}`} key={`${side}-${r.px}`}>
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
