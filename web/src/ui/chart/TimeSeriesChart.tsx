import { useEffect, useRef, useState, type ReactNode } from 'react'
import { EChart } from './EChart'
import type { EChartsType } from './echarts'
import { bodyFont, cssColor, esc, reducedMotion, seriesColor, useChartTheme } from './theme'
export { preloadCharts } from './EChart'
import { bytes } from '../../lib/fmt'
import { locale, tr } from '../../i18n'
import { C } from '../../i18n/common'

const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(' ')

export type ChartSeries = { key: string; label: string; values: number[]; color?: string }
/** bytes: per-bucket byte counts (tooltip adds the rate); size: a byte amount (memory); rate: bytes/s; pct: 0–100. */
export type ChartUnit = 'bytes' | 'size' | 'rate' | 'pct'

const fmtAxis = (u: ChartUnit, v: number) => u === 'pct' ? `${+v.toFixed(2)}%` : u === 'rate' ? bytes(v, 0) + '/s' : bytes(v, 0)
const fmtTip = (u: ChartUnit, v: number, step: number) => u === 'pct' ? `${v.toFixed(1)}%` : u === 'rate' ? bytes(v) + '/s' : u === 'size' ? bytes(v)
  : `${bytes(v)}<span style="color:var(--muted);margin-left:4px">(${bytes(v / step)}/s)</span>`

/** Max of a 4-interval axis whose ticks are round numbers in a byte unit. */
function niceMax(v: number): number {
  if (v <= 0) return 4 * 1024
  const units = [1, 1024, 1024 ** 2, 1024 ** 3, 1024 ** 4, 1024 ** 5]
  const raw = v / 4
  let u = units[0]
  for (const x of units) if (raw >= x) u = x
  const n = raw / u
  const step = [1, 2, 5, 10, 25, 50, 100, 250, 500, 1000].find((s) => s >= n) ?? 1000
  return 4 * step * u
}

/*
 * Motion spec (see docs in the PR / PLAN): every update is classified, and each class has one transition.
 *  first    – clip wipe in                         refresh  – tail morphs, window slides (points keyed by timestamp)
 *  filter   – every point morphs to its new value   resample – same window, new step: shared buckets morph
 *  set      – survivors morph, newcomers grow from their stack slot, leavers collapse in theirs
 *  domain / swap – different window or mostly-new series: dissolve (old frame fades out over the new one)
 *  zoom     – drag-to-zoom preview: the selected slice expands to the full width before the data arrives
 *  theme    – instant, same frame as the CSS
 */
type Kind = 'first' | 'refresh' | 'filter' | 'resample' | 'domain' | 'set' | 'swap' | 'theme' | 'zoom'
type Easing = 'cubicOut' | 'cubicInOut' | 'linear'
const MOTION: Record<Kind, [number, Easing]> = {
  first: [600, 'cubicOut'], refresh: [300, 'cubicOut'], filter: [400, 'cubicInOut'], resample: [400, 'cubicInOut'],
  domain: [0, 'linear'], swap: [0, 'linear'], set: [400, 'cubicInOut'], theme: [0, 'linear'], zoom: [420, 'cubicInOut'],
}
const DISSOLVE_MS = 240
const GRID = { left: 56, right: 12, top: 8, bottom: 26 }

type Snap = { times: number[]; step: number; stacked: boolean; series: ChartSeries[]; theme: number; unit: ChartUnit }

function classify(prev: Snap | null, next: Snap): Kind {
  if (!prev || prev.times.length === 0) return 'first'
  if (prev.stacked !== next.stacked) return 'swap'
  const ids = (s: Snap) => s.series.map((x) => x.key).join('\u0000')
  if (ids(prev) !== ids(next)) {
    // Mostly-new series (by user → by node): collapsing everything while the rest grows dips to empty mid-way.
    const kept = next.series.filter((s) => prev.series.some((p) => p.key === s.key)).length
    return kept * 2 >= Math.max(prev.series.length, next.series.length) && kept > 0 ? 'set' : 'swap'
  }
  if (next.times.length === 0) return 'filter'
  // A different window: echarts' line diff can't morph points from far off-screen (it snaps past 3000px), so dissolve.
  const slide = Math.max(prev.step, next.step) * 3
  if (Math.abs(prev.times[0] - next.times[0]) > slide || Math.abs(prev.times[prev.times.length - 1] - next.times[next.times.length - 1]) > slide) return 'domain'
  if (prev.step !== next.step) return 'resample'
  if (prev.theme !== next.theme && prev.series.every((s, i) => s.values === next.series[i].values)) return 'theme'
  // Same ids/step/window: a refresh only changes the newest buckets.
  const at = new Map(prev.times.map((t, i) => [t, i]))
  let changed = 0, cmp = 0
  next.series.forEach((s, si) => {
    const ps = prev.series[si]
    for (let i = 0; i < next.times.length - 2; i++) {
      const j = at.get(next.times[i])
      if (j === undefined) continue
      cmp++
      if (ps.values[j] !== s.values[i]) changed++
    }
  })
  return cmp > 0 && changed / cmp < 0.1 ? 'refresh' : 'filter'
}

