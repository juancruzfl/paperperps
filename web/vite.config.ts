/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The Go server owns /api, /ws and /healthz. In dev, Vite proxies them to it so the app
// talks to a single origin, as it does in production.
const server = 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  // Unit tests live next to the code; e2e/ is Playwright's (npm run e2e).
  test: { include: ['src/**/*.test.ts'] },
  server: {
    proxy: {
      '/api': server,
      '/healthz': server,
      '/ws': { target: server, ws: true },
    },
  },
})
