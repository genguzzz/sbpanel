import type { ReactNode } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { api, nodeUsed, type Overview, type SeriesResp, type Node, type ProcInfo, type Template, type User } from '../lib/api'
import { filterDefs } from '../lib/users'
import { bytes, rate, pct, duration, ago, date } from '../lib/fmt'
import { go, href } from '../lib/router'
import { Badge, Card, PageHeader, Progress, Skeleton, Stat, Empty, Button, cx } from '../ui/kit'
import { TimeChart, ShareList, TweenBytes } from '../ui/charts'
import { IconArrowDown, IconArrowUp, IconUsers, IconServer } from '../ui/icons'
import { tr } from '../i18n'
import { O } from '../i18n/overview'
import { NM } from '../i18n/nodedetail'

export function OverviewPage() {
  const ov = useQuery({ queryKey: ['overview'], queryFn: () => api<Overview>('/overview'), refetchInterval: 5000 })
  const from = Math.floor(Date.now() / 1000) - 86400
  const series = useQuery({
    queryKey: ['series', 'overview'],
    queryFn: () => api<SeriesResp>(`/traffic/series?step=300&group=total&from=${from}`),
    refetchInterval: 30000,
    placeholderData: keepPreviousData,
  })
  const o = ov.data
  const s = series.data?.series[0]
  const t = tr(O)
  return (
    <>
      <PageHeader title={t.title} subtitle={t.subtitle} />
      <div className="grid grid-cols-2 gap-3 sm:gap-4 xl:grid-cols-4">
        {o ? (
          <>
            <Stat label={t.today} value={<TweenBytes value={o.today_down + o.today_up} />}
              sub={<Parts items={[<><IconArrowDown size={12} className="inline" /> {bytes(o.today_down)}</>, <><IconArrowUp size={12} className="inline" /> {bytes(o.today_up)}</>]} />} />
            <Stat label={t.month} value={<TweenBytes value={o.month_down + o.month_up} />}
              sub={<Parts items={[<><IconArrowDown size={12} className="inline" /> {bytes(o.month_down)}</>, <><IconArrowUp size={12} className="inline" /> {bytes(o.month_up)}</>]} />} />
            <Stat label={t.rate} value={<><TweenBytes value={o.rate_down + o.rate_up} />/s</>}
              sub={<Parts items={[<><IconArrowDown size={12} className="inline" /> {rate(o.rate_down)}</>, <><IconArrowUp size={12} className="inline" /> {rate(o.rate_up)}</>, t.conns(o.active_conns)]} />} />
            <Stat label={t.onlineUsers} icon={<IconUsers size={14} />} value={<>{o.online_users}<span className="text-[15px] font-normal text-subtle"> / {o.active_users}</span></>}
              sub={<Parts items={[t.deviceIps(o.online_ips), t.nodesOnline(o.nodes_online, o.nodes_total)]} />} />
          </>
        ) : [0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-[112px]" />)}
      </div>

      {o && <Attention o={o} last={s?.points[s.points.length - 1]} />}

      <Card className="mt-4" title={t.chart24} action={<a href={href('traffic')} className="text-[13px] text-accent hover:underline">{t.toTraffic}</a>}>
        {s ? (
          <TimeChart
            step={series.data!.step}
            times={s.points.map((p) => p[0])}
            series={[
              { key: 'down', label: t.down, values: s.points.map((p) => p[2]), color: 'var(--s1)' },
              { key: 'up', label: t.up, values: s.points.map((p) => p[1]), color: 'var(--s2)' },
            ]}
          />
        ) : series.data ? <Empty title={t.noTraffic} hint={t.noTrafficHint} /> : <Skeleton className="h-[240px]" />}
      </Card>

      <div className="mt-4 grid gap-4 lg:grid-cols-3">
        <Card title={t.topUsers}>
          <ShareList rows={(o?.top_users ?? []).map((r) => ({ key: r.key, label: r.label, value: r.up + r.down }))}
            onSelect={(k) => go('users', { id: k })} emptyText={t.noUserTraffic} />
        </Card>
        <Card title={t.topDomains}>
          <ShareList mono rows={(o?.top_domains ?? []).map((r) => ({ key: r.key, label: r.label, value: r.up + r.down }))}
            onSelect={(k) => go('traffic', { domain: k })} emptyText={t.noDomains} />
        </Card>
        <Card title={t.outbounds}>
          <ShareList rows={(o?.top_outbounds ?? []).map((r) => ({ key: r.key, label: r.label, value: r.up + r.down }))} emptyText={t.noOutbounds} />
        </Card>
      </div>

      <div className="mt-6 mb-3 flex items-center justify-between">
        <h2 className="text-[15px] font-semibold">{t.nodes}</h2>
        <a href={href('nodes')} className="text-[13px] text-accent hover:underline">{t.manageNodes}</a>
      </div>
      {o && o.nodes.length === 0 && (
        <Card><Empty title={t.noNodes} hint={t.noNodesHint} action={<Button variant="primary" onPress={() => go('nodes')}>{t.addNode}</Button>} /></Card>
      )}
      <div className="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
        {o?.nodes.map((n) => <NodeCard key={n.id} n={n} />)}
      </div>
      {o?.master && !o.nodes.some((n) => n.master) && (
        <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-1 rounded-[16px] border bg-surface px-5 py-3 text-xs" title={tr(NM).panelHint}>
          <span className="text-[13px] font-medium">{tr(NM).panelProc}</span>
          <div className="min-w-[240px] flex-1"><ProcLine name={tr(NM).sbmaster} p={o.master} color={PROC_COLOR.sbmaster} /></div>
        </div>
      )}
    </>
  )
}

/** Sub-line segments that wrap as whole units instead of breaking inside a number. */
function Parts({ items }: { items: ReactNode[] }) {
  return <span className="flex flex-wrap gap-x-3 gap-y-0.5">{items.map((x, i) => <span key={i} className="whitespace-nowrap">{x}</span>)}</span>
}

/** "What needs my attention?" Only rendered when something does; otherwise a single green line. */
function Attention({ o, last }: { o: Overview; last?: [number, number, number] }) {
  const templates = useQuery({ queryKey: ['templates'], queryFn: () => api<Template[]>('/templates') })
  const users = useQuery({ queryKey: ['users'], queryFn: () => api<User[]>('/users'), refetchInterval: 30000 })
  const en = o.nodes.filter((n) => n.enrolled)
  const offline = en.filter((n) => !n.online)
  const failing = en.filter((n) => n.online && n.apply_error)
  const pending = o.nodes.filter((n) => !n.enrolled)
  const hot = o.nodes.filter((n) => n.traffic_limit > 0 && (n.month_up + n.month_down) / n.traffic_limit >= 0.8)
  const count = (id: string) => (users.data ?? []).filter(filterDefs.find((f) => f.id === id)!.test).length
  const drafts = (templates.data ?? []).filter((t) => t.has_draft).length
  type Item = { tone: 'bad' | 'warn' | 'neutral'; title: string; detail?: string; to: string; cta: string }
  const items: Item[] = []
  const t = tr(O)
  if (offline.length) items.push({ tone: 'bad', title: t.offline(offline.length), detail: offline.map((n) => n.name).join(t.listSep), to: href('nodes'), cta: t.viewNodes })
  if (failing.length) items.push({ tone: 'bad', title: t.failing(failing.length), detail: failing.map((n) => `${n.name}${t.colon}${n.apply_error}`).join(t.errSep), to: href('nodes'), cta: t.viewErrors })
  if (hot.length) items.push({ tone: 'warn', title: t.hot(hot.length), detail: hot.map((n) => `${n.name} ${pct(n.month_up + n.month_down, n.traffic_limit).toFixed(0)}%`).join(t.listSep), to: href('nodes'), cta: t.viewNodes })
  const exp = count('expiring'), q80 = count('quota80'), lim = count('limited')
  if (exp) items.push({ tone: 'warn', title: t.expiring(exp), to: href('users', { f: 'expiring', sort: 'expire' }), cta: t.extend })
  if (q80) items.push({ tone: 'warn', title: t.quota80(q80), to: href('users', { f: 'quota80', sort: 'usage' }), cta: t.view })
  if (lim) items.push({ tone: 'bad', title: t.limited(lim), detail: t.limitedDetail, to: href('users', { f: 'limited' }), cta: t.view })
  if (pending.length) items.push({ tone: 'neutral', title: t.pending(pending.length), to: href('nodes'), cta: t.viewCmd })
  if (drafts) items.push({ tone: 'neutral', title: t.drafts(drafts), to: href('templates'), cta: t.publish })
  if (o.nodes_total === 0) return null
  const dot = { bad: 'bg-bad', warn: 'bg-warn', neutral: 'bg-muted' }
  if (items.length === 0) {
    return (
      <div className="mt-4 flex animate-content-in flex-wrap items-center gap-2 rounded-[16px] border bg-good/6 px-4 py-3 text-[13px]">
        <span className="h-2 w-2 rounded-full bg-good" />
        <span className="font-medium text-good">{t.allGood}</span>
        <span className="text-subtle">· {t.nOnline(o.nodes_online)}</span>
        {last && <span className="text-subtle tabular">· {t.last5} ↓ {bytes(last[2])} ↑ {bytes(last[1])}</span>}
      </div>
    )
  }
  return (
    <Card className="mt-4 animate-content-in" title={<span className="flex items-center gap-2">{t.attention} <Badge tone={items.some((i) => i.tone === 'bad') ? 'bad' : 'warn'}>{items.length}</Badge></span>} pad={false}>
      <ul className="pb-2">
        {items.map((x) => (
          <li key={x.title}>
            <a href={x.to} className="group flex items-center gap-3 px-5 py-2.5 text-[13px] outline-none transition-colors duration-(--dur-1) hover:bg-hl-low focus-visible:bg-hl-low">
              <span className={cx('h-2 w-2 shrink-0 rounded-full', dot[x.tone])} />
              <span className="min-w-0 flex-1">
                <span className="font-medium">{x.title}</span>
                {x.detail && <span className="mt-0.5 block truncate text-xs text-muted">{x.detail}</span>}
              </span>
              <span className="shrink-0 text-accent transition-transform duration-(--dur-1) group-hover:translate-x-0.5">{x.cta} →</span>
            </a>
          </li>
        ))}
      </ul>
    </Card>
  )
}

export function nodeStatus(n: Node): { tone: 'good' | 'warn' | 'bad' | 'neutral'; text: string } {
  const t = tr(O)
  if (!n.enrolled) return { tone: 'neutral', text: t.stPending }
  if (!n.online) return { tone: 'bad', text: t.stOffline }
  if (n.apply_error) return { tone: 'warn', text: t.stError }
  if (n.applied_revision !== n.revision) return { tone: 'warn', text: t.stSync }
  return { tone: 'good', text: t.stOnline }
}

/** CPU % with one decimal below 10 (process usage is usually small). */
export const pctText = (v: number) => (v < 10 ? v.toFixed(1) : v.toFixed(0)) + '%'

/** Each sbpanel process keeps one colour everywhere: bar segments, swatches, chart lines. */
export const PROC_COLOR = { sbnode: 'var(--s2)', sbmaster: 'var(--s7)' } as const

/** Machine usage bar (light) with the sbpanel processes' shares drawn solid inside it, in their colours. */
export function UsageBar({ label, value, total, parts = [], online }: { label: string; value: string; total: number; parts?: { v: number; color: string }[]; online: boolean }) {
  const v = online ? Math.max(0, Math.min(100, total)) : 0
  let left = 0
  const segs = online ? parts.map((p) => { const w = Math.max(0, Math.min(v - left, p.v)); const s = { left, w, color: p.color }; left += w; return s }) : []
  const tint = v >= 95 ? 'bg-bad/45' : v >= 80 ? 'bg-warn/50' : 'bg-accent/30'
  return (
    <div>
      <div className="mb-1 flex justify-between text-xs text-subtle"><span>{label}</span><span className="tabular">{online ? value : '—'}</span></div>
      <div className="relative h-1.5 w-full overflow-hidden rounded-full bg-overlay" role="progressbar" aria-label={label}
        aria-valuenow={Math.round(v)} aria-valuemin={0} aria-valuemax={100} aria-valuetext={value}>
        <div className={cx('absolute inset-y-0 left-0 rounded-full transition-[width,background-color] duration-300', tint)} style={{ width: `${v}%` }} />
        {segs.map((s, i) => <div key={i} className="absolute inset-y-0 transition-[left,width] duration-300" style={{ left: `${s.left}%`, width: `${s.w}%`, background: s.color }} />)}
      </div>
    </div>
  )
}

/** One sbpanel process: swatch matching its bar segment, name, CPU and RSS. */
export function ProcLine({ name, p, color }: { name: string; p?: ProcInfo | null; color: string }) {
  const m = tr(NM)
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="h-2 w-2 shrink-0 rounded-[2px]" style={{ background: color }} aria-hidden />
      <span className="font-mono text-[12px] text-text">{name}</span>
      {p ? <span className="ml-auto flex shrink-0 gap-3 tabular text-subtle">
        <span>{m.cpuOf(pctText(p.cpu_pct ?? 0))}</span><span>{m.rss} {bytes(p.rss ?? 0, 0)}</span>
      </span> : <span className="ml-auto truncate text-muted">{m.noProc}</span>}
    </div>
  )
}

/** Bar segments for the node's sbpanel processes (sbnode, plus sbmaster where the panel runs). */
export function procParts(n: Node) {
  const ps = [{ p: n.proc, color: PROC_COLOR.sbnode }, { p: n.master, color: PROC_COLOR.sbmaster }].filter((x) => !!x.p)
  return {
    cpu: ps.map((x) => ({ v: x.p!.cpu_pct ?? 0, color: x.color })),
    mem: ps.map((x) => ({ v: pct(x.p!.rss ?? 0, n.sys?.mem_total ?? 0), color: x.color })),
  }
}

/** The node's billing-period traffic against its cap: the machine's metered uplink (by the
 * provider's counting) once the node reports it, with what users moved through it beside it. */
export function NodeQuota({ n, detail }: { n: Node; detail?: boolean }) {
  const t = tr(O)
  const m = tr(NM)
  const s = n.server
  const used = nodeUsed(n)
  const users = n.month_up + n.month_down
  return (
    <div>
      <div className="mb-1 flex justify-between gap-2 text-xs text-subtle">
        <span className="truncate" title={s?.tracked ? m.periodX(date(s.period_start), date(s.period_end - 1)) : m.notTracked}>
          {s?.tracked ? `${m.server} · ${m.modes(n.traffic_mode)} · ${m.resetDay(n.reset_day)}` : t.monthReset(n.reset_day)}
        </span>
        <span className="shrink-0 tabular">{bytes(used)}{n.traffic_limit > 0 ? ` / ${bytes(n.traffic_limit, 0)}` : ''}</span>
      </div>
      <Progress value={n.traffic_limit > 0 ? pct(used, n.traffic_limit) : 0} />
      {s?.tracked && (
        <div className="mt-1.5 flex justify-between gap-2 text-xs text-muted tabular">
          <span>{m.inOut(bytes(s.rx), bytes(s.tx))}{detail && s.adj !== 0 ? ` · ${m.corrected((s.adj > 0 ? '+' : '−') + bytes(Math.abs(s.adj)))}` : ''}</span>
          <span>{m.userTraffic(bytes(users))}</span>
        </div>
      )}
      {detail && s?.tracked && <div className="mt-1 text-xs text-muted tabular">{m.periodX(date(s.period_start), date(s.period_end - 1))}</div>}
    </div>
  )
}

export function NodeCard({ n, actions }: { n: Node; actions?: React.ReactNode }) {
  const st = nodeStatus(n)
  const cpu = n.sys?.cpu_pct ?? 0
  const memP = pct(n.sys?.mem_used ?? 0, n.sys?.mem_total ?? 0)
  const t = tr(O)
  const m = tr(NM)
  const pr = procParts(n)
  const open = href('nodes', { id: n.id })
  // The whole card opens the detail drawer; buttons and links inside keep their own action.
  const onCard = (e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest('a,button,[role=button],[role=menu],input')) return
    if (window.getSelection()?.toString()) return
    location.hash = open
  }
  return (
    <section onClick={onCard} className="group/card cursor-pointer rounded-[16px] border bg-surface p-5 transition-[border-color,box-shadow] duration-(--dur-1) hover:border-hl-high hover:shadow-[0_2px_10px_rgb(0_0_0/0.05)]">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <IconServer size={16} className="text-subtle" />
            <a href={open} className="truncate rounded-[4px] font-semibold outline-none hover:underline focus-visible:ring-2 focus-visible:ring-accent" aria-label={`${n.name} · ${m.details}`}>{n.name}</a>
            <Badge tone={st.tone} dot>{st.text}</Badge>
          </div>
          <div className="mt-1 truncate text-xs text-muted">
            {n.address || '—'}{n.info?.singbox ? ` · sing-box ${n.info.singbox}` : ''}{n.online ? ` · ${t.uptime(duration(n.sys?.uptime ?? 0))}` : n.last_seen ? ` · ${ago(n.last_seen)}` : ''}
          </div>
        </div>
        {actions}
      </div>
      {n.apply_error && <div className="mt-3 rounded-[10px] bg-warn/10 px-3 py-2 font-mono text-xs break-all text-warn">{n.apply_error}</div>}
      <div className="mt-4 grid grid-cols-2 gap-x-5 gap-y-3 text-[13px]">
        <Metric label={t.rates} value={n.online ? `${rate(n.rate_down)} / ${rate(n.rate_up)}` : '—'} />
        <Metric label={t.connsUsers} value={n.online ? `${n.active_conns} / ${n.online_users}` : '—'} />
        <UsageBar label="CPU" online={n.online} total={cpu} parts={pr.cpu} value={cpu.toFixed(0) + '%'} />
        <UsageBar label={t.memory} online={n.online} total={memP} parts={pr.mem}
          value={`${bytes(n.sys?.mem_used ?? 0, 0)} / ${bytes(n.sys?.mem_total ?? 0, 0)}`} />
      </div>
      {n.online && (
        <div className="mt-3 flex flex-col gap-1 rounded-[10px] bg-overlay/70 px-3 py-2 text-xs" title={m.procShare}>
          <ProcLine name={m.sbnode} p={n.proc} color={PROC_COLOR.sbnode} />
          {n.master && <ProcLine name={m.sbmaster} p={n.master} color={PROC_COLOR.sbmaster} />}
        </div>
      )}
      <div className="mt-4 border-t pt-3">
        <NodeQuota n={n} />
        <div className={cx('mt-2 flex justify-between text-xs text-muted tabular')}>
          <span>{t.todayX(bytes(n.today_down + n.today_up))}</span>
          <span>{t.inboundsN(n.inbounds)} · r{n.applied_revision}{n.applied_revision !== n.revision ? `→${n.revision}` : ''}{n.spool_backlog > 0 ? ` · ${t.backlog(n.spool_backlog)}` : ''}</span>
        </div>
      </div>
    </section>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-subtle">{label}</div>
      <div className="mt-0.5 tabular">{value}</div>
    </div>
  )
}
