import { useMemo, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { describeFill, refreshAccount, useAccount } from '../lib/account'
import { formatNumber, formatUsd, pxDecimals } from '../lib/format'
import { coinStates, maxOrderSize, summarize } from '../lib/margin'
import { useBook, useTickers } from '../lib/market'
import { toast } from '../lib/toasts'
import type { OrderType, Side, Ticker } from '../lib/types'
import { LeverageControl } from './LeverageControl'

// Mirrors the server defaults (server/internal/config, engine.Config).
const TAKER_FEE = 0.00045
const MAKER_FEE = 0.00015
const DEFAULT_LEVERAGE = 20
const MIN_NOTIONAL = 10

const usd = (n: number, signed = false) => formatUsd(n, { signed })

// Rendered with key={coin}, so switching markets starts a clean form.
export function OrderForm({ ticker }: { ticker: Ticker | undefined }) {
  const { me, secret, status } = useAccount()
  const tickers = useTickers()
  const coin = ticker?.coin ?? ''
  const book = useBook(coin)
  const [side, setSide] = useState<Side>('buy')
  const [type, setType] = useState<OrderType>('market')
  const [size, setSize] = useState('')
  const [price, setPrice] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const szDecimals = ticker?.szDecimals ?? 0
  const mid = book?.bids[0] && book?.asks[0] ? (Number(book.bids[0].px) + Number(book.asks[0].px)) / 2 : Number(ticker?.markPx ?? 0)
  const pxDec = pxDecimals(mid, szDecimals)
  const buy = side === 'buy'

  const position = me?.positions.find((p) => p.coin === coin)
  const leverage = position?.leverage ?? Math.min(DEFAULT_LEVERAGE, ticker?.maxLeverage ?? DEFAULT_LEVERAGE)
  const positionSize = Number(position?.size ?? 0)

  const summary = useMemo(() => (me ? summarize(Number(me.balance), coinStates(me, tickers)) : null), [me, tickers])

  const refPx = type === 'limit' ? Number(price) || 0 : buy ? Number(book?.asks[0]?.px ?? mid) : Number(book?.bids[0]?.px ?? mid)
  const sz = Number(size) || 0
  const notional = sz * refPx
  const maxSz = summary
    ? maxOrderSize({
        buy,
        px: refPx,
        leverage,
        available: summary.available,
        positionSize,
        feeRate: TAKER_FEE,
        szDecimals,
      })
    : 0
  const closing = buy ? Math.max(0, -positionSize) : Math.max(0, positionSize)
  const marginRequired = (Math.max(0, sz - closing) * refPx) / leverage
  const crossesBook =
    type === 'market' ||
    (buy ? refPx >= Number(book?.asks[0]?.px ?? Infinity) : refPx <= Number(book?.bids[0]?.px ?? 0))
  const fee = notional * (crossesBook ? TAKER_FEE : MAKER_FEE)

  const setFraction = (f: number) => {
    const step = 10 ** szDecimals
    setSize(String(Math.floor(maxSz * f * step) / step))
  }

  const problem = (() => {
    if (!ticker) return 'Loading market…'
    if (ticker.stale) return 'Market data is not live right now'
    if (type === 'limit' && !(Number(price) > 0)) return 'Enter a price'
    if (!(sz > 0)) return 'Enter a size'
    if (notional < MIN_NOTIONAL) return `Minimum order value is $${MIN_NOTIONAL}`
    return null
  })()

  const submit = async () => {
    if (!secret || problem) return
    setSubmitting(true)
    setError(null)
    try {
      const res = await api.placeOrder(secret, {
        coin,
        side,
        type,
        sz: size,
        ...(type === 'limit' ? { px: price } : {}),
      })
      if (res.fill) toast(describeFill(res.fill), 'success')
      if (res.order.status === 'open') {
        toast(`Limit order placed: ${buy ? 'buy' : 'sell'} ${Number(res.order.sz) - Number(res.order.filledSz)} ${coin} at ${price}`, 'info')
      }
      if (res.order.statusReason) toast(res.order.statusReason, 'info')
      setSize('')
      await refreshAccount()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setSubmitting(false)
    }
  }

  if (status === 'loading') return <div className="panel-empty">Setting up your paper account…</div>
  if (status === 'error') return <div className="panel-empty">Couldn’t load your account. Reload to try again.</div>

  return (
    <form
      className="order-form"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <div className="form-row between">
        <span className="muted">Cross</span>
        {ticker && <LeverageControl coin={coin} leverage={leverage} maxLeverage={ticker.maxLeverage} />}
      </div>

      <div className="segmented" role="tablist" aria-label="Order type">
        {(['market', 'limit'] as const).map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={type === t}
            className={type === t ? 'active' : ''}
            onClick={() => {
              setType(t)
              if (t === 'limit' && !price && mid) setPrice(mid.toFixed(pxDec))
            }}
          >
            {t === 'market' ? 'Market' : 'Limit'}
          </button>
        ))}
      </div>

      <div className="side-toggle" role="radiogroup" aria-label="Side">
        <button type="button" role="radio" aria-checked={buy} className={buy ? 'buy active' : 'buy'} onClick={() => setSide('buy')}>
          Buy / Long
        </button>
        <button type="button" role="radio" aria-checked={!buy} className={!buy ? 'sell active' : 'sell'} onClick={() => setSide('sell')}>
          Sell / Short
        </button>
      </div>

      <div className="form-row between small">
        <span className="muted">Available</span>
        <span className="num">{summary ? usd(summary.available) : '—'}</span>
      </div>
      <div className="form-row between small">
        <span className="muted">Current position</span>
        <span className={`num ${positionSize > 0 ? 'up' : positionSize < 0 ? 'down' : ''}`}>
          {formatNumber(positionSize, szDecimals)} {coin}
        </span>
      </div>

      {type === 'limit' && (
        <label className="field">
          <span>Price (USD)</span>
          <input inputMode="decimal" value={price} onChange={(e) => setPrice(e.target.value)} placeholder="0" />
          <button type="button" className="field-action" onClick={() => setPrice(mid.toFixed(pxDec))}>
            Mid
          </button>
        </label>
      )}

      <label className="field">
        <span>Size ({coin})</span>
        <input inputMode="decimal" value={size} onChange={(e) => setSize(e.target.value)} placeholder="0" />
      </label>
      <div className="fractions">
        {[0.25, 0.5, 0.75, 1].map((f) => (
          <button key={f} type="button" onClick={() => setFraction(f)} disabled={maxSz <= 0}>
            {f * 100}%
          </button>
        ))}
      </div>

      <dl className="order-info">
        <div>
          <dt>Order value</dt>
          <dd>{usd(notional)}</dd>
        </div>
        <div>
          <dt>Margin required</dt>
          <dd>{usd(marginRequired)}</dd>
        </div>
        <div>
          <dt>Est. fee ({crossesBook ? 'taker' : 'maker'})</dt>
          <dd>{usd(fee)}</dd>
        </div>
        <div>
          <dt>Max size</dt>
          <dd>
            {formatNumber(maxSz, szDecimals)} {coin}
          </dd>
        </div>
      </dl>

      {error && <p className="form-error">{error}</p>}

      <button type="submit" className={`submit ${side}`} disabled={!!problem || submitting}>
        {submitting ? 'Placing…' : (problem ?? `${buy ? 'Buy / Long' : 'Sell / Short'} ${coin}`)}
      </button>

      {summary && <AccountSummary summary={summary} />}
    </form>
  )
}

function AccountSummary({ summary }: { summary: ReturnType<typeof summarize> }) {
  const rows: [string, number, boolean?][] = [
    ['Account value', summary.accountValue],
    ['Balance', summary.balance],
    ['Unrealized PnL', summary.unrealizedPnl, true],
    ['Margin used', summary.marginUsed],
    ['Reserved by orders', summary.orderMargin],
  ]
  return (
    <dl className="account-summary">
      {rows.map(([label, value, signed]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd className={signed ? (value > 0 ? 'up' : value < 0 ? 'down' : '') : ''}>{usd(value, signed)}</dd>
        </div>
      ))}
    </dl>
  )
}
