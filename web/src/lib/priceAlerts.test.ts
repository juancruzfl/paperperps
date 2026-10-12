import { beforeEach, describe, expect, it } from 'vitest'
import { addPriceAlert, checkPriceAlerts, reached, removePriceAlert, usePriceAlerts, type PriceAlert } from './priceAlerts'
import type { Ticker } from './types'

const ticker = (coin: string, markPx: string) => ({ coin, markPx }) as Ticker

describe('price alerts', () => {
  beforeEach(() => usePriceAlerts.setState({ alerts: [] }))

  it('fires when the mark reaches the price from either side', () => {
    const alerts: PriceAlert[] = [
      { id: 'a', coin: 'BTC', px: 70000, above: true },
      { id: 'b', coin: 'BTC', px: 60000, above: false },
      { id: 'c', coin: 'ETH', px: 3000, above: true },
    ]
    expect(reached(alerts, [ticker('BTC', '65000'), ticker('ETH', '2900')])).toEqual([])
    expect(reached(alerts, [ticker('BTC', '70000')]).map((a) => a.id)).toEqual(['a'])
    expect(reached(alerts, [ticker('BTC', '59999.5'), ticker('ETH', '3100')]).map((a) => a.id)).toEqual(['b', 'c'])
  })

  it('sets the direction from the current mark and rejects bad prices', () => {
    expect(addPriceAlert('BTC', 70000, 65000)).toBeNull()
    expect(addPriceAlert('BTC', 60000, 65000)).toBeNull()
    expect(usePriceAlerts.getState().alerts.map((a) => a.above)).toEqual([true, false])
    expect(addPriceAlert('BTC', 70000, 65000)).toMatch(/already/)
    expect(addPriceAlert('BTC', 65000, 65000)).toMatch(/current price/)
    expect(addPriceAlert('BTC', 0, 65000)).toMatch(/above zero/)
    removePriceAlert(usePriceAlerts.getState().alerts[0].id)
    expect(usePriceAlerts.getState().alerts).toHaveLength(1)
  })
})

describe('checkPriceAlerts', () => {
  it('removes alerts once they fire', () => {
    usePriceAlerts.setState({ alerts: [{ id: 'a', coin: 'BTC', px: 70000, above: true }, { id: 'b', coin: 'BTC', px: 80000, above: true }] })
    checkPriceAlerts([ticker('BTC', '71000')])
    expect(usePriceAlerts.getState().alerts.map((a) => a.id)).toEqual(['b'])
  })
})
