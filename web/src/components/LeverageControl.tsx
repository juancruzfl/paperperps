import { useRef, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { refreshAccount, useAccount } from '../lib/account'
import { toast } from '../lib/toasts'

type Props = { coin: string; leverage: number; maxLeverage: number }

export function LeverageControl({ coin, leverage, maxLeverage }: Props) {
  const secret = useAccount((s) => s.secret)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [value, setValue] = useState(leverage)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const open = () => {
    setValue(leverage)
    setError(null)
    dialogRef.current?.showModal()
  }

  const save = async () => {
    if (!secret) return
    setSaving(true)
    setError(null)
    try {
      await api.setLeverage(secret, coin, value)
      await refreshAccount()
      toast(`${coin} leverage set to ${value}x`, 'success')
      dialogRef.current?.close()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <button type="button" className="chip" onClick={open} aria-label={`Leverage ${leverage}x, change`}>
        {leverage}x
      </button>
      <dialog ref={dialogRef} className="dialog" aria-labelledby="leverage-title">
        <h3 id="leverage-title">{coin} leverage</h3>
        <p className="muted small">
          Higher leverage needs less margin per position, but a smaller move against you can wipe it
          out. Max for {coin}: {maxLeverage}x.
        </p>
        <div className="leverage-row">
          <input
            type="range"
            min={1}
            max={maxLeverage}
            value={value}
            onChange={(e) => setValue(Number(e.target.value))}
            aria-label="Leverage"
          />
          <span className="num leverage-value">{value}x</span>
        </div>
        {error && <p className="form-error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Cancel
          </button>
          <button type="button" className="primary" onClick={() => void save()} disabled={saving || value === leverage}>
            {saving ? 'Saving…' : 'Confirm'}
          </button>
        </div>
      </dialog>
    </>
  )
}
