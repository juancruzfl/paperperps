// Market data store and hooks. Components read from the store; the hooks keep the
// matching websocket topics subscribed while any component needs them.

import { useEffect, useSyncExternalStore } from 'react'
import { create } from 'zustand'
import { socket, type ServerMessage } from './socket'
import type { Book, Status, Ticker, Trade } from './types'

const MAX_TRADES = 100

type MarketState = {
  tickers: Ticker[]
  books: Record<string, Book | undefined>
  trades: Record<string, Trade[] | undefined>
}

export const useMarketStore = create<MarketState>(() => ({
  tickers: [],
  books: {},
  trades: {},
}))

function onTickers(msg: ServerMessage) {
  useMarketStore.setState({ tickers: msg.data as Ticker[] })
}

function onBook(msg: ServerMessage) {
  const book = msg.data as Book | null
  if (!book) return
  useMarketStore.setState((s) => ({ books: { ...s.books, [book.coin]: book } }))
}

function onTrades(msg: ServerMessage) {
  const coin = msg.topic.slice('trades:'.length)
  const incoming = (msg.data as Trade[] | null) ?? []
  useMarketStore.setState((s) => {
    // Updates are newest first; snapshots replace what we have.
    const existing = msg.snapshot ? [] : (s.trades[coin] ?? [])
    const seen = new Set(existing.map((t) => t.tid))
    const fresh = incoming.filter((t) => !seen.has(t.tid))
    return { trades: { ...s.trades, [coin]: [...fresh, ...existing].slice(0, MAX_TRADES) } }
  })
}

// Handlers are module-level constants, so the subscription only changes with the topic.
function useTopic(topic: string, handler: (msg: ServerMessage) => void) {
  useEffect(() => socket.subscribe(topic, handler), [topic, handler])
}

export function useTickers(): Ticker[] {
  useTopic('markets', onTickers)
  return useMarketStore((s) => s.tickers)
}

export function useTicker(coin: string): Ticker | undefined {
  useTopic('markets', onTickers)
  return useMarketStore((s) => s.tickers.find((t) => t.coin === coin))
}

export function useBook(coin: string): Book | undefined {
  useTopic(`book:${coin}`, onBook)
  return useMarketStore((s) => s.books[coin])
}

export function useTrades(coin: string): Trade[] | undefined {
  useTopic(`trades:${coin}`, onTrades)
  return useMarketStore((s) => s.trades[coin])
}

export function useConnection() {
  return useSyncExternalStore(socket.onStateChange, socket.getState)
}

export async function fetchStatus(signal?: AbortSignal): Promise<Status> {
  const res = await fetch('/api/status', { signal })
  if (!res.ok) throw new Error(`status request failed: ${res.status}`)
  return res.json() as Promise<Status>
}
