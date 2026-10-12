// Number formatting that follows Hyperliquid's display rules: prices show up to 5
// significant figures and at most (6 - szDecimals) decimals.

const MAX_PERP_DECIMALS = 6
const SIG_FIGS = 5

/** Decimals to show for a price around px, for an asset with szDecimals. */
export function pxDecimals(px: number, szDecimals: number): number {
  const max = Math.max(0, MAX_PERP_DECIMALS - szDecimals)
  if (!Number.isFinite(px) || px <= 0) return max
  const wanted = SIG_FIGS - Math.floor(Math.log10(px)) - 1
  return Math.min(max, Math.max(0, wanted))
}

const fixedCache = new Map<number, Intl.NumberFormat>()

function fixed(decimals: number): Intl.NumberFormat {
  let f = fixedCache.get(decimals)
  if (!f) {
    f = new Intl.NumberFormat('en-US', {
      minimumFractionDigits: decimals,
      maximumFractionDigits: decimals,
    })
    fixedCache.set(decimals, f)
  }
  return f
}

export function formatNumber(value: string | number, decimals: number): string {
  const n = typeof value === 'string' ? Number(value) : value
  return Number.isFinite(n) ? fixed(decimals).format(n) : '—'
}

/** $1,234.56, or -$1,234.56; signed adds + to positive amounts. */
export function formatUsd(value: number, opts: { signed?: boolean; decimals?: number } = {}): string {
  if (!Number.isFinite(value)) return '—'
  const decimals = opts.decimals ?? 2
  const abs = fixed(decimals).format(Math.abs(value))
  // Treat anything that rounds to zero as zero, so we never print -$0.00.
  const isZero = Number(abs.replace(/,/g, '')) === 0
  const sign = isZero ? '' : value < 0 ? '-' : opts.signed ? '+' : ''
  return `${sign}$${abs}`
}

const compactUsd = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  notation: 'compact',
  maximumFractionDigits: 2,
})

/** $1.23M style, for volumes and open interest. */
export function formatUsdCompact(value: number): string {
  return Number.isFinite(value) ? compactUsd.format(value) : '—'
}

/** Signed percentage, e.g. +1.23%. ratio is a fraction (0.0123). */
export function formatPct(ratio: number, decimals = 2): string {
  if (!Number.isFinite(ratio)) return '—'
  const sign = ratio > 0 ? '+' : ''
  return `${sign}${(ratio * 100).toFixed(decimals)}%`
}

/** 24h change of a ticker as an absolute amount and ratio. */
export function change24h(markPx: string, prevDayPx: string): { abs: number; ratio: number } {
  const mark = Number(markPx)
  const prev = Number(prevDayPx)
  if (!prev) return { abs: 0, ratio: 0 }
  return { abs: mark - prev, ratio: (mark - prev) / prev }
}

const timeFmt = new Intl.DateTimeFormat(undefined, {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

export function formatTime(ms: number): string {
  return timeFmt.format(ms)
}

/** mm:ss until the next hourly funding payment. */
export function fundingCountdown(nowMs: number): string {
  const left = 3_600_000 - (nowMs % 3_600_000)
  const m = Math.floor(left / 60_000)
  const s = Math.floor((left % 60_000) / 1000)
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

/** A duration as its two largest units: 45s, 12m, 3h 12m, 2d 4h. */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const s = Math.round(seconds)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`
  const d = Math.floor(h / 24)
  return h % 24 ? `${d}d ${h % 24}h` : `${d}d`
}
