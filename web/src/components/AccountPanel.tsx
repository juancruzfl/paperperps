import { useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { describeFill, loadHistory, refreshAccount, useAccount } from '../lib/account'
import { formatNumber, formatPct, formatUsd, pxDecimals } from '../lib/format'
import { coinStates, liquidationPrice, positionMargin, summarize, unrealizedPnl } from '../lib/margin'
import { IsolatedMarginControl } from './MarginControls'
import { Performance } from './Performance'
import { ModifyOrderControl, PositionTPSLControl } from './OrderDialogs'
import { isTrigger, orderTags, triggerDirection, typeLabel } from '../lib/orders'
import type { Order } from '../lib/types'
import { useTickers } from '../lib/market'
import { toast } from '../lib/toasts'
import type { Ticker } from '../lib/types'

const TABS = ['Positions', 'Open Orders', 'Trade History', 'Order History', 'Funding History', 'Performance'] as const
type Tab = (typeof TABS)[number]

const dateTime = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})
const when = (iso: string) => dateTime.format(new Date(iso))
const usd = (n: number, signed = false) => formatUsd(n, { signed })
const tone = (n: number) => (n > 0 ? 'up' : n < 0 ? 'down' : '')

export function AccountPanel({ onSelectCoin }: { onSelectCoin: (coin: string) => void }) {
  const [tab, setTab] = useState<Tab>('Positions')
  const me = useAccount((s) => s.me)
  const openCount = me?.openOrders.length ?? 0
  const posCount = me?.positions.filter((p) => Number(p.size) !== 0).length ?? 0

  return (
    <section className="panel account-panel" aria-label="Account">
      <div className="tabs" role="tablist">
        {TABS.map((t) => (
          <button key={t} type="button" role="tab" aria-selected={tab === t} className={tab === t ? 'active' : ''} onClick={() => setTab(t)}>
            {t}
            {t === 'Positions' && posCount > 0 && ` (${posCount})`}
            {t === 'Open Orders' && openCount > 0 && ` (${openCount})`}
          </button>
        ))}
      </div>
      <div className="table-wrap">
        {tab === 'Positions' && <Positions onSelectCoin={onSelectCoin} />}
        {tab === 'Open Orders' && <OpenOrders />}
        {tab === 'Trade History' && <TradeHistory />}
        {tab === 'Order History' && <OrderHistory />}
        {tab === 'Funding History' && <FundingHistory />}
        {tab === 'Performance' && <Performance />}
      </div>
    </section>
  )
}

function decimalsFor(tickers: Ticker[], coin: string, px: number) {
  const t = tickers.find((x) => x.coin === coin)
  return { sz: t?.szDecimals ?? 4, px: pxDecimals(px, t?.szDecimals ?? 4) }
}

