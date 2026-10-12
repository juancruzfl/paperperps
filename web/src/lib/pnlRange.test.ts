import { describe, expect, it } from 'vitest'
import { pnlWindow } from './pnlRange'

const now = Date.parse('2026-10-11T12:00:00Z')
const at = (hoursAgo: number, pnl: number) => ({ t: new Date(now - hoursAgo * 3600_000).toISOString(), cumulativePnl: String(pnl) })

describe('pnlWindow', () => {
  const history = [at(72, 100), at(30, 300), at(12, 150), at(2, 250)]

  it('keeps everything for all time', () => {
    const w = pnlWindow(history, 260, 'all', now)
    expect(w.points.map((p) => Number(p.cumulativePnl))).toEqual([100, 300, 150, 250])
    expect(w.pnl).toBe(260)
    expect(w.maxDrawdown).toBe(150)
  })

  it('rebases a window on the last snapshot before it', () => {
    const w = pnlWindow(history, 260, '24h', now)
    expect(w.points.map((p) => Number(p.cumulativePnl))).toEqual([0, -150, -50])
    expect(w.pnl).toBe(-40)
    expect(w.maxDrawdown).toBe(150)
  })

  it('handles an empty history', () => {
    expect(pnlWindow([], 0, '7d', now)).toEqual({ points: [], pnl: 0, maxDrawdown: 0 })
  })
})
