// Minimal routing: the selected market lives in the path, /trade/BTC.

import { useCallback, useSyncExternalStore } from 'react'

export const DEFAULT_COIN = 'BTC'

const listeners = new Set<() => void>()

function subscribe(listener: () => void) {
  listeners.add(listener)
  window.addEventListener('popstate', listener)
  return () => {
    listeners.delete(listener)
    window.removeEventListener('popstate', listener)
  }
}

function coinFromPath(): string {
  const m = /^\/trade\/([^/]+)\/?$/.exec(location.pathname)
  return m ? decodeURIComponent(m[1]) : DEFAULT_COIN
}

export function useCoin(): [string, (coin: string, replace?: boolean) => void] {
  const coin = useSyncExternalStore(subscribe, coinFromPath)
  const setCoin = useCallback((next: string, replace = false) => {
    const path = `/trade/${encodeURIComponent(next)}`
    if (location.pathname === path) return
    if (replace) history.replaceState(null, '', path)
    else history.pushState(null, '', path)
    for (const l of listeners) l()
  }, [])
  return [coin, setCoin]
}
