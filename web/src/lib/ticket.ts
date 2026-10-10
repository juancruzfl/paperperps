// Lets other components hand a price to the order form, e.g. clicking an order book level.
// The order form registers a handler while it's mounted.

type PriceHandler = (px: string) => void

let handler: PriceHandler | undefined

export function onPricePick(fn: PriceHandler): () => void {
  handler = fn
  return () => {
    if (handler === fn) handler = undefined
  }
}

export function pickPrice(px: string) {
  handler?.(px)
}
