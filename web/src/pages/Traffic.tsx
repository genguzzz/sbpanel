import { useMemo, useRef, useState, type ReactNode } from 'react'
import { Dialog, Popover } from 'react-aria-components'
import { keepPreviousData, useQueries, useQuery } from '@tanstack/react-query'
import { arc, pie, type PieArcDatum } from 'd3-shape'
import { api, apiRaw, type SeriesResp, type TopRow, type User, type Node } from '../lib/api'
import { bytes, num, pct, rate } from '../lib/fmt'
import { useRoute, go } from '../lib/router'
import { Button, Card, PageHeader, Segmented, Select, Skeleton, cx, popoverCls } from '../ui/kit'
import { TimeChart, TweenBytes, entitySlot, entityColor, seriesColor } from '../ui/charts'
import { IconCheck, IconSearch, IconX } from '../ui/icons'
import { tr } from '../i18n'
import { C } from '../i18n/common'
import { TR } from '../i18n/traffic'

const ranges = [
  { id: '1h', get label() { return tr(TR).r1h }, sec: 3600 },
  { id: '6h', get label() { return tr(TR).r6h }, sec: 6 * 3600 },
  { id: '24h', get label() { return tr(TR).r24h }, sec: 86400 },
  { id: '7d', get label() { return tr(TR).r7d }, sec: 7 * 86400 },
  { id: '30d', get label() { return tr(TR).r30d }, sec: 30 * 86400 },
] as const
type RangeID = (typeof ranges)[number]['id']

const STEPS = [60, 300, 900, 1800, 3600, 10800, 21600, 86400]
const MAX_POINTS = 2000
const KEEP = 30 * 86400
/** Smallest allowed step that keeps the series at ≤ 720 points, never finer than `min`. */
const autoStep = (span: number, min = 60) => STEPS.find((st) => st >= min && span / st <= 720) ?? 86400

/** Every key the page can filter on; the URL carries them as comma-separated values (OR within a key). */
const FILTER_KEYS = ['node', 'user', 'inbound', 'outbound', 'rule', 'domain', 'host', 'src_ip', 'port'] as const
type FilterKey = (typeof FILTER_KEYS)[number]
/** Keys that only the 5-minute dimension tables have: filtering or splitting by them coarsens the series. */
const DETAIL_KEYS: FilterKey[] = ['rule', 'domain', 'host', 'src_ip', 'port']
const MONO: string[] = ['domain', 'host', 'src_ip', 'port']

/** Left column: the dimensions you narrow down by. */
const FACETS: FilterKey[] = ['node', 'user', 'inbound', 'outbound', 'rule']
/** Main ranking: what you look at. */
const ANALYSIS = ['domain', 'host', 'src_ip', 'port'] as const
type AnalysisDim = (typeof ANALYSIS)[number]
const SPLITS = ['total', 'node', 'user', 'outbound', 'inbound', 'domain', 'rule'] as const

const dimLabel = (k: string): string => {
  const t = tr(TR)
  return ({ domain: t.dDomain, host: t.dHost, src_ip: t.dSrc, port: t.dPort, user: t.dUser, node: t.dNode, inbound: t.dInbound, outbound: t.dOutbound, rule: t.dRule } as Record<string, string>)[k] ?? k
}
const splitLabel = (k: string): string => {
  const t = tr(TR)
  return ({ total: t.gTotal, user: t.gUser, node: t.gNode, inbound: t.gInbound, outbound: t.gOutbound, domain: t.gDomain, rule: t.gRule } as Record<string, string>)[k] ?? k
}
const precLabel = (p: string) => (p === '1m' ? tr(TR).prec1m : p === '5m' ? tr(TR).prec5m : p === '1h' ? tr(TR).prec1h : '')
const OTHER_COLOR = 'var(--hl-high)'

async function fetchTop(path: string): Promise<{ rows: TopRow[]; precision: string }> {
  const { data, headers } = await apiRaw<TopRow[] | { precision?: string; rows?: TopRow[]; items?: TopRow[] }>(path)
  if (Array.isArray(data)) return { rows: data, precision: headers.get('X-Precision') ?? '' }
  return { rows: data?.rows ?? data?.items ?? [], precision: data?.precision ?? headers.get('X-Precision') ?? '' }
}

