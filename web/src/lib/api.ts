// REST client. Authenticated calls send the account secret as a bearer token.

import type { CreatedAccount, Fill, FundingPayment, Me, Order, OrderResult, OrderType, Side } from './types'

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
  fills: (secret: string) => request<Fill[]>('/api/me/fills', { secret }),
  orderHistory: (secret: string) => request<Order[]>('/api/me/orders', { secret }),
  funding: (secret: string) => request<FundingPayment[]>('/api/me/funding', { secret }),
  placeOrder: (
    secret: string,
    order: { coin: string; side: Side; type: OrderType; sz: string; px?: string },
  ) => request<OrderResult>('/api/orders', { method: 'POST', secret, body: JSON.stringify(order) }),
  cancelOrder: (secret: string, id: string) =>
    request<void>(`/api/orders/${encodeURIComponent(id)}`, { method: 'DELETE', secret }),
  setLeverage: (secret: string, coin: string, leverage: number) =>
    request<void>(`/api/me/leverage/${encodeURIComponent(coin)}`, {
      method: 'PUT',
      secret,
      body: JSON.stringify({ leverage }),
    }),
}
