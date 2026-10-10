import { useEffect, useRef, useState } from 'react'
import { desktopPermission, setDesktop, setSounds, useAlertPrefs } from '../lib/alerts'
import { api, ApiError } from '../lib/api'
import {
  dismissRecoveryNotice,
  forgetAccount,
  refreshAccount,
  recoveryLink,
  recoveryNoticeDismissed,
  restoreAccount,
  useAccount,
} from '../lib/account'
import { formatUsd } from '../lib/format'
import { coinStates, summarize } from '../lib/margin'
import { useTickers } from '../lib/market'
import { toast } from '../lib/toasts'

async function copy(text: string, what: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast(`${what} copied`, 'success')
  } catch {
    window.prompt(`Copy your ${what.toLowerCase()}:`, text)
  }
}

export function AccountMenu() {
  const { me, secret, status } = useAccount()
  const tickers = useTickers()
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const restoreRef = useRef<HTMLDialogElement>(null)
  const resetRef = useRef<HTMLDialogElement>(null)
  const deleteRef = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  if (status !== 'ready' || !me || !secret) {
    return <div className="account-menu muted small">{status === 'error' ? 'Account unavailable' : 'Loading account…'}</div>
  }
  const value = summarize(Number(me.balance), coinStates(me, tickers)).accountValue

  return (
    <div className="account-menu" ref={rootRef}>
      <button type="button" className="account-button" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <span className="account-value num">{formatUsd(value)}</span>
        <span className="account-handle">{me.handle}</span>
      </button>
      {open && (
        <div className="menu-panel" role="menu">
          <p className="menu-note">
            Your account has no password. Anyone with your recovery link can use it, so keep it
            private, and save it to use this account on another device.
          </p>
          <button type="button" role="menuitem" onClick={() => void copy(recoveryLink(secret), 'Recovery link')}>
            Copy recovery link
          </button>
          <button type="button" role="menuitem" onClick={() => void copy(secret, 'Recovery key')}>
            Copy recovery key
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false)
              restoreRef.current?.showModal()
            }}
          >
            Restore another account…
          </button>
          <AlertToggles />
          <button
            type="button"
            role="menuitem"
            className="danger"
            onClick={() => {
              setOpen(false)
              void refreshAccount() // fresh cooldown and PnL figures for the dialog
              resetRef.current?.showModal()
            }}
          >
            Reset account…
          </button>
          <button
            type="button"
            role="menuitem"
            className="danger"
            onClick={() => {
              setOpen(false)
              deleteRef.current?.showModal()
            }}
          >
            Delete account…
          </button>
        </div>
      )}
      <RestoreDialog dialogRef={restoreRef} />
      <ResetDialog dialogRef={resetRef} />
      <DeleteDialog dialogRef={deleteRef} />
    </div>
  )
}

/** Sound and desktop-notification switches for fills and liquidations. */
function AlertToggles() {
  const { sounds, desktop } = useAlertPrefs()
  return (
    <div className="menu-section" role="group" aria-label="Alerts">
      <p className="menu-heading">Fill and liquidation alerts</p>
      <label className="check menu-check">
        <input type="checkbox" checked={sounds} onChange={(e) => setSounds(e.target.checked)} />
        Play a sound
      </label>
      {desktopPermission() !== 'unsupported' && (
        <label className="check menu-check">
          <input type="checkbox" checked={desktop} onChange={(e) => void setDesktop(e.target.checked)} />
          Notify me when this tab is in the background
        </label>
      )}
    </div>
  )
}

