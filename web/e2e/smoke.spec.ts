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

  await test.step('how it works, terms and privacy', async () => {
    await page.locator('footer').getByRole('link', { name: 'How it works' }).click()
    await expect(page.getByText('Paper trading only.')).toBeVisible()
    await page.locator('footer').getByRole('link', { name: 'Terms', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Terms of Service' })).toBeVisible()
    await expect(page).toHaveURL(/\/terms$/)
    await page.locator('footer').getByRole('link', { name: 'Privacy', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Privacy Policy' })).toBeVisible()
  })

  await test.step('delete the account: this browser starts over', async () => {
    const handle = await page.locator('.account-handle').innerText()
    await page.locator('.account-button').click()
    await page.getByRole('menuitem', { name: 'Delete account…' }).click()
    const dialog = page.locator('dialog[open]')
    await expect(dialog.getByRole('button', { name: 'Delete forever' })).toBeDisabled()
    await dialog.getByLabel('Type delete').fill('delete')
    await dialog.getByRole('button', { name: 'Delete forever' }).click()
    await expect(page.locator('.account-value')).toHaveText('$1,000.00')
    await expect(page.locator('.account-handle')).not.toHaveText(handle)
  })
})

test('phone layout has no sideways scroll @phone', async ({ page }) => {
  await page.goto('/trade/ETH')
  await expect(page.locator('.book-row.bid').first()).toBeAttached()
  await expect(page.getByRole('button', { name: /Buy \/ Long|Enter a size/ })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
  expect(overflow).toBeLessThanOrEqual(0)

  // Chart, order book and trades take turns above the order form.
  const tabs = page.getByRole('tablist', { name: 'Market view' })
  await expect(page.locator('.book-row.bid').first()).toBeHidden()
  await tabs.getByRole('tab', { name: 'Order Book' }).click()
  await expect(page.locator('.book-row.bid').first()).toBeVisible()
  await expect(page.locator('.chart-panel')).toBeHidden()
  await tabs.getByRole('tab', { name: 'Chart' }).click()
  await expect(page.locator('.chart-panel')).toBeVisible()
})

test('a trader shares a position and has a public profile', async ({ page, request }) => {
  await setPrice(request, 'ETH', 2500)
  await page.goto('/trade/ETH')
  await expect(page.locator('.book-row.bid').first()).toBeVisible()
  const handle = (await page.locator('.account-handle').textContent())?.trim() ?? ''
  expect(handle).not.toBe('')

  await test.step('open a position and preview its share card', async () => {
    await page.getByLabel('Size (ETH)').fill('1')
    await page.getByRole('button', { name: 'Buy / Long ETH' }).click()
    await expect(page.getByRole('tab', { name: 'Positions (1)' })).toBeVisible()
    await page.locator('.account-panel').getByRole('button', { name: 'Share', exact: true }).click()
    const dialog = page.locator('dialog[open]')
    await expect(dialog.getByRole('img', { name: 'Share card preview' })).toBeVisible()
    await expect(dialog.getByRole('button', { name: 'Download' })).toBeVisible()
    await expect(dialog.getByRole('button', { name: 'Copy profile link' })).toBeVisible()
    await dialog.getByRole('button', { name: 'Close' }).click()
  })

  await test.step('the profile shows the open position', async () => {
    await page.goto(`/u/${handle}`)
    await expect(page.getByRole('heading', { name: handle })).toBeVisible()
    await expect(page.locator('.trader-overview')).toContainText('Win rate')
    await expect(page.locator('.trader-positions').getByRole('cell', { name: /^ETH/ })).toBeVisible()
  })

  await test.step('hiding from the leaderboard hides the profile', async () => {
    await page.goto('/leaderboard')
    // The box follows the server, so it flips once the change is saved.
    const optIn = page.getByLabel('Show me on the leaderboard')
    await optIn.click()
    await expect(optIn).not.toBeChecked()
    await page.goto(`/u/${handle}`)
    await expect(page.getByRole('heading', { name: 'No public profile' })).toBeVisible()
  })
})

// One test for these, since each test's new account counts toward the server's limit of
// five new accounts per IP.
test('a newcomer takes the guide, journals a trade, gets a price alert, and sees trade stats', async ({ page, request }) => {
  await setPrice(request, 'SOL', 150)
  await page.goto('/trade/SOL')
  await expect(page.locator('.book-row.bid').first()).toBeVisible()
  const handle = (await page.locator('.account-handle').textContent())?.trim() ?? ''

  await test.step('the beginner guide', async () => {
    await page.getByRole('button', { name: 'New to perps? Take the tour' }).click()
    await expect(page.getByRole('dialog', { name: 'Welcome to perps' })).toBeVisible()
    for (const title of ['Pick a market', 'The mark price', 'The order book', 'Leverage']) {
      await page.getByRole('button', { name: 'Next' }).click()
      await expect(page.getByRole('dialog', { name: title })).toBeVisible()
    }
    await expect(page.getByRole('dialog', { name: 'Leverage' })).toContainText('liquidates it')
    await page.keyboard.press('Escape')
    await expect(page.locator('.guide-card')).toHaveCount(0)

    // It can be reopened from the account menu, and ends with a call to trade.
    await page.locator('.account-button').click()
    await page.getByRole('menuitem', { name: 'Beginner guide' }).click()
    for (let i = 0; i < 9; i++) await page.getByRole('button', { name: 'Next' }).click()
    await page.getByRole('button', { name: 'Start trading' }).click()
    await expect(page.locator('.guide-card')).toHaveCount(0)
  })

  await test.step('open and close a position', async () => {
    await page.getByLabel('Size (SOL)').fill('2')
    await page.getByRole('button', { name: 'Buy / Long SOL' }).click()
    await expect(page.getByRole('tab', { name: 'Positions (1)' })).toBeVisible()
    await page.getByRole('radio', { name: 'Sell / Short' }).click()
    await page.getByLabel('Size (SOL)').fill('2')
    await page.getByRole('button', { name: 'Sell / Short SOL' }).click()
    await expect(page.getByText('No open positions')).toBeVisible()
  })

  await test.step('write a journal note on the closing fill', async () => {
    await page.getByRole('tab', { name: 'Trade History' }).click()
    await page.locator('.account-panel tbody tr').first().getByRole('button', { name: /Add a note/ }).click()
    const dialog = page.locator('dialog[open]')
    await dialog.locator('textarea').fill('Closed too early, the breakout held.')
    await dialog.getByRole('button', { name: 'Save' }).click()
    await expect(page.locator('.fill-note')).toHaveText('Closed too early, the breakout held.')
  })

  await test.step('a price alert fires when the mark reaches it', async () => {
    await page.getByRole('button', { name: 'Price alerts for SOL' }).click()
    const dialog = page.locator('dialog[open]')
    await dialog.getByLabel('SOL at').fill('155')
    await dialog.getByRole('button', { name: 'Add' }).click()
    await expect(dialog.locator('.alerts-list li')).toHaveCount(1)
    await dialog.getByRole('button', { name: 'Done' }).click()
    await setPrice(request, 'SOL', 156)
    await expect(page.getByText('Price alert: SOL rose to 155')).toBeVisible()
    await expect(page.locator('.alerts-count')).toHaveCount(0)
  })

  await test.step('the profile shows trade stats', async () => {
    await page.goto(`/u/${handle}`)
    await page.getByRole('tab', { name: 'Trade stats' }).click()
    await expect(page.locator('.trade-stats-grid')).toContainText('Closed positions1')
    await expect(page.locator('.trade-stats').getByRole('cell', { name: 'SOL' })).toBeVisible()
  })
})
