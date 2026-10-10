import type { Book, Level } from './types'

export type DepthRow = {
  px: number
  sz: number
  total: number // cumulative size from the best price outward
  depth: number // total / largest total shown, 0..1, for the background bar
}

export type Depth = {
  asks: DepthRow[] // best (lowest) first
  bids: DepthRow[] // best (highest) first
  spread: number | null
  spreadRatio: number | null // spread / mid
}

function cumulate(levels: Level[], rows: number): DepthRow[] {
  let total = 0
  return levels.slice(0, rows).map((l) => {
    const sz = Number(l.sz)
    total += sz
    return { px: Number(l.px), sz, total, depth: 0 }
  })
}

/** Cumulative depth for the top `rows` levels per side, scaled against both sides. */
export function computeDepth(book: Book, rows: number): Depth {
  const asks = cumulate(book.asks, rows)
  const bids = cumulate(book.bids, rows)
  const max = Math.max(asks.at(-1)?.total ?? 0, bids.at(-1)?.total ?? 0)
  for (const r of [...asks, ...bids]) r.depth = max > 0 ? r.total / max : 0

  const bestAsk = asks[0]?.px
  const bestBid = bids[0]?.px
  let spread = null
  let spreadRatio = null
  if (bestAsk !== undefined && bestBid !== undefined) {
    spread = bestAsk - bestBid
    spreadRatio = spread / ((bestAsk + bestBid) / 2)
  }
  return { asks, bids, spread, spreadRatio }
}

/** Groupings the server offers, as significant figures (market.GroupedSigFigs). */
export const GROUPED_SIG_FIGS = [4, 3, 2] as const

/** The price step of a grouping to sigFigs significant figures, at price px. */
export function groupStep(px: number, sigFigs: number): number {
  if (!(px > 0)) return 0
  return 10 ** (Math.floor(Math.log10(px)) - sigFigs + 1)
}

/** Decimal places a price step needs: 0.01 → 2, 10 → 0. */
export function stepDecimals(step: number): number {
  return step >= 1 ? 0 : Math.round(-Math.log10(step))
}

export type Grouping = { sigFigs: number; step: number }

/**
 * The steps to offer for a market: the raw book (sigFigs 0, at the market's own price
 * step), then each server grouping that's coarser than it.
 */
export function groupings(px: number, rawStep: number): Grouping[] {
  const out: Grouping[] = [{ sigFigs: 0, step: rawStep }]
  for (const n of GROUPED_SIG_FIGS) {
    const step = groupStep(px, n)
    if (step > rawStep * 1.5) out.push({ sigFigs: n, step })
  }
  return out
}
