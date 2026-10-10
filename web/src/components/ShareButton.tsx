import { useRef, useState } from 'react'
import { cardFileName, type ShareCard } from '../lib/share'
import { drawShareCard } from '../lib/shareCard'
import { toast } from '../lib/toasts'

const toBlob = (canvas: HTMLCanvasElement) =>
  new Promise<Blob>((resolve, reject) => canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('no image'))), 'image/png'))

/**
 * "Share" button and dialog: previews the card, then shares it (phones), copies it, or
 * downloads it. build runs on open, so the card shows the numbers at that moment.
 */
export function ShareButton({ build, label = 'Share', className = 'small-btn' }: { build: () => ShareCard; label?: string; className?: string }) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const [card, setCard] = useState<ShareCard | null>(null)
  const [busy, setBusy] = useState(false)

  const open = () => {
    const c = build()
    setCard(c)
    dialogRef.current?.showModal()
    // The canvas is always mounted, so it can be drawn right away.
    if (canvasRef.current) drawShareCard(canvasRef.current, c)
  }

  const file = async () => {
    if (!canvasRef.current || !card) throw new Error('no card')
    return new File([await toBlob(canvasRef.current)], cardFileName(card), { type: 'image/png' })
  }

  const run = async (action: () => Promise<void>) => {
    setBusy(true)
    try {
      await action()
    } catch (err) {
      // Closing the system share sheet isn't an error.
      if (!(err instanceof DOMException && err.name === 'AbortError')) toast('Couldn’t share the image', 'error')
    } finally {
      setBusy(false)
    }
  }

  const url = card && card.link.includes('/u/') ? `${location.protocol}//${card.link}` : null
  const canShareFiles = typeof navigator.canShare === 'function' && navigator.canShare({ files: [new File([], 'x.png', { type: 'image/png' })] })
  const canCopyImage = typeof ClipboardItem !== 'undefined' && !!navigator.clipboard?.write

  return (
    <>
      <button type="button" className={className} onClick={open}>
        {label}
      </button>
      <dialog ref={dialogRef} className="dialog share-dialog" aria-labelledby="share-title" onClose={() => setCard(null)}>
        <h3 id="share-title">Share</h3>
        <canvas ref={canvasRef} className="share-preview" role="img" aria-label="Share card preview" />
        <div className="share-actions">
          {canShareFiles && (
            <button type="button" className="primary" disabled={busy} onClick={() => void run(async () => {
              const f = await file()
              await navigator.share({ files: [f], ...(url ? { url } : {}) })
            })}>
              Share…
            </button>
          )}
          {canCopyImage && (
            <button type="button" className={canShareFiles ? 'ghost' : 'primary'} disabled={busy} onClick={() => void run(async () => {
              if (!canvasRef.current) return
              await navigator.clipboard.write([new ClipboardItem({ 'image/png': toBlob(canvasRef.current) })])
              toast('Image copied', 'success')
            })}>
              Copy image
            </button>
          )}
          <button type="button" className="ghost" disabled={busy} onClick={() => void run(async () => {
            const f = await file()
            const a = document.createElement('a')
            a.href = URL.createObjectURL(f)
            a.download = f.name
            a.click()
            setTimeout(() => URL.revokeObjectURL(a.href), 1000)
          })}>
            Download
          </button>
          {url && (
            <button type="button" className="ghost" disabled={busy} onClick={() => void run(async () => {
              await navigator.clipboard.writeText(url)
              toast('Profile link copied', 'success')
            })}>
              Copy profile link
            </button>
          )}
        </div>
        {!url && card?.kind !== 'profile' && (
          <p className="muted small share-note">Show yourself on the leaderboard to get a public profile link on your cards.</p>
        )}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={() => dialogRef.current?.close()}>
            Close
          </button>
        </div>
      </dialog>
    </>
  )
}
