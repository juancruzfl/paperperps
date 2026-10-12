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

export type Page = 'trade' | 'leaderboard' | 'about' | 'terms' | 'privacy' | 'trader'

const PAGES: Page[] = ['leaderboard', 'about', 'terms', 'privacy']

function pageFromPath(): Page {
  if (location.pathname.startsWith('/u/')) return 'trader'
  return PAGES.find((p) => location.pathname.startsWith(`/${p}`)) ?? 'trade'
}

function traderFromPath(): string {
  const m = /^\/u\/([^/]+)\/?$/.exec(location.pathname)
  return m ? decodeURIComponent(m[1]).toLowerCase() : ''
}

/** A trader's public profile path. Navigate to it with a plain link (see interceptLinks). */
export const traderPath = (handle: string) => `/u/${encodeURIComponent(handle)}`

/** The handle in /u/<handle>, on a trader's profile page. */
export function useTraderHandle(): string {
  return useSyncExternalStore(subscribe, traderFromPath)
}

/** Goes to an in-app path, as a link click would. */
export function navigate(path: string) {
  if (location.pathname !== path) history.pushState(null, '', path)
  for (const l of listeners) l()
  window.scrollTo(0, 0)
}

/**
 * Makes plain <a href="/…"> links inside the app navigate without a page reload. Links with
 * a modifier key, a target or another origin behave normally.
 */
export function interceptLinks() {
  document.addEventListener('click', (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    const a = (e.target as Element | null)?.closest('a')
    if (!a || a.target || a.origin !== location.origin || !a.pathname.startsWith('/')) return
    if (a.getAttribute('href')?.startsWith('mailto:')) return
    e.preventDefault()
    navigate(a.pathname)
  })
}

/** The current page, and a function to go to another. */
export function usePage(): [Page, (page: Page) => void] {
  const page = useSyncExternalStore(subscribe, pageFromPath)
  const go = useCallback((next: Page) => {
    if (pageFromPath() === 'trade') lastCoin = coinFromPath() // to come back to it
    const path = next === 'trade' ? `/trade/${encodeURIComponent(lastCoin)}` : next === 'trader' ? '/leaderboard' : `/${next}`
    if (location.pathname !== path) history.pushState(null, '', path)
    for (const l of listeners) l()
    window.scrollTo(0, 0)
  }, [])
  return [page, go]
}

// The market to return to when leaving the leaderboard.
let lastCoin = DEFAULT_COIN

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
