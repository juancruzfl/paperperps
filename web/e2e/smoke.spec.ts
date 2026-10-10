import { expect, test, type APIRequestContext } from '@playwright/test'

// The fake Hyperliquid (server/cmd/fakehl) quotes BTC with the best bid at the price and
// the best ask one dollar above. Tests move the price through its control endpoint.
const fakeHL = process.env.E2E_FAKEHL ?? 'http://localhost:9999'

async function setPrice(request: APIRequestContext, coin: string, px: number) {
  const res = await request.post(`${fakeHL}/control/price`, { data: { coin, px: String(px) } })
  expect(res.ok()).toBeTruthy()
}

test('a visitor trades, a take-profit fires, and the account resets', async ({ page, request }) => {
  await setPrice(request, 'BTC', 80000)
  await page.goto('/')

  await test.step('first visit: a funded anonymous account', async () => {
    await expect(page).toHaveURL(/\/trade\/BTC$/)
    await expect(page.locator('.account-value')).toHaveText('$1,000.00')
    await expect(page.getByText('in paper money')).toBeVisible()
    await expect(page.locator('.book-row.bid').first()).toBeVisible()
  })

  await test.step('market buy fills at the ask', async () => {
    await page.getByLabel('Size (BTC)').fill('0.01')
    await page.getByRole('button', { name: 'Buy / Long BTC' }).click()
    await expect(page.getByText('Bought 0.01 BTC at 80,001')).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Positions (1)' })).toBeVisible()
  })

  await test.step('set a take-profit and stop-loss on the position', async () => {
    await page.locator('.tpsl-link').click()
    const dialog = page.locator('dialog[open]')
    await dialog.getByLabel('Take profit').fill('81000')
    await dialog.getByLabel('Stop loss').fill('79000')
    await dialog.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('BTC TP/SL set')).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Open Orders (2)' })).toBeVisible()
  })

  await test.step('price rises through the take-profit: closed, stop-loss cancelled', async () => {
    await setPrice(request, 'BTC', 81500)
    await expect(page.getByText('No open positions')).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Open Orders', exact: true })).toBeVisible()
    await page.getByRole('tab', { name: 'Trade History' }).click()
    await expect(page.locator('.account-panel tbody tr')).toHaveCount(2)
  })

  await test.step('clicking an order book level fills in a limit price', async () => {
    const bid = page.locator('.book-row.bid').first()
    const px = (await bid.locator('.px').innerText()).replace(/,/g, '')
    await bid.click()
    await expect(page.getByRole('tab', { name: 'Limit', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByLabel('Price (USD)')).toHaveValue(px)
  })

  await test.step('leaderboard shows the visitor', async () => {
    await page.getByRole('link', { name: 'Leaderboard' }).click()
    await expect(page.getByRole('heading', { name: 'Leaderboard' })).toBeVisible()
    await expect(page.getByText('Not on the board yet')).toBeVisible()
    await page.getByRole('link', { name: 'Trade' }).click()
  })

  await test.step('reset back to $1,000', async () => {
    await page.locator('.account-button').click()
    await page.getByRole('menuitem', { name: 'Reset account…' }).click()
    await page.getByRole('button', { name: 'Reset to $1,000' }).click()
    await expect(page.getByText('Account reset')).toBeVisible()
    await expect(page.locator('.account-value')).toHaveText('$1,000.00')
  })

  await test.step('how it works', async () => {
    await page.getByRole('link', { name: 'How it works' }).click()
    await expect(page.getByText('Paper trading only.')).toBeVisible()
  })
})

test('phone layout has no sideways scroll @phone', async ({ page }) => {
  await page.goto('/trade/ETH')
  await expect(page.locator('.book-row.bid').first()).toBeAttached()
  await expect(page.getByRole('button', { name: /Buy \/ Long|Enter a size/ })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
  expect(overflow).toBeLessThanOrEqual(0)
})
