// Draws a share card on a canvas: 1200×630, the size social networks show large. Every
// card says PAPER TRADING, so a screenshot can't pass as real gains.

import { formatPct, formatUsd } from './format'
import type { ShareCard } from './share'

export const CARD_W = 1200
export const CARD_H = 630

const C = {
  bg: '#0b0f14',
  surface: '#121821',
  border: '#1f2833',
  text: '#e6edf3',
  muted: '#8b98a5',
  long: '#3ddc97',
  short: '#ff5c7a',
  warn: '#f5b942',
}
const SANS = 'Inter, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif'
const font = (weight: number, size: number) => `${weight} ${size}px ${SANS}`

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  ctx.roundRect(x, y, w, h, r)
}

/** A rounded label; returns its width. */
function pill(ctx: CanvasRenderingContext2D, text: string, x: number, y: number, fg: string, bg: string, size = 22): number {
  ctx.font = font(700, size)
  const w = ctx.measureText(text).width + size * 1.2
  const h = size * 1.75
  roundRect(ctx, x, y, w, h, h / 2)
  ctx.fillStyle = bg
  ctx.fill()
  ctx.fillStyle = fg
  ctx.textBaseline = 'middle'
  ctx.fillText(text, x + size * 0.6, y + h / 2 + 1)
  ctx.textBaseline = 'alphabetic'
  return w
}

function logo(ctx: CanvasRenderingContext2D, x: number, y: number, size: number) {
  const s = size / 32
  roundRect(ctx, x, y, size, size, 7 * s)
  ctx.fillStyle = C.surface
  ctx.fill()
  ctx.beginPath()
  ctx.moveTo(x + 7 * s, y + 21 * s)
  ctx.lineTo(x + 13 * s, y + 15 * s)
  ctx.lineTo(x + 17 * s, y + 19 * s)
  ctx.lineTo(x + 25 * s, y + 10 * s)
  ctx.strokeStyle = C.long
  ctx.lineWidth = 3 * s
  ctx.lineCap = 'round'
  ctx.lineJoin = 'round'
  ctx.stroke()
}

/** Shrinks the font until text fits in maxWidth. */
function fitText(ctx: CanvasRenderingContext2D, text: string, weight: number, size: number, maxWidth: number) {
  let s = size
  ctx.font = font(weight, s)
  while (s > 20 && ctx.measureText(text).width > maxWidth) {
    s -= 4
    ctx.font = font(weight, s)
  }
}

export function drawShareCard(canvas: HTMLCanvasElement, card: ShareCard) {
  canvas.width = CARD_W
  canvas.height = CARD_H
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  const up = card.pnl >= 0
  const tone = up ? C.long : C.short

  ctx.fillStyle = C.bg
  ctx.fillRect(0, 0, CARD_W, CARD_H)
  const glow = ctx.createRadialGradient(CARD_W * 0.85, CARD_H * 1.1, 0, CARD_W * 0.85, CARD_H * 1.1, 700)
  glow.addColorStop(0, up ? 'rgba(61, 220, 151, 0.22)' : 'rgba(255, 92, 122, 0.2)')
  glow.addColorStop(1, 'rgba(0, 0, 0, 0)')
  ctx.fillStyle = glow
  ctx.fillRect(0, 0, CARD_W, CARD_H)

  // Header: brand, and the paper-trading label.
  logo(ctx, 64, 56, 52)
  ctx.fillStyle = C.text
  ctx.font = font(700, 32)
  ctx.fillText('PaperPerps', 132, 94)
  ctx.font = font(700, 20)
  const label = 'PAPER TRADING · NOT REAL FUNDS'
  const lw = ctx.measureText(label).width + 24
  pill(ctx, label, CARD_W - 64 - lw, 62, C.bg, C.warn, 20)

  trendLine(ctx, up, tone)
  if (card.kind === 'profile') drawProfile(ctx, card, tone)
  else drawTrade(ctx, card, tone)

  // Footer: who, and where to find them.
  ctx.fillStyle = C.border
  ctx.fillRect(64, CARD_H - 100, CARD_W - 128, 2)
  ctx.font = font(600, 26)
  ctx.fillStyle = C.muted
  ctx.fillText(card.kind === 'profile' ? 'Lifetime PnL on paper' : `@${card.handle}`, 64, CARD_H - 46)
  ctx.textAlign = 'right'
  ctx.fillStyle = C.text
  ctx.fillText(card.link, CARD_W - 64, CARD_H - 46)
  ctx.textAlign = 'left'
}

// Room the headline numbers may use before reaching the trend line.
const TEXT_W = 660

