import { useEffect, useState } from 'react'
import { fetchStatus, type Status } from './api'
import './App.css'

const usd = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  maximumFractionDigits: 0,
})

// Placeholder until the trading terminal lands in M1/M2: proves the frontend, API and
// database are wired together end to end.
function App() {
  const [status, setStatus] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    fetchStatus(controller.signal)
      .then(setStatus)
      .catch((err: unknown) => {
        if (!controller.signal.aborted) setError(String(err))
      })
    return () => controller.abort()
  }, [])

  return (
    <>
      <div className="banner">PAPER TRADING · NOT REAL FUNDS</div>
      <main className="shell">
        <h1 className="brand">
          <img src="/favicon.svg" alt="" />
          PaperPerps
        </h1>
        <p className="tagline">
          Practice perpetual futures on live market data. No login, no wallet,{' '}
          {usd.format(Number(status?.startingBalance ?? 1000))} to start.
        </p>

        <section className="card">
          <h2>Server status</h2>
          {error && <p className="error">Can’t reach the server: {error}</p>}
          {!error && !status && <p>Connecting…</p>}
          {status && (
            <>
              <dl className="stats">
                <div>
                  <dt>Engine</dt>
                  <dd>
                    <span className={`dot ${status.engine === 'leader' ? 'ok' : ''}`} />
                    {status.engine}
                  </dd>
                </div>
                <div>
                  <dt>Market data</dt>
                  <dd>Hyperliquid {status.network}</dd>
                </div>
                <div>
                  <dt>Version</dt>
                  <dd>{status.version.slice(0, 7)}</dd>
                </div>
              </dl>
              <ul className="markets" aria-label="Markets">
                {status.markets.map((coin) => (
                  <li key={coin}>{coin}</li>
                ))}
              </ul>
            </>
          )}
        </section>

        <p className="footer">
          PaperPerps is not affiliated with Hyperliquid. Prices come from Hyperliquid’s public
          API; all orders are simulated.
        </p>
      </main>
    </>
  )
}

export default App
