import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { AccountMenu, RecoveryNotice } from './components/AccountMenu'
import { AccountPanel } from './components/AccountPanel'
import { Guide } from './components/Guide'
import { Chart } from './components/Chart'
import { LeaderboardPage } from './components/LeaderboardPage'
import { MarketSelector } from './components/MarketSelector'
import { OrderBook } from './components/OrderBook'
import { OrderForm } from './components/OrderForm'
import { StatsBar } from './components/StatsBar'
import { Toasts } from './components/Toasts'
import { Trades } from './components/Trades'
import { bootAccount, useAccount } from './lib/account'
import { chartLines } from './lib/chartLines'
import { LEGAL_CONTACT } from './lib/legal'
import { fetchStatus, useConnection, useTickers, useTrades } from './lib/market'
import { DEFAULT_COIN, useCoin, usePage } from './lib/route'
import { alertLines, checkPriceAlerts, usePriceAlerts } from './lib/priceAlerts'
import type { Status } from './lib/types'
import './App.css'

// Pages most visitors never open load on demand, keeping the trading screen's bundle small.
const AboutPage = lazy(() => import('./components/AboutPage').then((m) => ({ default: m.AboutPage })))
const TermsPage = lazy(() => import('./components/LegalPages').then((m) => ({ default: m.TermsPage })))
const PrivacyPage = lazy(() => import('./components/LegalPages').then((m) => ({ default: m.PrivacyPage })))
const PortfolioPage = lazy(() => import('./components/PortfolioPage').then((m) => ({ default: m.PortfolioPage })))
const TraderPage = lazy(() => import('./components/TraderPage').then((m) => ({ default: m.TraderPage })))

const NAV_LABELS = { trade: 'Trade', portfolio: 'Portfolio', leaderboard: 'Leaderboard' }

type BookTab = 'book' | 'trades'
type MobileView = 'chart' | BookTab

const MOBILE_VIEWS: [MobileView, string][] = [
  ['chart', 'Chart'],
  ['book', 'Order Book'],
  ['trades', 'Trades'],
]

function App() {
  const [coin, setCoin] = useCoin()
  const [page, setPage] = usePage()
  const tickers = useTickers()
  const ticker = tickers.find((t) => t.coin === coin)
  const me = useAccount((s) => s.me)
  const priceAlerts = usePriceAlerts((s) => s.alerts)
  const lines = useMemo(
    () => [...chartLines(me, tickers, coin), ...alertLines(priceAlerts, coin)],
    [me, tickers, coin, priceAlerts],
  )
  useEffect(() => checkPriceAlerts(tickers), [tickers])
  const [bookTab, setBookTab] = useState<BookTab>('book')
  // On phones the chart and the book/trades panel take turns in one spot.
  const [showChart, setShowChart] = useState(true)
  const mobileView: MobileView = showChart ? 'chart' : bookTab
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
    if (page === 'trader') return // TraderPage titles itself with the handle
    if (page !== 'trade') {
      const titles = { portfolio: 'Portfolio', leaderboard: 'Leaderboard', about: 'How it works', terms: 'Terms of Service', privacy: 'Privacy Policy' }
      document.title = `${titles[page]} · PaperPerps`
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
          {(['trade', 'portfolio', 'leaderboard'] as const).map((p) => (
            <a
              key={p}
              href={p === 'trade' ? '/' : `/${p}`}
              className={page === p || (p === 'leaderboard' && page === 'trader') ? 'active' : undefined}
              aria-current={page === p ? 'page' : undefined}
              onClick={(e) => {
                e.preventDefault()
                setPage(p)
              }}
            >
              {NAV_LABELS[p]}
            </a>
          ))}
        </nav>
        <AccountMenu />
      </header>
      <RecoveryNotice />
      <ConnectionBanner />
      <SuspensionBanner />

      {page === 'leaderboard' ? (
        <LeaderboardPage />
      ) : page !== 'trade' ? (
        <Suspense fallback={<main className="page"><div className="panel-empty">Loading…</div></main>}>
          {page === 'about' ? (
            <AboutPage />
          ) : page === 'terms' ? (
            <TermsPage />
          ) : page === 'privacy' ? (
            <PrivacyPage />
          ) : page === 'portfolio' ? (
            <PortfolioPage />
          ) : (
            <TraderPage />
          )}
        </Suspense>
      ) : (
        <>
      <StatsBar ticker={ticker} />

      <main className="terminal" data-mobile-view={mobileView === 'chart' ? 'chart' : 'market'}>
        {/* Phones show one of chart, book or trades at a time; wider screens show all. */}
        <div className="tabs mobile-tabs" role="tablist" aria-label="Market view">
          {MOBILE_VIEWS.map(([view, label]) => (
            <button
              key={view}
              type="button"
              role="tab"
              aria-selected={mobileView === view}
              className={mobileView === view ? 'active' : ''}
              onClick={() => {
                setShowChart(view === 'chart')
                if (view !== 'chart') setBookTab(view)
              }}
            >
              {label}
            </button>
          ))}
        </div>

        <section className="panel chart-panel" aria-label="Chart">
          {ticker ? (
            <Chart coin={coin} szDecimals={ticker.szDecimals} refPx={Number(ticker.markPx)} lines={lines} />
          ) : (
            <div className="panel-empty">Loading chart…</div>
          )}
        </section>

        <BookPanel coin={coin} szDecimals={ticker?.szDecimals ?? 2} tab={bookTab} onTab={setBookTab} />

        <section className="panel order-panel" aria-label="Place order">
          <OrderForm key={coin} ticker={ticker} />
        </section>

        <AccountPanel onSelectCoin={setCoin} />
      </main>
        </>
      )}

      {page === 'trade' && <Guide ticker={ticker} />}
      <Toasts />
      <Footer status={status} />
    </div>
  )
}

function BookPanel({ coin, szDecimals, tab, onTab: setTab }: { coin: string; szDecimals: number; tab: BookTab; onTab: (t: BookTab) => void }) {
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
        <OrderBook szDecimals={szDecimals} coin={coin} />
      ) : (
        <Trades trades={trades} szDecimals={szDecimals} coin={coin} />
      )}
    </section>
  )
}

/** Explains what a suspended account can still do. */
function SuspensionBanner() {
  const suspension = useAccount((s) => s.me?.suspension)
  if (!suspension) return null
  return (
    <div className="suspension-banner" role="alert">
      This account is suspended{suspension.reason ? ` (${suspension.reason})` : ''}. You can close positions and
      cancel orders, but not open new positions, reset or change your handle. Questions:{' '}
      <a href={`mailto:${LEGAL_CONTACT}`}>{LEGAL_CONTACT}</a>
    </div>
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
      <span className="muted">
        Paper trading with pretend money. Not affiliated with Hyperliquid. Not financial advice.{' '}
        <a href="/about">How it works</a> · <a href="/terms">Terms</a> · <a href="/privacy">Privacy</a>
      </span>
    </footer>
  )
}

export default App
