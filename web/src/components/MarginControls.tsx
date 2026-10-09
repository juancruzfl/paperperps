import { useRef, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { refreshAccount, useAccount } from '../lib/account'
import { formatUsd } from '../lib/format'
import { toast } from '../lib/toasts'

type Mode = 'cross' | 'isolated'

/** Button + dialog to switch a coin between cross and isolated margin. */
export function MarginModeControl({ coin, mode }: { coin: string; mode: Mode }) {
  const secret = useAccount((s) => s.secret)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [choice, setChoice] = useState<Mode>(mode)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const open = () => {
    setChoice(mode)
    setError(null)
    dialogRef.current?.showModal()
  }

  const save = async () => {
    if (!secret) return
    setSaving(true)
    setError(null)
    try {
      await api.setMarginMode(secret, coin, choice)
      await refreshAccount()
      toast(`${coin} margin mode: ${choice}`, 'success')
      dialogRef.current?.close()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <button type="button" className="chip" onClick={open}>
        {mode === 'cross' ? 'Cross' : 'Isolated'}
      </button>
      <dialog ref={dialogRef} className="dialog" aria-labelledby="mode-title">
        <h3 id="mode-title">{coin} margin mode</h3>
        <div className="mode-options" role="radiogroup">
          <button type="button" role="radio" aria-checked={choice === 'cross'} className={choice === 'cross' ? 'active' : ''} onClick={() => setChoice('cross')}>
            <strong>Cross</strong>
            <span>All cross positions share your account’s margin. A loss on one can be covered by the rest, but a bad enough loss liquidates them all together.</span>
          </button>
          <button type="button" role="radio" aria-checked={choice === 'isolated'} className={choice === 'isolated' ? 'active' : ''} onClick={() => setChoice('isolated')}>
            <strong>Isolated</strong>
            <span>The position only uses the margin you give it. If it’s liquidated, you lose that margin and nothing else.</span>
          </button>
        </div>
        <p className="muted small">You can only switch with no open {coin} position or orders.</p>
        {error && <p className="form-error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Cancel
          </button>
          <button type="button" className="primary" onClick={() => void save()} disabled={saving || choice === mode}>
            {saving ? 'Saving…' : 'Confirm'}
          </button>
        </div>
      </dialog>
    </>
  )
}

/** Small button + dialog to add or remove margin on an isolated position. */
export function IsolatedMarginControl({ coin, margin, available }: { coin: string; margin: number; available: number }) {
  const secret = useAccount((s) => s.secret)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [direction, setDirection] = useState<'add' | 'remove'>('add')
  const [amount, setAmount] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const open = () => {
    setAmount('')
    setError(null)
    dialogRef.current?.showModal()
  }

  const save = async () => {
    if (!secret || !(Number(amount) > 0)) return
    setSaving(true)
    setError(null)
    try {
      await api.adjustIsolatedMargin(secret, coin, direction === 'add' ? amount : `-${amount}`)
      await refreshAccount()
      toast(`${direction === 'add' ? 'Added' : 'Removed'} $${amount} ${direction === 'add' ? 'to' : 'from'} ${coin} margin`, 'success')
      dialogRef.current?.close()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <button type="button" className="icon-btn" onClick={open} aria-label={`Adjust ${coin} isolated margin`} title="Adjust margin">
        ✎
      </button>
      <dialog ref={dialogRef} className="dialog" aria-labelledby={`iso-${coin}`}>
        <h3 id={`iso-${coin}`}>{coin} isolated margin</h3>
        <div className="segmented">
          {(['add', 'remove'] as const).map((d) => (
            <button key={d} type="button" className={direction === d ? 'active' : ''} onClick={() => setDirection(d)}>
              {d === 'add' ? 'Add' : 'Remove'}
            </button>
          ))}
        </div>
        <p className="muted small">
          Current margin {formatUsd(margin)} · available to add {formatUsd(Math.max(0, available))}. More margin moves
          the liquidation price further away.
        </p>
        <label className="field">
          <span>Amount (USD)</span>
          <input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="0" />
        </label>
        {error && <p className="form-error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Cancel
          </button>
          <button type="button" className="primary" onClick={() => void save()} disabled={saving || !(Number(amount) > 0)}>
            {saving ? 'Saving…' : direction === 'add' ? 'Add margin' : 'Remove margin'}
          </button>
        </div>
      </dialog>
    </>
  )
}
