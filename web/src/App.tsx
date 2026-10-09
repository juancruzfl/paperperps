import { useEffect, useState } from 'react'
import { AccountMenu, RecoveryNotice } from './components/AccountMenu'
import { AccountPanel } from './components/AccountPanel'
import { Chart } from './components/Chart'
import { MarketSelector } from './components/MarketSelector'
import { OrderBook } from './components/OrderBook'
import { OrderForm } from './components/OrderForm'
import { StatsBar } from './components/StatsBar'
import { Toasts } from './components/Toasts'
import { Trades } from './components/Trades'
import { bootAccount } from './lib/account'
import { fetchStatus, useBook, useConnection, useTickers, useTrades } from './lib/market'
import { DEFAULT_COIN, useCoin } from './lib/route'
import type { Status } from './lib/types'
import './App.css'

type BookTab = 'book' | 'trades'

function App() {
  const [coin, setCoin] = useCoin()
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
    if (tickers.length > 0 && !ticker) {
      const fallback = tickers.some((t) => t.coin === DEFAULT_COIN) ? DEFAULT_COIN : tickers[0].coin
      setCoin(fallback, true)
    } else if (location.pathname === '/' || location.pathname === '/restore') {
      setCoin(coin, true)
    }
  }, [coin, ticker, tickers, setCoin])

  useEffect(() => {
    const px = ticker ? ` ${Number(ticker.markPx).toLocaleString('en-US')}` : ''
    document.title = `${coin}${px} · PaperPerps`
  }, [coin, ticker])

  return (
    <div className="app">
      <div className="banner">PAPER TRADING · NOT REAL FUNDS</div>
      <header className="header">
        <a className="logo" href="/">
          <img src="/favicon.svg" alt="" />
          <span className="logo-text">PaperPerps</span>
        </a>
        <MarketSelector coin={coin} tickers={tickers} onSelect={setCoin} />
        <AccountMenu />
      </header>
      <RecoveryNotice />

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

      <Toasts />
      <Footer status={status} />
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

function Footer({ status }: { status: Status | null }) {
  const conn = useConnection()
  const label = conn === 'open' ? 'Connected' : conn === 'connecting' ? 'Connecting…' : 'Reconnecting…'
  return (
    <footer className="footer">
      <span>
        <span className={`dot ${conn === 'open' ? 'ok' : 'bad'}`} />
        {label}
        {status && ` · Hyperliquid ${status.network} data`}
      </span>
      <span className="muted">Not affiliated with Hyperliquid. All orders are simulated.</span>
    </footer>
  )
}

export default App