export function TrafficPage() {
  const { params } = useRoute()
  const cFrom = Number(params.get('from')) || 0
  const cTo = Number(params.get('to')) || 0
  const custom = cFrom > 0 && cTo > cFrom
  const range: RangeID | 'custom' = custom ? 'custom' : ((params.get('range') as RangeID) || '24h')
  const stepSel = params.get('step') || 'auto'
  const group = (SPLITS as readonly string[]).includes(params.get('group') ?? '') ? params.get('group')! : 'total'
  const dim: AnalysisDim = (ANALYSIS as readonly string[]).includes(params.get('dim') ?? '') ? (params.get('dim') as AnalysisDim) : 'domain'
  const [q, setQ] = useState(params.get('q') || '')
  const [hover, setHover] = useState<string | null>(null)
  const t = tr(TR)

  const filters = useMemo(() => {
    const f: Partial<Record<FilterKey, string[]>> = {}
    for (const k of FILTER_KEYS) {
      const v = (params.get(k) ?? '').split(',').filter(Boolean)
      if (v.length) f[k] = v
    }
    return f
  }, [params])
  const active = FILTER_KEYS.filter((k) => filters[k]?.length)

  const nav = (patch: Record<string, string | undefined>) => {
    const next: Record<string, string> = Object.fromEntries(params.entries())
    for (const [k, v] of Object.entries(patch)) { if (v) next[k] = v; else delete next[k] }
    go('traffic', next)
  }
  const set = (k: string, v: string) => nav(k === 'range' ? { range: v, step: undefined, from: undefined, to: undefined } : { [k]: v })
  const setRange = (f: number, to: number) => nav({ range: undefined, step: undefined, from: String(f), to: String(to) })
  /** Toggle one value of a filter. Picking a domain while ranking domains drills into its hosts. */
  const toggle = (k: FilterKey, v: string) => {
    const cur = filters[k] ?? []
    const on = !cur.includes(v)
    const nextVals = on ? [...cur, v] : cur.filter((x) => x !== v)
    const patch: Record<string, string | undefined> = { [k]: nextVals.join(',') || undefined }
    if (on && k === 'domain' && dim === 'domain') patch.dim = 'host'
    if (k === dim) setQ('')
    nav(patch)
  }
  const clearAll = () => nav(Object.fromEntries(FILTER_KEYS.map((k) => [k, undefined])))
  const [picker, setPicker] = useState(false)
  // PC can show the filters as a bar of pills (default) or the original side column; phones always get the bar
  const [side, setSideRaw] = useState(() => { try { return localStorage.getItem(FACET_LAYOUT_KEY) === 'side' } catch { return false } })
  const setSide = (v: boolean) => { setSideRaw(v); try { localStorage.setItem(FACET_LAYOUT_KEY, v ? 'side' : 'bar') } catch { /* private mode */ } }
  const customRef = useRef<HTMLSpanElement>(null)

  const r = ranges.find((x) => x.id === range) ?? ranges[2]
  const to = custom ? cTo : Math.floor(Date.now() / 1000 / 60) * 60 + 60
  const from = custom ? cFrom : to - r.sec
  const span = to - from
  // Splitting or filtering by a connection detail reads 5-minute data, so 1-minute steps don't exist there.
  const detail = DETAIL_KEYS.some((k) => filters[k]?.length) || group === 'domain' || group === 'rule'
  const minStep = detail ? 300 : 60
  const auto = autoStep(span, minStep)
  const stepOpts = STEPS.filter((st) => st >= minStep && span / st <= MAX_POINTS)
  const step = stepSel !== 'auto' && stepOpts.includes(Number(stepSel)) ? Number(stepSel) : auto
  const rangeKey = custom ? `${from}-${to}` : range
  /** Query string for every active filter except `skip` (a facet never filters itself, so you can switch). */
  const qs = (skip?: FilterKey) => FILTER_KEYS.filter((k) => k !== skip && filters[k]?.length).map((k) => `&${k}=${encodeURIComponent(filters[k]!.join(','))}`).join('')
  const fKey = qs()
  const refetch: number | false = custom ? false : 30000

  const users = useQuery({ queryKey: ['users'], queryFn: () => api<User[]>('/users') })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<Node[]>('/nodes') })
  const series = useQuery({
    queryKey: ['series', rangeKey, step, group, fKey],
    queryFn: () => api<SeriesResp & { conns?: number; precision?: string }>(`/traffic/series?from=${from}&to=${to}&step=${step}&group=${group}${fKey}`),
    refetchInterval: refetch, placeholderData: keepPreviousData,
  })
  const top = useQuery({
    queryKey: ['top', dim, rangeKey, fKey],
    queryFn: () => fetchTop(`/traffic/top?dim=${dim}&from=${from}&to=${to}&limit=500${fKey}`),
    refetchInterval: refetch, placeholderData: keepPreviousData,
  })
  const facetQ = useQueries({
    queries: FACETS.map((k) => {
      const sk = qs(k)
      return {
        queryKey: ['facet', k, rangeKey, sk],
        queryFn: () => fetchTop(`/traffic/top?dim=${k}&from=${from}&to=${to}&limit=200${sk}`),
        refetchInterval: refetch, placeholderData: keepPreviousData,
      }
    }),
  })

  const userName = (id: string) => users.data?.find((u) => String(u.id) === id)?.username ?? `#${id}`
  const nodeName = (id: string) => nodes.data?.find((n) => n.id === id)?.name ?? id
  const valueLabel = (k: FilterKey, v: string) => {
    if (k === 'user') return userName(v)
    if (k === 'node') return nodeName(v)
    if (k === 'inbound') { const i = v.indexOf('/'); return i > 0 ? `${nodeName(v.slice(0, i))} / ${v.slice(i + 1)}` : v }
    return v || '(none)'
  }

  const chartSeries = useMemo(() => {
    const s = series.data?.series ?? []
    if (group === 'total') {
      const tot = s[0]
      if (!tot) return []
      return [
        { key: 'down', label: t.down, values: tot.points.map((p) => p[2]), color: 'var(--s1)' },
        { key: 'up', label: t.up, values: tot.points.map((p) => p[1]), color: 'var(--s2)' },
      ]
    }
    // Colour and stack order follow the entity (first-seen slot), never its rank, so they survive filters and refreshes.
    return s.map((x) => ({ key: x.key, label: x.label, values: x.points.map((p) => p[1] + p[2]), color: x.key === '(other)' ? OTHER_COLOR : entityColor(group, x.key) }))
      .sort((a, b) => entitySlot(group, a.key) - entitySlot(group, b.key))
  }, [series.data, group])
  const times = series.data?.series[0]?.points.map((p) => p[0]) ?? []
  // a split series is one value of a filter key: clicking it in the legend filters by it
  const pickable = group !== 'total' // every other split is a filter key
  const realStep = series.data?.step ?? step
  const stats = useMemo(() => {
    const s = series.data?.series ?? []
    let down = 0, up = 0, peak = 0, peakAt = 0
    for (const x of s) { down += x.down; up += x.up }
    const n = s[0]?.points.length ?? 0
    for (let i = 0; i < n; i++) {
      let v = 0
      for (const x of s) v += x.points[i][1] + x.points[i][2]
      if (v > peak) { peak = v; peakAt = s[0].points[i][0] }
    }
    return { down, up, peak: peak / realStep, peakAt, conns: series.data?.conns ?? 0 }
  }, [series.data, realStep])

  const topRows = top.data?.rows ?? []
  const topTotal = topRows.reduce((a, x) => a + x.up + x.down, 0)
  const rows = topRows.filter((x) => !q || x.label.toLowerCase().includes(q.toLowerCase()))
  const DONUT_N = 8
  const slices = useMemo(() => {
    const head = topRows.slice(0, DONUT_N).map((x, i) => ({ key: x.key, label: x.label, value: x.up + x.down, color: seriesColor(i) }))
    const rest = topRows.slice(DONUT_N).reduce((a, x) => a + x.up + x.down, 0)
    return rest > 0 ? [...head, { key: '(other)', label: t.other, value: rest, color: OTHER_COLOR }] : head
  }, [topRows])
  const sliceColor = new Map(slices.map((s) => [s.key, s.color]))
  const precision = top.data?.precision ?? ''
  const sel = filters[dim] ?? []

  return (
    <>
      <PageHeader title={t.title} subtitle={t.subtitle} />
      <div className="mb-3 flex flex-wrap items-end gap-3">
        <div className="flex min-w-0 max-w-full flex-col gap-1.5">
          <span className="text-[13px] font-medium text-subtle">{t.range}</span>
          <Segmented label={t.range} value={range} onChange={(v) => (v === 'custom' ? setPicker(true) : set('range', v))}
            options={[...ranges.map((x) => ({ id: x.id as RangeID | 'custom', label: x.label })), {
              id: 'custom', label: <span ref={customRef} className="tabular" onClick={() => custom && setPicker(true)}>{custom ? rangeLabel(from, to) : t.custom}</span>,
            }]} />
          <Popover triggerRef={customRef} isOpen={picker} onOpenChange={setPicker} placement="bottom" className={cx('w-[340px] max-w-[calc(100vw-24px)]', popoverCls)}>
            <Dialog aria-label={t.customRange} className="p-3 outline-none">
              <RangePicker from={custom ? from : to - r.sec} to={custom ? to : Math.floor(Date.now() / 60000) * 60}
                onApply={(f, t) => { setPicker(false); setRange(f, t) }} onCancel={() => setPicker(false)} />
            </Dialog>
          </Popover>
        </div>
        <Select label={t.step} className="w-40" value={stepOpts.includes(Number(stepSel)) ? stepSel : 'auto'} onChange={(v) => set('step', v)}
          options={[{ id: 'auto', label: t.auto(stepLabel(auto)) }, ...stepOpts.map((st) => ({ id: String(st), label: stepLabel(st) }))]} />
        <div className="ml-auto hidden flex-col gap-1.5 lg:flex">
          <span className="text-[13px] font-medium text-subtle">{t.facetLayout}</span>
          <Segmented label={t.facetLayout} value={side ? 'side' : 'bar'} onChange={(v) => setSide(v === 'side')}
            options={[{ id: 'bar', label: t.layoutBar }, { id: 'side', label: t.layoutSide }]} />
        </div>
      </div>

      {/*
        Filter bar: one pill per dimension; its list opens in a popover, so picking a value never
        reflows the page. Values picked from the ranking (domain, host…) show as chips after the pills.
      */}
      <div className={cx('mb-4 flex min-h-11 items-center gap-2 rounded-[12px] border bg-surface px-2 py-1.5', side && 'lg:hidden')}>
        <div className="-my-1 flex min-w-0 flex-1 items-center gap-2 overflow-x-auto py-1 [scrollbar-width:none] sm:flex-wrap sm:overflow-visible [&::-webkit-scrollbar]:hidden">
          {FACETS.map((k, i) => (
            <FacetPicker key={k} name={dimLabel(k)} ns={k} busy={facetQ[i].isPlaceholderData} loading={facetQ[i].isPending && !facetQ[i].data}
              rows={facetQ[i].data?.rows ?? []} selected={filters[k] ?? []} label={(v) => valueLabel(k, v)}
              onToggle={(v) => toggle(k, v)} onClear={() => nav({ [k]: undefined })} />
          ))}
          {active.filter((k) => !FACETS.includes(k)).flatMap((k) => filters[k]!.map((v) => <FilterChip key={k + v} k={k} label={valueLabel(k, v)} onRemove={() => toggle(k, v)} />))}
          {active.length === 0 && <span className="hidden px-1 text-xs text-muted lg:inline">{t.noFilter}</span>}
        </div>
        {active.length > 0 && <button type="button" onClick={clearAll} className="shrink-0 cursor-pointer px-2 text-[13px] text-subtle outline-none hover:text-text hover:underline focus-visible:ring-2 focus-visible:ring-accent">{t.clearAll}</button>}
      </div>

      {side && (
        <div className="mb-4 hidden min-h-9 flex-wrap items-center gap-2 rounded-[12px] border border-dashed px-3 py-1.5 lg:flex">
          <span className="text-[13px] font-medium text-subtle">{t.filters}</span>
          {active.length === 0 && <span className="text-[13px] text-muted">{t.noFilter}</span>}
          {active.flatMap((k) => filters[k]!.map((v) => <FilterChip key={k + v} k={k} label={valueLabel(k, v)} onRemove={() => toggle(k, v)} />))}
          {active.length > 0 && <button type="button" onClick={clearAll} className="ml-auto cursor-pointer text-[13px] text-subtle outline-none hover:text-text hover:underline focus-visible:ring-2 focus-visible:ring-accent">{t.clearAll}</button>}
        </div>
      )}

      <div className={cx('grid items-start gap-4', side && 'lg:grid-cols-[300px_minmax(0,1fr)]')}>
        {side && (
          <aside className="hidden min-w-0 flex-col gap-4 lg:sticky lg:top-4 lg:flex">
            <Card title={t.facets}>
              <p className="-mt-1 mb-3 text-xs text-muted">{t.facetsSideHint}</p>
              <div className="-mx-2 flex flex-col divide-y">
                {FACETS.map((k, i) => (
                  <Facet key={k} name={dimLabel(k)} ns={k} busy={facetQ[i].isPlaceholderData} loading={facetQ[i].isPending && !facetQ[i].data}
                    rows={facetQ[i].data?.rows ?? []} selected={filters[k] ?? []} onToggle={(v) => toggle(k, v)} />
                ))}
              </div>
            </Card>
          </aside>
        )}
        <div className="flex min-w-0 flex-col gap-4">
          <div className={cx('grid grid-cols-2 gap-3', side ? 'xl:grid-cols-4' : 'lg:grid-cols-4')}>
            <Kpi label={t.kTotal} value={<TweenBytes value={stats.down + stats.up} />} sub={<>↓ {bytes(stats.down)} · ↑ {bytes(stats.up)}</>} />
            <Kpi label={t.kPeak} value={rate(stats.peak)} sub={stats.peakAt ? t.kPeakAt(md(new Date(stats.peakAt * 1000))) : t.kAvg(rate((stats.down + stats.up) / Math.max(1, span)))} />
            <Kpi label={t.kConns} value={num(stats.conns)} sub={t.kAvg(`${num(Math.round(stats.conns / Math.max(1, span / 3600)))}/h`)} />
            <Kpi label={t.kItems(dimLabel(dim))} value={num(topRows.length)} sub={precLabel(precision)} />
          </div>

          <Card busy={series.isPlaceholderData}
            title={<span>{t.trend}<span className="ml-2 text-[13px] font-normal text-subtle tabular">{t.perPoint(stepLabel(realStep))}</span></span>}
>
            <div className="mb-3 flex min-w-0 items-center gap-2">
              <span className="shrink-0 text-[13px] text-subtle">{t.splitBy}</span>
              <div className="min-w-0 flex-1">
                <Segmented label={t.splitBy} value={group} onChange={(v) => set('group', v === 'total' ? '' : v)} options={SPLITS.map((s) => ({ id: s, label: splitLabel(s) }))} />
              </div>
            </div>
            {series.isPending && !series.data ? <Skeleton className="h-[260px]" /> : (
              <TimeChart height={260} step={realStep} times={times} series={chartSeries} stacked={group !== 'total'}
                onPick={pickable ? (k: string) => toggle(group as FilterKey, k) : undefined} picked={pickable ? filters[group as FilterKey] ?? [] : undefined}
                onBrush={(f: number, t: number) => { const lo = Math.max(Math.floor(Date.now() / 1000) - KEEP, Math.floor(f / 60) * 60); const hi = Math.ceil(t / 60) * 60; if (hi - lo < 120) return false; setRange(lo, hi) }} />
            )}
            <div className="mt-1 text-xs text-muted">{t.zoomHint}{pickable && <> · {t.legendHint}</>}{detail && <> · {t.detailMode}</>}</div>
          </Card>

          <Card busy={top.isPlaceholderData} pad={false}
            title={<span>{t.analysis}<span className="ml-2 text-xs font-normal text-muted">{precLabel(precision)}</span></span>}
            action={
              <label className="relative">
                <IconSearch size={14} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
                <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t.filter} aria-label={t.filter}
                  className="h-8 w-44 rounded-full border bg-base pr-3 pl-8 text-[13px] outline-none focus:border-accent" />
              </label>
            }>
            <div className="px-5 pt-1 pb-3">
              <Segmented label={t.analysis} value={dim} onChange={(v) => { setQ(''); set('dim', v) }} options={ANALYSIS.map((d) => ({ id: d, label: dimLabel(d) }))} />
            </div>
            {top.isPending && !top.data ? <div className="px-5 pb-5"><Skeleton className="h-72" /></div> : topRows.length === 0 ? (
              <div className="py-14 text-center text-[13px] text-muted">{t.empty}</div>
            ) : (
              <div className="grid border-t md:grid-cols-[260px_minmax(0,1fr)]">
                <div className="flex flex-col items-center gap-3 border-b px-5 py-5 md:border-r md:border-b-0">
                  <Donut slices={slices} total={topTotal} hover={hover} onHover={setHover} selected={sel}
                    onSelect={(k) => k !== '(other)' && toggle(dim, k)} center={dimLabel(dim)} />
                  <ul className="flex w-full flex-col gap-0.5">
                    {slices.map((s) => (
                      <li key={s.key}>
                        <button type="button" disabled={s.key === '(other)'} onClick={() => toggle(dim, s.key)}
                          onMouseEnter={() => setHover(s.key)} onMouseLeave={() => setHover(null)}
                          className={cx('flex w-full items-center gap-2 rounded-[8px] px-2 py-1 text-left text-[12.5px] outline-none transition-colors enabled:cursor-pointer enabled:hover:bg-overlay focus-visible:ring-2 focus-visible:ring-accent',
                            hover === s.key && 'bg-overlay', sel.includes(s.key) && 'font-semibold')}>
                          <span className="size-2.5 shrink-0 rounded-full" style={{ background: s.color }} />
                          <span className={cx('min-w-0 flex-1 truncate', MONO.includes(dim) && s.key !== '(other)' && 'font-mono')} title={s.label}>{s.label}</span>
                          <span className="shrink-0 text-muted tabular">{pct(s.value, topTotal).toFixed(1)}%</span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
                <RankTable dim={dim} rows={rows} total={topTotal} colorOf={(k) => sliceColor.get(k)} hover={hover} onHover={setHover}
                  selected={sel} onToggle={(k) => toggle(dim, k)} />
              </div>
            )}
          </Card>
        </div>
      </div>
    </>
  )
}

function Kpi({ label, value, sub }: { label: string; value: ReactNode; sub?: ReactNode }) {
  return (
    <div className="min-w-0 rounded-[16px] border bg-surface px-4 py-3.5">
      <div className="truncate text-[13px] text-subtle">{label}</div>
      <div className="mt-1 truncate text-[20px] leading-7 font-semibold tabular">{value}</div>
      {sub && <div className="mt-0.5 truncate text-xs text-muted tabular">{sub}</div>}
    </div>
  )
}

const FACET_LAYOUT_KEY = 'sbp.traffic.facets'
const FACET_N = 5
const FACET_SEARCH = 8

/** One active filter value with a remove button. */
function FilterChip({ k, label, onRemove }: { k: FilterKey; label: string; onRemove: () => void }) {
  return (
    <span className="inline-flex h-7 max-w-[16rem] shrink-0 items-center gap-1 rounded-full bg-accent/12 pr-1 pl-3 text-[13px] text-accent-text">
      <span className="shrink-0 text-subtle">{dimLabel(k)}</span>
      <span className={cx('truncate font-medium', MONO.includes(k) && 'font-mono text-[12.5px]')}>{label}</span>
      <button type="button" aria-label={`× ${dimLabel(k)} ${label}`} onClick={onRemove}
        className="grid size-5 shrink-0 cursor-pointer place-items-center rounded-full outline-none hover:bg-accent/20 focus-visible:ring-2 focus-visible:ring-accent"><IconX size={12} /></button>
    </span>
  )
}

/** A dimension's values: a 100% composition bar, then the rows in `shown` (click toggles a filter). */
function FacetValues({ ns, rows, shown, selected, onToggle, listClass }: {
  ns: string; rows: TopRow[]; shown: TopRow[]; selected: string[]; onToggle: (k: string) => void; listClass?: string
}) {
  const total = rows.reduce((a, x) => a + x.up + x.down, 0)
  const color = (k: string, i: number) => (i < 8 ? entityColor(ns, k) : OTHER_COLOR)
  const rest = rows.slice(8).reduce((a, y) => a + y.up + y.down, 0)
  return (
    <>
      <div className="mx-1 mb-2 flex h-2 shrink-0 overflow-hidden rounded-full bg-overlay" aria-hidden>
        {rows.slice(0, 9).map((x, i) => (
          <div key={x.key} className={cx('h-full transition-[width,opacity] duration-(--dur-3)', selected.length > 0 && !selected.includes(x.key) && 'opacity-30')}
            style={{ width: `${pct(i < 8 ? x.up + x.down : rest, total)}%`, background: color(x.key, i) }} />
        ))}
      </div>
      <ul className={cx('flex flex-col', listClass)}>
        {shown.map((x) => {
          const on = selected.includes(x.key)
          return (
            <li key={x.key}>
              <button type="button" aria-pressed={on} onClick={() => onToggle(x.key)} title={x.label}
                className={cx('flex min-h-8 w-full cursor-pointer items-center gap-2 rounded-[8px] px-1.5 py-1 text-left text-[13px] outline-none transition-colors focus-visible:ring-2 focus-visible:ring-accent',
                  on ? 'bg-accent/12 text-accent-text' : 'hover:bg-overlay')}>
                <span className="grid size-3.5 shrink-0 place-items-center rounded-[4px]" style={{ background: on ? undefined : color(x.key, rows.indexOf(x)) }}>
                  {on && <IconCheck size={13} />}
                </span>
                <span className="min-w-0 flex-1 truncate">{x.label}</span>
                <span className="shrink-0 text-xs tabular text-subtle">{bytes(x.up + x.down)}</span>
                <span className="w-9 shrink-0 text-right text-[11px] text-muted tabular">{pct(x.up + x.down, total).toFixed(0)}%</span>
              </button>
            </li>
          )
        })}
      </ul>
    </>
  )
}

type FacetProps = { name: string; ns: string; rows: TopRow[]; selected: string[]; onToggle: (k: string) => void; busy?: boolean; loading?: boolean }

/** Side column (PC "side" layout): one dimension with its top values inline. */
function Facet({ name, ns, rows, selected, onToggle, busy, loading }: FacetProps) {
  const [open, setOpen] = useState(false)
  const shown = open ? rows : [...rows.slice(0, FACET_N), ...rows.slice(FACET_N).filter((x) => selected.includes(x.key))]
  return (
    <section className={cx('px-2 py-3 transition-opacity first:pt-0 last:pb-0', busy && 'opacity-60')}>
      <div className="mb-2 flex items-baseline justify-between gap-2 px-1">
        <h3 className="text-[13px] font-semibold">{name}</h3>
        <span className="text-xs text-muted tabular">{rows.length > 0 && num(rows.length)}</span>
      </div>
      {loading ? <Skeleton className="h-20" /> : rows.length === 0 ? <div className="px-1 text-xs text-muted">—</div> : (
        <>
          <FacetValues ns={ns} rows={rows} shown={shown} selected={selected} onToggle={onToggle} listClass={open ? 'max-h-[320px] overflow-auto' : undefined} />
          {rows.length > FACET_N && (
            <button type="button" onClick={() => setOpen(!open)} className="mt-1 cursor-pointer px-1.5 text-xs text-accent-text outline-none hover:underline focus-visible:ring-2 focus-visible:ring-accent">
              {open ? tr(TR).showLess : tr(TR).showAll(rows.length)}
            </button>
          )}
        </>
      )}
    </section>
  )
}

/**
 * Filter bar: one dimension as a pill whose values open in a popover. The list is computed under
 * every *other* filter, so it stays put while you pick several.
 */
function FacetPicker({ name, ns, rows, selected, label, onToggle, onClear, busy, loading }: FacetProps & { label: (k: string) => string; onClear: () => void }) {
  const ref = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const t = tr(TR)
  const shown = q ? rows.filter((x) => x.label.toLowerCase().includes(q.toLowerCase())) : rows
  const on = selected.length > 0
  return (
    <span className={cx('inline-flex h-8 shrink-0 items-center rounded-full border text-[13px] transition-colors', on ? 'border-accent/40 bg-accent/12 text-accent-text' : 'bg-base hover:border-hl-high')}>
      <button ref={ref} type="button" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)}
        className={cx('inline-flex h-full max-w-[15rem] cursor-pointer items-center gap-1.5 rounded-full pl-3 outline-none focus-visible:ring-2 focus-visible:ring-accent', on ? 'pr-1' : 'pr-3')}>
        <span className={cx('shrink-0', on ? 'text-subtle' : 'font-medium')}>{name}</span>
        {on ? <span className="truncate font-medium">{label(selected[0])}{selected.length > 1 && ` +${selected.length - 1}`}</span>
          : <span className="text-xs text-muted tabular">{loading ? '…' : num(rows.length)} ▾</span>}
      </button>
      {on && (
        <button type="button" aria-label={t.clearDim(name)} onClick={onClear}
          className="mr-1 grid size-6 shrink-0 cursor-pointer place-items-center rounded-full outline-none hover:bg-accent/20 focus-visible:ring-2 focus-visible:ring-accent"><IconX size={12} /></button>
      )}
      <Popover triggerRef={ref} isOpen={open} onOpenChange={(v) => { setOpen(v); if (!v) setQ('') }} placement="bottom start"
        className={cx('w-[340px] max-w-[calc(100vw-24px)]', popoverCls)}>
        <Dialog aria-label={name} className={cx('flex max-h-[min(440px,70vh)] flex-col p-2 outline-none transition-opacity', busy && 'opacity-60')}>
          <div className="mb-2 flex items-baseline justify-between gap-2 px-1">
            <span className="text-[13px] font-semibold">{name}</span>
            <span className="text-xs text-muted">{t.facetsHint}</span>
          </div>
          {loading ? <Skeleton className="h-24" /> : rows.length === 0 ? <div className="px-1 py-4 text-center text-xs text-muted">{t.empty}</div> : (
            <>
              {rows.length > FACET_SEARCH && (
                <label className="relative mx-1 mb-2 block shrink-0">
                  <IconSearch size={14} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
                  <input autoFocus value={q} onChange={(e) => setQ(e.target.value)} placeholder={t.filter} aria-label={t.filter}
                    className="h-9 w-full rounded-full border bg-base pr-3 pl-8 text-[13px] outline-none focus:border-accent" />
                </label>
              )}
              <FacetValues ns={ns} rows={rows} shown={shown} selected={selected} onToggle={onToggle} listClass="min-h-0 overflow-y-auto overscroll-contain" />
            </>
          )}
        </Dialog>
      </Popover>
    </span>
  )
}

