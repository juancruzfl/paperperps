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
