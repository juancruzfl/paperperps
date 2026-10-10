import { AreaSeries, ColorType, createChart, type IChartApi, type ISeriesApi, type UTCTimestamp } from 'lightweight-charts'
import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../lib/api'
import { useAccount } from '../lib/account'
import { formatUsd } from '../lib/format'
import { coinStates, score, summarize } from '../lib/margin'
import { useTickers } from '../lib/market'
import type { EquityPoint } from '../lib/types'

/** Lifetime PnL over time (from equity snapshots), with the live value as the last point. */
export function Performance() {
  const me = useAccount((s) => s.me)
  const secret = useAccount((s) => s.secret)
  const tickers = useTickers()
  const containerRef = useRef<HTMLDivElement>(null)
  const seriesRef = useRef<ISeriesApi<'Area'> | null>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const [points, setPoints] = useState<EquityPoint[] | null>(null)

  // Live lifetime PnL, mirroring engine.Score.
  const live = useMemo(() => {
    if (!me) return null
    const s = summarize(Number(me.balance), coinStates(me, tickers))
    return score(Number(me.priorPnl), Number(me.balance), s.unrealizedPnl, Number(me.startingBalance))
  }, [me, tickers])
  // This run's PnL is the lifetime figure minus what earlier runs (before resets) made.
  const runPnl = me && live !== null ? live - Number(me.priorPnl) : 0

  useEffect(() => {
    if (!secret) return
    let cancelled = false
    const load = () => api.equity(secret).then((p) => !cancelled && setPoints(p), () => {})
    void load()
    const id = setInterval(load, 5 * 60_000) // snapshots are taken every 5 minutes
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [secret])

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
    const negative = (live ?? 0) < 0
    series.applyOptions({
      lineColor: negative ? '#ff5c7a' : '#3ddc97',
      topColor: negative ? 'rgba(255, 92, 122, 0.2)' : 'rgba(61, 220, 151, 0.25)',
      bottomColor: negative ? 'rgba(255, 92, 122, 0.02)' : 'rgba(61, 220, 151, 0.02)',
    })
    chartRef.current?.timeScale().fitContent()
  }, [points, live])

  if (!me) return null
  const tone = (n: number) => (n > 0 ? 'up' : n < 0 ? 'down' : '')
  return (
    <div className="performance">
      <dl className="perf-stats">
        <div>
          <dt>Lifetime PnL</dt>
          <dd className={tone(live ?? 0)}>{formatUsd(live ?? 0, { signed: true })}</dd>
        </div>
        <div>
          <dt>This run</dt>
          <dd className={tone(runPnl)}>{formatUsd(runPnl, { signed: true })}</dd>
        </div>
        <div>
          <dt>Resets</dt>
          <dd>{me.resets}</dd>
        </div>
      </dl>
      <div className="perf-chart">
        <div className="perf-canvas" ref={containerRef} />
        {points !== null && points.length === 0 && (
          <div className="perf-empty muted small">Your PnL history starts with your first trade. Points are added every 5 minutes.</div>
        )}
      </div>
    </div>
  )
}
