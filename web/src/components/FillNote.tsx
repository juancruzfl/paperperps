import { useRef, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { useAccount } from '../lib/account'
import { toast } from '../lib/toasts'
import type { Fill } from '../lib/types'

export const MAX_NOTE_LENGTH = 1000

/** A fill's journal note in Trade History, with a dialog to write or edit it. */
export function FillNote({ fill, title }: { fill: Fill; title: string }) {
  const secret = useAccount((s) => s.secret)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [text, setText] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const open = () => {
    setText(fill.note)
    setError(null)
    dialogRef.current?.showModal()
  }

  const save = async () => {
    if (!secret) return
    const note = text.trim()
    setSaving(true)
    setError(null)
    try {
      await api.setFillNote(secret, fill.id, note)
      useAccount.setState((s) => ({
        history: { ...s.history, fills: s.history.fills?.map((f) => (f.id === fill.id ? { ...f, note } : f)) },
      }))
      toast(note ? 'Note saved' : 'Note removed', 'success')
      dialogRef.current?.close()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      {fill.note ? (
        <button type="button" className="link fill-note" title={fill.note} onClick={open}>
          {fill.note}
        </button>
      ) : (
        <button type="button" className="icon-btn fill-note-add" onClick={open} aria-label={`Add a note to ${title}`}>
          + Note
        </button>
      )}
      <dialog ref={dialogRef} className="dialog note-dialog" aria-labelledby={`note-${fill.id}`}>
        <h3 id={`note-${fill.id}`}>Journal · {title}</h3>
        <p className="muted small">Why did you take this trade, and what would you do differently? Only you can see this.</p>
        <textarea
          className="note-input"
          rows={5}
          maxLength={MAX_NOTE_LENGTH}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="e.g. Entered on the breakout, stop too tight, got wicked out."
        />
        <div className="note-count muted small">
          {text.length}/{MAX_NOTE_LENGTH}
        </div>
        {error && <p className="form-error">{error}</p>}
        <div className="dialog-actions">
          {fill.note && (
            <button type="button" className="ghost note-delete" onClick={() => setText('')} disabled={saving}>
              Clear
            </button>
          )}
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
