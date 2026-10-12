import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { formatNumber, formatUsd, pxDecimals } from '../lib/format'
import { useGuide } from '../lib/guide'
import type { Ticker } from '../lib/types'

// The beginner guide: a short tour of the trade screen that explains perps, leverage, margin
// and liquidation, pointing at the real controls as it goes.

type Step = {
  /** CSS selector of the element to point at; the card is centered when it's missing or hidden. */
  target?: string
  title: string
  body: (ticker: Ticker | undefined) => ReactNode
  /** Runs before the step shows, e.g. to switch a phone's tab to what it points at. */
  prepare?: () => void
}

const EXAMPLE_MARGIN = 100
const EXAMPLE_LEVERAGE = 10

const STEPS: Step[] = [
  {
    title: 'Welcome to perps',
    body: () => (
      <>
        <p>
          A <strong>perpetual future</strong> (perp) lets you bet on a coin’s price without owning it.{' '}
          <strong>Long</strong> profits when the price goes up; <strong>short</strong> profits when it goes down.
        </p>
        <p>Everything here is paper money, so this is the place to make mistakes. This tour takes about a minute.</p>
      </>
    ),
  },
  {
    target: '.selector',
    title: 'Pick a market',
    body: () => (
      <p>
        Each market is a coin priced in USD, with live prices from Hyperliquid. The badge shows the most leverage that
        market allows.
      </p>
    ),
  },
  {
    target: '.stats-bar',
    title: 'The mark price',
    body: () => (
      <p>
        Your profit and loss, and liquidations, use the <strong>mark price</strong>. <strong>Funding</strong> is a small
        hourly payment between longs and shorts that keeps the perp near the real coin’s price.
      </p>
    ),
  },
  {
    target: '.book-panel',
    // Phones show the book behind a tab.
    prepare: () => {
      const tab = [...document.querySelectorAll<HTMLButtonElement>('.mobile-tabs button')].find((b) => b.textContent === 'Order Book')
      if (tab && tab.offsetParent !== null) tab.click()
    },
    title: 'The order book',
    body: () => (
      <p>
        Red rows are people selling, green rows people buying. A <strong>market order</strong> fills right away against
        these rows; a big order eats through several and gets a worse average price.
      </p>
    ),
  },
  {
    target: '.order-form .form-row',
    title: 'Leverage',
    body: (t) => {
      const notional = EXAMPLE_MARGIN * EXAMPLE_LEVERAGE
      const mark = t ? Number(t.markPx) : 0
      // Isolated margin: liquidated when losses leave only maintenance margin, which is half
      // the margin needed at max leverage (docs/plan.md §4.5).
      const maintenance = t ? 1 / (2 * t.maxLeverage) : 0
      const liqMove = 1 / EXAMPLE_LEVERAGE - maintenance
      return (
        <>
          <p>
            Leverage multiplies your position. With {formatUsd(EXAMPLE_MARGIN, { decimals: 0 })} of margin at{' '}
            {EXAMPLE_LEVERAGE}x, you control {formatUsd(notional, { decimals: 0 })}
            {t && mark > 0 && (
              <>
                {' '}
                of {t.coin} (about {formatNumber(notional / mark, t.szDecimals)} {t.coin} at{' '}
                {formatNumber(mark, pxDecimals(mark, t.szDecimals))})
              </>
            )}
            .
          </p>
          <p>
            A 1% move is then {formatUsd(notional / 100, { decimals: 0 })}: 10% of your margin, in either direction.
          </p>
          {t && (
            <p>
              In <strong>isolated</strong> margin, where only that {formatUsd(EXAMPLE_MARGIN, { decimals: 0 })} backs the
              trade, a move of about <strong>{(liqMove * 100).toFixed(1)}%</strong> against you liquidates it. In{' '}
              <strong>cross</strong> margin (the default), your whole balance backs it, so it lasts longer but can lose
              more.
            </p>
          )}
        </>
      )
    },
  },
  {
    target: '.side-toggle',
    title: 'Long or short',
    body: () => (
      <p>
        Choose <strong>Buy / Long</strong> if you think the price will rise, <strong>Sell / Short</strong> if you think
        it will fall. Then enter a size, in the coin or with the percentage buttons.
      </p>
    ),
  },
  {
    target: '.order-info',
    title: 'Margin and liquidation',
    body: () => (
      <>
        <p>
          <strong>Margin required</strong> is the part of your balance the trade locks up. <strong>Est. liq. price</strong>{' '}
          is where you’d be <strong>liquidated</strong>: the position is closed automatically and that margin is lost.
        </p>
        <p>Higher leverage means less margin, but a liquidation price much closer to where you are.</p>
      </>
    ),
  },
  {
    target: '.order-form .options',
    title: 'Protect yourself',
    body: () => (
      <p>
        Tick <strong>Take profit / stop loss</strong> to close the trade automatically at a gain, or before a loss gets
        big. A stop loss above your liquidation price means a bad trade costs what you planned, not everything.
      </p>
    ),
  },
  {
    target: '.account-panel',
    title: 'Your positions',
    body: () => (
      <p>
        Open positions show their live profit, liquidation price and a Close button. Trade History keeps every fill, and
        you can add a journal note to each one.
      </p>
    ),
  },
  {
    title: 'Try a first trade',
    body: () => (
      <>
        <p>
          A good first trade: pick BTC, set leverage to {EXAMPLE_LEVERAGE}x or less, tap <strong>25%</strong>, add a stop
          loss, and place it. Then watch how the profit moves with the price.
        </p>
        <p>
          If it goes badly, you can reset to $1,000 once a day. Find this guide again in the account menu, and the full
          rules under <a href="/about">How it works</a>.
        </p>
      </>
    ),
  },
]

