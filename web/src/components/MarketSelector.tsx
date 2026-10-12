import { useEffect, useMemo, useRef, useState } from 'react'
import { change24h, formatNumber, formatPct, formatUsdCompact, pxDecimals } from '../lib/format'
import type { Ticker } from '../lib/types'
import { ChevronDownIcon } from './Icons'

type Props = {
  coin: string
  tickers: Ticker[]
  onSelect: (coin: string) => void
}

export function MarketSelector({ coin, tickers, onSelect }: Props) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const rootRef = useRef<HTMLDivElement>(null)
  const current = tickers.find((t) => t.coin === coin)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    return tickers
      .filter((t) => !t.closeOnly && t.coin.toLowerCase().includes(q))
      .sort((a, b) => Number(b.dayNtlVlm) - Number(a.dayNtlVlm))
  }, [tickers, query])

  const pick = (c: string) => {
    onSelect(c)
    setOpen(false)
    setQuery('')
  }

  return (
    <div className="selector" ref={rootRef}>
      <button
        type="button"
        className="selector-button"
        aria-expanded={open}
        aria-haspopup="listbox"
        onClick={() => setOpen((o) => !o)}
      >
        <span className="selector-coin">{coin}-USD</span>
        {current && <span className="badge">{current.maxLeverage}x</span>}
        <ChevronDownIcon size={14} className="icon chevron" />
      </button>

      {open && (
        <div className="selector-panel">
          <input
            autoFocus
            className="selector-search"
            placeholder="Search markets"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && rows[0]) pick(rows[0].coin)
            }}
          />
          <table className="selector-table">
            <thead>
              <tr>
                <th>Market</th>
                <th className="num">Mark</th>
                <th className="num">24h</th>
                <th className="num hide-sm">Funding</th>
                <th className="num hide-sm">Volume</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((t) => {
                const ch = change24h(t.markPx, t.prevDayPx)
                return (
                  <tr
                    key={t.coin}
                    className={t.coin === coin ? 'active' : undefined}
                    onClick={() => pick(t.coin)}
                  >
                    <td>
                      {t.coin}
                      <span className="badge">{t.maxLeverage}x</span>
                    </td>
                    <td className="num">
                      {formatNumber(t.markPx, pxDecimals(Number(t.markPx), t.szDecimals))}
                    </td>
                    <td className={`num ${ch.ratio >= 0 ? 'up' : 'down'}`}>{formatPct(ch.ratio)}</td>
                    <td className="num hide-sm">{formatPct(Number(t.funding), 4)}</td>
                    <td className="num hide-sm">{formatUsdCompact(Number(t.dayNtlVlm))}</td>
                  </tr>
                )
              })}
              {rows.length === 0 && (
                <tr>
                  <td colSpan={5} className="empty">
                    No markets match “{query}”
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
