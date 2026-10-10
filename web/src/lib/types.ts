// Wire types for the PaperPerps API. Decimals arrive as strings to keep their precision.

export type Ticker = {
  coin: string
  szDecimals: number
  maxLeverage: number
  markPx: string
  midPx: string | null
  oraclePx: string
  prevDayPx: string
  dayNtlVlm: string
  funding: string // hourly rate
  openInterest: string // coin units
  stale: boolean
  closeOnly?: boolean // no longer listed; held positions can only be closed
}

export type Level = { px: string; sz: string; n: number }

export type Book = {
  coin: string
  time: number
  bids: Level[]
  asks: Level[]
}

export type Trade = {
  coin: string
  side: 'B' | 'A' // B: taker bought, A: taker sold
  px: string
  sz: string
  time: number
  tid: number
  hash: string
}

export type Candle = {
  t: number // open time, ms
  T: number // close time, ms
  o: string
  h: string
  l: string
  c: string
  v: string
  n: number
}

export type Status = {
  version: string
  engine: 'leader' | 'standby'
  network: 'testnet' | 'mainnet'
  markets: string[]
  startingBalance: string
}

// --- accounts & trading ---

export type Side = 'buy' | 'sell'
export type OrderType =
  | 'market'
  | 'limit'
  | 'stop_market'
  | 'stop_limit'
  | 'take_profit_market'
  | 'take_profit_limit'

export type TIF = 'gtc' | 'ioc' | 'alo'

export type Position = {
  coin: string
  size: string // signed: negative is short
  entryPx: string
  leverage: number
  marginMode: 'cross' | 'isolated'
  isolatedMargin: string
}

export type Order = {
  id: string
  coin: string
  side: Side
  type: OrderType
  px: string | null
  triggerPx: string | null
  sz: string
  filledSz: string
  tif: TIF | null
  reduceOnly: boolean
  parentOrderId: string | null
  ocoGroup: string | null
  status: 'open' | 'triggered' | 'filled' | 'cancelled'
  statusReason: string | null
  createdAt: string
  updatedAt: string
}

export type Fill = {
  id: string
  orderId: string | null
  coin: string
  side: Side
  px: string
  sz: string
  fee: string
  liquidity: 'maker' | 'taker'
  closedPnl: string
  isLiquidation: boolean
  createdAt: string
}

export type FundingPayment = {
  coin: string
  fundingHour: string
  size: string
  rate: string
  oraclePx: string
  amount: string
}

export type MarginSummary = {
  balance: string
  unrealizedPnl: string
  accountValue: string
  crossAccountValue: string
  marginUsed: string
  crossMaintenance: string
  isolatedMargin: string
  orderMargin: string
  available: string
}

export type Me = {
  accountId: string
  handle: string
  createdAt: string
  leaderboardOptIn: boolean
  lastResetAt: string | null
  resets: number
  runId: string
  startingBalance: string
  balance: string
  priorPnl: string // PnL of runs ended by resets
  lifetimePnl: string // leaderboard score at the time of the request
  resetAvailableAt: string | null
  positions: Position[]
  openOrders: Order[]
  summary: MarginSummary
}

export type CreatedAccount = { accountId: string; secret: string; handle: string }

export type OrderResult = { order: Order; fill: Fill | null; children?: Order[] }

export type LiquidationInfo = {
  coins: string[]
  cross: boolean
  fills: Fill[]
  liquidationFee: string
  badDebt: string
}

export type AccountEvent = {
  type: 'order' | 'fill' | 'leverage' | 'margin' | 'liquidation' | 'funding' | 'reset'
  order?: Order
  fill?: Fill
  liquidation?: LiquidationInfo
}

// --- leaderboard & performance ---

export type LeaderboardWindow = '24h' | '7d' | '30d' | 'all'

export type LeaderboardEntry = {
  rank: number
  handle: string
  pnl: string
  volume: string
  trades: number
  resets: number
  you?: boolean
}

export type Leaderboard = {
  window: LeaderboardWindow
  updatedAt: string
  minTrades: number
  minAgeHours: number
  entries: LeaderboardEntry[]
  you?: LeaderboardEntry & { eligible: boolean; reason?: string }
}

export type EquityPoint = { t: string; equity: string; cumulativePnl: string }