function ResetDialog({ dialogRef }: { dialogRef: React.RefObject<HTMLDialogElement | null> }) {
  const { me, secret } = useAccount()
  const tickers = useTickers()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (!me || !secret) return <dialog ref={dialogRef} className="dialog" />

  const openPnl = summarize(Number(me.balance), coinStates(me, tickers)).unrealizedPnl
  const hasPositions = me.positions.some((p) => Number(p.size) !== 0)
  // The server only sends resetAvailableAt while the cooldown is running.
  const nextReset = me.resetAvailableAt ? new Date(me.resetAvailableAt) : null
  const waiting = nextReset !== null

  const reset = async () => {
    setBusy(true)
    setError(null)
    try {
      await api.reset(secret)
      await refreshAccount()
      toast('Account reset: you have a fresh $1,000', 'success')
      dialogRef.current?.close()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog ref={dialogRef} className="dialog" aria-labelledby="reset-title">
      <h3 id="reset-title">Reset your account?</h3>
      <ul className="reset-list small">
        <li>Your balance goes back to $1,000 and your open orders are cancelled.</li>
        <li>
          Open positions are wiped, not traded.
          {hasPositions && (
            <>
              {' '}
              Their unrealized PnL right now (<strong className={openPnl < 0 ? 'down' : 'up'}>{formatUsd(openPnl, { signed: true })}</strong>)
              is locked in.
            </>
          )}
        </li>
        <li>
          Your leaderboard score keeps everything you made or lost before the reset (lifetime PnL now{' '}
          <strong>{formatUsd(Number(me.lifetimePnl), { signed: true })}</strong>).
        </li>
        <li>You can reset once every 24 hours.</li>
      </ul>
      {nextReset && <p className="form-error">You can reset again at {nextReset.toLocaleString()}.</p>}
      {error && <p className="form-error">{error}</p>}
      <div className="dialog-actions">
        <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
          Cancel
        </button>
        <button type="button" className="primary danger" onClick={() => void reset()} disabled={busy || waiting}>
          {busy ? 'Resetting…' : 'Reset to $1,000'}
        </button>
      </div>
    </dialog>
  )
}

function DeleteDialog({ dialogRef }: { dialogRef: React.RefObject<HTMLDialogElement | null> }) {
  const { me, secret } = useAccount()
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const remove = async () => {
    if (!secret) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteAccount(secret)
      forgetAccount()
      // Start over as a new visitor.
      location.assign('/')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
      setBusy(false)
    }
  }

  return (
    <dialog ref={dialogRef} className="dialog" aria-labelledby="delete-title">
      <h3 id="delete-title">Delete your account?</h3>
      <p className="small">
        This permanently deletes <strong>{me?.handle}</strong> and everything we store about it: balances, positions,
        orders, trade and funding history, and your place on the leaderboard. It can’t be undone, and your recovery link
        stops working. This browser then starts a new account.
      </p>
      <label className="field">
        <span>Type delete</span>
        <input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
      </label>
      {error && <p className="form-error">{error}</p>}
      <div className="dialog-actions">
        <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
          Cancel
        </button>
        <button type="button" className="primary danger" onClick={() => void remove()} disabled={busy || typed.trim().toLowerCase() !== 'delete'}>
          {busy ? 'Deleting…' : 'Delete forever'}
        </button>
      </div>
    </dialog>
  )
}

function RestoreDialog({ dialogRef }: { dialogRef: React.RefObject<HTMLDialogElement | null> }) {
  const [key, setKey] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    // Accept a full recovery link as well as a bare key.
    const secret = key.includes('#') ? key.slice(key.indexOf('#') + 1) : key
    setBusy(true)
    setError(null)
    try {
      await restoreAccount(secret)
      toast('Account restored', 'success')
      setKey('')
      dialogRef.current?.close()
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? 'No account matches that key.' : 'Could not reach the server.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog ref={dialogRef} className="dialog" aria-labelledby="restore-title">
      <h3 id="restore-title">Restore an account</h3>
      <p className="muted small">
        Paste a recovery link or key. This browser will switch to that account; save your current
        recovery link first if you want to come back to it.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        <input className="text-input" value={key} onChange={(e) => setKey(e.target.value)} placeholder="Recovery link or key" autoComplete="off" />
        {error && <p className="form-error">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Cancel
          </button>
          <button type="submit" className="primary" disabled={!key.trim() || busy}>
            {busy ? 'Restoring…' : 'Restore'}
          </button>
        </div>
      </form>
    </dialog>
  )
}

export function RecoveryNotice() {
  const { secret, newlyCreated } = useAccount()
  const [hidden, setHidden] = useState(recoveryNoticeDismissed)
  if (!secret || hidden || !newlyCreated) return null
  const dismiss = () => {
    dismissRecoveryNotice()
    setHidden(true)
  }
  return (
    <div className="notice" role="status">
      <span>
        You’ve got <strong>$1,000</strong> in paper money. Save your recovery link to keep this
        account on another device; there’s no login. By using PaperPerps you agree to the{' '}
        <a href="/terms">Terms</a> and <a href="/privacy">Privacy Policy</a>.
      </span>
      <span className="notice-actions">
        <button type="button" className="primary small-btn" onClick={() => void copy(recoveryLink(secret), 'Recovery link')}>
          Copy recovery link
        </button>
        <button type="button" className="ghost small-btn" onClick={dismiss}>
          Dismiss
        </button>
      </span>
    </div>
  )
}