type Slice = { key: string; label: string; value: number; color: string }

/** SVG donut of the ranking's top slices; hover is shared with the legend and the table. */
function Donut({ slices, total, hover, onHover, onSelect, selected, center }: {
  slices: Slice[]; total: number; hover: string | null; onHover: (k: string | null) => void; onSelect: (k: string) => void; selected: string[]; center: string
}) {
  const S = 200, R = S / 2
  const arcs = pie<Slice>().value((d) => d.value).sort(null).padAngle(slices.length > 1 ? 0.012 : 0)(slices)
  const base = arc<PieArcDatum<Slice>>().innerRadius(R * 0.62).outerRadius(R - 6).cornerRadius(3)
  const big = arc<PieArcDatum<Slice>>().innerRadius(R * 0.62).outerRadius(R - 1).cornerRadius(3)
  const h = slices.find((s) => s.key === hover)
  return (
    <svg width={S} height={S} viewBox={`${-R} ${-R} ${S} ${S}`} role="img" aria-label={center} className="shrink-0">
      {arcs.map((a) => {
        const dim = (hover && hover !== a.data.key) || (selected.length > 0 && !selected.includes(a.data.key) && hover !== a.data.key)
        return (
          <path key={a.data.key} d={(hover === a.data.key ? big : base)(a) ?? ''} fill={a.data.color}
            className={cx('transition-opacity duration-(--dur-2)', a.data.key !== '(other)' && 'cursor-pointer')} opacity={dim ? 0.35 : 1}
            onMouseEnter={() => onHover(a.data.key)} onMouseLeave={() => onHover(null)} onClick={() => onSelect(a.data.key)}>
            <title>{`${a.data.label} · ${bytes(a.data.value)} · ${pct(a.data.value, total).toFixed(1)}%`}</title>
          </path>
        )
      })}
      <text textAnchor="middle" y={-14} className="fill-[var(--muted)] text-[11px]">{h ? truncate(h.label, 22) : center}</text>
      <text textAnchor="middle" y={8} className="fill-[var(--text)] text-[17px] font-semibold tabular">{bytes(h ? h.value : total)}</text>
      <text textAnchor="middle" y={26} className="fill-[var(--muted)] text-[11px] tabular">{h ? `${pct(h.value, total).toFixed(1)}%` : '100%'}</text>
    </svg>
  )
}
const truncate = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + '…' : s)

