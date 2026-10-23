import { useRef, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { refreshAccount, useAccount } from '../lib/account'
import { MAX_DEGEN_LEVERAGE, TAKER_FEE } from '../lib/margin'
import { toast } from '../lib/toasts'

type Props = { coin: string; leverage: number; maxLeverage: number }

// Quick picks above the real cap; a 1–1000 slider alone is too coarse to aim with.
const DEGEN_PRESETS = [100, 250, 500, MAX_DEGEN_LEVERAGE]

const pct = (ratio: number) => `${(ratio * 100).toFixed(ratio < 0.01 ? 3 : 1)}%`

export function LeverageControl({ coin, leverage, maxLeverage }: Props) {
  const secret = useAccount((s) => s.secret)
  const degenMode = useAccount((s) => s.me?.degenMode ?? false)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [value, setValue] = useState(leverage)
  const [saving, setSaving] = useState(false)
  const [toggling, setToggling] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const limit = degenMode ? Math.max(MAX_DEGEN_LEVERAGE, maxLeverage) : maxLeverage
  const aboveCap = value > maxLeverage

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

  const toggleDegen = async (on: boolean) => {
    if (!secret) return
    setToggling(true)
    setError(null)
    try {
      await api.setDegenMode(secret, on)
      await refreshAccount()
      if (!on) setValue((v) => Math.min(v, maxLeverage))
      toast(on ? `Degen mode on: up to ${MAX_DEGEN_LEVERAGE}x` : 'Degen mode off', on ? 'success' : 'info')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setToggling(false)
    }
  }

  return (
    <>
      <button
        type="button"
        className={`chip${leverage > maxLeverage ? ' degen' : ''}`}
        onClick={open}
        aria-label={`Leverage ${leverage}x, change`}
      >
        {leverage}x
      </button>
      <dialog ref={dialogRef} className="dialog" aria-labelledby="leverage-title">
        <h3 id="leverage-title">{coin} leverage</h3>
        <p className="muted small">
          Higher leverage needs less margin per position, but a smaller move against you can wipe it
          out. Max for {coin}: {maxLeverage}x{degenMode && `, or ${limit}x in degen mode`}.
        </p>
        <div className="leverage-row">
          <input
            type="range"
            min={1}
            max={limit}
            value={value}
            onChange={(e) => setValue(Number(e.target.value))}
            aria-label="Leverage"
          />
          <input
            type="number"
            className="leverage-input"
            min={1}
            max={limit}
            value={value}
            onChange={(e) => setValue(Math.min(limit, Math.max(1, Math.round(Number(e.target.value) || 1))))}
            aria-label="Leverage, exact"
          />
          <span className="muted">x</span>
        </div>
        {degenMode && (
          <div className="leverage-presets">
            {[maxLeverage, ...DEGEN_PRESETS.filter((p) => p > maxLeverage)].map((p) => (
              <button key={p} type="button" className={`chip${value === p ? ' active' : ''}`} onClick={() => setValue(p)}>
                {p}x
              </button>
            ))}
          </div>
        )}
        {aboveCap && (
          <p className="degen-warning small" role="note">
            Above Hyperliquid’s {maxLeverage}x cap. At {value}x a market order’s fee alone is {pct(TAKER_FEE * value)} of
            your margin, and a move of about {pct(1 / (2 * value))} against you gets you liquidated.
          </p>
        )}
        <label className="check degen-toggle">
          <input type="checkbox" checked={degenMode} disabled={toggling} onChange={(e) => void toggleDegen(e.target.checked)} />
          <span>
            <strong>Degen mode</strong>
            <span className="muted small">
              {' '}
              Up to {MAX_DEGEN_LEVERAGE}x on every market. Trading above a market’s real cap puts a degen badge on your
              leaderboard entry and profile for good.
            </span>
          </span>
        </label>
        {error && <p className="form-error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Cancel
          </button>
          <button type="button" className="primary" onClick={() => void save()} disabled={saving || value === leverage || value > limit}>
            {saving ? 'Saving…' : 'Confirm'}
          </button>
        </div>
      </dialog>
    </>
  )
}