/** Stable merge of old and new id order, so a removed series collapses in its own slot of the stack. */
function unionOrder(oldIds: string[], newIds: string[]): string[] {
  const out = [...newIds]
  oldIds.forEach((id, k) => {
    if (out.includes(id)) return
    let at = 0
    for (let p = k - 1; p >= 0; p--) { const i = out.indexOf(oldIds[p]); if (i >= 0) { at = i + 1; break } }
    out.splice(at, 0, id)
  })
  return out
}

/** Cheap content signature: parents rebuild arrays on every render, which must not count as an update. */
function signature(s: Snap): string {
  const t = s.times
  let out = `${t.length}:${t[0]}:${t[t.length - 1]}:${s.step}:${s.stacked}:${s.theme}`
  for (const x of s.series) {
    let sum = 0
    for (let i = 0; i < x.values.length; i++) sum += x.values[i] * ((i % 7) + 1)
    out += `|${x.key}:${x.label}:${x.color}:${x.values.length}:${sum}`
  }
  return out
}

function yMax(s: Snap): number {
  let max = 0
  for (let i = 0; i < s.times.length; i++) {
    let acc = 0
    for (const x of s.series) { const v = x.values[i] ?? 0; acc = s.stacked ? acc + v : Math.max(acc, v) }
    max = Math.max(max, acc)
  }
  if (s.unit === 'pct') return [0.4, 1, 2, 4, 10, 20, 40, 60, 100].find((x) => x >= max * 1.05) ?? Math.ceil(max / 20) * 20
  return niceMax(max)
}

