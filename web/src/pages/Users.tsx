import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import { Dialog, DialogTrigger, Popover } from 'react-aria-components'
import { api, type User, type TopRow, type SeriesResp, type Group, type SubInfo, type UserLimit, type InboundUse, type Limit, type Node } from '../lib/api'
import { D as LD } from '../i18n/limits'
import { bytes, date, dateTime, parseSize, ago, expiresIn, GB, DAY } from '../lib/fmt'
import { useRoute, go, replace, withParams, href } from '../lib/router'
import { httpUrl } from '../lib/url'
import { flashRows, leaveRows, settleRows, useAction, useApplyTracker, useRowFx, useSubUrl, useUserBatch, useUserPatch } from '../lib/actions'
import { filterDefs, sortDefs, matchQuery, usedOf, usedPct, lastSeenOf, USERNAME_RE, type FilterId, type SortId } from '../lib/users'
import {
  ActionMenu, Badge, Busy, Button, Card, CheckList, Checkbox, Chip, Confirm, CopyButton, CopyIcon, CopyText, Drawer, Empty, Field,
  IconButton, ModalDialog, PageHeader, Presence, Progress, RelTime, Segmented, Select, Skeleton, Switch, Tabs, copyText,
  cx, popoverCls, useCopy, useToast, type MenuEntry,
} from '../ui/kit'
import { ShareList, TimeChart } from '../ui/charts'
import type { ProfileMeta } from '../lib/routing'
import { IconPlus, IconSearch, IconX, IconArrowDown, IconEdit } from '../ui/icons'
import { tr, useT } from '../i18n'
import { D } from '../i18n/users'

const statusInfo: Record<string, { tone: 'good' | 'warn' | 'bad' | 'neutral'; text: string }> = {
  active: { tone: 'good', get text() { return tr(D).active } },
  limited: { tone: 'warn', get text() { return tr(D).limited } },
  expired: { tone: 'bad', get text() { return tr(D).expired } },
}

// reset_strategy: none | day | week | month_rolling | month (= month:1) | month:N | node:<id>
type ResetMode = 'none' | 'day' | 'week' | 'month_rolling' | 'monthday' | 'node'
const resetModes: { id: ResetMode; readonly label: string }[] = [
  { id: 'none', get label() { return tr(D).rNone } }, { id: 'day', get label() { return tr(D).rDay } }, { id: 'week', get label() { return tr(D).rWeek } },
  { id: 'month_rolling', get label() { return tr(D).rRolling } }, { id: 'monthday', get label() { return tr(D).rMonthDay } }, { id: 'node', get label() { return tr(D).rNode } },
]
const monthDayOf = (s: string) => (s === 'month' ? 1 : s.startsWith('month:') ? Number(s.slice(6)) : 0)
const resetModeOf = (s: string): ResetMode => (s.startsWith('node:') ? 'node' : monthDayOf(s) ? 'monthday' : ((s || 'none') as ResetMode))

/** Human text for a user's reset strategy. */
function resetText(s: string, nodes: Node[]): string {
  const t = tr(D)
  if (s.startsWith('node:')) {
    const n = nodes.find((x) => x.id === s.slice(5))
    return n ? t.rNodeOf(n.name, n.reset_day || 1) : t.rNodeGone
  }
  const d = monthDayOf(s)
  return d ? t.rDayOf(d) : resetModes.find((m) => m.id === (s || 'none'))?.label ?? s
}

const useNodes = () => useQuery({ queryKey: ['nodes'], queryFn: () => api<Node[]>('/nodes'), staleTime: 30_000 })

/** Usage reset: a cycle, plus the day of the month or the node it follows. */
function ResetPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const t = useT(D)
  const nodes = (useNodes().data ?? []).filter((n) => n.enrolled)
  const mode = resetModeOf(value)
  const [day, setDay] = useState(String(monthDayOf(value) || new Date().getDate()))
  const dayN = Number(day)
  const dayErr = !Number.isInteger(dayN) || dayN < 1 || dayN > 31
  const pick = (m: ResetMode) => {
    if (m === 'monthday') onChange(`month:${dayErr ? 1 : dayN}`)
    else if (m === 'node') onChange(nodes[0] ? `node:${nodes[0].id}` : 'month:1')
    else onChange(m)
  }
  return (
    <div className="flex flex-col gap-2">
      <Select label={t.resetLabel} value={mode} onChange={pick} options={resetModes.filter((m) => m.id !== 'node' || nodes.length > 0)} />
      {mode === 'monthday' && (
        <Field label={t.rDayField} hideLabel value={day} inputMode="numeric" description={t.rDayHint} isInvalid={dayErr} errorMessage={t.rDayHint}
          onChange={(v) => { setDay(v); const n = Number(v); if (Number.isInteger(n) && n >= 1 && n <= 31) onChange(`month:${n}`) }} />
      )}
      {mode === 'node' && (
        <Select label={t.rNodeField} hideLabel value={value.slice(5)} onChange={(id) => onChange(`node:${id}`)} description={t.rNodeHint}
          options={nodes.map((n) => ({ id: n.id, label: t.rNodeOf(n.name, n.reset_day || 1) }))} />
      )}
    </div>
  )
}

const mbps = (bps: number) => (bps > 0 ? +(bps * 8 / 1e6).toFixed(2) + ' Mbps' : tr(D).unlimited)
const IconQr = (p: { size?: number }) => <svg width={p.size ?? 16} height={p.size ?? 16} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="1.5" /><rect x="14" y="3" width="7" height="7" rx="1.5" /><rect x="3" y="14" width="7" height="7" rx="1.5" /><path d="M14 14h3v3M21 14v.01M14 21h3M21 17v4h-1" /></svg>
const IconChevron = ({ dir, size = 16 }: { dir: 'up' | 'down'; size?: number }) => <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={dir === 'up' ? 'm6 15 6-6 6 6' : 'm6 9 6 6 6-6'} /></svg>

function UserStatus({ u }: { u: User }) {
  if (!u.enabled) return <Badge tone="neutral" dot>{tr(D).disabled}</Badge>
  const s = statusInfo[u.status] ?? { tone: 'neutral' as const, text: u.status }
  return <Badge tone={s.tone} dot>{s.text}</Badge>
}

function Expiry({ u }: { u: User }) {
  const left = u.expire_at ? u.expire_at - Date.now() / 1000 : Infinity
  return <RelTime ts={u.expire_at} text={expiresIn(u.expire_at)} className={cx('tabular whitespace-nowrap', left <= 0 ? 'text-bad' : left <= 7 * DAY ? 'text-warn' : '')} />
}

function Usage({ u }: { u: User }) {
  const used = usedOf(u)
  return (
    <div className="min-w-[150px]">
      <div className="mb-1 flex justify-between gap-2 text-xs tabular whitespace-nowrap">
        <span>{bytes(used)} <span className="text-muted">/ {u.traffic_limit ? bytes(u.traffic_limit, 0) : tr(D).unlimited}</span></span>
        {u.traffic_limit > 0 && <span className={cx(usedPct(u) >= 95 ? 'text-bad' : usedPct(u) >= 80 ? 'text-warn' : 'text-muted')}>{usedPct(u).toFixed(0)}%</span>}
      </div>
      <Progress value={u.traffic_limit ? usedPct(u) : 0} />
    </div>
  )
}

function Online({ u }: { u: User }) {
  if (u.online_ips <= 0) return <span className="text-muted">—</span>
  return <span className="inline-flex items-center gap-1.5 whitespace-nowrap tabular"><span className="h-2 w-2 rounded-full bg-good" />{u.online_ips}{u.device_limit ? `/${u.device_limit}` : ''}</span>
}

function LastSeen({ u }: { u: User }) {
  if (u.online_ips > 0) return <span className="text-good">{tr(D).online}</span>
  const ts = lastSeenOf(u)
  if (ts) return <RelTime ts={ts} text={ago(ts)} className="text-subtle" />
  return u.sub_last_at ? <RelTime ts={u.sub_last_at} text={tr(D).subAgo(ago(u.sub_last_at))} className="text-muted" /> : <span className="text-muted">—</span>
}

/** Params straight from the URL, so debounced writes never use a stale closure. */
const liveParams = () => new URLSearchParams(location.hash.split('?')[1] ?? '')
const setP = (patch: Record<string, string | number | undefined>) => replace('users', withParams(liveParams(), patch))

