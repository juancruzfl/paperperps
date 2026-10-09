import { useMemo, useState } from 'react'
import { api, ApiError, type NewOrder } from '../lib/api'
import { describeFill, refreshAccount, useAccount } from '../lib/account'
import { formatNumber, formatUsd, pxDecimals } from '../lib/format'
import { coinStates, maintenanceRate, maxOrderSize, projectedLiquidationPrice, summarize, type CoinState } from '../lib/margin'
import { useBook, useTickers } from '../lib/market'
import { triggerDirection, typeLabel } from '../lib/orders'
import { toast } from '../lib/toasts'
import type { OrderType, Side, Ticker, TIF } from '../lib/types'
import { LeverageControl } from './LeverageControl'
import { MarginModeControl } from './MarginControls'

// Mirrors the server defaults (server/internal/config, engine.Config).
const TAKER_FEE = 0.00045
const MAKER_FEE = 0.00015
const DEFAULT_LEVERAGE = 20
const MIN_NOTIONAL = 10

const usd = (n: number, signed = false) => formatUsd(n, { signed })

type Tab = 'market' | 'limit' | 'stop' | 'take'

const TABS: [Tab, string][] = [
  ['market', 'Market'],
  ['limit', 'Limit'],
  ['stop', 'Stop'],
  ['take', 'Take profit'],
]

const TIF_LABEL: Record<TIF, string> = { gtc: 'GTC', ioc: 'IOC', alo: 'Post only' }

/** The order type a tab submits. Stop and take tabs execute at market unless given a limit. */
function orderType(tab: Tab, triggerLimit: boolean): OrderType {
  if (tab === 'stop') return triggerLimit ? 'stop_limit' : 'stop_market'
  if (tab === 'take') return triggerLimit ? 'take_profit_limit' : 'take_profit_market'
  return tab
}

