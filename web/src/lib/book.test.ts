import { describe, expect, it } from 'vitest'
import { groupStep, groupings, stepDecimals } from './book'

describe('order book grouping', () => {
  it('turns significant figures into a price step', () => {
    // BTC near 82,800: 4 sig figs is $10, 3 is $100, 2 is $1,000 (Hyperliquid's nSigFigs).
    expect(groupStep(82_800, 4)).toBe(10)
    expect(groupStep(82_800, 3)).toBe(100)
    expect(groupStep(82_800, 2)).toBe(1000)
    expect(groupStep(2_495.2, 4)).toBe(1)
    expect(groupStep(0.1834, 3)).toBeCloseTo(0.001)
    expect(groupStep(0, 3)).toBe(0)
  })

  it('offers only groupings coarser than the raw book', () => {
    expect(groupings(82_800, 1).map((g) => g.step)).toEqual([1, 10, 100, 1000])
    expect(groupings(82_800, 1).map((g) => g.sigFigs)).toEqual([0, 4, 3, 2])
    // ETH ticks at 0.1, so 4 sig figs ($1) is the first coarser step.
    expect(groupings(2_495.2, 0.1).map((g) => g.step)).toEqual([0.1, 1, 10, 100])
    // A coin whose raw step is already $10 skips the $10 grouping.
    expect(groupings(82_800, 10).map((g) => g.step)).toEqual([10, 100, 1000])
  })

  it('formats steps with the decimals they need', () => {
    expect(stepDecimals(1000)).toBe(0)
    expect(stepDecimals(1)).toBe(0)
    expect(stepDecimals(0.1)).toBe(1)
    expect(stepDecimals(0.0001)).toBe(4)
  })
})
