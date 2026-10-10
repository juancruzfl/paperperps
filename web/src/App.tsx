import { useEffect, useState } from 'react'
import { AccountMenu, RecoveryNotice } from './components/AccountMenu'
import { AccountPanel } from './components/AccountPanel'
import { AboutPage } from './components/AboutPage'
import { Chart } from './components/Chart'
import { LeaderboardPage } from './components/LeaderboardPage'
import { MarketSelector } from './components/MarketSelector'
import { OrderBook } from './components/OrderBook'
import { OrderForm } from './components/OrderForm'
import { StatsBar } from './components/StatsBar'
import { Toasts } from './components/Toasts'
import { Trades } from './components/Trades'
import { bootAccount } from './lib/account'
import { fetchStatus, useBook, useConnection, useTickers, useTrades } from './lib/market'
import { DEFAULT_COIN, useCoin, usePage } from './lib/route'
import type { Status } from './lib/types'
import './App.css'

type BookTab = 'book' | 'trades'

function App() {
  const [coin, setCoin] = useCoin()
  const [page, setPage] = usePage()
  const tickers = useTickers()
  const ticker = tickers.find((t) => t.coin === coin)
  const [status, setStatus] = useState<Status | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    fetchStatus(controller.signal).then(setStatus, () => {})
    return () => controller.abort()
  }, [])

  // A recovery link is /restore#<key>. Read the key, then drop it from the address bar.
  useEffect(() => {
    const secret = location.pathname === '/restore' ? decodeURIComponent(location.hash.slice(1)) : ''
    if (secret) setCoin(DEFAULT_COIN, true)
    bootAccount(secret || null)
  }, [setCoin])

  // Send unknown markets to the default one, and / to its canonical /trade/<coin> URL.
  useEffect(() => {
    if (page !== 'trade') return
    if (tickers.length > 0 && !ticker) {
      const fallback = tickers.some((t) => t.coin === DEFAULT_COIN) ? DEFAULT_COIN : tickers[0].coin
      setCoin(fallback, true)
    } else if (location.pathname === '/' || location.pathname === '/restore') {
      setCoin(coin, true)
    }
  }, [page, coin, ticker, tickers, setCoin])

  useEffect(() => {
    if (page !== 'trade') {
      document.title = `${page === 'leaderboard' ? 'Leaderboard' : 'How it works'} · PaperPerps`
      return
    }
    const px = ticker ? ` ${Number(ticker.markPx).toLocaleString('en-US')}` : ''
    document.title = `${coin}${px} · PaperPerps`
  }, [page, coin, ticker])

  return (
    <div className="app">
      <div className="banner">PAPER TRADING · NOT REAL FUNDS</div>
      <header className="header">
        <a className="logo" href="/">
          <img src="/favicon.svg" alt="" />
          <span className="logo-text">PaperPerps</span>
        </a>
        {page === 'trade' && <MarketSelector coin={coin} tickers={tickers} onSelect={setCoin} />}
        <nav className="nav">
          {(['trade', 'leaderboard'] as const).map((p) => (
            <a
              key={p}
              href={p === 'trade' ? '/' : '/leaderboard'}
              className={page === p ? 'active' : undefined}
              aria-current={page === p ? 'page' : undefined}
              onClick={(e) => {
                e.preventDefault()
                setPage(p)
              }}
            >
              {p === 'trade' ? 'Trade' : 'Leaderboard'}
            </a>
          ))}
        </nav>
        <AccountMenu />
      </header>
      <RecoveryNotice />
      <ConnectionBanner />

      {page === 'leaderboard' ? (
        <LeaderboardPage />
      ) : page === 'about' ? (
        <AboutPage />
      ) : (
        <>
      <StatsBar ticker={ticker} />

      <main className="terminal">
        <section className="panel chart-panel" aria-label="Chart">
          {ticker ? (
            <Chart coin={coin} szDecimals={ticker.szDecimals} refPx={Number(ticker.markPx)} />
          ) : (
            <div className="panel-empty">Loading chart…</div>
          )}
        </section>

        <BookPanel coin={coin} szDecimals={ticker?.szDecimals ?? 2} />

        <section className="panel order-panel" aria-label="Place order">
          <OrderForm key={coin} ticker={ticker} />
        </section>

        <AccountPanel onSelectCoin={setCoin} />
      </main>
        </>
      )}

      <Toasts />
      <Footer status={status} onAbout={() => setPage('about')} />
    </div>
  )
}

function BookPanel({ coin, szDecimals }: { coin: string; szDecimals: number }) {
  const [tab, setTab] = useState<BookTab>('book')
  const book = useBook(coin)
  const trades = useTrades(coin)

  return (
    <section className="panel book-panel" aria-label="Order book and trades">
      <div className="tabs" role="tablist">
        <button type="button" role="tab" aria-selected={tab === 'book'} className={tab === 'book' ? 'active' : ''} onClick={() => setTab('book')}>
          Order Book
        </button>
        <button type="button" role="tab" aria-selected={tab === 'trades'} className={tab === 'trades' ? 'active' : ''} onClick={() => setTab('trades')}>
          Trades
        </button>
      </div>
      {tab === 'book' ? (
        <OrderBook book={book} szDecimals={szDecimals} coin={coin} />
      ) : (
        <Trades trades={trades} szDecimals={szDecimals} coin={coin} />
      )}
    </section>
  )
}

/** Warns when live data has been cut off for more than a few seconds. */
function ConnectionBanner() {
  const up = useConnection() === 'open'
  const [show, setShow] = useState(false)
  // Keyed on up/down, not the exact state: retries flip between connecting and closed, and
  // that mustn't restart the timer.
  useEffect(() => {
    if (up) return
    const id = setTimeout(() => setShow(true), 4000) // ride out blips and deploys quietly
    return () => {
      clearTimeout(id)
      setShow(false)
    }
  }, [up])
  if (!show) return null
  return (
    <div className="conn-banner" role="status">
      Reconnecting to live prices… Prices and your positions will update once we’re back.
    </div>
  )
}

function Footer({ status, onAbout }: { status: Status | null; onAbout: () => void }) {
  const conn = useConnection()
  const label = conn === 'open' ? 'Connected' : conn === 'connecting' ? 'Connecting…' : 'Reconnecting…'
  return (
    <footer className="footer">
      <span>
        <span className={`dot ${conn === 'open' ? 'ok' : 'bad'}`} />
        {label}
        {status && ` · Hyperliquid ${status.network} data`}
      </span>
      <span className="muted">
        Paper trading with pretend money. Not affiliated with Hyperliquid. Not financial advice.{' '}
        <a
          href="/about"
          onClick={(e) => {
            e.preventDefault()
            onAbout()
          }}
        >
          How it works
        </a>
      </span>
    </footer>
  )
}

export default App
