// REST client. Authenticated calls send the account secret as a bearer token.

import type {
  CreatedAccount,
  EquityPoint,
  Fill,
  FundingPayment,
  Leaderboard,
  LeaderboardWindow,
  Me,
  Order,
  OrderResult,
  TraderProfile,
  OrderType,
  Side,
  TIF,
} from './types'

export type NewOrder = {
  coin: string
  side: Side
  type: OrderType
  sz: string
  px?: string
  triggerPx?: string
  tif?: TIF
  reduceOnly?: boolean
  takeProfit?: string
  stopLoss?: string
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function request<T>(path: string, init: RequestInit & { secret?: string } = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.secret) headers.set('Authorization', `Bearer ${init.secret}`)
  if (init.body) headers.set('Content-Type', 'application/json')
  const res = await fetch(path, { ...init, headers })
  if (res.status === 204) return undefined as T
  const body = (await res.json().catch(() => ({}))) as { error?: string; message?: string }
  if (!res.ok) {
    throw new ApiError(res.status, body.error ?? 'http_error', body.message ?? `Request failed (${res.status})`)
  }
  return body as T
}

export const api = {
  createAccount: () => request<CreatedAccount>('/api/accounts', { method: 'POST' }),
  me: (secret: string) => request<Me>('/api/me', { secret }),
  updateProfile: (secret: string, profile: { handle?: string; leaderboardOptIn?: boolean }) =>
    request<void>('/api/me', { method: 'PATCH', secret, body: JSON.stringify(profile) }),
  reset: (secret: string) => request<void>('/api/me/reset', { method: 'POST', secret }),
  deleteAccount: (secret: string) =>
    request<void>('/api/me', { method: 'DELETE', secret, body: JSON.stringify({ confirm: 'delete' }) }),
  equity: (secret: string) => request<EquityPoint[]>('/api/me/equity', { secret }),
  leaderboard: (window: LeaderboardWindow, secret?: string | null) =>
    request<Leaderboard>(`/api/leaderboard?window=${window}`, { secret: secret ?? undefined }),
  trader: (handle: string) => request<TraderProfile>(`/api/traders/${encodeURIComponent(handle)}`),
  fills: (secret: string) => request<Fill[]>('/api/me/fills', { secret }),
  setFillNote: (secret: string, id: string, note: string) =>
    request<void>(`/api/me/fills/${encodeURIComponent(id)}/note`, {
      method: 'PUT',
      secret,
      body: JSON.stringify({ note }),
    }),
  orderHistory: (secret: string) => request<Order[]>('/api/me/orders', { secret }),
  funding: (secret: string) => request<FundingPayment[]>('/api/me/funding', { secret }),
  placeOrder: (secret: string, order: NewOrder) =>
    request<OrderResult>('/api/orders', { method: 'POST', secret, body: JSON.stringify(order) }),
  modifyOrder: (secret: string, id: string, changes: { sz?: string; px?: string; triggerPx?: string }) =>
    request<Order>(`/api/orders/${encodeURIComponent(id)}/modify`, {
      method: 'POST',
      secret,
      body: JSON.stringify(changes),
    }),
  setPositionTPSL: (secret: string, coin: string, tpsl: { takeProfit?: string; stopLoss?: string }) =>
    request<Order[]>(`/api/me/tpsl/${encodeURIComponent(coin)}`, {
      method: 'PUT',
      secret,
      body: JSON.stringify(tpsl),
    }),
  cancelOrder: (secret: string, id: string) =>
    request<void>(`/api/orders/${encodeURIComponent(id)}`, { method: 'DELETE', secret }),
  setMarginMode: (secret: string, coin: string, mode: 'cross' | 'isolated') =>
    request<void>(`/api/me/margin-mode/${encodeURIComponent(coin)}`, {
      method: 'PUT',
      secret,
      body: JSON.stringify({ mode }),
    }),
  adjustIsolatedMargin: (secret: string, coin: string, amount: string) =>
    request<void>(`/api/me/isolated-margin/${encodeURIComponent(coin)}`, {
      method: 'POST',
      secret,
      body: JSON.stringify({ amount }),
    }),
  setLeverage: (secret: string, coin: string, leverage: number) =>
    request<void>(`/api/me/leverage/${encodeURIComponent(coin)}`, {
      method: 'PUT',
      secret,
      body: JSON.stringify({ leverage }),
    }),
}
