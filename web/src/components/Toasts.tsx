import { dismissToast, useToasts } from '../lib/toasts'

export function Toasts() {
  const toasts = useToasts((s) => s.toasts)
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => (
        <button key={t.id} type="button" className={`toast ${t.tone}`} onClick={() => dismissToast(t.id)}>
          {t.text}
        </button>
      ))}
    </div>
  )
}
