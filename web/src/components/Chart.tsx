import {
  CandlestickSeries,
  ColorType,
  createChart,
  HistogramSeries,
  LastPriceAnimationMode,
  LineSeries,
  LineStyle,
  LineType,
  TickMarkType,
  type IChartApi,
  type ISeriesApi,
  type Time,
  type UTCTimestamp,
} from 'lightweight-charts'
import { useEffect, useRef, useState } from 'react'
import { applyTrade, INTERVALS, toBar, type Bar, type Interval } from '../lib/candles'
import type { PriceLine } from '../lib/chartLines'
import { pxDecimals } from '../lib/format'
import { socket, type ServerMessage } from '../lib/socket'
import type { Candle, Trade } from '../lib/types'

const UP = '#3ddc97'
const DOWN = '#ff5c7a'
const WARN = '#f5b942'
const INTERVAL_KEY = 'paperperps.chartInterval'

// Degen mode adds a tick view: every trade is one step along the line, so it moves as fast
// as the market does. Ticks aren't evenly spaced in time, so the time axis counts trades and
// the labels look up each trade's real time.
const LIVE = 'Tick'
const LIVE_POINTS = 1000
const SWIM_MS = 200 // the dot glides to each new trade price, short enough to keep up with bursts

const easeOut = (x: number) => 1 - (1 - x) ** 3
const reducedMotion = () => window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
type View = Interval | typeof LIVE

function loadInterval(): Interval {
  try {
    const saved = localStorage.getItem(INTERVAL_KEY)
    if (saved && saved in INTERVALS) return saved as Interval
  } catch {
    // storage unavailable (private mode); fall through to the default
  }
  return '15m'
}

function saveInterval(i: Interval) {
  try {
    localStorage.setItem(INTERVAL_KEY, i)
  } catch {
    // not important enough to surface
  }
}

// lightweight-charts works in UTC; these show times in the viewer's timezone instead.
const crosshairFmt = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})
const secondsFmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const hourFmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
const dayFmt = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' })
const monthFmt = new Intl.DateTimeFormat(undefined, { month: 'short' })
const yearFmt = new Intl.DateTimeFormat(undefined, { year: 'numeric' })

const ms = (t: Time) => (t as number) * 1000

function tickFormatter(time: Time, type: TickMarkType): string {
  switch (type) {
    case TickMarkType.Year:
      return yearFmt.format(ms(time))
    case TickMarkType.Month:
      return monthFmt.format(ms(time))
    case TickMarkType.DayOfMonth:
      return dayFmt.format(ms(time))
    default:
      return hourFmt.format(ms(time))
  }
}

function volumePoint(b: Bar) {
  return {
    time: b.time as UTCTimestamp,
    value: b.volume,
    color: b.close >= b.open ? 'rgba(61, 220, 151, 0.35)' : 'rgba(255, 92, 122, 0.35)',
  }
}

type Props = {
  coin: string
  szDecimals: number
  refPx: number // any recent price, to pick the axis precision
  lines?: PriceLine[] // position, liquidation and order prices to mark
  degen?: boolean // degen mode: offer the 1s mark-price view
  aboveCap?: boolean // this coin's leverage is above its real cap: open the 1s view
}

const LINE_STYLE = { solid: LineStyle.Solid, dashed: LineStyle.Dashed, dotted: LineStyle.Dotted }