function Positions({ onSelectCoin }: { onSelectCoin: (coin: string) => void }) {
  const me = useAccount((s) => s.me)
  const secret = useAccount((s) => s.secret)
  const tickers = useTickers()
  const [closing, setClosing] = useState<string | null>(null)
  if (!me) return null
  const all = coinStates(me, tickers)
  const summary = summarize(Number(me.balance), all)
  const coins = all.filter((c) => c.size !== 0)
  if (coins.length === 0) return <div className="panel-empty">No open positions. Place an order to open one.</div>

  const close = async (coin: string, size: number, szDecimals: number) => {
    if (!secret) return
    setClosing(coin)
    try {
      const res = await api.placeOrder(secret, {
        coin,
        side: size > 0 ? 'sell' : 'buy',
        type: 'market',
        sz: Math.abs(size).toFixed(szDecimals),
        reduceOnly: true, // also works on markets that are no longer listed (close-only)
      })
      if (res.fill) toast(`Closed: ${describeFill(res.fill)}`, 'success')
      await refreshAccount()
    } catch (err) {
      toast(err instanceof ApiError ? err.message : 'Could not reach the server', 'error')
    } finally {
      setClosing(null)
    }
  }

  return (
    <table className="data-table">
      <thead>
        <tr>
          <th>Coin</th>
          <th className="num">Size</th>
          <th className="num">Value</th>
          <th className="num">Entry</th>
          <th className="num">Mark</th>
          <th className="num">PnL (ROE)</th>
          <th className="num">Liq. price</th>
          <th className="num">Margin</th>
          <th className="num">TP / SL</th>
          <th className="num">Close</th>
        </tr>
      </thead>
      <tbody>
        {coins.map((c) => {
          const dec = decimalsFor(tickers, c.coin, c.markPx)
          const pnl = unrealizedPnl(c)
          const margin = c.isolated ? c.isolatedMargin : positionMargin(c)
          const liq = liquidationPrice(c, summary)
          const { tp, sl } = positionTPSL(me.openOrders, c.coin)
          return (
            <tr key={c.coin}>
              <td>
                <button type="button" className="link" onClick={() => onSelectCoin(c.coin)}>
                  {c.coin}
                </button>
                <span className="badge">{c.leverage}x</span>
              </td>
              <td className={`num ${tone(c.size)}`}>
                {formatNumber(c.size, dec.sz)} {c.coin}
              </td>
              <td className="num">{usd(Math.abs(c.size) * c.markPx)}</td>
              <td className="num">{formatNumber(c.entryPx, dec.px)}</td>
              <td className="num">{formatNumber(c.markPx, dec.px)}</td>
              <td className={`num ${tone(pnl)}`}>
                {usd(pnl, true)} ({formatPct(margin > 0 ? pnl / margin : 0)})
              </td>
              <td className="num">{liq === null ? '—' : formatNumber(liq, dec.px)}</td>
              <td className="num">
                {usd(margin)} <span className="muted">{c.isolated ? 'Isolated' : 'Cross'}</span>
                {c.isolated && <IsolatedMarginControl coin={c.coin} margin={c.isolatedMargin} available={summary.available} />}
              </td>
              <td className="num">
                <PositionTPSLControl coin={c.coin} size={c.size} entryPx={c.entryPx} takeProfit={tp} stopLoss={sl} />
              </td>
              <td className="num">
                <button type="button" className="small-btn" disabled={closing === c.coin} onClick={() => void close(c.coin, c.size, dec.sz)}>
                  {closing === c.coin ? 'Closing…' : 'Market'}
                </button>
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

/** The live take-profit and stop-loss triggers protecting a position. */
function positionTPSL(orders: Order[], coin: string): { tp: number | null; sl: number | null } {
  const pending = new Set(orders.map((o) => o.id))
  let tp: number | null = null
  let sl: number | null = null
  for (const o of orders) {
    const waitingOnEntry = o.parentOrderId !== null && pending.has(o.parentOrderId)
    if (o.coin !== coin || !o.reduceOnly || o.status !== 'open' || !isTrigger(o.type) || waitingOnEntry) continue
    if (o.type.startsWith('take_profit')) tp = Number(o.triggerPx)
    else sl = Number(o.triggerPx)
  }
  return { tp, sl }
}

function OpenOrders() {
  const me = useAccount((s) => s.me)
  const secret = useAccount((s) => s.secret)
  const tickers = useTickers()
  if (!me) return null
  if (me.openOrders.length === 0) return <div className="panel-empty">No open orders.</div>

  const cancel = async (id: string) => {
    if (!secret) return
    try {
      await api.cancelOrder(secret, id)
      await refreshAccount()
    } catch (err) {
      toast(err instanceof ApiError ? err.message : 'Could not reach the server', 'error')
    }
  }

  return (
    <table className="data-table">
      <thead>
        <tr>
          <th>Time</th>
          <th>Coin</th>
          <th>Type</th>
          <th className="num">Price</th>
          <th className="num">Trigger</th>
          <th className="num">Size</th>
          <th className="num">Filled</th>
          <th className="num">Actions</th>
        </tr>
      </thead>
      <tbody>
        {[...me.openOrders].reverse().map((o) => {
          const dec = decimalsFor(tickers, o.coin, Number(o.px ?? o.triggerPx ?? 0))
          const waiting = o.parentOrderId !== null && me.openOrders.some((p) => p.id === o.parentOrderId)
          return (
            <tr key={o.id}>
              <td className="muted">{when(o.createdAt)}</td>
              <td>{o.coin}</td>
              <td className={o.side === 'buy' ? 'up' : 'down'}>
                {typeLabel(o.type)} {o.side}
                {orderTags(o).map((t) => (
                  <span key={t} className="tag">
                    {t}
                  </span>
                ))}
              </td>
              <td className="num">{o.px ? formatNumber(o.px, dec.px) : 'Market'}</td>
              <td className="num">
                {o.status === 'triggered'
                  ? 'Triggered'
                  : o.triggerPx
                    ? `${waiting ? 'After entry · ' : ''}Mark ${triggerDirection(o.type, o.side)} ${formatNumber(o.triggerPx, dec.px)}`
                    : '—'}
              </td>
              <td className="num">{formatNumber(o.sz, dec.sz)}</td>
              <td className="num">{formatNumber(o.filledSz, dec.sz)}</td>
              <td className="num actions">
                <ModifyOrderControl order={o} />
                <button type="button" className="small-btn" onClick={() => void cancel(o.id)}>
                  Cancel
                </button>
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

function useHistory<K extends 'fills' | 'orders' | 'funding'>(kind: K) {
  const rows = useAccount((s) => s.history[kind])
  const ready = useAccount((s) => s.status === 'ready')
  const [error, setError] = useState(false)
  useEffect(() => {
    if (ready) loadHistory(kind).catch(() => setError(true))
  }, [kind, ready])
  return { rows, error }
}

function TradeHistory() {
  const { rows, error } = useHistory('fills')
  const tickers = useTickers()
  if (error) return <div className="panel-empty">Couldn’t load trade history.</div>
  if (!rows) return <div className="panel-empty">Loading…</div>
  if (rows.length === 0) return <div className="panel-empty">No trades yet.</div>
  return (
    <table className="data-table">
      <thead>
        <tr>
          <th>Time</th>
          <th>Coin</th>
          <th>Side</th>
          <th className="num">Price</th>
          <th className="num">Size</th>
          <th className="num">Value</th>
          <th className="num">Fee</th>
          <th className="num">Closed PnL</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((f) => {
          const dec = decimalsFor(tickers, f.coin, Number(f.px))
          const pnl = Number(f.closedPnl)
          return (
            <tr key={f.id}>
              <td className="muted">{when(f.createdAt)}</td>
              <td>{f.coin}</td>
              <td className={f.side === 'buy' ? 'up' : 'down'}>
                {f.side === 'buy' ? 'Buy' : 'Sell'}
                <span className="muted"> · {f.liquidity}</span>
              </td>
              <td className="num">{formatNumber(f.px, dec.px)}</td>
              <td className="num">{formatNumber(f.sz, dec.sz)}</td>
              <td className="num">{usd(Number(f.sz) * Number(f.px))}</td>
              <td className="num">{usd(Number(f.fee))}</td>
              <td className={`num ${tone(pnl)}`}>{pnl === 0 ? '—' : usd(pnl, true)}</td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

function OrderHistory() {
  const { rows, error } = useHistory('orders')
  const tickers = useTickers()
  if (error) return <div className="panel-empty">Couldn’t load order history.</div>
  if (!rows) return <div className="panel-empty">Loading…</div>
  if (rows.length === 0) return <div className="panel-empty">No filled or cancelled orders yet.</div>
  return (
    <table className="data-table">
      <thead>
        <tr>
          <th>Time</th>
          <th>Coin</th>
          <th>Type</th>
          <th className="num">Price</th>
          <th className="num">Size</th>
          <th className="num">Filled</th>
          <th>Status</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((o) => {
          const dec = decimalsFor(tickers, o.coin, Number(o.px ?? 0))
          return (
            <tr key={o.id}>
              <td className="muted">{when(o.updatedAt)}</td>
              <td>{o.coin}</td>
              <td className={o.side === 'buy' ? 'up' : 'down'}>
                {typeLabel(o.type)} {o.side}
              </td>
              <td className="num">{o.px ? formatNumber(o.px, dec.px) : 'Market'}</td>
              <td className="num">{formatNumber(o.sz, dec.sz)}</td>
              <td className="num">{formatNumber(o.filledSz, dec.sz)}</td>
              <td title={o.statusReason ?? undefined}>
                {o.status === 'filled' ? 'Filled' : 'Cancelled'}
                {o.statusReason && <span className="muted"> · {o.statusReason}</span>}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

function FundingHistory() {
  const { rows, error } = useHistory('funding')
  if (error) return <div className="panel-empty">Couldn’t load funding history.</div>
  if (!rows) return <div className="panel-empty">Loading…</div>
  if (rows.length === 0) {
    return <div className="panel-empty">No funding payments yet. Open positions pay or receive funding at the top of every hour.</div>
  }
  return (
    <table className="data-table">
      <thead>
        <tr>
          <th>Time</th>
          <th>Coin</th>
          <th className="num">Size</th>
          <th className="num">Rate</th>
          <th className="num">Payment</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((f) => (
          <tr key={`${f.coin}-${f.fundingHour}`}>
            <td className="muted">{when(f.fundingHour)}</td>
            <td>{f.coin}</td>
            <td className="num">{f.size}</td>
            <td className="num">{formatPct(Number(f.rate), 4)}</td>
            <td className={`num ${tone(Number(f.amount))}`}>{usd(Number(f.amount), true)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