/** Ranked activity: who used how much, in which direction, with how many connections and users. */
function RankTable({ dim, rows, total, colorOf, hover, onHover, selected, onToggle }: {
  dim: string; rows: TopRow[]; total: number; colorOf: (k: string) => string | undefined; hover: string | null
  onHover: (k: string | null) => void; selected: string[]; onToggle: (k: string) => void
}) {
  const t = tr(TR)
  const max = rows.reduce((a, x) => Math.max(a, x.up + x.down), 0)
  return (
    <div className="max-h-[620px] min-w-0 overflow-auto">
      <table className="w-full text-[13px]">
        <thead className="sticky top-0 z-[1] bg-surface text-left text-xs text-subtle">
          <tr className="border-b">
            <th className="hidden w-10 py-2 pl-5 font-medium sm:table-cell">{t.rankCol}</th>
            <th className="py-2 pr-2 pl-4 font-medium sm:pl-2">{dimLabel(dim)}</th>
            <th className="w-[22%] px-2 py-2 font-medium">{t.share}</th>
            <th className="py-2 pr-4 pl-2 text-right font-medium sm:pr-2">{t.down}</th>
            <th className="hidden px-2 py-2 text-right font-medium sm:table-cell">{t.up}</th>
            <th className="hidden px-2 py-2 text-right font-medium sm:table-cell">{t.conns}</th>
            <th className="hidden py-2 pr-5 pl-2 text-right font-medium sm:table-cell">{t.usersCol}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((x, i) => {
            const v = x.up + x.down
            const on = selected.includes(x.key)
            const c = colorOf(x.key)
            return (
              <tr key={x.key} onClick={() => onToggle(x.key)} onMouseEnter={() => onHover(x.key)} onMouseLeave={() => onHover(null)}
                title={t.filterThis} className={cx('cursor-pointer border-b transition-colors last:border-0', on ? 'bg-accent/10' : hover === x.key ? 'bg-hl-low' : 'hover:bg-hl-low')}>
                <td className="hidden py-2 pl-5 text-xs text-muted tabular sm:table-cell">{i + 1}</td>
                <td className="max-w-[160px] py-2 pr-2 pl-4 sm:max-w-[280px] sm:pl-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="size-2 shrink-0 rounded-full" style={{ background: c ?? 'transparent' }} />
                    <span className={cx('truncate', MONO.includes(dim) && 'font-mono text-[12.5px]', on && 'font-semibold text-accent-text')} title={x.label}>{x.label}</span>
                  </div>
                </td>
                <td className="px-2 py-2">
                  <div className="flex items-center gap-2">
                    <div className="flex h-1.5 min-w-12 flex-1 overflow-hidden rounded-full bg-overlay">
                      <div className="h-full bg-[var(--s1)]" style={{ width: `${max ? (x.down / max) * 100 : 0}%` }} />
                      <div className="h-full bg-[var(--s2)]" style={{ width: `${max ? (x.up / max) * 100 : 0}%` }} />
                    </div>
                    <span className="w-11 shrink-0 text-right text-xs text-muted tabular">{pct(v, total).toFixed(1)}%</span>
                  </div>
                </td>
                <td className="py-2 pr-4 pl-2 text-right tabular whitespace-nowrap sm:pr-2">{bytes(x.down)}</td>
                <td className="hidden px-2 py-2 text-right tabular text-subtle sm:table-cell">{bytes(x.up)}</td>
                <td className="hidden px-2 py-2 text-right tabular text-subtle sm:table-cell">{num(x.conns)}</td>
                <td className="hidden py-2 pr-5 pl-2 text-right tabular text-subtle sm:table-cell">{x.users}</td>
              </tr>
            )
          })}
          {rows.length === 0 && <tr><td colSpan={7} className="py-10 text-center text-muted">{t.empty}</td></tr>}
        </tbody>
      </table>
    </div>
  )
}