export function Chart({ coin, szDecimals, refPx, lines = [], degen = false, aboveCap = false }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candlesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const volumeRef = useRef<ISeriesApi<'Histogram'> | null>(null)
  const markRef = useRef<ISeriesApi<'Line'> | null>(null)
  const tickTimes = useRef(new Map<number, number>()) // tick number -> trade time, ms
  const [saved, setSaved] = useState<Interval>(loadInterval)
  const [live, setLive] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [waiting, setWaiting] = useState(false)
  const view: View = live && degen ? LIVE : saved

  // Going above the cap opens the 1s view, once per crossing; picking another view sticks.
  const [prevAboveCap, setPrevAboveCap] = useState(aboveCap)
  if (aboveCap !== prevAboveCap) {
    setPrevAboveCap(aboveCap)
    if (aboveCap) setLive(true)
  }

  // Create the chart once.
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const chart = createChart(el, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: 'transparent' },
        textColor: '#8b98a5',
        fontFamily: 'ui-monospace, SF Mono, Menlo, Consolas, monospace',
        attributionLogo: true,
      },
      grid: {
        vertLines: { color: 'rgba(31, 40, 51, 0.6)' },
        horzLines: { color: 'rgba(31, 40, 51, 0.6)' },
      },
      rightPriceScale: { borderColor: '#1f2833' },
      timeScale: {
        borderColor: '#1f2833',
        timeVisible: true,
        secondsVisible: false,
        tickMarkFormatter: tickFormatter,
      },
      localization: { timeFormatter: (t: Time) => crosshairFmt.format(ms(t)) },
    })
    candlesRef.current = chart.addSeries(CandlestickSeries, {
      upColor: UP,
      downColor: DOWN,
      borderVisible: false,
      wickUpColor: UP,
      wickDownColor: DOWN,
    })
    volumeRef.current = chart.addSeries(HistogramSeries, {
      priceScaleId: '',
      priceFormat: { type: 'volume' },
      lastValueVisible: false,
      priceLineVisible: false,
    })
    volumeRef.current.priceScale().applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } })
    // A glowing dot (the last price animation) swims along the price, trailing a faint curve.
    markRef.current = chart.addSeries(LineSeries, {
      color: 'rgba(245, 185, 66, 0.45)',
      lineWidth: 2,
      lineType: LineType.Curved,
      lastPriceAnimation: LastPriceAnimationMode.Continuous,
      crosshairMarkerBackgroundColor: WARN,
      priceLineColor: WARN,
      visible: false,
    })
    chartRef.current = chart
    return () => {
      chart.remove()
      chartRef.current = null
      candlesRef.current = null
      volumeRef.current = null
      markRef.current = null
    }
  }, [])

  // Price lines are redrawn only when they actually change (they're recomputed every tick).
  const linesKey = JSON.stringify(lines)
  useEffect(() => {
    const series = view === LIVE ? markRef.current : candlesRef.current
    if (!series) return
    const drawn = (JSON.parse(linesKey) as PriceLine[]).map((l) =>
      series.createPriceLine({
        price: l.price,
        color: l.color,
        title: l.title,
        lineWidth: 1,
        lineStyle: LINE_STYLE[l.style],
        axisLabelVisible: true,
      }),
    )
    return () => drawn.forEach((p) => series.removePriceLine(p))
  }, [linesKey, view])

  // Axis precision follows the asset's price.
  const decimals = pxDecimals(refPx, szDecimals)
  useEffect(() => {
    const priceFormat = { type: 'price', precision: decimals, minMove: 10 ** -decimals } as const
    candlesRef.current?.applyOptions({ priceFormat })
    markRef.current?.applyOptions({ priceFormat })
  }, [decimals])

  // Switch series and time labels between candles and the tick line.
  useEffect(() => {
    const isLive = view === LIVE
    const tickTime = (t: Time) => {
      const at = tickTimes.current.get(t as number)
      return at === undefined ? '' : secondsFmt.format(at)
    }
    candlesRef.current?.applyOptions({ visible: !isLive })
    volumeRef.current?.applyOptions({ visible: !isLive })
    markRef.current?.applyOptions({ visible: isLive })
    chartRef.current?.applyOptions({
      timeScale: { tickMarkFormatter: isLive ? tickTime : tickFormatter },
      localization: { timeFormatter: isLive ? tickTime : (t: Time) => crosshairFmt.format(ms(t)) },
    })
  }, [view])

  // Tick view: one point per trade from the live trade feed, starting from the recent trades
  // the feed sends on subscribe. Each new point starts where the dot is and eases to the new
  // price, so the dot swims rather than jumps.
  useEffect(() => {
    const series = markRef.current
    if (view !== LIVE || !series) return
    const points: { time: UTCTimestamp; value: number }[] = []
    const times = tickTimes.current
    const seen = new Set<number>()
    let seq = 0
    let frame = 0
    let target = 0 // where the dot is headed
    series.setData([])
    setError(null)
    setWaiting(true)

    const swim = (head: { time: UTCTimestamp; value: number }, from: number, to: number) => {
      cancelAnimationFrame(frame)
      target = to
      if (reducedMotion()) {
        head.value = to
        series.update(head)
        return
      }
      const start = performance.now()
      const step = (now: number) => {
        const k = Math.min(1, (now - start) / SWIM_MS)
        head.value = from + (to - from) * easeOut(k)
        series.update(head)
        if (k < 1) frame = requestAnimationFrame(step)
      }
      frame = requestAnimationFrame(step)
    }

    const unsubscribe = socket.subscribe(`trades:${coin}`, (msg: ServerMessage) => {
      // Batches arrive newest first; ticks go oldest first. Reconnects resend recent trades.
      const fresh = ((msg.data as Trade[] | null) ?? [])
        .filter((t) => !seen.has(t.tid) && Number(t.px) > 0)
        .sort((a, b) => a.time - b.time || a.tid - b.tid)
      if (fresh.length === 0) return
      // Settle the dot where it was headed before adding more.
      cancelAnimationFrame(frame)
      const last = points.at(-1)
      if (last) {
        last.value = target
        series.update(last)
      }
      const from = last ? target : Number(fresh[0].px)
      for (const t of fresh) {
        seen.add(t.tid)
        const time = ++seq as UTCTimestamp
        times.set(time, t.time)
        points.push({ time, value: Number(t.px) })
      }
      const head = points[points.length - 1]
      const to = head.value
      head.value = from
      if (points.length > LIVE_POINTS) {
        for (const p of points.splice(0, points.length - LIVE_POINTS)) times.delete(p.time)
      }
      // A batch, or the first snapshot, is redrawn whole; the dot then swims to its last price.
      if (msg.snapshot || fresh.length > 1 || points.length === 1) series.setData(points)
      else series.update(head)
      swim(head, from, to)
      if (points.length >= 2) setWaiting(false)
    })
    return () => {
      cancelAnimationFrame(frame)
      unsubscribe()
      times.clear()
      markRef.current?.setData([]) // null once the chart is gone (unmount cleans that up first)
    }
  }, [coin, view])

  // Load history, then extend the last bar from live trades.
  useEffect(() => {
    const candles = candlesRef.current
    const volume = volumeRef.current
    if (view === LIVE || !candles || !volume) return
    const interval = view
    const intervalMs = INTERVALS[interval]
    const controller = new AbortController()
    let last: Bar | null = null
    let loaded = false
    const pending: Trade[] = []

    candles.setData([])
    volume.setData([])
    setError(null)

    const apply = (trades: Trade[]) => {
      // Updates arrive newest first; bars must be built oldest first.
      for (const t of [...trades].sort((a, b) => a.time - b.time)) {
        const bar = applyTrade(last, t, intervalMs)
        if (!bar) continue
        last = bar
        candles.update({ ...bar, time: bar.time as UTCTimestamp })
        volume.update(volumePoint(bar))
      }
    }

    const unsubscribe = socket.subscribe(`trades:${coin}`, (msg: ServerMessage) => {
      // The snapshot is recent history that the candle history already includes.
      if (msg.snapshot) return
      const trades = msg.data as Trade[]
      if (loaded) apply(trades)
      else pending.push(...trades)
    })

    fetch(`/api/markets/${encodeURIComponent(coin)}/candles?interval=${interval}`, {
      signal: controller.signal,
    })
      .then(async (res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const bars = ((await res.json()) as Candle[]).map(toBar)
        candles.setData(bars.map((b) => ({ ...b, time: b.time as UTCTimestamp })))
        volume.setData(bars.map(volumePoint))
        last = bars.at(-1) ?? null
        loaded = true
        apply(pending)
        const ts = chartRef.current?.timeScale()
        if (ts && bars.length > 0) {
          ts.setVisibleLogicalRange({ from: bars.length - 120, to: bars.length + 5 })
        }
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) setError(`Couldn’t load chart history (${String(err)})`)
      })

    return () => {
      controller.abort()
      unsubscribe()
    }
  }, [coin, view])

  const pick = (v: View) => {
    if (v === LIVE) {
      setLive(true)
      return
    }
    setLive(false)
    setSaved(v)
    saveInterval(v)
  }
  const views: View[] = degen ? [LIVE, ...(Object.keys(INTERVALS) as Interval[])] : (Object.keys(INTERVALS) as Interval[])

  return (
    <div className="chart">
      <div className="chart-toolbar" role="toolbar" aria-label="Chart interval">
        {views.map((v) => (
          <button
            key={v}
            type="button"
            className={[v === view ? 'active' : '', v === LIVE ? 'live' : ''].join(' ').trim() || undefined}
            aria-pressed={v === view}
            title={v === LIVE ? 'Every trade as it happens (degen mode). Liquidations use the mark price' : undefined}
            onClick={() => pick(v)}
          >
            {v}
          </button>
        ))}
        {view === LIVE && <span className="chart-live-note">Every trade · live</span>}
      </div>
      {/* lightweight-charts owns this element's children, so React must not render into it. */}
      <div className="chart-canvas" ref={containerRef} />
      {error && <div className="chart-error">{error}</div>}
      {view === LIVE && waiting && <div className="chart-waiting muted small">Waiting for trades…</div>}
    </div>
  )
}