export function UsersPage() {
  const t = useT(D)
  const { params } = useRoute()
  const qc = useQueryClient()
  const toast = useToast()
  const subUrl = useSubUrl()
  const run = useUserBatch()
  const fx = useRowFx()
  const users = useQuery({ queryKey: ['users'], queryFn: () => api<User[]>('/users'), refetchInterval: 10000 })
  const groups = useQuery({ queryKey: ['groups'], queryFn: () => api<Group[]>('/groups') })
  const gname = useMemo(() => new Map((groups.data ?? []).map((g) => [g.id, g.name])), [groups.data])

  const openId = Number(params.get('id')) || 0
  const legacy = params.get('filter') === 'problem' ? 'limited,expired,disabled' : params.get('filter') === 'online' ? 'online' : ''
  const fParam = params.get('f') ?? legacy
  const active = useMemo(() => new Set(fParam.split(',').filter(Boolean) as FilterId[]), [fParam])
  const group = params.get('g') ?? 'all'
  const sort = (params.get('sort') as SortId) || 'created'
  const sd = sortDefs.find((s) => s.id === sort) ?? sortDefs[0]
  const dir = (params.get('dir') as 'asc' | 'desc') || sd.defDir
  const qParam = params.get('q') ?? ''
  const [q, setQ] = useState(qParam)
  useEffect(() => { setQ((x) => (x === qParam ? x : qParam)) }, [qParam])
  useEffect(() => {
    if (q === (liveParams().get('q') ?? '')) return
    const t = setTimeout(() => setP({ q, page: undefined }), 150)
    return () => clearTimeout(t)
  }, [q])

  const all = useMemo(() => users.data ?? [], [users.data])
  const base = useMemo(() => all.filter((u) => matchQuery(u, qParam) && (group === 'all' || u.group_ids.includes(Number(group)))), [all, qParam, group])
  const counts = useMemo(() => Object.fromEntries(filterDefs.map((f) => [f.id, base.filter(f.test).length])) as Record<FilterId, number>, [base])
  const rows = useMemo(() => {
    const tests = filterDefs.filter((f) => active.has(f.id))
    const r = tests.length ? base.filter((u) => tests.some((t) => t.test(u))) : [...base]
    const k = sd.key
    r.sort((a, b) => { const x = k(a), y = k(b); const c = x < y ? -1 : x > y ? 1 : 0; return dir === 'asc' ? c : -c })
    return r
  }, [base, active, sd, dir])
  const PAGE = 100
  const paged = rows.length > 200
  const pages = Math.ceil(rows.length / PAGE)
  const page = Math.min(pages, Math.max(1, Number(params.get('page')) || 1))
  const visible = paged ? rows.slice((page - 1) * PAGE, page * PAGE) : rows

  // ---- selection & keyboard ----
  const [sel, setSel] = useState<Set<number>>(new Set())
  const anchor = useRef<number | null>(null)
  const [focus, setFocus] = useState(-1)
  useEffect(() => { // drop ids that no longer exist
    setSel((s) => { const ids = new Set(all.map((u) => u.id)); const n = new Set([...s].filter((i) => ids.has(i))); return n.size === s.size ? s : n })
  }, [all])
  const toggle = useCallback((id: number, idx: number, shift?: boolean) => {
    setSel((s) => {
      const n = new Set(s)
      if (shift && anchor.current !== null) {
        const [a, b] = [Math.min(anchor.current, idx), Math.max(anchor.current, idx)]
        const on = !s.has(id)
        visible.slice(a, b + 1).forEach((u) => (on ? n.add(u.id) : n.delete(u.id)))
      } else if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
    anchor.current = idx
  }, [visible])
  const allSel = rows.length > 0 && rows.every((u) => sel.has(u.id))
  const someSel = !allSel && rows.some((u) => sel.has(u.id))
  const focusRow = (i: number) => {
    const el = [...document.querySelectorAll<HTMLElement>(`[data-row="${i}"]`)].find((x) => x.offsetParent !== null)
    el?.focus()
    el?.scrollIntoView({ block: 'nearest' })
  }
  const openUser = (id: number, tab?: string) => go('users', withParams(liveParams(), { id, tab }))
  const closeUser = () => go('users', withParams(liveParams(), { id: undefined, tab: undefined }))
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (t?.closest?.('input, textarea, select, [contenteditable=true]')) return
      const dialogs = document.querySelectorAll('[role=dialog]').length
      if (openId) {
        if (dialogs > 1 || e.metaKey || e.ctrlKey || e.altKey) return
        if (e.key === 'j' || e.key === 'k') {
          e.preventDefault()
          const i = rows.findIndex((u) => u.id === openId)
          const n = rows[i + (e.key === 'j' ? 1 : -1)]
          if (n) setP({ id: n.id })
        }
        return
      }
      if (dialogs > 0) return
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'a') { e.preventDefault(); setSel(new Set(rows.map((u) => u.id))); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === 'j' || e.key === 'k') {
        e.preventDefault()
        const n = Math.max(0, Math.min(visible.length - 1, focus + (e.key === 'j' ? 1 : -1)))
        setFocus(n); focusRow(n)
      } else if (e.key === 'x' && focus >= 0 && visible[focus]) {
        e.preventDefault(); toggle(visible[focus].id, focus)
      } else if (e.key === 'Escape' && sel.size) setSel(new Set())
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [openId, rows, visible, focus, sel.size, toggle])

  // ---- actions ----
  const [creating, setCreating] = useState(() => params.get('new') === '1')
  useEffect(() => { if (params.get('new') === '1') { setCreating(true); setP({ new: undefined }) } }, [params])
  const [bulk, setBulk] = useState(false)
  const [deleting, setDeleting] = useState<number[] | null>(null)
  const [prompt, setPrompt] = useState<{ kind: 'days' | 'gb'; ids: number[] } | null>(null)
  const [groupsFor, setGroupsFor] = useState<number[] | null>(null)
  const [limitsFor, setLimitsFor] = useState<number[] | null>(null)
  const byId = useMemo(() => new Map(all.map((u) => [u.id, u])), [all])
  const who = (ids: number[]) => (ids.length === 1 ? byId.get(ids[0])?.username ?? t.user : t.nUsers(ids.length))

  const doDelete = async (ids: number[]) => {
    leaveRows(ids)
    try {
      if (ids.length === 1) await api(`/users/${ids[0]}`, { method: 'DELETE' })
      else await api('/users/batch', { method: 'POST', body: { ids, action: 'delete' } })
      setTimeout(() => { qc.invalidateQueries({ queryKey: ['users'] }); qc.invalidateQueries({ queryKey: ['groups'] }); settleRows(ids) }, 180)
      setSel(new Set())
      if (ids.includes(openId)) closeUser()
      toast('good', t.deleted(who(ids)))
    } catch (e) { settleRows(ids); toast('bad', (e as Error).message) }
  }
  const act = (ids: number[], k: string) => {
    const u = byId.get(ids[0])
    switch (k) {
      case 'copy': copyText(ids.map((i) => subUrl(byId.get(i)!)).join('\n')).then(() => toast('good', ids.length > 1 ? t.copiedN(ids.length) : t.copiedOne), () => toast('bad', t.copyFailed)); break
      case 'qr': openUser(ids[0], 'sub'); break
      case 'open': openUser(ids[0]); break
      case 'd7': case 'd30': case 'd90': case 'd365': { const n = Number(k.slice(1)); run(ids, 'extend_days', n, t.extended(who(ids), n)).catch(() => {}); break }
      case 'g10': case 'g50': case 'g100': case 'g500': { const n = Number(k.slice(1)); run(ids, 'add_traffic', n * GB, t.added(who(ids), n)).catch(() => {}); break }
      case 'dcustom': setPrompt({ kind: 'days', ids }); break
      case 'gcustom': setPrompt({ kind: 'gb', ids }); break
      case 'reset': run(ids, 'reset_usage', null, t.resetDone(who(ids))).catch(() => {}); break
      case 'enable': run(ids, 'enable', null, t.enabledX(who(ids))).catch(() => {}); break
      case 'disable': run(ids, 'disable', null, t.disabledX(who(ids))).catch(() => {}); break
      case 'toggle': if (u) act(ids, u.enabled ? 'disable' : 'enable'); break
      case 'kick': api(`/users/${ids[0]}/kick`, { method: 'POST' }).then(() => toast('good', t.kicked(who(ids))), (e) => toast('bad', (e as Error).message)); break
      case 'groups': setGroupsFor(ids); break
      case 'limits': setLimitsFor(ids); break
      case 'delete': setDeleting(ids); break
    }
  }
  const rowMenu = (u: User): MenuEntry[] => [
    { id: 'copy', label: t.copySub }, { id: 'qr', label: t.subQr }, 'sep',
    { id: 'd30', label: t.extend30 }, { id: 'g100', label: t.traffic100 }, { id: 'reset', label: t.resetUsage },
    { id: 'toggle', label: u.enabled ? t.disable : t.enable }, { id: 'kick', label: t.kick, isDisabled: u.online_ips === 0 }, 'sep',
    { id: 'delete', label: t.delete, danger: true },
  ]
  const clearFilters = () => { setQ(''); setP({ q: undefined, f: undefined, filter: undefined, g: undefined, page: undefined }) }
  const toggleChip = (id: FilterId) => {
    const n = new Set(active)
    if (n.has(id)) n.delete(id); else n.add(id)
    setP({ f: [...n].join(',') || undefined, filter: undefined, page: undefined })
  }
  const sortBy = (id: SortId) => {
    const d = sortDefs.find((s) => s.id === id)!
    const nd = id === sort ? (dir === 'asc' ? 'desc' : 'asc') : d.defDir
    setP({ sort: id === 'created' && nd === 'desc' ? undefined : id, dir: nd === d.defDir ? undefined : nd })
  }
  const Th = ({ id, children, className }: { id: SortId; children: ReactNode; className?: string }) => (
    <th className={cx('px-3 py-2 font-medium', className)} aria-sort={sort === id ? (dir === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button type="button" onClick={() => sortBy(id)} className={cx('inline-flex cursor-pointer items-center gap-1 rounded outline-none hover:text-text focus-visible:ring-2 focus-visible:ring-accent', sort === id && 'text-text')}>
        {children}
        <span className={cx('transition-[opacity,transform] duration-(--dur-1)', sort === id ? 'opacity-100' : 'opacity-0', dir === 'asc' && sort === id && 'rotate-180')}><IconArrowDown size={12} /></span>
      </button>
    </th>
  )
  const selIds = [...sel]

  return (
    <>
      <PageHeader title={t.title} subtitle={t.subtitle}
        actions={<>
          <Button onPress={() => setBulk(true)}>{t.bulkCreate}</Button>
          <Button variant="primary" onPress={() => setCreating(true)}><IconPlus size={16} />{t.newUser}</Button>
        </>} />
      <div className="mb-3 flex flex-wrap items-center gap-2 sm:gap-3">
        <label className="relative w-full sm:w-72 sm:flex-none">
          <IconSearch size={14} className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
          <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t.searchPh} aria-label={t.searchLabel}
            onKeyDown={(e) => { if (e.key === 'Escape' && q) { e.stopPropagation(); setQ('') } }}
            className="h-9 w-full rounded-full border bg-surface pr-8 pl-9 text-sm outline-none transition-[border-color,box-shadow] duration-(--dur-1) focus:border-accent focus:ring-2 focus:ring-accent/25 [&::-webkit-search-cancel-button]:hidden" />
          {q ? <button type="button" aria-label={t.clearSearch} onClick={() => setQ('')} className="absolute top-1/2 right-2 grid h-6 w-6 -translate-y-1/2 cursor-pointer place-items-center rounded-full text-muted hover:bg-overlay hover:text-text"><IconX size={12} /></button>
            : <kbd className="pointer-events-none absolute top-1/2 right-3 hidden -translate-y-1/2 rounded bg-overlay px-1.5 text-[11px] text-muted sm:block">/</kbd>}
        </label>
        {(groups.data?.length ?? 0) > 1 && (
          <Select hideLabel label={t.group} className="w-40" value={group} onChange={(v) => setP({ g: v === 'all' ? undefined : v, page: undefined })}
            options={[{ id: 'all', label: t.allGroups }, ...(groups.data ?? []).map((g) => ({ id: String(g.id), label: g.name }))]} />
        )}
        <Select hideLabel label={t.sort} className="w-36 lg:hidden" value={sort} onChange={(v) => sortBy(v as SortId)}
          options={sortDefs.map((s) => ({ id: s.id, label: s.label }))} />
        <span className="ml-auto text-[13px] text-subtle tabular">{t.count(rows.length, all.length)}</span>
      </div>
      <div className="no-scrollbar -mx-4 mb-4 flex gap-1.5 overflow-x-auto px-4 lg:mx-0 lg:flex-wrap lg:px-0" role="group" aria-label={t.filters}>
        <Chip active={active.size === 0} onPress={() => setP({ f: undefined, filter: undefined, page: undefined })} count={base.length}>{t.all}</Chip>
        {filterDefs.map((f) => <Chip key={f.id} active={active.has(f.id)} onPress={() => toggleChip(f.id)} count={counts[f.id]} tone={f.tone}>{f.label}</Chip>)}
      </div>

      <Card pad={false}>
        {users.isPending && !users.data ? <div className="p-5"><Skeleton className="h-40" /></div> : rows.length === 0 ? (
          all.length ? (
            <Empty title={t.noMatch} hint={t.noMatchHint} action={<Button onPress={clearFilters}>{t.clearFilters}</Button>} />
          ) : (
            <Empty title={t.noUsers} hint={t.noUsersHint}
              action={<>
                <Button variant="primary" onPress={() => setCreating(true)}><IconPlus size={16} />{t.newUser}</Button>
                <Button onPress={() => setBulk(true)}>{t.bulkCreate}</Button>
                <Button onPress={() => go('settings', { tab: 'import' })}>{t.migrate}</Button>
              </>} />
          )
        ) : (
          <>
            {/* desktop table */}
            <div className="hidden overflow-x-auto lg:block">
              <table className="w-full text-[13px]">
                <thead className="text-left text-xs whitespace-nowrap text-subtle">
                  <tr className="border-b">
                    <th className="w-10 py-2 pr-0 pl-4"><Checkbox label={t.selectAllFiltered} isSelected={allSel} isIndeterminate={someSel}
                      onChange={() => setSel(allSel ? new Set() : new Set(rows.map((u) => u.id)))} /></th>
                    <Th id="name" className="pl-2">{t.colUser}</Th>
                    <th className="px-3 py-2 font-medium">{t.colStatus}</th>
                    <Th id="usage" className="w-[220px]">{t.colUsage}</Th>
                    <Th id="expire">{t.colExpire}</Th>
                    <th className="px-3 py-2 font-medium">{t.colOnline}</th>
                    <Th id="seen" className="hidden xl:table-cell">{t.colSeen}</Th>
                    <th className="hidden px-3 py-2 font-medium 2xl:table-cell">{t.colGroups}</th>
                    <th className="w-[120px] px-3 py-2"><span className="sr-only">{t.colActions}</span></th>
                  </tr>
                </thead>
                <tbody>
                  {visible.map((u, i) => (
                    <tr key={u.id} data-row={i} tabIndex={0} aria-selected={sel.has(u.id)}
                      onClick={(e) => { if ((e.target as HTMLElement).closest('button, a, label, [role=checkbox]')) return; openUser(u.id) }}
                      onFocus={() => setFocus(i)}
                      onKeyDown={(e) => { if (e.key === 'Enter' && e.target === e.currentTarget) openUser(u.id) }}
                      className={cx('group cursor-pointer border-b outline-none transition-colors duration-(--dur-1) last:border-0 hover:bg-hl-low focus-visible:bg-hl-low focus-visible:shadow-[inset_2px_0_0_var(--accent)]',
                        sel.has(u.id) && 'bg-accent/6', openId === u.id && 'bg-hl-low', fx(u.id))}>
                      <td className="py-2.5 pr-0 pl-4" onMouseDown={(e) => e.shiftKey && e.preventDefault()} onClick={(e) => { e.stopPropagation(); toggle(u.id, i, e.shiftKey) }}>
                        <Checkbox label={t.selectX(u.username)} isSelected={sel.has(u.id)} onChange={() => {}} className="pointer-events-none" />
                      </td>
                      <td className="py-2.5 pr-3 pl-2"><div className="font-medium">{u.username}</div>{u.note && <div className="max-w-[200px] truncate text-xs text-muted">{u.note}</div>}</td>
                      <td className="px-3 py-2.5"><UserStatus u={u} /></td>
                      <td className="px-3 py-2.5"><Usage u={u} /></td>
                      <td className="px-3 py-2.5"><Expiry u={u} /></td>
                      <td className="px-3 py-2.5"><Online u={u} /></td>
                      <td className="hidden px-3 py-2.5 whitespace-nowrap xl:table-cell"><LastSeen u={u} /></td>
                      <td className="hidden px-3 py-2.5 2xl:table-cell">
                        <div className="flex max-w-[200px] flex-wrap gap-1">{u.group_ids.map((g) => <Badge key={g}>{gname.get(g) ?? g}</Badge>)}{u.group_ids.length === 0 && <span className="text-xs text-bad">{t.noGroup}</span>}</div>
                      </td>
                      <td className="px-3 py-1.5 text-right">
                        <div className="flex items-center justify-end opacity-0 transition-opacity duration-(--dur-1) group-focus-within:opacity-100 group-hover:opacity-100 has-[[aria-expanded=true]]:opacity-100 [@media(hover:none)]:opacity-100">
                          <RowCopy url={subUrl(u)} />
                          <QrPop u={u} />
                          <ActionMenu label={t.moreActions(u.username)} items={rowMenu(u)} onAction={(k) => act([u.id], k)} />
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {/* mobile cards */}
            <ul className="lg:hidden">
              <li className="flex items-center justify-between border-b px-4 py-2 text-xs text-subtle">
                <Checkbox label={t.selectAllFiltered} isSelected={allSel} isIndeterminate={someSel} onChange={() => setSel(allSel ? new Set() : new Set(rows.map((u) => u.id)))}>{t.selectAll}</Checkbox>
              </li>
              {visible.map((u, i) => (
                <li key={u.id} data-row={i} tabIndex={0} onFocus={() => setFocus(i)}
                  onKeyDown={(e) => { if (e.key === 'Enter' && e.target === e.currentTarget) openUser(u.id) }}
                  className={cx('flex items-start gap-3 border-b px-4 py-3 outline-none last:border-0 focus-visible:bg-hl-low', sel.has(u.id) && 'bg-accent/6', fx(u.id))}>
                  <div className="pt-0.5" onMouseDown={(e) => e.shiftKey && e.preventDefault()} onClick={(e) => { e.stopPropagation(); toggle(u.id, i, e.shiftKey) }}>
                    <Checkbox label={t.selectX(u.username)} isSelected={sel.has(u.id)} onChange={() => {}} className="pointer-events-none" />
                  </div>
                  <button type="button" onClick={() => openUser(u.id)} className="min-w-0 flex-1 cursor-pointer text-left outline-none">
                    <div className="flex items-center gap-2"><span className="truncate font-medium">{u.username}</span><UserStatus u={u} />{u.online_ips > 0 && <Online u={u} />}</div>
                    {u.note && <div className="truncate text-xs text-muted">{u.note}</div>}
                    <div className="mt-2"><Usage u={u} /></div>
                    <div className="mt-1.5 flex justify-between text-xs text-subtle"><span>{t.expiresLabel} <Expiry u={u} /></span><LastSeen u={u} /></div>
                  </button>
                  <ActionMenu label={t.actionsFor(u.username)} items={rowMenu(u)} onAction={(k) => act([u.id], k)} />
                </li>
              ))}
            </ul>
            {paged && (
              <div className="flex items-center justify-between gap-3 border-t px-4 py-3 text-[13px] text-subtle">
                <span className="tabular">{t.range((page - 1) * PAGE + 1, Math.min(rows.length, page * PAGE), rows.length)}</span>
                <div className="flex gap-2">
                  <Button size="sm" isDisabled={page <= 1} onPress={() => setP({ page: page - 1 })}>{t.prev}</Button>
                  <Button size="sm" isDisabled={page >= pages} onPress={() => setP({ page: page + 1 })}>{t.next}</Button>
                </div>
              </div>
            )}
          </>
        )}
      </Card>

      <Presence value={sel.size > 0 ? selIds : null}>
        {(ids, open) => <BatchBar ids={open ? selIds : ids} open={open} onClear={() => setSel(new Set())} onAction={(k) => act(open ? selIds : ids, k)} />}
      </Presence>

      <Presence value={creating || null}>
        {(_, open) => <UserForm isOpen={open} groups={groups.data ?? []} users={all} onClose={() => setCreating(false)} onOpen={(id) => { setCreating(false); openUser(id) }} />}
      </Presence>
      <Presence value={bulk || null}>
        {(_, open) => <BulkCreate isOpen={open} groups={groups.data ?? []} onClose={() => setBulk(false)} />}
      </Presence>
      <Presence value={openId || null}>
        {(id, open) => <UserDrawer id={id} isOpen={open} groups={groups.data ?? []} ids={rows.map((u) => u.id)}
          tab={(params.get('tab') as DrawerTab) || 'overview'} onTab={(t) => setP({ tab: t === 'overview' ? undefined : t })}
          onNav={(n) => setP({ id: n })} onClose={closeUser} onAct={(k) => act([id], k)} />}
      </Presence>
      <Confirm isOpen={!!deleting} onOpenChange={(v) => !v && setDeleting(null)} danger title={deleting && deleting.length > 1 ? t.deleteN(deleting.length) : t.deleteUser}
        confirmLabel={deleting && deleting.length > 1 ? t.deleteN(deleting.length) : t.delete}
        requireText={deleting && deleting.length > 1 ? String(deleting.length) : undefined}
        onConfirm={() => deleting && doDelete(deleting)}
        body={deleting && deleting.length > 1
          ? t.deleteManyBody(deleting.length)
          : t.deleteOneBody(deleting ? who(deleting) : '')} />
      <Presence value={prompt}>
        {(p, open) => <AmountPrompt kind={p.kind} count={p.ids.length} isOpen={open} onClose={() => setPrompt(null)}
          onSubmit={(n) => { setPrompt(null); if (p.kind === 'days') run(p.ids, 'extend_days', n, (n >= 0 ? t.extended : t.shortened)(who(p.ids), Math.abs(n))).catch(() => {}); else run(p.ids, 'add_traffic', Math.round(n * GB), (n >= 0 ? t.added : t.reduced)(who(p.ids), Math.abs(n))).catch(() => {}) }} />}
      </Presence>
      <Presence value={groupsFor}>
        {(ids, open) => <SetGroups ids={ids} isOpen={open} groups={groups.data ?? []} users={byId} onClose={() => setGroupsFor(null)}
          onSubmit={(gids) => { setGroupsFor(null); run(ids, 'set_groups', gids, t.groupsSet(who(ids))).catch(() => {}) }} />}
      </Presence>
      <Presence value={limitsFor}>
        {(ids, open) => <SetLimits ids={ids} isOpen={open} onClose={() => setLimitsFor(null)}
          onSubmit={(v) => { setLimitsFor(null); run(ids, 'set_limit', v, tr(LD).applied).then(() => { qc.invalidateQueries({ queryKey: ['limits'] }); qc.invalidateQueries({ queryKey: ['user'] }) }, () => {}) }} />}
      </Presence>
    </>
  )
}

function RowCopy({ url }: { url: string }) {
  const [copied, copy] = useCopy()
  return <IconButton label={copied ? tr(D).copied : tr(D).copySub} onPress={() => copy(url)}><CopyIcon copied={copied} size={15} /></IconButton>
}

function QrPop({ u }: { u: User }) {
  return (
    <DialogTrigger>
      <IconButton label={tr(D).subQr}><IconQr size={15} /></IconButton>
      <Popover placement="bottom end" className={popoverCls}>
        <Dialog aria-label={tr(D).qrOf(u.username)} className="p-2 outline-none"><QrBody id={u.id} /></Dialog>
      </Popover>
    </DialogTrigger>
  )
}

function QrBody({ id }: { id: number }) {
  const sub = useQuery({ queryKey: ['user', id, 'sub'], queryFn: () => api<SubInfo>(`/users/${id}/sub`) })
  return sub.data?.qr
    ? <img src={sub.data.qr} alt={tr(D).qrAlt} className="h-44 w-44 animate-content-in rounded-[8px] bg-white p-1.5 [image-rendering:pixelated]" />
    : <Skeleton className="h-44 w-44" />
}

function BatchBar({ ids, open, onClear, onAction }: { ids: number[]; open: boolean; onClear: () => void; onAction: (k: string) => void }) {
  useEffect(() => {
    if (!open) return
    document.documentElement.style.setProperty('--toast-lift', '64px')
    return () => { document.documentElement.style.removeProperty('--toast-lift') }
  }, [open])
  const n = ids.length
  const t = useT(D)
  return (
    <div role="toolbar" aria-label={t.batchLabel(n)}
      className={cx('fixed bottom-[calc(72px+env(safe-area-inset-bottom))] left-1/2 z-40 flex -translate-x-1/2 max-w-[calc(100vw-24px)] items-center gap-1 rounded-full border bg-surface p-1.5 shadow-[0_8px_24px_rgb(0_0_0/0.16)] lg:bottom-5',
        open ? 'animate-bar-in' : 'pointer-events-none animate-bar-out')}>
      <span className="flex shrink-0 items-center gap-1 pl-2 text-[13px] font-medium whitespace-nowrap">
        {t.selected(<span key={n} className="inline-block animate-num tabular">{n}</span>)}
        <IconButton label={t.clearSel} className="h-7 w-7" onPress={onClear}><IconX size={14} /></IconButton>
      </span>
      <span className="mx-1 h-5 w-px shrink-0 bg-hl-med" />
      <div className="no-scrollbar flex min-w-0 items-center gap-1 overflow-x-auto">
        <Button size="sm" variant="ghost" onPress={() => onAction('enable')}>{t.enable}</Button>
        <Button size="sm" variant="ghost" onPress={() => onAction('disable')}>{t.disable}</Button>
        <ActionMenu label={t.extend} placement="top" onAction={onAction} trigger={<Button size="sm" variant="ghost">{t.extendDots}</Button>}
          items={[{ id: 'd7', label: t.plusDays(7) }, { id: 'd30', label: t.plusDays(30) }, { id: 'd90', label: t.plusDays(90) }, { id: 'd365', label: t.plusDays(365) }, 'sep', { id: 'dcustom', label: t.customDays }]} />
        <ActionMenu label={t.addTraffic} placement="top" onAction={onAction} trigger={<Button size="sm" variant="ghost">{t.addTrafficDots}</Button>}
          items={[{ id: 'g10', label: '+10 GB' }, { id: 'g50', label: '+50 GB' }, { id: 'g100', label: '+100 GB' }, { id: 'g500', label: '+500 GB' }, 'sep', { id: 'gcustom', label: t.custom }]} />
        <Button size="sm" variant="ghost" onPress={() => onAction('reset')}>{t.resetUsage}</Button>
        <Button size="sm" variant="ghost" onPress={() => onAction('groups')}>{t.setGroups}</Button>
        <Button size="sm" variant="ghost" onPress={() => onAction('limits')}>{tr(LD).batchBtn}</Button>
        <Button size="sm" variant="ghost" onPress={() => onAction('copy')}>{t.copySub}</Button>
        <Button size="sm" variant="danger" onPress={() => onAction('delete')}>{t.delete}</Button>
      </div>
    </div>
  )
}

function AmountPrompt({ kind, count, isOpen, onClose, onSubmit }: { kind: 'days' | 'gb'; count: number; isOpen: boolean; onClose: () => void; onSubmit: (n: number) => void }) {
  const t = useT(D)
  const [v, setV] = useState(kind === 'days' ? '30' : '100')
  const n = Number(v)
  const ok = v.trim() !== '' && Number.isFinite(n) && n !== 0
  return (
    <ModalDialog isOpen={isOpen} onOpenChange={(x) => !x && onClose()} title={kind === 'days' ? t.extendFor(count) : t.addFor(count)}
      footer={<><Button onPress={onClose}>{t.cancel}</Button><Button variant="primary" isDisabled={!ok} onPress={() => onSubmit(n)}>{t.ok}</Button></>}>
      <form onSubmit={(e) => { e.preventDefault(); if (ok) onSubmit(n) }}>
        <Field label={kind === 'days' ? t.daysLabel : t.gbLabel} value={v} onChange={setV} autoFocus inputMode="decimal" isInvalid={v.trim() !== '' && !ok} errorMessage={t.nonZero}
          description={kind === 'days' ? t.daysDesc : t.gbDesc} />
      </form>
    </ModalDialog>
  )
}

function SetGroups({ ids, isOpen, groups, users, onClose, onSubmit }: { ids: number[]; isOpen: boolean; groups: Group[]; users: Map<number, User>; onClose: () => void; onSubmit: (g: number[]) => void }) {
  const common = groups.filter((g) => ids.every((i) => users.get(i)?.group_ids.includes(g.id))).map((g) => g.id)
  const t = useT(D)
  const [gids, setGids] = useState<number[]>(common)
  return (
    <ModalDialog isOpen={isOpen} onOpenChange={(x) => !x && onClose()} title={t.setGroupsFor(ids.length)}
      footer={<><Button onPress={onClose}>{t.cancel}</Button><Button variant="primary" onPress={() => onSubmit(gids)}>{t.applyTo(ids.length)}</Button></>}>
      <p className="mb-3 text-[13px] text-subtle">{t.replaceNote}</p>
      <CheckList label={t.groups} value={gids} onChange={setGids} options={groups.map((g) => ({ id: g.id, label: g.name, hint: t.nInbounds(g.inbound_ids.length) }))} empty={t.noGroupsYet} />
      {gids.length === 0 && <div className="mt-3"><Badge tone="bad">{t.noGroupWarn}</Badge></div>}
    </ModalDialog>
  )
}

// ---- create / edit ------------------------------------------------------------

function gb(n: number) { return n ? String(+(n / GB).toFixed(2)) : '' }
function toMbps(bps: number) { return bps ? String(+(bps * 8 / 1e6).toFixed(2)) : '' }
function fromMbps(s: string) { const v = parseFloat(s); return s.trim() && v > 0 ? Math.round(v * 1e6 / 8) : 0 }
const ymd = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const inDays = (n: number) => ymd(new Date(Date.now() + n * DAY * 1000))

const presets = [
  { id: 'trial', get label() { return tr(D).pTrial }, get hint() { return tr(D).pTrialHint }, limit: '10', unit: 'GB', days: 7, reset: 'month_rolling' },
  { id: 'std', get label() { return tr(D).pStd }, get hint() { return tr(D).pStdHint }, limit: '100', unit: 'GB', days: 30, reset: 'month_rolling' },
  { id: 'free', get label() { return tr(D).pFree }, get hint() { return tr(D).pFreeHint }, limit: '', unit: 'GB', days: 0, reset: 'month_rolling' },
] as const

type FormState = { username: string; limit: string; unit: 'GB' | 'TB'; exp: string; reset: string; devices: string; up: string; down: string; note: string; gids: number[]; profile: string; lims: { id: number; size: string }[] }

/** Create (u undefined) or edit a user. Creating ends on a "created" view with the subscription link. */
function UserForm({ u, groups, users, isOpen, onClose, onOpen }: { u?: User; groups: Group[]; users: User[]; isOpen: boolean; onClose: () => void; onOpen?: (id: number) => void }) {
  const t = useT(D)
  const qc = useQueryClient()
  const track = useApplyTracker()
  const init = useCallback((): FormState => {
    const tb = (u?.traffic_limit ?? 0) >= 1024 * GB && (u!.traffic_limit / (1024 * GB)) % 1 === 0
    return {
      username: u?.username ?? '', limit: tb ? String(u!.traffic_limit / (1024 * GB)) : gb(u?.traffic_limit ?? 0), unit: tb ? 'TB' : 'GB',
      exp: u?.expire_at ? ymd(new Date(u.expire_at * 1000)) : '', reset: u ? (u.reset_strategy || 'none') : 'month_rolling',
      devices: u?.device_limit ? String(u.device_limit) : '', up: toMbps(u?.speed_up ?? 0), down: toMbps(u?.speed_down ?? 0),
      note: u?.note ?? '', gids: u?.group_ids ?? groups.filter((g) => g.is_default).map((g) => g.id), profile: String(u?.sub_profile_id ?? 0),
      lims: (u?.limits ?? []).map((l) => ({ id: l.id, size: l.traffic_limit ? String(+(l.traffic_limit / GB).toFixed(2)) : '' })),
    }
  }, [u, groups])
  const [f, setF] = useState<FormState>(init)
  const [initial] = useState(() => JSON.stringify(init()))
  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setF((x) => ({ ...x, [k]: v }))
  const [preset, setPreset] = useState('')
  const [created, setCreated] = useState<{ id: number; username: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [touched, setTouched] = useState(false)

  const nameErr = !f.username ? (touched ? t.nameRequired : '')
    : !USERNAME_RE.test(f.username) ? t.nameInvalid
      : users.some((x) => x.username === f.username && x.id !== u?.id) ? t.nameTaken : ''
  const limitBytes = (() => {
    const s = f.limit.trim()
    if (!s) return 0
    if (/[kmgtp]/i.test(s)) return parseSize(s)
    const n = Number(s)
    return Number.isFinite(n) && n >= 0 ? Math.round(n * (f.unit === 'TB' ? 1024 * GB : GB)) : NaN
  })()
  const devN = f.devices.trim() ? Number(f.devices) : 0
  const devErr = !Number.isInteger(devN) || devN < 0 ? t.nonNegInt : ''
  const dirty = !created && JSON.stringify(f) !== initial
  const limSets = f.lims.map((l) => ({ id: l.id, traffic_limit: l.size.trim() ? parseSize(l.size) : 0 }))
  const valid = !!f.username && !nameErr && !Number.isNaN(limitBytes) && !devErr && limSets.every((l) => !Number.isNaN(l.traffic_limit))

  const applyPreset = (id: string) => {
    const p = presets.find((x) => x.id === id)!
    setPreset(id)
    setF((x) => ({ ...x, limit: p.limit, unit: p.unit, exp: p.days ? inDays(p.days) : '', reset: p.reset }))
  }
  const submit = async () => {
    setTouched(true)
    if (!valid) return
    setBusy(true); setErr('')
    const body = {
      username: f.username, traffic_limit: limitBytes, expire_at: f.exp ? Math.floor(new Date(f.exp + 'T23:59:59').getTime() / 1000) : 0,
      reset_strategy: f.reset, device_limit: devN, speed_up: fromMbps(f.up), speed_down: fromMbps(f.down), note: f.note, group_ids: f.gids, sub_profile_id: Number(f.profile), limits: limSets,
    }
    try {
      let id: number
      if (u) { await api(`/users/${u.id}`, { method: 'PATCH', body }); id = u.id }
      else id = (await api<{ id: number }>('/users', { method: 'POST', body })).id
      qc.invalidateQueries({ queryKey: ['users'] }); qc.invalidateQueries({ queryKey: ['user', id] }); qc.invalidateQueries({ queryKey: ['groups'] })
      flashRows([id])
      track({ text: u ? t.saved(f.username) : t.createdX(f.username) })
      if (u) onClose()
      else setCreated({ id, username: f.username })
    } catch (e) {
      const m = (e as Error).message
      if (/already exists|UNIQUE/i.test(m)) setErr(t.nameTaken); else setErr(m)
    } finally { setBusy(false) }
  }
  const again = () => { setCreated(null); setF(init()); setPreset(''); setTouched(false); setErr('') }

  const csub = useQuery({ queryKey: ['user', created?.id, 'sub'], queryFn: () => api<SubInfo>(`/users/${created!.id}/sub`), enabled: !!created })
  if (created) {
    return (
      <ModalDialog isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={t.created} label={t.created}
        footer={<div key="done" className="flex w-full animate-content-in flex-wrap justify-end gap-2"><Button variant="ghost" className="mr-auto" onPress={() => onOpen?.(created.id)}>{t.viewDetails}</Button><Button onPress={again}><IconPlus size={14} />{t.another}</Button>
          <CopyButton size="md" variant="primary" label={t.copyLink} text={csub.data?.url ?? ''} isDisabled={!csub.data} /></div>}>
        <CreatedBody c={created} sub={csub.data} />
      </ModalDialog>
    )
  }
  return (
    <ModalDialog wide isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={u ? t.editX(u.username) : t.newUser} dirty={dirty}
      footer={<>{err && <span className="mr-auto self-center text-[13px] text-bad">{err}</span>}<Button onPress={onClose}>{t.cancel}</Button>
        <Button variant="primary" isPending={busy} isDisabled={!valid} onPress={submit}>{u ? t.save : t.create}</Button></>}>
      <form className="grid gap-5 md:grid-cols-[1fr_240px]" onSubmit={(e) => { e.preventDefault(); submit() }}>
        <div className="flex min-w-0 flex-col gap-4">
          {!u && (
            <div>
              <div className="mb-1.5 text-[13px] font-medium text-subtle">{t.presets}</div>
              <div className="flex flex-wrap gap-1.5">
                {presets.map((p) => (
                  <Chip key={p.id} active={preset === p.id} onPress={() => applyPreset(p.id)}>{p.label}<span className={cx('text-[11px]', preset === p.id ? 'opacity-80' : 'text-muted')}>{p.hint}</span></Chip>
                ))}
              </div>
            </div>
          )}
          <Field label={t.username} value={f.username} onChange={(v) => { set('username', v); setErr('') }} autoFocus={!u}
            isInvalid={!!nameErr} errorMessage={nameErr} description={t.usernameDesc} />
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between gap-2">
                <label htmlFor="uf-limit" className="text-[13px] font-medium text-subtle">{t.quota}</label>
                <Segmented label={t.unit} value={f.unit} onChange={(v) => set('unit', v)} options={[{ id: 'GB', label: 'GB' }, { id: 'TB', label: 'TB' }]} />
              </div>
              <input id="uf-limit" value={f.limit} onChange={(e) => { set('limit', e.target.value); setPreset('') }} placeholder={t.unlimited} inputMode="decimal"
                aria-invalid={Number.isNaN(limitBytes) || undefined}
                className={cx('h-9 w-full rounded-[10px] border bg-base px-3 text-sm outline-none transition-[border-color,box-shadow] duration-(--dur-1) placeholder:text-muted focus:border-accent focus:ring-2 focus:ring-accent/25', Number.isNaN(limitBytes) && 'border-bad')} />
              <span className={cx('text-xs', Number.isNaN(limitBytes) ? 'text-bad' : 'text-muted')}>{Number.isNaN(limitBytes) ? t.badSize : limitBytes ? `= ${bytes(limitBytes)}` : t.blankUnlimited}</span>
            </div>
            <ResetPicker value={f.reset} onChange={(v) => set('reset', v)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <div className="text-[13px] font-medium text-subtle">{t.expiry}</div>
            <div className="flex flex-wrap items-center gap-1.5">
              {[7, 30, 90, 365].map((n) => <Chip key={n} active={f.exp === inDays(n)} onPress={() => { set('exp', inDays(n)); setPreset('') }}>{t.nDays(n)}</Chip>)}
              <Chip active={!f.exp} onPress={() => { set('exp', ''); setPreset('') }}>{t.never}</Chip>
              <input type="date" aria-label={t.expiryDate} value={f.exp} onChange={(e) => { set('exp', e.target.value); setPreset('') }}
                className="h-8 rounded-full border bg-base px-3 text-[13px] outline-none focus:border-accent focus:ring-2 focus:ring-accent/25" />
            </div>
            <span className="text-xs text-muted">{f.exp ? t.expiresAt(f.exp, expiresIn(Math.floor(new Date(f.exp + 'T23:59:59').getTime() / 1000))) : t.neverExpires}</span>
          </div>
          <div className="grid grid-cols-3 gap-3">
            <Field label={t.deviceLimit} value={f.devices} onChange={(v) => set('devices', v)} placeholder={t.unlimited} inputMode="numeric" isInvalid={!!devErr} errorMessage={devErr} />
            <Field label={t.upMbps} value={f.up} onChange={(v) => set('up', v)} placeholder={t.unlimited} inputMode="decimal" />
            <Field label={t.downMbps} value={f.down} onChange={(v) => set('down', v)} placeholder={t.unlimited} inputMode="decimal" />
          </div>
          <Field label={t.note} value={f.note} onChange={(v) => set('note', v)} />
        </div>
        <CheckList label={t.groupsLabel} value={f.gids} onChange={(v) => set('gids', v)}
          options={groups.map((g) => ({ id: g.id, label: g.name, hint: t.nInbounds(g.inbound_ids.length) }))}
          empty={t.noGroupsYet} />
        <ProfileSelect value={f.profile} onChange={(v) => set('profile', v)} />
        <LimitPicker value={f.lims} onChange={(v) => set('lims', v)} />
        <button type="submit" hidden />
      </form>
    </ModalDialog>
  )
}

function CreatedBody({ c, sub }: { c: { id: number; username: string }; sub?: SubInfo }) {
  const t = useT(D)
  return (
    <div className="flex animate-content-in flex-col items-center gap-4 text-center sm:flex-row sm:items-start sm:text-left">
      {sub?.qr ? <img src={sub.qr} alt={t.qrAlt} className="h-40 w-40 shrink-0 animate-content-in rounded-[12px] bg-white p-1.5 [image-rendering:pixelated]" /> : <Skeleton className="h-40 w-40 shrink-0" />}
      <div className="flex min-w-0 flex-1 flex-col gap-3">
        <div className="flex items-center justify-center gap-2 sm:justify-start"><span className="grid h-6 w-6 animate-check place-items-center rounded-full bg-good/15 text-good">✓</span><b className="text-[15px]">{c.username}</b></div>
        <p className="text-[13px] text-subtle">{t.shareHint}</p>
        <div className="rounded-[10px] border bg-base px-3 py-2 text-left">{sub ? <CopyText text={sub.url} className="w-full" /> : <Skeleton className="h-4" />}</div>
        {sub && <a href={httpUrl(sub.formats.page)} target="_blank" rel="noopener noreferrer" className="text-[13px] text-accent hover:underline">{t.openPortal}</a>}
      </div>
    </div>
  )
}

function BulkCreate({ groups, isOpen, onClose }: { groups: Group[]; isOpen: boolean; onClose: () => void }) {
  const t = useT(D)
  const qc = useQueryClient()
  const subUrl = useSubUrl()
  const [prefix, setPrefix] = useState('user')
  const [count, setCount] = useState('10')
  const [start, setStart] = useState('1')
  const [limit, setLimit] = useState('')
  const [days, setDays] = useState('')
  const [reset, setReset] = useState('month_rolling')
  const [gids, setGids] = useState<number[]>(groups.filter((g) => g.is_default).map((g) => g.id))
  const [done, setDone] = useState<User[] | null>(null)
  const n = Number(count) || 0
  const s = Number(start) || 1
  const countErr = !Number.isInteger(n) || n < 1 ? t.atLeast1 : n > 500 ? t.max500 : ''
  const width = Math.max(2, String(s + n - 1).length)
  const preview = !countErr ? `${prefix}${String(s).padStart(width, '0')} … ${prefix}${String(s + n - 1).padStart(width, '0')}` : ''
  const run = useAction({
    fn: () => {
      const l = limit.trim() ? parseSize(limit) : 0
      if (Number.isNaN(l)) throw new Error(t.badTraffic)
      return api<{ ids: number[] }>('/users/bulk', { method: 'POST', body: { prefix, count: n, start: s, traffic_limit: l, expire_days: Number(days) || 0, reset_strategy: reset, group_ids: gids } })
    },
    apply: true, success: (r) => t.createdN(r.ids.length),
    onSuccess: async (r) => {
      flashRows(r.ids)
      await qc.invalidateQueries({ queryKey: ['users'] })
      qc.invalidateQueries({ queryKey: ['groups'] })
      const list = qc.getQueryData<User[]>(['users']) ?? []
      const ids = new Set(r.ids)
      setDone(list.filter((u) => ids.has(u.id)))
    },
  })
  const csv = () => {
    const rows = [['username', 'subscription_url'], ...(done ?? []).map((u) => [u.username, subUrl(u)])]
    const blob = new Blob(['﻿' + rows.map((r) => r.map((c) => `"${c.replace(/"/g, '""')}"`).join(',')).join('\n')], { type: 'text/csv;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob); a.download = `${prefix}-users.csv`; a.click()
    setTimeout(() => URL.revokeObjectURL(a.href), 1000)
  }
  if (done) {
    return (
      <ModalDialog isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={t.createdN(done.length)}
        footer={<><Button className="mr-auto" onPress={csv}>{t.exportCsv}</Button><CopyButton size="md" label={t.copyAll} text={() => done.map((u) => `${u.username}\t${subUrl(u)}`).join('\n')} />
          <Button variant="primary" onPress={onClose}>{t.done}</Button></>}>
        <ul className="max-h-[50vh] animate-content-in overflow-y-auto rounded-[10px] border bg-base text-[13px]">
          {done.map((u) => (
            <li key={u.id} className="flex items-center gap-3 border-b px-3 py-2 last:border-0">
              <span className="w-28 shrink-0 truncate font-medium">{u.username}</span>
              <CopyText text={subUrl(u)} className="min-w-0 flex-1" />
            </li>
          ))}
        </ul>
      </ModalDialog>
    )
  }
  return (
    <ModalDialog wide isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={t.bulkTitle}
      footer={<><Button onPress={onClose}>{t.cancel}</Button><Button variant="primary" isPending={run.isPending} isDisabled={!!countErr} onPress={() => run.mutate()}>{t.createN(countErr ? '' : n)}</Button></>}>
      <div className="grid gap-5 md:grid-cols-[1fr_240px]">
        <div className="flex min-w-0 flex-col gap-4">
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label={t.prefix} value={prefix} onChange={setPrefix} />
            <Field label={t.quantity} value={count} onChange={setCount} inputMode="numeric" description="1–500" isInvalid={!!countErr} errorMessage={countErr} />
            <Field label={t.startAt} value={start} onChange={setStart} inputMode="numeric" />
          </div>
          {preview && <div className="text-[13px] text-subtle">{t.willCreate(<span className="font-mono text-text">{preview}</span>)}</div>}
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label={t.quotaGb} value={limit} onChange={setLimit} placeholder={t.unlimited} />
            <Field label={t.validDays} value={days} onChange={setDays} placeholder={t.never} inputMode="numeric" />
            <ResetPicker value={reset} onChange={setReset} />
          </div>
        </div>
        <CheckList label={t.groups} value={gids} onChange={setGids} options={groups.map((g) => ({ id: g.id, label: g.name }))} />
      </div>
    </ModalDialog>
  )
}

// ---- detail drawer --------------------------------------------------------------

type UserDetail = { user: User; ips: { ip: string; node: string; first_seen: number; last_seen: number; online: boolean }[]; limits?: UserLimit[]; inbound_usage?: InboundUse[] }
type DrawerTab = 'overview' | 'lines' | 'sub' | 'devices'

function UserDrawer({ id, isOpen, groups, ids, tab, onTab, onNav, onClose, onAct }: {
  id: number; isOpen: boolean; groups: Group[]; ids: number[]; tab: DrawerTab
  onTab: (t: DrawerTab) => void; onNav: (id: number) => void; onClose: () => void; onAct: (k: string) => void
}) {
  const t = useT(D)
  const qc = useQueryClient()
  const run = useUserBatch()
  const patch = useUserPatch()
  const toast = useToast()
  const subUrl = useSubUrl()
  const nodes = useNodes().data ?? []
  const live = isOpen ? 10000 : false
  const d = useQuery({ queryKey: ['user', id], queryFn: () => api<UserDetail>(`/users/${id}`), refetchInterval: live, placeholderData: keepPreviousData, enabled: isOpen })
  const sub = useQuery({ queryKey: ['user', id, 'sub'], queryFn: () => api<SubInfo>(`/users/${id}/sub`), placeholderData: keepPreviousData, enabled: isOpen })
  const [from] = useState(() => Math.floor(Date.now() / 1000) - 7 * 86400)
  const series = useQuery({ queryKey: ['series', 'user', id], queryFn: () => api<SeriesResp>(`/traffic/series?step=3600&group=total&user=${id}&from=${from}`), placeholderData: keepPreviousData, enabled: isOpen })
  const domains = useQuery({ queryKey: ['top', 'domain', 'user', id], queryFn: () => api<TopRow[]>(`/traffic/top?dim=domain&user=${id}&from=${from}&limit=12`), placeholderData: keepPreviousData, enabled: isOpen })
  const [editing, setEditing] = useState(false)
  const [confirm, setConfirm] = useState<'' | 'rotate' | 'reset'>('')
  const [optEnabled, setOpt] = useState<boolean | null>(null)

  const u = d.data?.user
  useEffect(() => { setOpt(null) }, [u?.enabled, u?.id])
  const enabled = optEnabled ?? u?.enabled ?? false
  const s = series.data?.series[0]
  const gname = new Map(groups.map((g) => [g.id, g.name]))
  const i = ids.indexOf(id)
  const prev = i > 0 ? ids[i - 1] : undefined
  const next = i >= 0 && i < ids.length - 1 ? ids[i + 1] : undefined
  const switching = d.isPlaceholderData
  const used = u ? usedOf(u) : 0

  const setEnabled = (v: boolean) => {
    if (!u) return
    setOpt(v)
    qc.setQueryData<User[]>(['users'], (l) => l?.map((x) => (x.id === u.id ? { ...x, enabled: v } : x)))
    run([u.id], v ? 'enable' : 'disable', null, (v ? t.enabledX : t.disabledX)(u.username)).catch(() => { setOpt(null); qc.invalidateQueries({ queryKey: ['users'] }) })
  }
  const rotate = useAction({ fn: () => api(`/users/${id}`, { method: 'PATCH', body: { rotate_secrets: true } }), apply: true, success: t.rotated, invalidate: [['user', id], ['users']] })

  const title = u
    ? <span className="flex min-w-0 items-center gap-2"><span className="truncate">{u.username}</span><UserStatus u={{ ...u, enabled }} /></span>
    : t.colUser
  return (
    <Drawer isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={title} label={u ? t.userX(u.username) : t.colUser} contentKey={u?.id}
      actions={<>
        <IconButton label={t.prevK} isDisabled={prev === undefined} onPress={() => prev !== undefined && onNav(prev)}><IconChevron dir="up" /></IconButton>
        <IconButton label={t.nextJ} isDisabled={next === undefined} onPress={() => next !== undefined && onNav(next)}><IconChevron dir="down" /></IconButton>
      </>}>
      {!u ? (d.isError ? <Empty title={t.notFound} hint={t.notFoundHint} /> : <div className="flex flex-col gap-4"><Skeleton className="h-9" /><Skeleton className="h-40" /><Skeleton className="h-64" /></div>) : (
        <Busy busy={switching} className="flex flex-col gap-5">
          <div className="flex flex-wrap items-center gap-2">
            <CopyButton label={t.copySub} variant="primary" text={subUrl(u)} />
            <DialogTrigger>
              <Button size="sm"><IconQr size={14} />{t.qr}</Button>
              <Popover placement="bottom start" className={popoverCls}><Dialog aria-label={t.subQr} className="p-2 outline-none"><QrBody id={u.id} /></Dialog></Popover>
            </DialogTrigger>
            <Button size="sm" onPress={() => setEditing(true)}><IconEdit size={14} />{t.edit}</Button>
            <ActionMenu label={t.more} onAction={(k) => k === 'reset' || k === 'rotate' ? setConfirm(k) : onAct(k)}
              items={[{ id: 'reset', label: t.resetUsage }, { id: 'rotate', label: t.rotate }, { id: 'kick', label: t.kick, isDisabled: u.online_ips === 0 }, 'sep', { id: 'delete', label: t.deleteUserMenu, danger: true }]} />
            <div className="flex-1" />
            <Switch isSelected={enabled} onChange={setEnabled}>{t.enable}</Switch>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <Box label={t.usedPeriod} value={bytes(used)} sub={u.traffic_limit ? t.quotaOf(bytes(u.traffic_limit), usedPct(u).toFixed(1)) : t.noQuota}
              bar={u.traffic_limit ? usedPct(u) : undefined}
              chips={<>
                <QuickChip onPress={() => run([u.id], 'add_traffic', 50 * GB, t.added(u.username, 50)).catch(() => {})}>+50GB</QuickChip>
                <QuickChip onPress={() => run([u.id], 'add_traffic', 100 * GB, t.added(u.username, 100)).catch(() => {})}>+100GB</QuickChip>
                <QuickChip isDisabled={!u.traffic_limit} onPress={() => patch(u, { traffic_limit: 0 }, t.unlimitedTraffic(u.username)).catch(() => {})}>{t.unlimited}</QuickChip>
              </>} />
            <Box label={t.expiry} value={u.expire_at ? date(u.expire_at) : t.never} sub={u.expire_at ? expiresIn(u.expire_at) : t.resetX(resetText(u.reset_strategy, nodes))}
              chips={<>
                {[7, 30, 90].map((n) => <QuickChip key={n} onPress={() => run([u.id], 'extend_days', n, t.extended(u.username, n)).catch(() => {})}>{t.plusDays(n)}</QuickChip>)}
                <QuickChip isDisabled={!u.expire_at} onPress={() => patch(u, { expire_at: 0 }, t.neverExpire(u.username)).catch(() => {})}>{t.never}</QuickChip>
              </>} />
            <Box label={t.speed} value={`${mbps(u.speed_up)} / ${mbps(u.speed_down)}`} sub={t.devicesOf(u.device_limit || t.unlimited)} />
            <Box label={t.onlineDevices} value={String(u.online_ips)} sub={t.lifetime(bytes(u.life_down + u.life_up))} />
          </div>
          <div className="flex flex-wrap gap-1.5">
            {u.group_ids.length ? u.group_ids.map((g) => <Badge key={g} tone="accent">{gname.get(g) ?? g}</Badge>) : <Badge tone="bad">{t.noGroupBadge}</Badge>}
          </div>

          <div>
            <Tabs label={t.userDetail} value={tab} onChange={onTab} tabs={[
              { id: 'overview', label: t.tTraffic }, { id: 'lines', label: tr(LD).tLines, count: d.data!.limits?.filter((l) => l.exhausted).length || undefined },
              { id: 'sub', label: t.tSub, count: sub.data?.links.length },
              { id: 'devices', label: t.tDevices, count: d.data!.ips.length },
            ]} />
            <div key={tab} className="flex animate-content-in flex-col gap-4">
              {tab === 'overview' && <>
                <Card title={t.chart7d} busy={series.isPlaceholderData}>
                  {s ? <TimeChart height={180} step={3600} times={s.points.map((p) => p[0])}
                    series={[{ key: 'down', label: t.down, values: s.points.map((p) => p[2]), color: 'var(--s1)' }, { key: 'up', label: t.up, values: s.points.map((p) => p[1]), color: 'var(--s2)' }]} />
                    : series.data ? <div className="grid h-[180px] place-items-center text-[13px] text-muted">{t.no7d}</div> : <Skeleton className="h-[180px]" />}
                </Card>
                <Card title={t.domains7d} busy={domains.isPlaceholderData}>
                  <ShareList mono rows={(domains.data ?? []).map((r) => ({ key: r.key, label: r.label, value: r.up + r.down }))}
                    onSelect={(k) => go('traffic', { user: id, dim: 'host', q: k, range: '7d' })} />
                </Card>
              </>}
              {tab === 'lines' && <Lines d={d.data!} />}
              {tab === 'sub' && (sub.data ? <SubPanel s={sub.data} busy={sub.isPlaceholderData} /> : <Skeleton className="h-48" />)}
              {tab === 'devices' && <Devices d={d.data!} access={sub.data?.access} />}
            </div>
          </div>
        </Busy>
      )}
      <Presence value={editing && u ? u : null}>
        {(uu, open) => <UserForm isOpen={open} u={uu} groups={groups} users={qc.getQueryData<User[]>(['users']) ?? []} onClose={() => setEditing(false)} />}
      </Presence>
      <Confirm isOpen={confirm === 'rotate'} onOpenChange={(v) => !v && setConfirm('')} danger title={t.rotate} confirmLabel={t.rotate}
        onConfirm={() => rotate.mutate()} body={t.rotateBody} />
      <Confirm isOpen={confirm === 'reset'} onOpenChange={(v) => !v && setConfirm('')} title={t.resetTitle} confirmLabel={t.reset}
        onConfirm={() => { if (u) run([u.id], 'reset_usage', null, t.resetDone(u.username)).catch(() => {}); else toast('bad', t.notFound) }}
        body={t.resetBody} />
    </Drawer>
  )
}

function Lines({ d }: { d: UserDetail }) {
  const t = useT(LD)
  const limits = d.limits ?? []
  const use = d.inbound_usage ?? []
  const max = Math.max(1, ...use.map((x) => x.up + x.down))
  return <>
    <Card title={t.limitsCard} action={<a className="text-[13px] text-accent hover:underline" href={href('limits')}>{t.manage}</a>}>
      {limits.length === 0 ? <div className="text-[13px] text-muted">{t.noLimits}</div> : (
        <ul className="flex flex-col gap-4">
          {limits.map((l) => <LimitRow key={l.id} uid={d.user.id} l={l} />)}
        </ul>
      )}
    </Card>
    <Card title={t.byInbound} pad={false}>
      <div className="overflow-x-auto">
        <table className="w-full text-[13px] whitespace-nowrap">
          <thead className="text-left text-xs text-subtle"><tr className="border-b"><th className="px-5 py-2 font-medium">{t.inbound}</th><th className="px-3 py-2 font-medium">{t.node}</th><th className="w-[40%] px-5 py-2 text-right font-medium">{t.used}</th></tr></thead>
          <tbody>
            {use.map((x) => (
              <tr key={x.inbound_id} className="border-b last:border-0">
                <td className="max-w-[220px] px-5 py-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <Badge>{x.type}</Badge>
                    <span className={cx('truncate', x.blocked && 'text-muted line-through')} title={x.tag}>{x.remark || x.tag}</span>
                    {x.blocked && <Badge tone="bad">{t.closed}</Badge>}
                  </div>
                </td>
                <td className="px-3 py-2 text-subtle">{x.node}</td>
                <td className="px-5 py-2">
                  <div className="flex items-center justify-end gap-3">
                    <div className="hidden h-1.5 w-24 overflow-hidden rounded-full bg-overlay sm:block"><div className={cx('h-full rounded-full', x.blocked ? 'bg-bad' : 'bg-accent/70')} style={{ width: `${((x.up + x.down) / max) * 100}%` }} /></div>
                    <span className="w-20 text-right tabular" title={`↑ ${bytes(x.up)} · ↓ ${bytes(x.down)}`}>{x.up + x.down ? bytes(x.up + x.down) : '—'}</span>
                  </div>
                </td>
              </tr>
            ))}
            {use.length === 0 && <tr><td colSpan={3} className="py-6 text-center text-muted">{t.noUse}</td></tr>}
          </tbody>
        </table>
      </div>
    </Card>
  </>
}

function LimitRow({ uid, l }: { uid: number; l: UserLimit }) {
  const t = useT(LD)
  const [size, setSize] = useState(l.override ? String(+(l.traffic_limit / GB).toFixed(2)) : '')
  const set = useAction({
    fn: (body: { attach?: boolean; traffic_limit?: number; reset_usage?: boolean }) => api(`/users/${uid}/limits/${l.id}`, { method: 'PUT', body }),
    apply: true, success: t.ownSaved, invalidate: [['user', uid], ['users'], ['limits']],
  })
  const pct = l.traffic_limit ? Math.min(100, (l.used / l.traffic_limit) * 100) : 0
  const v = parseSize(size)
  return (
    <li className="flex flex-col gap-1.5">
      <div className="flex min-w-0 items-center gap-2">
        <span className="truncate font-medium">{l.name}</span>
        {l.exhausted && <Badge tone="bad">{t.exhausted}</Badge>}
        {l.override && <Badge tone="accent">{t.own}</Badge>}
        <span className="flex-1" />
        <span className="text-[13px] tabular">{t.ofLimit(bytes(l.used), bytes(l.traffic_limit))}</span>
      </div>
      <Progress value={l.exhausted ? 100 : pct} />
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
        <span>{l.period === 'month' ? t.resetsOn(date(l.next_reset)) : t.resetsCycle}</span>
        {l.override && <span>{t.def(bytes(l.default_limit))}</span>}
        <span className="flex-1" />
        <DialogTrigger>
          <Button size="sm" variant="ghost">{t.setOwn}</Button>
          <Popover placement="bottom end" className={popoverCls}>
            <Dialog aria-label={t.ownLimit} className="flex w-64 flex-col gap-3 p-3 outline-none">
              {({ close }) => <>
                <Field label={t.ownLimit} value={size} onChange={setSize} inputMode="decimal" placeholder={String(+(l.default_limit / GB).toFixed(2))}
                  description={size.trim() && v > 0 ? `= ${bytes(v)}` : t.def(bytes(l.default_limit))} autoFocus />
                <div className="flex flex-wrap gap-1.5">
                  <Button size="sm" variant="primary" isDisabled={!(v > 0)} onPress={() => { set.mutate({ attach: true, traffic_limit: v }); close() }}>{t.saveOwn}</Button>
                  {l.override && <Button size="sm" onPress={() => { setSize(''); set.mutate({ attach: true, traffic_limit: 0 }); close() }}>{t.useDefault}</Button>}
                </div>
              </>}
            </Dialog>
          </Popover>
        </DialogTrigger>
        {l.used > 0 && <Button size="sm" variant="ghost" onPress={() => set.mutate({ reset_usage: true })}>{t.resetUse}</Button>}
        <Button size="sm" variant="ghost" onPress={() => set.mutate({ attach: false })}>{t.remove}</Button>
      </div>
    </li>
  )
}

/** Batch: attach a preset to (or remove it from) the selected users. */
function SetLimits({ ids, isOpen, onClose, onSubmit }: { ids: number[]; isOpen: boolean; onClose: () => void; onSubmit: (v: { rule_id: number; attach: boolean; traffic_limit: number }) => void }) {
  const t = useT(LD)
  const c = useT(D)
  const limits = useQuery({ queryKey: ['limits'], queryFn: () => api<Limit[]>('/limits'), enabled: isOpen })
  const [rid, setRid] = useState('')
  const [mode, setMode] = useState<'attach' | 'remove'>('attach')
  const [size, setSize] = useState('')
  const v = size.trim() ? parseSize(size) : 0
  const ok = !!rid && (mode === 'remove' || v >= 0)
  const sel = (limits.data ?? []).find((l) => String(l.id) === rid)
  return (
    <ModalDialog isOpen={isOpen} onOpenChange={(x) => !x && onClose()} title={t.batchTitle(ids.length)}
      footer={<><Button onPress={onClose}>{c.cancel}</Button><Button variant={mode === 'remove' ? 'danger' : 'primary'} isDisabled={!ok}
        onPress={() => onSubmit({ rule_id: Number(rid), attach: mode === 'attach', traffic_limit: mode === 'attach' ? v : 0 })}>{c.applyTo(ids.length)}</Button></>}>
      {limits.data && limits.data.length === 0 ? <Empty title={t.noPresets} action={<a className="text-[13px] text-accent hover:underline" href={href('limits')}>{t.makePreset}</a>} /> : (
        <div className="flex flex-col gap-4">
          <Select label={t.preset} value={rid} onChange={setRid}
            options={(limits.data ?? []).map((l) => ({ id: String(l.id), label: `${l.name} · ${bytes(l.traffic_limit)}` }))} />
          <Segmented label={t.preset} value={mode} onChange={setMode} options={[{ id: 'attach', label: t.doAttach }, { id: 'remove', label: t.doRemove }]} />
          {mode === 'attach' && <Field label={t.customOpt} value={size} onChange={setSize} inputMode="decimal" placeholder={sel ? String(+(sel.traffic_limit / GB).toFixed(2)) : ''}
            description={size.trim() && v > 0 ? `= ${bytes(v)}` : sel ? t.def(bytes(sel.traffic_limit)) : undefined} isInvalid={Number.isNaN(v)} errorMessage={t.limitBad} />}
        </div>
      )}
    </ModalDialog>
  )
}

/** User form: which presets apply (none by default), each with an optional custom size. */
function LimitPicker({ value, onChange }: { value: { id: number; size: string }[]; onChange: (v: { id: number; size: string }[]) => void }) {
  const t = useT(LD)
  const limits = useQuery({ queryKey: ['limits'], queryFn: () => api<Limit[]>('/limits') })
  const ls = limits.data ?? []
  if (!limits.data) return null
  return (
    <fieldset className="flex min-w-0 flex-col gap-2">
      <legend className="mb-1 text-[13px] font-medium text-subtle">{t.formTitle}</legend>
      <div className="text-xs text-muted">{t.formHint}</div>
      {ls.length === 0 ? <div className="text-[13px] text-muted">{t.noPresets} · <a className="text-accent hover:underline" href={href('limits')}>{t.makePreset}</a></div> : (
        <div className="flex flex-col gap-2 rounded-[10px] border bg-base p-3">
          {ls.map((l) => {
            const cur = value.find((x) => x.id === l.id)
            const v = cur?.size.trim() ? parseSize(cur.size) : 0
            return (
              <div key={l.id} className="flex flex-col gap-1.5">
                <Checkbox isSelected={!!cur} onChange={(on) => onChange(on ? [...value, { id: l.id, size: '' }] : value.filter((x) => x.id !== l.id))}>
                  <span>{l.name}<span className="ml-2 text-xs text-muted">{bytes(l.traffic_limit)} · {l.period === 'month' ? t.monthly(l.reset_day) : t.withCycle}</span></span>
                </Checkbox>
                {cur && <div className="pl-6"><Field hideLabel label={t.formSize} value={cur.size} placeholder={`${t.formSize}: ${+(l.traffic_limit / GB).toFixed(2)} GB`} inputMode="decimal"
                  onChange={(s) => onChange(value.map((x) => (x.id === l.id ? { ...x, size: s } : x)))}
                  isInvalid={Number.isNaN(v)} errorMessage={t.limitBad} description={cur.size.trim() && v > 0 ? `= ${bytes(v)}` : undefined} /></div>}
              </div>
            )
          })}
        </div>
      )}
    </fieldset>
  )
}

function QuickChip({ children, onPress, isDisabled }: { children: ReactNode; onPress: () => void; isDisabled?: boolean }) {
  return (
    <button type="button" disabled={isDisabled} onClick={onPress}
      className="h-6 cursor-pointer rounded-full bg-overlay px-2 text-xs font-medium text-subtle outline-none transition-[background-color,color,transform] duration-(--dur-1) hover:bg-accent/15 hover:text-accent active:scale-95 focus-visible:ring-2 focus-visible:ring-accent disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-overlay disabled:hover:text-subtle">
      {children}
    </button>
  )
}

function Devices({ d, access }: { d: UserDetail; access?: SubInfo['access'] }) {
  const t = useT(D)
  return <>
    <Card title={t.srcIps} pad={false}>
      <div className="overflow-x-auto">
        <table className="w-full text-[13px] whitespace-nowrap">
          <thead className="text-left text-xs text-subtle"><tr className="border-b"><th className="px-5 py-2 font-medium">IP</th><th className="px-3 py-2 font-medium">{t.node}</th><th className="px-3 py-2 font-medium">{t.first}</th><th className="px-5 py-2 font-medium">{t.last}</th></tr></thead>
          <tbody>
            {d.ips.map((x) => (
              <tr key={x.ip + x.node} className="border-b last:border-0">
                <td className="px-5 py-2 font-mono text-[12.5px]"><span className={cx('mr-2 inline-block h-1.5 w-1.5 rounded-full', x.online ? 'bg-good' : 'bg-hl-high')} />{x.ip}</td>
                <td className="px-3 py-2">{x.node}</td>
                <td className="px-3 py-2 tabular text-subtle"><RelTime ts={x.first_seen} text={dateTime(x.first_seen)} /></td>
                <td className="px-5 py-2 tabular text-subtle">{x.online ? <span className="text-good">{t.online}</span> : <RelTime ts={x.last_seen} text={ago(x.last_seen)} />}</td>
              </tr>
            ))}
            {d.ips.length === 0 && <tr><td colSpan={4} className="py-6 text-center text-muted">{t.noConns}</td></tr>}
          </tbody>
        </table>
      </div>
    </Card>
    <Card title={t.subAccess} pad={false}>
      <div className="overflow-x-auto">
        <table className="w-full text-[13px] whitespace-nowrap">
          <thead className="text-left text-xs text-subtle"><tr className="border-b"><th className="px-5 py-2 font-medium">{t.time}</th><th className="px-3 py-2 font-medium">IP</th><th className="px-3 py-2 font-medium">{t.format}</th><th className="px-5 py-2 font-medium">{t.client}</th></tr></thead>
          <tbody>
            {(access ?? []).map((a, i) => (
              <tr key={i} className="border-b last:border-0">
                <td className="px-5 py-2 tabular text-subtle"><RelTime ts={a.ts} text={dateTime(a.ts)} /></td>
                <td className="px-3 py-2 font-mono text-[12.5px]">{a.ip}</td>
                <td className="px-3 py-2"><Badge>{a.format}</Badge></td>
                <td className="max-w-[220px] truncate px-5 py-2 text-xs text-muted" title={a.ua}>{a.ua || '—'}</td>
              </tr>
            ))}
            {access && access.length === 0 && <tr><td colSpan={4} className="py-6 text-center text-muted">{t.noFetch}</td></tr>}
          </tbody>
        </table>
      </div>
    </Card>
  </>
}

function SubPanel({ s, busy }: { s: SubInfo; busy?: boolean }) {
  const [fmt, setFmt] = useState<'singbox' | 'mihomo' | 'page'>('singbox')
  const t = useT(D)
  const fmtLabel = { singbox: 'sing-box', mihomo: 'Clash / mihomo', page: t.portal }
  return (
    <Busy busy={busy} className="flex flex-col gap-4">
      {!s.active && <div className="rounded-[10px] bg-warn/10 px-3 py-2 text-[13px] text-warn">{t.inactive}</div>}
      <Card title={t.subLink}>
        <div className="grid gap-4 sm:grid-cols-[1fr_auto]">
          <div className="flex min-w-0 flex-col gap-3">
            <div className="rounded-[10px] border bg-base px-3 py-2"><CopyText text={s.url} className="w-full" /></div>
            <div className="flex flex-wrap items-center gap-2">
              <CopyButton label={t.copySub} variant="primary" text={s.url} />
              <a href={httpUrl(s.formats.page)} target="_blank" rel="noopener noreferrer" className="inline-flex h-7 items-center rounded-full px-3 text-[13px] text-accent hover:bg-overlay">{t.previewPortal}</a>
            </div>
            <p className="text-xs text-muted">{t.universalHint}</p>
          </div>
          {s.qr && <img src={s.qr} alt={t.qrAlt} className="mx-auto h-36 w-36 rounded-[10px] bg-white p-1.5 [image-rendering:pixelated]" />}
        </div>
        <details className="group mt-4 rounded-[10px] border">
          <summary className="flex cursor-pointer list-none items-center justify-between px-3 py-2 text-[13px] text-subtle outline-none hover:text-text focus-visible:ring-2 focus-visible:ring-accent [&::-webkit-details-marker]:hidden">
            {t.otherFormats}<span className="transition-transform duration-(--dur-2) group-open:rotate-180"><IconChevron dir="down" size={14} /></span>
          </summary>
          <div className="flex animate-content-in flex-col gap-3 border-t px-3 py-3">
            <Segmented label={t.fmtLabel} value={fmt} onChange={setFmt} options={(Object.keys(fmtLabel) as (keyof typeof fmtLabel)[]).map((k) => ({ id: k, label: fmtLabel[k] }))} />
            <div className="rounded-[10px] border bg-base px-3 py-2"><CopyText text={s.formats[fmt]} className="w-full" /></div>
          </div>
        </details>
      </Card>
      <Card title={t.nodeLinks(s.links.length)} pad={false}>
        {s.links.length === 0 ? <div className="px-5 py-8 text-center text-[13px] text-muted">{t.noLinks}</div> : (
          <ul>
            {s.links.map((l, i) => (
              <li key={i} className="flex items-center gap-3 border-b px-5 py-2.5 last:border-0">
                <Badge tone="accent">{l.type}</Badge>
                <span className="min-w-0 flex-1 truncate text-[13px]">{l.remark}</span>
                <CopyText text={l.uri} display={t.copy} />
              </li>
            ))}
          </ul>
        )}
      </Card>
    </Busy>
  )
}

function Box({ label, value, sub, chips, bar }: { label: string; value: string; sub?: string; chips?: ReactNode; bar?: number }) {
  return (
    <div className="rounded-[12px] border bg-base p-3">
      <div className="text-xs text-subtle">{label}</div>
      <div key={value} className="mt-1 animate-num text-[17px] font-semibold tabular">{value}</div>
      {sub && <div className="mt-0.5 text-xs text-muted tabular">{sub}</div>}
      {bar !== undefined && <div className="mt-2"><Progress value={bar} /></div>}
      {chips && <div className="mt-2.5 flex flex-wrap gap-1">{chips}</div>}
    </div>
  )
}

export const usersHref = (f: FilterId) => href('users', { f })

/** Per-user routing profile override (Subscriptions → Routing). */
function ProfileSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const t = useT(D)
  const profiles = useQuery({ queryKey: ['routing-profiles'], queryFn: () => api<ProfileMeta[]>('/routing/profiles') })
  return (
    <Select label={t.profile} value={value} onChange={onChange} description={t.profileDesc}
      options={[{ id: '0', label: t.followGroup }, ...(profiles.data ?? []).map((p) => ({ id: String(p.id), label: p.name }))]} />
  )
}
