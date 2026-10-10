import { AreaSeries, ColorType, createChart, type IChartApi, type ISeriesApi, type UTCTimestamp } from 'lightweight-charts'
import { useEffect, useRef } from 'react'

export type PnlPoint = { t: string; cumulativePnl: string }

type Props = {
  /** Snapshots, oldest first; null while loading. */
  points: PnlPoint[] | null
  /** The live value, appended as the last point. */
  live: number | null
  empty: string
}

/** Lifetime PnL over time as an area chart, green above zero and red below. */
export function PnlChart({ points, live, empty }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const seriesRef = useRef<ISeriesApi<'Area'> | null>(null)
  const chartRef = useRef<IChartApi | null>(null)

  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const chart = createChart(el, {
      autoSize: true,
      layout: { background: { type: ColorType.Solid, color: 'transparent' }, textColor: '#8b98a5', attributionLogo: true },
      grid: { vertLines: { visible: false }, horzLines: { color: 'rgba(31, 40, 51, 0.6)' } },
      rightPriceScale: { borderColor: '#1f2833' },
      timeScale: { borderColor: '#1f2833', timeVisible: true },
    })
    seriesRef.current = chart.addSeries(AreaSeries, {
      lineColor: '#3ddc97',
      topColor: 'rgba(61, 220, 151, 0.25)',
      bottomColor: 'rgba(61, 220, 151, 0.02)',
      lineWidth: 2,
      priceFormat: { type: 'price', precision: 2, minMove: 0.01 },
    })
    chartRef.current = chart
    return () => {
      chart.remove()
      chartRef.current = null
      seriesRef.current = null
    }
  }, [])

  useEffect(() => {
    const series = seriesRef.current
    if (!series || !points) return
    const data = points.map((p) => ({ time: (Date.parse(p.t) / 1000) as UTCTimestamp, value: Number(p.cumulativePnl) }))
    if (live !== null) {
      const now = Math.floor(Date.now() / 1000)
      if (!data.length || now > data[data.length - 1].time) data.push({ time: now as UTCTimestamp, value: live })
    }
    series.setData(data)
    const negative = (live ?? data.at(-1)?.value ?? 0) < 0
    series.applyOptions({
      lineColor: negative ? '#ff5c7a' : '#3ddc97',
      topColor: negative ? 'rgba(255, 92, 122, 0.2)' : 'rgba(61, 220, 151, 0.25)',
      bottomColor: negative ? 'rgba(255, 92, 122, 0.02)' : 'rgba(61, 220, 151, 0.02)',
    })
    chartRef.current?.timeScale().fitContent()
  }, [points, live])

  return (
    <div className="perf-chart">
      <div className="perf-canvas" ref={containerRef} />
      {points !== null && points.length === 0 && <div className="perf-empty muted small">{empty}</div>}
    </div>
  )
}
