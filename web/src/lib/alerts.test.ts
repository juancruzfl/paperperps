import { describe, expect, it, vi } from 'vitest'
import { alertOnce } from './alerts'

describe('alertOnce', () => {
  it('alerts once per key while an order fills in pieces', () => {
    vi.useFakeTimers()
    expect(alertOnce('fill:a')).toBe(true)
    expect(alertOnce('fill:a')).toBe(false) // the next piece of the same fill
    expect(alertOnce('fill:b')).toBe(true) // another order
    vi.advanceTimersByTime(3_001)
    expect(alertOnce('fill:a')).toBe(true) // a later, separate fill
    vi.useRealTimers()
  })
})