type Box = { top: number; left: number; width: number; height: number }

const PAD = 6
const GAP = 12
const CARD_WIDTH = 340

/** The tour itself; renders nothing until startGuide() is called. */
export function Guide({ ticker }: { ticker: Ticker | undefined }) {
  const step = useGuide((s) => s.step)
  const cardRef = useRef<HTMLDivElement>(null)
  const [box, setBox] = useState<Box | null>(null)
  const [cardPos, setCardPos] = useState<{ top: number; left: number } | null>(null)
  const current = step === null ? null : STEPS[step]

  const close = useCallback(() => useGuide.setState({ step: null }), [])
  const go = useCallback((delta: number) => {
    useGuide.setState((s) => {
      if (s.step === null) return s
      const next = s.step + delta
      return { step: next < 0 || next >= STEPS.length ? null : next }
    })
  }, [])

  // Find the target, scroll it into view, and place the card next to it.
  const place = useCallback(() => {
    if (!current) return
    const el = current.target ? document.querySelector<HTMLElement>(current.target) : null
    const r = el?.getBoundingClientRect()
    const visible = r && r.width > 0 && r.height > 0
    const card = cardRef.current?.getBoundingClientRect()
    const cardH = card?.height ?? 200
    const cardW = Math.min(CARD_WIDTH, window.innerWidth - 32)
    if (!visible) {
      setBox(null)
      setCardPos({ top: Math.max(16, (window.innerHeight - cardH) / 2), left: (window.innerWidth - cardW) / 2 })
      return
    }
    setBox({ top: r.top - PAD, left: r.left - PAD, width: r.width + 2 * PAD, height: r.height + 2 * PAD })
    // Prefer below, then above, then beside (for tall targets), then over the target's top.
    let top: number
    let left = Math.min(Math.max(16, r.left), window.innerWidth - cardW - 16)
    if (r.bottom + GAP + cardH <= window.innerHeight - 8) top = r.bottom + GAP
    else if (r.top - GAP - cardH >= 8) top = r.top - GAP - cardH
    else if (r.left - GAP - cardW >= 8) {
      top = Math.min(Math.max(8, r.top), window.innerHeight - cardH - 8)
      left = r.left - GAP - cardW
    } else if (r.right + GAP + cardW <= window.innerWidth - 8) {
      top = Math.min(Math.max(8, r.top), window.innerHeight - cardH - 8)
      left = r.right + GAP
    } else top = Math.max(8, Math.min(r.top + 8, window.innerHeight - cardH - 8))
    setCardPos({ top, left })
  }, [current])

  useEffect(() => {
    if (!current?.target) return
    current.prepare?.()
    const el = document.querySelector<HTMLElement>(current.target)
    el?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [current])

  useEffect(() => {
    if (!current) return
    // Next frame, once the card has its real height; and again after any smooth scroll.
    const raf = requestAnimationFrame(place)
    const timer = setTimeout(place, 400)
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => {
      cancelAnimationFrame(raf)
      clearTimeout(timer)
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, true)
    }
  }, [current, place])

  useEffect(() => {
    if (step === null) return
    cardRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
      else if (e.key === 'ArrowRight') go(1)
      else if (e.key === 'ArrowLeft') go(-1)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [step, close, go])

  if (step === null || !current) return null
  const last = step === STEPS.length - 1

  return (
    <div className="guide">
      {box ? (
        <div className="guide-spot" style={{ top: box.top, left: box.left, width: box.width, height: box.height }} />
      ) : (
        <div className="guide-dim" />
      )}
      <div
        ref={cardRef}
        className="guide-card"
        role="dialog"
        aria-labelledby="guide-title"
        tabIndex={-1}
        style={{ top: cardPos?.top ?? -9999, left: cardPos?.left ?? 0, width: Math.min(CARD_WIDTH, window.innerWidth - 32) }}
      >
        <div className="guide-progress muted small">
          {step + 1} of {STEPS.length}
        </div>
        <h3 id="guide-title">{current.title}</h3>
        <div className="guide-body">{current.body(ticker)}</div>
        <div className="guide-actions">
          <button type="button" className="link muted" onClick={close}>
            {last ? 'Close' : 'Skip tour'}
          </button>
          <span className="guide-nav">
            {step > 0 && (
              <button type="button" className="ghost small-btn" onClick={() => go(-1)}>
                Back
              </button>
            )}
            <button type="button" className="primary small-btn" onClick={() => go(1)}>
              {last ? 'Start trading' : 'Next'}
            </button>
          </span>
        </div>
      </div>
    </div>
  )
}