function stepLabel(s: number) {
  const t = tr(TR)
  return s >= 86400 ? t.day(s / 86400) : s >= 3600 ? t.hour(s / 3600) : t.min(s / 60)
}

const pad2 = (n: number) => String(n).padStart(2, '0')
const md = (d: Date) => `${d.getMonth() + 1}/${d.getDate()} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`
const dur = (sec: number) => {
  const c = tr(C), m = Math.round(sec / 60), d = Math.floor(m / 1440), h = Math.floor((m % 1440) / 60)
  return m >= 1440 ? (h ? c.durDH(d, h) : c.unitD(d)) : m >= 60 ? (m % 60 ? c.durHM(h, m % 60) : c.unitH(h)) : c.unitM(m)
}
function rangeLabel(f: number, t: number) { return `${md(new Date(f * 1000))} – ${md(new Date(t * 1000))}` }
/** unix seconds ↔ the local "YYYY-MM-DDTHH:mm" value of <input type=datetime-local>. */
const toLocal = (ts: number) => { const d = new Date(ts * 1000); return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}T${pad2(d.getHours())}:${pad2(d.getMinutes())}` }
const fromLocal = (v: string) => (v ? Math.floor(new Date(v).getTime() / 1000) : NaN)

function RangePicker({ from, to, onApply, onCancel }: { from: number; to: number; onApply: (f: number, t: number) => void; onCancel: () => void }) {
  const [a, setA] = useState(toLocal(from))
  const [b, setB] = useState(toLocal(to))
  const now = Math.floor(Date.now() / 1000)
  const min = now - KEEP
  const f = fromLocal(a), t = fromLocal(b)
  const L = tr(TR)
  const err = Number.isNaN(f) || Number.isNaN(t) ? L.errFill
    : f < min - 60 ? L.errMin
      : t > now + 60 ? L.errMax
        : f >= t ? L.errOrder : ''
  const day0 = (d = new Date()) => { const x = new Date(d); x.setHours(0, 0, 0, 0); return Math.floor(x.getTime() / 1000) }
  const quick = [
    { label: L.qToday, f: day0(), t: now },
    { label: L.qYesterday, f: day0() - 86400, t: day0() },
    { label: L.qWeek, f: (() => { const d = new Date(); const wd = (d.getDay() + 6) % 7; return day0() - wd * 86400 })(), t: now },
    { label: L.q3d, f: now - 3 * 86400, t: now },
    { label: L.q30d, f: min + 60, t: now },
  ]
  const inputCls = 'h-9 w-full rounded-[10px] border bg-base px-3 text-sm tabular outline-none transition-[border-color,box-shadow] duration-(--dur-1) focus:border-accent focus:ring-2 focus:ring-accent/25'
  return (
    <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); if (!err) onApply(f, t) }}>
      <div className="flex flex-wrap gap-1.5">
        {quick.map((x) => (
          <button key={x.label} type="button" onClick={() => { setA(toLocal(Math.max(x.f, min + 60))); setB(toLocal(x.t)) }}
            className="h-7 cursor-pointer rounded-full bg-overlay px-3 text-[13px] text-subtle outline-none transition-colors duration-(--dur-1) hover:text-text focus-visible:ring-2 focus-visible:ring-accent">{x.label}</button>
        ))}
      </div>
      <label className="flex flex-col gap-1.5 text-[13px] font-medium text-subtle">{L.start}
        <input type="datetime-local" step={60} value={a} min={toLocal(min)} max={toLocal(now)} onChange={(e) => setA(e.target.value)} className={inputCls} />
      </label>
      <label className="flex flex-col gap-1.5 text-[13px] font-medium text-subtle">{L.end}
        <input type="datetime-local" step={60} value={b} min={toLocal(min)} max={toLocal(now)} onChange={(e) => setB(e.target.value)} className={inputCls} />
      </label>
      <div className={cx('min-h-4 text-xs', err ? 'text-bad' : 'text-muted')}>{err || L.spanX(rangeLabel(f, t), dur(t - f))}</div>
      <div className="flex justify-end gap-2">
        <Button size="sm" onPress={onCancel}>{tr(C).cancel}</Button>
        <Button size="sm" variant="primary" type="submit" isDisabled={!!err}>{tr(C).apply}</Button>
      </div>
    </form>
  )
}
