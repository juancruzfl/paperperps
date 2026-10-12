// State of the beginner guide (components/Guide.tsx): which step is showing, if any.

import { create } from 'zustand'
import { DEFAULT_COIN, navigate } from './route'

export const useGuide = create<{ step: number | null }>(() => ({ step: null }))

/** Starts the tour, going to the trade screen first if needed. */
export function startGuide() {
  if (!location.pathname.startsWith('/trade/')) navigate(`/trade/${DEFAULT_COIN}`)
  useGuide.setState({ step: 0 })
}
