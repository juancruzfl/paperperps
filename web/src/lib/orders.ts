// Describing orders for people.

import type { Order, OrderType, Side } from './types'

export const isTrigger = (t: OrderType) => t.startsWith('stop') || t.startsWith('take_profit')

export const isLimitPriced = (t: OrderType) => t === 'limit' || t.endsWith('_limit')

const TYPE_LABEL: Record<OrderType, string> = {
  market: 'Market',
  limit: 'Limit',
  stop_market: 'Stop market',
  stop_limit: 'Stop limit',
  take_profit_market: 'Take profit market',
  take_profit_limit: 'Take profit limit',
}

export const typeLabel = (t: OrderType) => TYPE_LABEL[t]

/**
 * Which way the mark must move to fire a trigger order (mirrors Triggered in Go): stops
 * fire when price moves against the position, take-profits when it moves in favor.
 */
export function triggerDirection(type: OrderType, side: Side): '≥' | '≤' {
  const stop = type.startsWith('stop')
  return stop === (side === 'buy') ? '≥' : '≤'
}

/** Short tags like "Reduce only", "TP/SL". */
export function orderTags(o: Order): string[] {
  const tags: string[] = []
  if (o.parentOrderId || (o.reduceOnly && isTrigger(o.type))) tags.push('TP/SL')
  else if (o.reduceOnly) tags.push('Reduce only')
  if (o.tif === 'alo') tags.push('Post only')
  if (o.tif === 'ioc' && o.type === 'limit') tags.push('IOC')
  return tags
}
