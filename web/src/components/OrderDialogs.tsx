import { useRef, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { refreshAccount, useAccount } from '../lib/account'
import { formatUsd } from '../lib/format'
import { isLimitPriced, typeLabel } from '../lib/orders'
import { toast } from '../lib/toasts'
import type { Order } from '../lib/types'
import { PencilIcon } from './Icons'

const errorText = (err: unknown) => (err instanceof ApiError ? err.message : 'Could not reach the server')

/** "Modify" button + dialog to change an open order's size, price or trigger. */
export function ModifyOrderControl({ order }: { order: Order }) {
  const secret = useAccount((s) => s.secret)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [sz, setSz] = useState('')
  const [px, setPx] = useState('')
  const [trigger, setTrigger] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const pendingTrigger = order.status === 'open' && order.triggerPx !== null

  const open = () => {
    setSz(String(Number(order.sz)))
    setPx(order.px ? String(Number(order.px)) : '')
    setTrigger(order.triggerPx ? String(Number(order.triggerPx)) : '')
    setError(null)
    dialogRef.current?.showModal()
  }

  const save = async () => {
    if (!secret) return
    const changes: { sz?: string; px?: string; triggerPx?: string } = {}
    if (sz && Number(sz) !== Number(order.sz)) changes.sz = sz
    if (px && Number(px) !== Number(order.px)) changes.px = px
    if (trigger && Number(trigger) !== Number(order.triggerPx)) changes.triggerPx = trigger
    if (Object.keys(changes).length === 0) {
      dialogRef.current?.close()
      return
    }
    setSaving(true)
    setError(null)
    try {
      await api.modifyOrder(secret, order.id, changes)
      await refreshAccount()
      toast('Order modified', 'success')
      dialogRef.current?.close()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <button type="button" className="small-btn" onClick={open}>
        Modify
      </button>
      <dialog ref={dialogRef} className="dialog" aria-labelledby={`modify-${order.id}`}>
        <h3 id={`modify-${order.id}`}>
          Modify {typeLabel(order.type).toLowerCase()} {order.side} · {order.coin}
        </h3>
        <div className="dialog-fields">
          {pendingTrigger && (
            <label className="field">
              <span>Trigger (mark)</span>
              <input inputMode="decimal" value={trigger} onChange={(e) => setTrigger(e.target.value)} />
            </label>
          )}
          {isLimitPriced(order.type) && (
            <label className="field">
              <span>Limit price</span>
              <input inputMode="decimal" value={px} onChange={(e) => setPx(e.target.value)} />
            </label>
          )}
          <label className="field">
            <span>Size ({order.coin})</span>
            <input inputMode="decimal" value={sz} onChange={(e) => setSz(e.target.value)} />
          </label>
        </div>
        {error && <p className="form-error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Cancel
          </button>
          <button type="button" className="primary" onClick={() => void save()} disabled={saving}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </dialog>
    </>
  )
}

/** Shows a position's TP/SL and edits them in a dialog. */
export function PositionTPSLControl(props: {
  coin: string
  size: number
  entryPx: number
  takeProfit: number | null
  stopLoss: number | null
}) {
  const { coin, size, entryPx, takeProfit, stopLoss } = props
  const secret = useAccount((s) => s.secret)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [tp, setTp] = useState('')
  const [sl, setSl] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const open = () => {
    setTp(takeProfit ? String(takeProfit) : '')
    setSl(stopLoss ? String(stopLoss) : '')
    setError(null)
    dialogRef.current?.showModal()
  }

  const save = async (remove = false) => {
    if (!secret) return
    setSaving(true)
    setError(null)
    try {
      await api.setPositionTPSL(secret, coin, remove ? {} : { ...(tp ? { takeProfit: tp } : {}), ...(sl ? { stopLoss: sl } : {}) })
      await refreshAccount()
      toast(remove ? `${coin} TP/SL removed` : `${coin} TP/SL set`, 'success')
      dialogRef.current?.close()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setSaving(false)
    }
  }

  const pnl = (exit: string) => (Number(exit) > 0 ? size * (Number(exit) - entryPx) : null)
  const fmt = (v: number | null) => (v === null ? '—' : v.toLocaleString('en-US'))

  return (
    <>
      <button type="button" className="link tpsl-link" onClick={open} title="Set take profit / stop loss">
        {fmt(takeProfit)} / {fmt(stopLoss)} <PencilIcon size={12} className="icon muted" />
      </button>
      <dialog ref={dialogRef} className="dialog" aria-labelledby={`tpsl-${coin}`}>
        <h3 id={`tpsl-${coin}`}>{coin} take profit / stop loss</h3>
        <p className="muted small">
          Market orders that close the whole {size > 0 ? 'long' : 'short'} when the mark price reaches them. Filling one
          cancels the other. Setting new ones replaces the old.
        </p>
        <div className="dialog-fields">
          <label className="field">
            <span>Take profit</span>
            <input inputMode="decimal" value={tp} onChange={(e) => setTp(e.target.value)} placeholder="—" />
          </label>
          {pnl(tp) !== null && <span className="hint">Est. PnL {formatUsd(pnl(tp)!, { signed: true })}</span>}
          <label className="field">
            <span>Stop loss</span>
            <input inputMode="decimal" value={sl} onChange={(e) => setSl(e.target.value)} placeholder="—" />
          </label>
          {pnl(sl) !== null && <span className="hint">Est. PnL {formatUsd(pnl(sl)!, { signed: true })}</span>}
        </div>
        {error && <p className="form-error">{error}</p>}
        <div className="dialog-actions">
          {(takeProfit !== null || stopLoss !== null) && (
            <button type="button" className="ghost" onClick={() => void save(true)} disabled={saving}>
              Remove
            </button>
          )}
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Cancel
          </button>
          <button type="button" className="primary" onClick={() => void save()} disabled={saving || (!tp && !sl)}>
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </dialog>
    </>
  )
}
