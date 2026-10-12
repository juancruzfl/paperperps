import { describe, expect, it } from 'vitest'
import { cardFileName, cardLink, closedTradeCard, impliedEntry, roe } from './share'
import type { Fill } from './types'

const fill = (side: 'buy' | 'sell', px: string, sz: string, closedPnl: string): Fill => ({
  id: 'f',
  orderId: null,
  coin: 'BTC',
  side,
  px,
  sz,
  fee: '0',
  liquidity: 'taker',
  closedPnl,
  isLiquidation: false,
  note: '',
  createdAt: '2026-10-10T00:00:00Z',
})

describe('share cards', () => {
  it('derives the entry of a closed trade from its PnL', () => {
    // A long from 80,000 closed at 82,000 on 0.1 BTC: +200.
    expect(impliedEntry(fill('sell', '82000', '0.1', '200'))).toBeCloseTo(80000)
    // A short from 80,000 closed at 82,000: -200.
    expect(impliedEntry(fill('buy', '82000', '0.1', '-200'))).toBeCloseTo(80000)
  })

  it('computes ROE on margin', () => {
    // 0.1 BTC at 80,000 and 20x uses 400 margin; +200 is +50%.
    expect(roe(200, 0.1, 80000, 20)).toBeCloseTo(0.5)
    expect(roe(200, -0.1, 80000, 20)).toBeCloseTo(0.5)
    expect(roe(1, 0, 80000, 20)).toBeNull()
  })

  it('builds a closed-trade card', () => {
    const c = closedTradeCard(fill('sell', '82000', '0.1', '200'), 20, 'brave-otter-412', 'x/u/brave-otter-412', (p) => p.toFixed(0))
    expect(c).toMatchObject({ kind: 'closed', side: 'long', entryPx: '80000', exitPx: '82000', pnl: 200 })
    expect(c.roe).toBeCloseTo(0.5)
    expect(closedTradeCard(fill('buy', '82000', '0.1', '-200'), null, 'h', 'x', String).roe).toBeNull()
  })

  it('links the profile only when it is public', () => {
    const me = { handle: 'brave-otter-412', leaderboardOptIn: true, leaderboardHidden: false, suspension: null }
    expect(cardLink(me, 'paperperps.lol')).toBe('paperperps.lol/u/brave-otter-412')
    expect(cardLink({ ...me, leaderboardOptIn: false }, 'paperperps.lol')).toBe('paperperps.lol')
    expect(cardLink({ ...me, leaderboardHidden: true }, 'paperperps.lol')).toBe('paperperps.lol')
    expect(cardLink({ ...me, suspension: { at: '', reason: '' } }, 'paperperps.lol')).toBe('paperperps.lol')
    expect(cardLink(null, 'paperperps.lol')).toBe('paperperps.lol')
  })

  it('names downloads', () => {
    expect(cardFileName({ kind: 'profile', handle: 'h', pnl: 0, winRate: null, trades: 0, rank: 0, link: '' })).toBe('paperperps-h.png')
  })
})
