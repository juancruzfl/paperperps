// What a share card shows. Drawing is in shareCard.ts; this is the math, kept testable.

import type { Fill, Me } from './types'

export type TradeCard = {
  kind: 'position' | 'closed'
  coin: string
  side: 'long' | 'short'
  leverage: number | null
  roe: number | null // return on margin, as a ratio
  pnl: number
  entryPx: string // formatted
  exitPx: string // formatted: the mark for an open position, the fill price for a close
  handle: string
  link: string
}

export type ProfileCard = {
  kind: 'profile'
  handle: string
  pnl: number
  winRate: number | null
  trades: number
  rank: number // 0 when unranked
  link: string
}

export type ShareCard = TradeCard | ProfileCard

/**
 * The address printed on a card: the trader's public profile when they have one (shown on
 * the leaderboard), otherwise the site.
 */
export function cardLink(me: Pick<Me, 'handle' | 'leaderboardOptIn' | 'leaderboardHidden' | 'suspension'> | null, host = location.host): string {
  if (me && me.leaderboardOptIn && !me.leaderboardHidden && !me.suspension) return `${host}/u/${me.handle}`
  return host
}

/** Return on the margin a position uses at the given price: pnl / (size · px / leverage). */
export function roe(pnl: number, size: number, px: number, leverage: number): number | null {
  const margin = (Math.abs(size) * px) / leverage
  return margin > 0 && leverage > 0 ? pnl / margin : null
}

/**
 * A closing fill only records its own price and the PnL it realized; the entry is implied:
 * selling closes a long (pnl = sz · (px − entry)), buying closes a short (pnl = sz · (entry − px)).
 */
export function impliedEntry(f: Pick<Fill, 'side' | 'px' | 'sz' | 'closedPnl'>): number {
  const px = Number(f.px)
  const perUnit = Number(f.closedPnl) / Number(f.sz)
  return f.side === 'sell' ? px - perUnit : px + perUnit
}

export function closedTradeCard(
  f: Fill,
  leverage: number | null,
  handle: string,
  link: string,
  fmtPx: (px: number) => string,
): TradeCard {
  const entry = impliedEntry(f)
  const pnl = Number(f.closedPnl)
  return {
    kind: 'closed',
    coin: f.coin,
    side: f.side === 'sell' ? 'long' : 'short',
    leverage,
    roe: leverage ? roe(pnl, Number(f.sz), entry, leverage) : null,
    pnl,
    entryPx: fmtPx(entry),
    exitPx: fmtPx(Number(f.px)),
    handle,
    link,
  }
}

/** The file name a downloaded card gets. */
export function cardFileName(card: ShareCard): string {
  return card.kind === 'profile' ? `paperperps-${card.handle}.png` : `paperperps-${card.coin}-${card.side}.png`.toLowerCase()
}
