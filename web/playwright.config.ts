import { defineConfig, devices } from '@playwright/test'

// Browser smoke tests against a full stack with a fake Hyperliquid (scripts/e2e.sh starts
// it). Run with `make e2e`.
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:8098',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'desktop',
      // Locally use the installed Google Chrome; CI installs Playwright's Chromium.
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 }, channel: process.env.CI ? undefined : 'chrome' },
      grepInvert: /@phone/,
    },
    {
      name: 'phone',
      use: { ...devices['Pixel 7'], channel: process.env.CI ? undefined : 'chrome' },
      grep: /@phone/,
    },
  ],
})
