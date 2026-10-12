import { useRef, useState } from 'react'
import { formatNumber, pxDecimals } from '../lib/format'
import { addPriceAlert, MAX_PRICE_ALERTS, removePriceAlert, usePriceAlerts } from '../lib/priceAlerts'
import { toast } from '../lib/toasts'
import type { Ticker } from '../lib/types'

/** "Alerts" button in the stats bar, with a dialog to add and remove price alerts. */
export function PriceAlertControl({ ticker }: { ticker: Ticker }) {
  const alerts = usePriceAlerts((s) => s.alerts)
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [px, setPx] = useState('')
  const [error, setError] = useState<string | null>(null)
  const mark = Number(ticker.markPx)
  const dec = pxDecimals(mark, ticker.szDecimals)
  const forCoin = alerts.filter((a) => a.coin === ticker.coin).length
  // This coin's alerts first, then the rest; nearest price first within a coin.
  const sorted = [...alerts].sort(
    (a, b) => Number(b.coin === ticker.coin) - Number(a.coin === ticker.coin) || a.coin.localeCompare(b.coin) || a.px - b.px,
  )

  const open = () => {
    setPx('')
    setError(null)
    dialogRef.current?.showModal()
  }

  const add = () => {
    const target = Number(px)
    const problem = addPriceAlert(ticker.coin, target, mark)
    setError(problem)
    if (problem) return
    setPx('')
    toast(`Alert set: ${ticker.coin} ${target > mark ? 'rises' : 'falls'} to ${formatNumber(target, dec)}`, 'success')
  }

  return (
    <>
      <button type="button" className="small-btn alerts-btn" onClick={open} aria-label={`Price alerts for ${ticker.coin}`}>
        <span aria-hidden>🔔</span> Alerts{forCoin > 0 && <span className="alerts-count">{forCoin}</span>}
      </button>
      <dialog ref={dialogRef} className="dialog alerts-dialog" aria-labelledby="price-alerts-title">
        <h3 id="price-alerts-title">Price alerts</h3>
        <p className="muted small">
          {ticker.coin} mark is <span className="mono">{formatNumber(mark, dec)}</span>. Get a sound and a toast when it
          reaches your price.
        </p>
        <form
          className="alerts-form"
          onSubmit={(e) => {
            e.preventDefault()
            add()
          }}
        >
          <label className="field">
            <span>{ticker.coin} at</span>
            <input inputMode="decimal" value={px} onChange={(e) => setPx(e.target.value)} placeholder={formatNumber(mark, dec)} />
          </label>
          <button type="submit" className="primary small-btn" disabled={!px}>
            Add
          </button>
        </form>
        {error && <p className="form-error">{error}</p>}

        {sorted.length > 0 && (
          <ul className="alerts-list">
            {sorted.map((a) => (
              <li key={a.id}>
                <span>
                  <strong>{a.coin}</strong> <span className={a.above ? 'up' : 'down'}>{a.above ? '↑ rises' : '↓ falls'} to</span>{' '}
                  <span className="mono">{a.px.toLocaleString('en-US', { maximumFractionDigits: 8 })}</span>
                </span>
                <button type="button" className="icon-btn" onClick={() => removePriceAlert(a.id)} aria-label={`Remove ${a.coin} alert at ${a.px}`}>
                  ✕
                </button>
              </li>
            ))}
          </ul>
        )}
        <p className="muted small alerts-note">
          {alerts.length}/{MAX_PRICE_ALERTS} alerts. Each fires once. They’re checked while PaperPerps is open in this
          browser; turn on desktop notifications in the account menu to hear about them from another tab.
        </p>
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Done
          </button>
        </div>
      </dialog>
    </>
  )
}
