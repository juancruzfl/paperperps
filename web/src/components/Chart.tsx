import {
  CandlestickSeries,
  ColorType,
  createChart,
  HistogramSeries,
  LineStyle,
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
const INTERVAL_KEY = 'paperperps.chartInterval'

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
}

const LINE_STYLE = { solid: LineStyle.Solid, dashed: LineStyle.Dashed, dotted: LineStyle.Dotted }

export function Chart({ coin, szDecimals, refPx, lines = [] }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candlesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const volumeRef = useRef<ISeriesApi<'Histogram'> | null>(null)
  const [interval, setChartInterval] = useState<Interval>(loadInterval)
  const [error, setError] = useState<string | null>(null)

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
    chartRef.current = chart
    return () => {
      chart.remove()
      chartRef.current = null
      candlesRef.current = null
      volumeRef.current = null
    }
  }, [])

  // Price lines are redrawn only when they actually change (they're recomputed every tick).
  const linesKey = JSON.stringify(lines)
  useEffect(() => {
    const series = candlesRef.current
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
  }, [linesKey])

  // Axis precision follows the asset's price.
  const decimals = pxDecimals(refPx, szDecimals)
  useEffect(() => {
    candlesRef.current?.applyOptions({
      priceFormat: { type: 'price', precision: decimals, minMove: 10 ** -decimals },
    })
  }, [decimals])

  // Load history, then extend the last bar from live trades.
  useEffect(() => {
    const candles = candlesRef.current
    const volume = volumeRef.current
    if (!candles || !volume) return
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
  }, [coin, interval])

  const pick = (i: Interval) => {
    setChartInterval(i)
    saveInterval(i)
  }

  return (
    <div className="chart">
      <div className="chart-toolbar" role="toolbar" aria-label="Chart interval">
        {(Object.keys(INTERVALS) as Interval[]).map((i) => (
          <button
            key={i}
            type="button"
            className={i === interval ? 'active' : undefined}
            aria-pressed={i === interval}
            onClick={() => pick(i)}
          >
            {i}
          </button>
        ))}
      </div>
      {/* lightweight-charts owns this element's children, so React must not render into it. */}
      <div className="chart-canvas" ref={containerRef} />
      {error && <div className="chart-error">{error}</div>}
    </div>
  )
}