export function TimeSeriesChart({ times, step, series, stacked = false, height = 240, empty, onBrush, unit = 'bytes', onPick, picked }: {
  times: number[]; step: number; series: ChartSeries[]; stacked?: boolean; height?: number; empty?: ReactNode; unit?: ChartUnit
  /** Drag across the plot to select a sub-range (unix seconds). Return false to reject the selection. */
  onBrush?: (from: number, to: number) => boolean | void
  /** clicking a legend entry (e.g. to filter by that series); keys in `picked` show as selected */
  onPick?: (key: string) => void
  picked?: string[]
}) {
  const theme = useChartTheme()
  const chart = useRef<EChartsType | null>(null)
  const prev = useRef<Snap | null>(null)
  const applied = useRef('')
  const timers = useRef<number[]>([])
  const host = useRef<HTMLDivElement>(null)
  const [brush, setBrush] = useState<{ a: number; b: number } | null>(null)
  const live = useRef({ times, step, series, unit }) // what the tooltip describes: always the real data, never ghosts
  live.current = { times, step, series, unit }
  const total = series.reduce((a, s) => a + s.values.reduce((x, y) => x + y, 0), 0)

  const color = (s: ChartSeries, i: number) => cssColor(s.color ?? seriesColor(i))
  const tok = (n: string) => cssColor(`var(--${n})`)

  const lineOf = (snap: Snap, s: ChartSeries, i: number, mode: 'real' | 'zero', enterMs: number) => {
    const c = color(s, i)
    return {
      id: s.key, name: s.label, type: 'line', stack: snap.stacked ? 'total' : undefined,
      smooth: true, smoothMonotone: 'x', symbol: 'circle', symbolSize: 7, showSymbol: false,
      lineStyle: { width: 2, color: c, join: 'round' },
      itemStyle: { color: c, borderColor: tok('surface'), borderWidth: 2 },
      areaStyle: snap.stacked ? { color: c, opacity: 0.55 }
        : snap.series.length === 1 ? { color: c, opacity: 0.16 }
        : undefined,
      emphasis: { disabled: true }, // hover shows the point; never dims the other series
      animationDuration: enterMs, animationEasing: 'cubicOut',
      // Points are keyed by bucket timestamp so refreshes slide instead of every point shifting one index.
      data: snap.times.map((t, k) => ({ id: String(t), value: [t * 1000, mode === 'zero' ? 0 : (s.values[k] ?? 0)] })),
    }
  }

  const baseOption = (snap: Snap, max: number, dur: number, easing: Easing, rm: boolean): Record<string, unknown> => {
    const font = bodyFont()
    const n = snap.times.length
    return {
      animation: !rm, animationDurationUpdate: dur, animationEasingUpdate: easing, animationThreshold: 4000,
      textStyle: { fontFamily: font },
      grid: { ...GRID, containLabel: false },
      xAxis: {
        type: 'time', min: n ? snap.times[0] * 1000 : undefined, max: n ? snap.times[n - 1] * 1000 : undefined,
        axisLine: { show: false }, axisTick: { show: false }, splitLine: { show: false },
        axisLabel: {
          color: tok('muted'), fontSize: 11, hideOverlap: true,
          // Level formatter: day boundaries read as dates (7d / 30d), intra-day ticks as times.
          formatter: { year: '{yyyy}', month: tr(C).monthFmt, day: '{M}/{d}', hour: '{HH}:{mm}', minute: '{HH}:{mm}', second: '{HH}:{mm}:{ss}', millisecond: '{HH}:{mm}', none: '{M}/{d} {HH}:{mm}' },
        },
      },
      yAxis: {
        type: 'value', min: 0, max, interval: max / 4,
        axisLabel: { color: tok('muted'), fontSize: 11, formatter: (v: number) => fmtAxis(snap.unit, v) },
        splitLine: { lineStyle: { color: tok('grid'), width: 1 } },
      },
      tooltip: {
        show: true, trigger: 'axis', triggerOn: 'mousemove|click', confine: true, appendTo: 'body', transitionDuration: rm ? 0 : 0.18,
        backgroundColor: 'var(--surface)', borderColor: 'var(--hl-med)', borderWidth: 1, padding: [8, 12],
        extraCssText: 'border-radius:12px;box-shadow:0 8px 24px rgb(0 0 0/.14);min-width:180px;',
        textStyle: { color: 'var(--text)', fontSize: 12, fontFamily: font },
        axisPointer: { type: 'line', snap: true, animation: !rm, lineStyle: { color: tok('subtle'), type: [3, 3], width: 1 } },
        formatter: (ps: { axisValue: number }[]) => {
          const L = live.current
          const k = L.times.indexOf(Math.round((ps[0]?.axisValue ?? 0) / 1000))
          if (k < 0) return ''
          const head = `<div style="font-weight:500;margin-bottom:4px" class="tabular">${esc(new Date(L.times[k] * 1000).toLocaleString(locale(), { hour12: false }))}</div>`
          return head + L.series.map((s, i) => `<div style="display:flex;align-items:center;justify-content:space-between;gap:16px;padding:2px 0">`
            + `<span style="display:inline-flex;align-items:center;gap:6px;color:var(--subtle);min-width:0"><i style="flex:none;width:8px;height:8px;border-radius:2px;background:${esc(color(s, i))}"></i><span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:200px">${esc(s.label)}</span></span>`
            + `<span class="tabular" style="white-space:nowrap">${fmtTip(L.unit, s.values[k] ?? 0, L.step)}</span></div>`).join('')
        },
      },
    }
  }

  const clearTimers = () => { timers.current.forEach((t) => { clearTimeout(t); cancelAnimationFrame(t) }); timers.current = [] }

  const dissolve = (c: EChartsType, rm: boolean) => {
    const box = host.current
    if (!box) return
    const img = new Image()
    img.alt = ''
    img.src = c.getDataURL({ type: 'png', pixelRatio: devicePixelRatio, excludeComponents: ['tooltip'] })
    img.style.cssText = 'position:absolute;left:0;top:0;width:100%;height:100%;pointer-events:none'
    box.appendChild(img)
    img.animate([{ opacity: 1 }, { opacity: 0 }], { duration: rm ? 120 : DISSOLVE_MS, easing: 'cubic-bezier(.4,0,1,1)', fill: 'forwards' })
      .finished.then(() => img.remove(), () => img.remove())
  }

  const render = () => {
    const c = chart.current
    if (!c) return
    const next: Snap = { times, step, stacked, series, theme, unit }
    const sig = signature(next)
    if (sig === applied.current) return
    applied.current = sig
    clearTimers()
    const kind = classify(prev.current, next)
    const rm = reducedMotion()
    const [dur, easing]: [number, Easing] = rm ? [0, 'linear'] : MOTION[kind]
    const max = yMax(next) // fixed across all phases, so survivors never re-target mid-flight
    const base = baseOption(next, max, dur, easing, rm)
    const enter = kind === 'first' && !rm ? MOTION.first[0] : 0
    const finalSeries = series.map((s, i) => lineOf(next, s, i, 'real', enter))
    const p = prev.current
    prev.current = next

    if (kind === 'domain' || kind === 'swap') {
      dissolve(c, rm)
      c.setOption({ ...base, series: finalSeries }, { replaceMerge: ['series'] })
      return
    }
    const newIds = series.map((s) => s.key)
    const added = p && kind === 'set' && !rm ? newIds.filter((id) => !p.series.some((s) => s.key === id)) : []
    const removed = p && kind === 'set' && !rm ? p.series.filter((s) => !newIds.includes(s.key)) : []
    if (!added.length && !removed.length) {
      c.setOption({ ...base, series: finalSeries }, { replaceMerge: ['series'] })
      return
    }
    // Phase A: survivors morph; newcomers sit flat in their slot; leavers (silent ghosts) collapse in theirs.
    const order = unionOrder(p!.series.map((s) => s.key), newIds)
    const byId = new Map<string, object>()
    series.forEach((s, i) => byId.set(s.key, lineOf(next, s, i, added.includes(s.key) ? 'zero' : 'real', 0)))
    removed.forEach((s) => {
      const ghost: Snap = { ...next, series: [...series, s] }
      const ghostValues = { ...s, values: next.times.map(() => 0) }
      byId.set(s.key, { ...lineOf(ghost, ghostValues, p!.series.indexOf(s), 'zero', 0), silent: true })
    })
    c.setOption({ ...base, series: order.map((id) => byId.get(id)!) }, { replaceMerge: ['series'] })
    // Phase B (next frame): newcomers grow from the baseline / the layer below.
    if (added.length) {
      timers.current.push(requestAnimationFrame(() => c.setOption({
        series: added.map((id) => ({ id, data: finalSeries[newIds.indexOf(id)].data })),
      })))
    }
    // Phase C: once the ghosts are flat, drop them (an invisible change).
    timers.current.push(window.setTimeout(() => c.setOption({ ...base, animationDurationUpdate: 0, series: finalSeries }, { replaceMerge: ['series'] }), dur + 60))
  }

  useEffect(render)
  useEffect(() => clearTimers, [])

  // ---- drag-to-zoom -------------------------------------------------------------------------------
  const plotX = (e: React.PointerEvent<HTMLDivElement>) => {
    const r = e.currentTarget.getBoundingClientRect()
    return Math.max(GRID.left, Math.min(r.width - GRID.right, e.clientX - r.left))
  }
  const timeAt = (px: number) => {
    const v = chart.current?.convertFromPixel({ xAxisIndex: 0 }, px) as number | undefined
    return typeof v === 'number' && isFinite(v) ? v / 1000 : NaN
  }
  /** Expand the selected slice of the current data to the full width while the new range loads. */
  const previewZoom = (f: number, t: number) => {
    const c = chart.current, p = prev.current
    if (!c || !p || reducedMotion()) return
    const keep = p.times.map((tt, i) => [tt, i] as const).filter(([tt]) => tt >= f - p.step && tt <= t).map(([, i]) => i)
    if (keep.length < 2) return
    const snap: Snap = { ...p, times: keep.map((i) => p.times[i]), series: p.series.map((s) => ({ ...s, values: keep.map((i) => s.values[i] ?? 0) })) }
    clearTimers()
    const [dur, easing] = MOTION.zoom
    c.setOption({ ...baseOption(snap, yMax(snap), dur, easing, false), series: snap.series.map((s, i) => lineOf(snap, s, i, 'real', 0)) }, { replaceMerge: ['series'] })
    prev.current = snap
  }
  const onDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!onBrush || e.button !== 0 || times.length < 2 || e.pointerType === 'touch') return
    const r = e.currentTarget.getBoundingClientRect(), y = e.clientY - r.top
    if (y < GRID.top || y > r.height - GRID.bottom) return
    e.currentTarget.setPointerCapture(e.pointerId)
    // Freeze the tooltip/crosshair while selecting.
    chart.current?.setOption({ tooltip: { triggerOn: 'none' } })
    chart.current?.dispatchAction({ type: 'hideTip' })
    chart.current?.dispatchAction({ type: 'updateAxisPointer', currTrigger: 'leave' })
    const x0 = plotX(e)
    setBrush({ a: x0, b: x0 })
  }
  const onMove = (e: React.PointerEvent<HTMLDivElement>) => { if (brush) setBrush({ ...brush, b: plotX(e) }) }
  const endBrush = (commit: boolean) => {
    const b = brush
    setBrush(null)
    chart.current?.dispatchAction({ type: 'hideTip' })
    chart.current?.dispatchAction({ type: 'updateAxisPointer', currTrigger: 'leave' })
    chart.current?.setOption({ tooltip: { triggerOn: 'mousemove|click' } })
    if (!commit || !b || !onBrush || Math.abs(b.b - b.a) <= 6) return
    const f = Math.round(timeAt(Math.min(b.a, b.b))), t = Math.round(timeAt(Math.max(b.a, b.b)))
    if (!isFinite(f) || !isFinite(t)) return
    if (onBrush(f, t) !== false) previewZoom(f, t)
  }

  return (
    <div className="relative w-full select-none">
      {series.length > 1 && (
        <div className="mb-3 flex flex-wrap gap-x-4 gap-y-1.5 text-xs text-subtle" aria-label={tr(C).legend}>
          {series.map((s, i) => {
            // a button only when the entry can be picked; otherwise plain text for screen readers
            const pick = onPick && s.key !== '(other)' ? { type: 'button' as const, onClick: () => onPick(s.key), 'aria-pressed': !!picked?.includes(s.key) } : null
            const Tag = pick ? 'button' : 'span'
            return (
              <Tag key={s.key} {...pick}
                className={cx('-mx-1.5 -my-0.5 inline-flex animate-content-in items-center gap-1.5 rounded-full px-1.5 py-0.5 outline-none',
                  pick && 'cursor-pointer transition-colors hover:bg-overlay focus-visible:ring-2 focus-visible:ring-accent', picked?.includes(s.key) && 'bg-accent/12 font-medium')}>
                <span className="h-2.5 w-2.5 rounded-[3px]" style={{ background: s.color ?? seriesColor(i) }} />
                <span className="text-text">{s.label}</span>
              </Tag>
            )
          })}
        </div>
      )}
      <div ref={host} role="img" aria-label={tr(C).trafficTrend(bytes(total), !!onBrush)}
        className="relative" style={{ height, cursor: onBrush ? 'crosshair' : undefined }}
        onPointerDown={onDown} onPointerMove={onMove} onPointerUp={() => endBrush(true)} onPointerCancel={() => endBrush(false)}>
        <EChart height={height} onReady={(c) => { chart.current = c; applied.current = ''; render() }} />
        {brush && Math.abs(brush.b - brush.a) > 1 && (
          <div className="pointer-events-none absolute rounded-[2px]" style={{
            left: Math.min(brush.a, brush.b), width: Math.abs(brush.b - brush.a), top: GRID.top, bottom: GRID.bottom,
            background: 'color-mix(in srgb, var(--accent) 14%, transparent)', border: '1px solid color-mix(in srgb, var(--accent) 50%, transparent)',
          }} />
        )}
        <div className="pointer-events-none absolute grid place-items-center text-[13px] text-muted"
          style={{ left: GRID.left, right: GRID.right, top: GRID.top, bottom: GRID.bottom, opacity: total === 0 ? 1 : 0, transition: 'opacity var(--dur-3) var(--ease-std)' }}>
          {empty ?? tr(C).noTraffic}
        </div>
      </div>
    </div>
  )
}
