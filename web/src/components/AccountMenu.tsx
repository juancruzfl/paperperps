import { useEffect, useRef, useState } from 'react'
import { ApiError } from '../lib/api'
import {
  dismissRecoveryNotice,
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
        </div>
      )}
      <RestoreDialog dialogRef={restoreRef} />
    </div>
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
        account on another device; there’s no login.
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
