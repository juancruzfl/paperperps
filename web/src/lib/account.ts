// The visitor's anonymous account. The secret lives in localStorage; anyone holding it can
// use the account, which is how recovery links work. There is no other login.

import { create } from 'zustand'
import { api, ApiError } from './api'
import { socket, type ServerMessage } from './socket'
import { toast } from './toasts'
import type { AccountEvent, Fill, FundingPayment, Me, Order } from './types'

const SECRET_KEY = 'paperperps.secret'
const NOTICE_KEY = 'paperperps.recoveryNoticeDismissed'

let memorySecret: string | null = null

function loadSecret(): string | null {
  try {
    return localStorage.getItem(SECRET_KEY) ?? memorySecret
  } catch {
    return memorySecret
  }
}

function saveSecret(secret: string) {
  memorySecret = secret
  try {
    localStorage.setItem(SECRET_KEY, secret)
  } catch {
    // Private mode: the account lasts for this tab only. The recovery link still works.
  }
}

/** Forgets this browser's account after it was deleted on the server. */
export function forgetAccount() {
  memorySecret = null
  try {
    localStorage.removeItem(SECRET_KEY)
    localStorage.removeItem(NOTICE_KEY)
  } catch {
    // nothing stored
  }
}

export function recoveryNoticeDismissed(): boolean {
  try {
    return localStorage.getItem(NOTICE_KEY) === '1'
  } catch {
    return false
  }
}

export function dismissRecoveryNotice() {
  try {
    localStorage.setItem(NOTICE_KEY, '1')
  } catch {
    // ignore
  }
}

export function recoveryLink(secret: string): string {
  // The secret goes in the fragment, which browsers never send to servers or put in logs.
  return `${location.origin}/restore#${secret}`
}

type History = { fills?: Fill[]; orders?: Order[]; funding?: FundingPayment[] }

type AccountState = {
  status: 'loading' | 'ready' | 'error'
  error: string | null
  secret: string | null
  me: Me | null
  history: History
  newlyCreated: boolean
}

export const useAccount = create<AccountState>(() => ({
  status: 'loading',
  error: null,
  secret: null,
  me: null,
  history: {},
  newlyCreated: false,
}))

let unsubscribeEvents: (() => void) | null = null
let initPromise: Promise<void> | null = null

/** Loads the stored account, or creates one on first visit. Safe to call repeatedly. */
export function initAccount(): Promise<void> {
  initPromise ??= (async () => {
    try {
      const secret = loadSecret()
      if (secret) {
        try {
          await activate(secret, false)
          return
        } catch (err) {
          // A stored key the server doesn't know (e.g. a wiped dev database): start fresh.
          if (!(err instanceof ApiError && err.status === 401)) throw err
        }
      }
      const created = await api.createAccount()
      saveSecret(created.secret)
      await activate(created.secret, true)
    } catch (err) {
      initPromise = null
      useAccount.setState({ status: 'error', error: String(err instanceof Error ? err.message : err) })
    }
  })()
  return initPromise
}

let booted = false

/**
 * Starts the account once per page load: from a recovery link's key if one was opened,
 * otherwise from storage (or a new account).
 */
export function bootAccount(recoverySecret: string | null) {
  if (booted) return
  booted = true
  if (!recoverySecret) {
    void initAccount()
    return
  }
  restoreAccount(recoverySecret).then(
    () => toast('Account restored from your recovery link', 'success'),
    () => {
      toast('That recovery link doesn’t match any account', 'error')
      void initAccount()
    },
  )
}

/** Switches to the account a recovery key belongs to. Throws if the key is unknown. */
export async function restoreAccount(secret: string) {
  const trimmed = secret.trim()
  await api.me(trimmed) // validate before replacing the current key
  saveSecret(trimmed)
  await activate(trimmed, false)
}

async function activate(secret: string, newlyCreated: boolean) {
  const me = await api.me(secret)
  useAccount.setState({ status: 'ready', error: null, secret, me, history: {}, newlyCreated })
  unsubscribeEvents?.()
  unsubscribeEvents = socket.subscribe(`account:${me.accountId}`, onAccountEvent, { token: secret })
}

export async function refreshAccount() {
  const { secret } = useAccount.getState()
  if (!secret) return
  const me = await api.me(secret)
  useAccount.setState({ me })
}

export async function loadHistory(kind: keyof History) {
  const { secret } = useAccount.getState()
  if (!secret) return
  const rows =
    kind === 'fills'
      ? await api.fills(secret)
      : kind === 'orders'
        ? await api.orderHistory(secret)
        : await api.funding(secret)
  useAccount.setState((s) => ({ history: { ...s.history, [kind]: rows } }))
}

function onAccountEvent(msg: ServerMessage) {
  const ev = msg.data as AccountEvent
  if (ev.type === 'fill' && ev.fill && ev.order?.type === 'limit' && ev.fill.liquidity === 'maker') {
    // Resting orders fill in the background; the user didn't just click anything.
    toast(`Limit order filled: ${describeFill(ev.fill)}`, 'success')
  }
  if (ev.type === 'liquidation' && ev.liquidation) {
    const l = ev.liquidation
    const what = l.cross ? `Your cross positions (${l.coins.join(', ')}) were` : `Your isolated ${l.coins[0]} position was`
    const fee = Number(l.liquidationFee)
    toast(
      `${what} liquidated: equity fell below maintenance margin.${fee > 0 ? ` Remaining margin forfeited: $${fee.toFixed(2)}.` : ''}`,
      'error',
      15000,
    )
  }
  if (ev.type === 'order' && ev.order?.status === 'cancelled' && ev.order.statusReason?.includes('insufficient margin')) {
    toast(`${ev.order.coin} limit order cancelled: not enough margin when it would have filled`, 'error')
  }
  void refreshAccount()
  const { history } = useAccount.getState()
  for (const kind of Object.keys(history) as (keyof History)[]) void loadHistory(kind)
}

const plain = new Intl.NumberFormat('en-US', { maximumFractionDigits: 6 })

export function describeFill(f: Fill): string {
  const verb = f.side === 'buy' ? 'Bought' : 'Sold'
  return `${verb} ${plain.format(Number(f.sz))} ${f.coin} at ${plain.format(Number(f.px))}`
}
