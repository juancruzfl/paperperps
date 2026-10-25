import { useMemo, useState } from 'react'
import { formatUsd } from '../lib/format'
import { dayKey } from '../lib/portfolio'
import { ChevronDownIcon } from './Icons'

const monthFmt = new Intl.DateTimeFormat('en-US', { month: 'long', year: 'numeric' })
const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

const compact = (n: number) => {
  const abs = Math.abs(n)
  const s = abs >= 1000 ? `${(abs / 1000).toFixed(1)}k` : abs >= 100 ? abs.toFixed(0) : abs.toFixed(2)
  return `${n < 0 ? '-' : '+'}$${s}`
}

/** A month of daily PnL: green and red days, deeper for bigger moves. */
export function PnlCalendar({ days }: { days: Map<string, number> }) {
  const [offset, setOffset] = useState(0) // months back from this one
  const [now] = useState(() => new Date()) // when the calendar opened; "today" for the grid
  const month = new Date(now.getFullYear(), now.getMonth() - offset, 1)
  const first = useMemo(() => [...days.keys()][0], [days])
  const canGoBack = first !== undefined && first < dayKey(month)

  const cells: ({ key: string; day: number; pnl: number | undefined; future: boolean } | null)[] = []
  const lead = (month.getDay() + 6) % 7 // Monday first
  for (let i = 0; i < lead; i++) cells.push(null)
  const today = dayKey(now)
  let total = 0
  let max = 0
  const inMonth = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
  for (let d = 1; d <= inMonth; d++) {
    const key = dayKey(new Date(month.getFullYear(), month.getMonth(), d))
    const pnl = days.get(key)
    if (pnl !== undefined) {
      total += pnl
      max = Math.max(max, Math.abs(pnl))
    }
    cells.push({ key, day: d, pnl, future: key > today })
  }
  const traded = cells.filter((c) => c?.pnl !== undefined && Math.abs(c.pnl) >= 0.005)
  const green = traded.filter((c) => c!.pnl! > 0).length

  return (
    <div className="pnl-calendar">
      <div className="pnl-calendar-head">
        <div className="pnl-calendar-nav">
          <button type="button" className="icon-btn" aria-label="Previous month" disabled={!canGoBack} onClick={() => setOffset(offset + 1)}>
            <ChevronDownIcon className="icon prev" />
          </button>
          <strong>{monthFmt.format(month)}</strong>
          <button type="button" className="icon-btn" aria-label="Next month" disabled={offset === 0} onClick={() => setOffset(offset - 1)}>
            <ChevronDownIcon className="icon next" />
          </button>
        </div>
        <span className="muted small">
          <span className={total > 0 ? 'up' : total < 0 ? 'down' : ''}>{formatUsd(total, { signed: true })}</span>
          {traded.length > 0 && ` · ${green} of ${traded.length} days green`}
        </span>
      </div>
      <div className="pnl-calendar-grid" role="grid" aria-label={`Daily PnL, ${monthFmt.format(month)}`}>
        {WEEKDAYS.map((w) => (
          <div key={w} className="pnl-calendar-weekday" role="columnheader">
            {w}
          </div>
        ))}
        {cells.map((c, i) => {
          if (!c) return <div key={`lead-${i}`} />
          const has = c.pnl !== undefined && Math.abs(c.pnl) >= 0.005
          // Depth: 0.25 to 1 of the month's biggest day.
          const depth = has && max > 0 ? 0.25 + 0.75 * (Math.abs(c.pnl!) / max) : 0
          return (
            <div
              key={c.key}
              role="gridcell"
              className={`pnl-day${has ? (c.pnl! > 0 ? ' gain' : ' loss') : ''}${c.future ? ' future' : ''}${c.key === today ? ' today' : ''}`}
              style={has ? { ['--depth' as string]: depth } : undefined}
              title={has ? `${c.key}: ${formatUsd(c.pnl!, { signed: true })}` : c.key}
            >
              <span className="pnl-day-num">{c.day}</span>
              {has && <span className="pnl-day-value">{compact(c.pnl!)}</span>}
            </div>
          )
        })}
      </div>
    </div>
  )
}