// Rendered with key={coin}, so switching markets starts a clean form.
export function OrderForm({ ticker }: { ticker: Ticker | undefined }) {
  const { me, secret, status } = useAccount()
  const tickers = useTickers()
  const coin = ticker?.coin ?? ''
  const book = useBook(coin)
  const [side, setSide] = useState<Side>('buy')
  const [tab, setTab] = useState<Tab>('market')
  const [size, setSize] = useState('')
  const [price, setPrice] = useState('')
  const [trigger, setTrigger] = useState('')
  const [triggerLimit, setTriggerLimit] = useState(false)
  const [tif, setTif] = useState<TIF>('gtc')
  const [reduceOnly, setReduceOnly] = useState(false)
  const [withTpsl, setWithTpsl] = useState(false)
  const [tp, setTp] = useState('')
  const [sl, setSl] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const type = orderType(tab, triggerLimit)
  const trig = tab === 'stop' || tab === 'take'
  const limitPriced = tab === 'limit' || (trig && triggerLimit)
  const szDecimals = ticker?.szDecimals ?? 0
  const mid = book?.bids[0] && book?.asks[0] ? (Number(book.bids[0].px) + Number(book.asks[0].px)) / 2 : Number(ticker?.markPx ?? 0)
  const pxDec = pxDecimals(mid, szDecimals)
  const buy = side === 'buy'
  const canAttach = (tab === 'market' || tab === 'limit') && !reduceOnly

  const position = me?.positions.find((p) => p.coin === coin)
  const leverage = position?.leverage ?? Math.min(DEFAULT_LEVERAGE, ticker?.maxLeverage ?? DEFAULT_LEVERAGE)
  const positionSize = Number(position?.size ?? 0)
  const mode = position?.marginMode ?? 'cross'
  const closable = buy ? Math.max(0, -positionSize) : Math.max(0, positionSize)

  const states = useMemo(() => (me ? coinStates(me, tickers) : []), [me, tickers])
  const summary = useMemo(() => (me ? summarize(Number(me.balance), states) : null), [me, states])

  const touchPx = buy ? Number(book?.asks[0]?.px ?? mid) : Number(book?.bids[0]?.px ?? mid)
  const refPx = limitPriced ? Number(price) || 0 : trig ? Number(trigger) || 0 : touchPx
  const sz = Number(size) || 0
  const notional = sz * refPx
  const maxSz = reduceOnly
    ? closable
    : summary
      ? maxOrderSize({ buy, px: refPx, leverage, available: summary.available, positionSize, feeRate: TAKER_FEE, szDecimals })
      : 0
  const marginRequired = reduceOnly ? 0 : (Math.max(0, sz - closable) * refPx) / leverage
  const crossesBook =
    !limitPriced || (buy ? refPx >= Number(book?.asks[0]?.px ?? Infinity) : refPx <= Number(book?.bids[0]?.px ?? 0))
  const fee = notional * (crossesBook ? TAKER_FEE : MAKER_FEE)

  // Liquidation estimate for the position this order would leave.
  let liqPx: number | null = null
  if (me && sz > 0 && refPx > 0 && !reduceOnly) {
    const flat: CoinState = {
      coin, size: 0, entryPx: 0, leverage, markPx: mid, orders: [],
      isolated: mode === 'isolated', isolatedMargin: 0, maintenanceRate: maintenanceRate(ticker?.maxLeverage ?? leverage),
    }
    const withCoin = states.some((c) => c.coin === coin) ? states : [...states, flat]
    liqPx = projectedLiquidationPrice(withCoin, Number(me.balance), coin, buy, sz, refPx, fee)
  }

  // Expected PnL at the TP/SL prices, for an entry of sz at refPx.
  const pnlAt = (exit: string) => (Number(exit) > 0 && sz > 0 ? (buy ? 1 : -1) * sz * (Number(exit) - refPx) : null)
  const tpPnl = pnlAt(tp)
  const slPnl = pnlAt(sl)

  const setFraction = (f: number) => {
    const step = 10 ** szDecimals
    setSize(String(Math.floor(maxSz * f * step) / step))
  }

  const problem = (() => {
    if (!ticker) return 'Loading market…'
    if (ticker.stale) return 'Market data is not live right now'
    if (trig && !(Number(trigger) > 0)) return 'Enter a trigger price'
    if (limitPriced && !(Number(price) > 0)) return 'Enter a price'
    if (!(sz > 0)) return 'Enter a size'
    if (reduceOnly && closable === 0) return `No ${buy ? 'short' : 'long'} position to reduce`
    if (!reduceOnly && notional < MIN_NOTIONAL) return `Minimum order value is $${MIN_NOTIONAL}`
    return null
  })()

  const submit = async () => {
    if (!secret || problem) return
    setSubmitting(true)
    setError(null)
    const order: NewOrder = { coin, side, type, sz: size }
    if (limitPriced) order.px = price
    if (trig) order.triggerPx = trigger
    if (tab === 'limit') order.tif = tif
    if (reduceOnly) order.reduceOnly = true
    if (canAttach && withTpsl) {
      if (tp) order.takeProfit = tp
      if (sl) order.stopLoss = sl
    }
    try {
      const res = await api.placeOrder(secret, order)
      if (res.fill) toast(describeFill(res.fill), 'success')
      const o = res.order
      if (o.status === 'open' && trig) {
        toast(`${typeLabel(o.type)} placed: ${o.side} ${Number(o.sz)} ${coin} when mark ${triggerDirection(o.type, o.side)} ${trigger}`, 'info')
      } else if (o.status === 'open') {
        toast(`Limit order placed: ${o.side} ${Number(o.sz) - Number(o.filledSz)} ${coin} at ${price}`, 'info')
      }
      if (res.children?.length) {
        toast(o.status === 'open' ? 'TP/SL attached: active once the order fills' : 'TP/SL set on your position', 'info')
      }
      if (o.statusReason) toast(o.statusReason, 'info')
      setSize('')
      setTp('')
      setSl('')
      await refreshAccount()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setSubmitting(false)
    }
  }

  if (status === 'loading') return <div className="panel-empty">Setting up your paper account…</div>
  if (status === 'error') return <div className="panel-empty">Couldn’t load your account. Reload to try again.</div>

  const priceField = (label: string, value: string, onChange: (v: string) => void, withMid = true) => (
    <label className="field">
      <span>{label}</span>
      <input inputMode="decimal" value={value} onChange={(e) => onChange(e.target.value)} placeholder="0" />
      {withMid && (
        <button type="button" className="field-action" onClick={() => onChange(mid.toFixed(pxDec))}>
          Mid
        </button>
      )}
    </label>
  )

  return (
    <form
      className="order-form"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <div className="form-row between">
        <MarginModeControl coin={coin} mode={mode} />
        {ticker && <LeverageControl coin={coin} leverage={leverage} maxLeverage={ticker.maxLeverage} />}
      </div>

      <div className="segmented" role="tablist" aria-label="Order type">
        {TABS.map(([t, label]) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={tab === t}
            className={tab === t ? 'active' : ''}
            onClick={() => {
              setTab(t)
              setError(null)
              if (t === 'limit' && !price && mid) setPrice(mid.toFixed(pxDec))
            }}
          >
            {label}
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

      {trig && (
        <>
          {priceField('Trigger (mark)', trigger, setTrigger)}
          <p className="hint">
            {buy ? 'Buys' : 'Sells'} when the mark price {triggerDirection(type, side) === '≥' ? 'rises to' : 'falls to'}{' '}
            {Number(trigger) > 0 ? formatNumber(trigger, pxDec) : 'the trigger'}, then executes{' '}
            {triggerLimit ? 'as a limit order' : 'at market'}.
          </p>
          <label className="check">
            <input type="checkbox" checked={triggerLimit} onChange={(e) => setTriggerLimit(e.target.checked)} />
            Use a limit price once triggered
          </label>
        </>
      )}
      {limitPriced && priceField(trig ? 'Limit price' : 'Price (USD)', price, setPrice)}

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

      <div className="options">
        <label className="check">
          <input type="checkbox" checked={reduceOnly} onChange={(e) => setReduceOnly(e.target.checked)} />
          Reduce only
        </label>
        {canAttach && (
          <label className="check">
            <input type="checkbox" checked={withTpsl} onChange={(e) => setWithTpsl(e.target.checked)} />
            Take profit / stop loss
          </label>
        )}
        {tab === 'limit' && (
          <select className="tif" value={tif} onChange={(e) => setTif(e.target.value as TIF)} aria-label="Time in force">
            {(Object.keys(TIF_LABEL) as TIF[]).map((t) => (
              <option key={t} value={t}>
                {TIF_LABEL[t]}
              </option>
            ))}
          </select>
        )}
      </div>

      {canAttach && withTpsl && (
        <div className="tpsl-fields">
          {priceField('TP price', tp, setTp, false)}
          <span className={`hint num ${tpPnl !== null && tpPnl < 0 ? 'down' : 'up'}`}>{tpPnl === null ? '' : usd(tpPnl, true)}</span>
          {priceField('SL price', sl, setSl, false)}
          <span className={`hint num ${slPnl !== null && slPnl > 0 ? 'up' : 'down'}`}>{slPnl === null ? '' : usd(slPnl, true)}</span>
        </div>
      )}

      <dl className="order-info">
        <div>
          <dt>Order value</dt>
          <dd>{usd(notional)}</dd>
        </div>
        <div>
          <dt>{trig ? 'Margin needed at trigger' : 'Margin required'}</dt>
          <dd>{usd(marginRequired)}</dd>
        </div>
        <div>
          <dt>Est. fee ({crossesBook ? 'taker' : 'maker'})</dt>
          <dd>{usd(fee)}</dd>
        </div>
        <div>
          <dt>Est. liq. price</dt>
          <dd>{liqPx === null ? '—' : formatNumber(liqPx, pxDec)}</dd>
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
        {submitting ? 'Placing…' : (problem ?? `${trig ? `Place ${typeLabel(type).toLowerCase()}` : buy ? 'Buy / Long' : 'Sell / Short'} ${coin}`)}
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
    ['Cross margin used', summary.marginUsed],
    ['Cross maintenance', summary.crossMaintenance],
    ['Isolated margin', summary.isolatedMargin],
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