/** A decorative price line on the right: rising for a gain, falling for a loss. */
function trendLine(ctx: CanvasRenderingContext2D, up: boolean, tone: string) {
  const steps = [0, 0.18, 0.1, 0.34, 0.27, 0.5, 0.44, 0.68, 0.6, 0.86, 1]
  const x0 = 760
  const x1 = CARD_W - 64
  const top = 170
  const bottom = 420
  const pts = steps.map((v, i) => {
    const t = i / (steps.length - 1)
    const y = up ? bottom - v * (bottom - top) : top + v * (bottom - top)
    return [x0 + t * (x1 - x0), y] as const
  })
  const fill = ctx.createLinearGradient(0, top, 0, bottom + 40)
  fill.addColorStop(0, up ? 'rgba(61, 220, 151, 0.22)' : 'rgba(255, 92, 122, 0.2)')
  fill.addColorStop(1, 'rgba(0, 0, 0, 0)')
  ctx.beginPath()
  ctx.moveTo(pts[0][0], bottom + 40)
  for (const [x, y] of pts) ctx.lineTo(x, y)
  ctx.lineTo(pts[pts.length - 1][0], bottom + 40)
  ctx.closePath()
  ctx.fillStyle = fill
  ctx.fill()
  ctx.beginPath()
  pts.forEach(([x, y], i) => (i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y)))
  ctx.strokeStyle = tone
  ctx.lineWidth = 5
  ctx.lineCap = 'round'
  ctx.lineJoin = 'round'
  ctx.stroke()
  const [lx, ly] = pts[pts.length - 1]
  ctx.beginPath()
  ctx.arc(lx, ly, 9, 0, Math.PI * 2)
  ctx.fillStyle = tone
  ctx.fill()
}

function drawTrade(ctx: CanvasRenderingContext2D, card: Extract<ShareCard, { kind: 'position' | 'closed' }>, tone: string) {
  const sideColor = card.side === 'long' ? C.long : C.short
  ctx.fillStyle = C.text
  ctx.font = font(800, 44)
  ctx.fillText(card.coin, 64, 200)
  let x = 64 + ctx.measureText(card.coin).width + 20
  x += pill(ctx, card.side.toUpperCase(), x, 165, C.bg, sideColor) + 10
  if (card.leverage) x += pill(ctx, `${card.leverage}x`, x, 165, C.text, C.border) + 10
  pill(ctx, card.kind === 'position' ? 'OPEN' : 'CLOSED', x, 165, C.muted, C.surface)

  // The headline number: ROE when known, else the dollar PnL.
  const headline = card.roe !== null ? formatPct(card.roe) : formatUsd(card.pnl, { signed: true })
  ctx.fillStyle = tone
  fitText(ctx, headline, 800, 140, TEXT_W)
  ctx.fillText(headline, 56, 340)

  ctx.font = font(700, 38)
  ctx.fillText(card.roe !== null ? `${formatUsd(card.pnl, { signed: true })}` : 'PnL', 64, 398)

  const cols: [string, string][] = [
    ['Entry', card.entryPx],
    [card.kind === 'position' ? 'Mark' : 'Exit', card.exitPx],
  ]
  cols.forEach(([k, v], i) => {
    const cx = 64 + i * 300
    ctx.font = font(500, 24)
    ctx.fillStyle = C.muted
    ctx.fillText(k, cx, 456)
    ctx.font = font(700, 34)
    ctx.fillStyle = C.text
    ctx.fillText(v, cx, 496)
  })
}

function drawProfile(ctx: CanvasRenderingContext2D, card: Extract<ShareCard, { kind: 'profile' }>, tone: string) {
  ctx.fillStyle = C.text
  fitText(ctx, `@${card.handle}`, 800, 52, TEXT_W)
  ctx.fillText(`@${card.handle}`, 64, 205)

  const headline = formatUsd(card.pnl, { signed: true })
  ctx.fillStyle = tone
  fitText(ctx, headline, 800, 130, TEXT_W)
  ctx.fillText(headline, 56, 340)

  const cols: [string, string][] = [
    ['Win rate', card.winRate === null ? '—' : formatPct(card.winRate, 0).replace('+', '')],
    ['Trades', card.trades.toLocaleString('en-US')],
    ['All-time rank', card.rank > 0 ? `#${card.rank}` : 'Unranked'],
  ]
  cols.forEach(([k, v], i) => {
    const cx = 64 + i * 240
    ctx.font = font(500, 24)
    ctx.fillStyle = C.muted
    ctx.fillText(k, cx, 444)
    ctx.font = font(700, 38)
    ctx.fillStyle = C.text
    ctx.fillText(v, cx, 490)
  })
}
