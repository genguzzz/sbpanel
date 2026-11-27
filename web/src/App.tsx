import { Component, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Dialog, DialogTrigger, Modal, ModalOverlay, Popover, Button as RButton } from 'react-aria-components'
import { api, type Template, type User, type Node, type UpdateState } from './lib/api'
import { U } from './i18n/updates'
import { useRoute, href } from './lib/router'
import { useSubUrl } from './lib/actions'
import { C } from './i18n/common'
import { cx, IconButton, LangSwitch, ModalDialog, Spinner, copyText, popoverCls, useIndicator, useToast } from './ui/kit'
import {
  IconOverview, IconChart, IconServer, IconPlug, IconUsers, IconBell, IconSun, IconMoon, IconLogout, IconSearch, IconGlobe, IconKey, IconMenu, IconRefresh, IconFeedback, IconGithub,
} from './ui/icons'
import { Login } from './pages/Login'
import { OverviewPage } from './pages/Overview'
import { TrafficPage } from './pages/Traffic'
import { NodesPage } from './pages/Nodes'
import { InboundsPage } from './pages/Inbounds'
import { UsersPage } from './pages/Users'
import { EventsPage } from './pages/Events'
import { GroupsPage } from './pages/Groups'
import { LimitsPage } from './pages/Limits'
import { SubscriptionsPage } from './pages/Subscriptions'
import { TemplatesPage } from './pages/Templates'
import { LivePage } from './pages/Live'
import { SettingsPage } from './pages/Settings'
import { CertsPage } from './pages/Certs'
import { preloadCharts } from './ui/charts'
import { setLocal, useAppearance } from './lib/appearance'
import { fullTime } from './lib/fmt'
import { tr } from './i18n'
import { REPO_URL, issueUrl } from './lib/url'
import { KW, SH } from './i18n/shell'

const IconGroup = (p: { size?: number }) => <svg width={p.size ?? 18} height={p.size ?? 18} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="2" /><rect x="14" y="3" width="7" height="7" rx="2" /><rect x="3" y="14" width="7" height="7" rx="2" /><path d="M14 17.5h7M17.5 14v7" /></svg>
const IconRoute = (p: { size?: number }) => <svg width={p.size ?? 18} height={p.size ?? 18} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="6" cy="19" r="2.5" /><circle cx="18" cy="5" r="2.5" /><path d="M8.5 19H16a3.5 3.5 0 0 0 0-7H8a3.5 3.5 0 0 1 0-7h7.5" /></svg>
const IconPulse = (p: { size?: number }) => <svg width={p.size ?? 18} height={p.size ?? 18} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M3 12h4l3-8 4 16 3-8h4" /></svg>
const IconGear = (p: { size?: number }) => <svg width={p.size ?? 18} height={p.size ?? 18} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" /></svg>

const IconGauge = (p: { size?: number }) => <svg width={p.size ?? 18} height={p.size ?? 18} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4.5 17.5a8.5 8.5 0 1 1 15 0" /><path d="m12 13.5 3.5-4" /><circle cx="12" cy="14" r="1.2" /></svg>
const IconShield = (p: { size?: number }) => <svg width={p.size ?? 18} height={p.size ?? 18} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M12 3 4.5 6v5.5c0 4.6 3.2 8.4 7.5 9.5 4.3-1.1 7.5-4.9 7.5-9.5V6z" /><path d="m9 12 2.2 2.2L15.5 10" /></svg>

type NavItem = { id: string; readonly label: string; key: string; icon: ReactNode; el: () => ReactNode; mobile?: boolean; readonly words?: string }
const groups: { readonly label: string; items: NavItem[] }[] = [
  {
    get label() { return tr(SH).gObserve }, items: [
      { id: 'overview', get label() { return tr(SH).overview }, key: 'o', icon: <IconOverview />, el: () => <OverviewPage />, mobile: true, get words() { return 'dashboard home ' + tr(KW).overview } },
      { id: 'traffic', get label() { return tr(SH).traffic }, key: 't', icon: <IconChart />, el: () => <TrafficPage />, mobile: true, get words() { return 'traffic domain ip ' + tr(KW).traffic } },
      { id: 'live', get label() { return tr(SH).live }, key: 'l', icon: <IconPulse />, el: () => <LivePage />, get words() { return 'connections live ' + tr(KW).live } },
      { id: 'events', get label() { return tr(SH).events }, key: 'e', icon: <IconBell />, el: () => <EventsPage />, get words() { return 'events audit log ' + tr(KW).events } },
    ],
  },
  {
    get label() { return tr(SH).gUsers }, items: [
      { id: 'users', get label() { return tr(SH).users }, key: 'u', icon: <IconUsers />, el: () => <UsersPage />, mobile: true, get words() { return 'users clients ' + tr(KW).users } },
      { id: 'groups', get label() { return tr(SH).groups }, key: 'p', icon: <IconGroup />, el: () => <GroupsPage />, get words() { return 'groups squad ' + tr(KW).groups } },
      { id: 'limits', get label() { return tr(SH).limits }, key: 'q', icon: <IconGauge />, el: () => <LimitsPage />, get words() { return 'limits quota ' + tr(KW).limits } },
      { id: 'subscriptions', get label() { return tr(SH).subscriptions }, key: 's', icon: <IconGlobe />, el: () => <SubscriptionsPage />, get words() { return 'routing clash surge profile subscription host cdn ' + tr(KW).subscriptions } },
    ],
  },
  {
    get label() { return tr(SH).gNodes }, items: [
      { id: 'nodes', get label() { return tr(SH).nodes }, key: 'n', icon: <IconServer />, el: () => <NodesPage />, mobile: true, get words() { return 'nodes server ' + tr(KW).nodes } },
      { id: 'inbounds', get label() { return tr(SH).inbounds }, key: 'i', icon: <IconPlug />, el: () => <InboundsPage />, get words() { return 'inbounds vless reality probe ' + tr(KW).inbounds } },
      { id: 'certs', get label() { return tr(SH).certs }, key: 'c', icon: <IconShield />, el: () => <CertsPage />, get words() { return 'certs certificate tls ssl acme letsencrypt cloudflare dns domain certbot ' + tr(KW).certs } },
      { id: 'templates', get label() { return tr(SH).templates }, key: 'r', icon: <IconRoute />, el: () => <TemplatesPage />, get words() { return 'route outbound dns ' + tr(KW).templates } },
    ],
  },
  {
    get label() { return tr(SH).gSystem }, items: [
      { id: 'settings', get label() { return tr(SH).settings }, key: 'x', icon: <IconGear />, el: () => <SettingsPage />, get words() { return 'settings 2fa token backup import 3x-ui ' + tr(KW).settings } },
    ],
  },
]
const allItems = groups.flatMap((g) => g.items)

function useTheme() {
  const { dark } = useAppearance()
  // The header toggle flips this browser between light and dark; palette etc. live in Settings → Appearance.
  const toggle = () => setLocal({ mode: dark ? 'light' : 'dark' })
  return { dark, toggle }
}

export function App() {
  const qc = useQueryClient()
  const me = useQuery({ queryKey: ['me'], queryFn: () => api<{ username: string }>('/auth/me'), retry: false })
  const route = useRoute()
  const { dark, toggle } = useTheme()
  const [palette, setPalette] = useState(false)
  const [help, setHelp] = useState(false)
  const [more, setMore] = useState(false)
  const templates = useQuery({ queryKey: ['templates'], queryFn: () => api<Template[]>('/templates'), enabled: !!me.data, refetchInterval: 30000 })
  const drafts = (templates.data ?? []).filter((t) => t.has_draft).length
  const current = allItems.find((i) => i.id === route.page) ?? allItems[0]
  const [navRef, bar] = useIndicator<HTMLElement>('[aria-current=page]', current.id + String(!!me.data))

  useEffect(() => {
    const on = () => qc.setQueryData(['me'], null)
    window.addEventListener('sbp:unauthorized', on)
    return () => window.removeEventListener('sbp:unauthorized', on)
  }, [qc])

  useEffect(() => {
    let armed = 0
    const on = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette((v) => !v); return }
      const t = e.target as HTMLElement
      if (t?.closest?.('input, textarea, select, [contenteditable=true]') || e.metaKey || e.ctrlKey || e.altKey) return
      if (document.querySelector('[role=dialog]')) return
      if (e.key === '/') { const f = document.querySelector<HTMLInputElement>('main input[type=search], main input[type=text], main input:not([type])'); if (f) { e.preventDefault(); f.focus() } return }
      if (e.key === '?') { e.preventDefault(); setHelp(true); return }
      if (e.key === 'g') { armed = Date.now(); return }
      const it = allItems.find((i) => i.key === e.key)
      if (Date.now() - armed < 800 && it) { location.hash = href(it.id); armed = 0 }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [])

  // A new page starts at the top; param-only changes (filters, drawers) keep the scroll position.
  useEffect(() => { window.scrollTo(0, 0) }, [route.page])
  // Signed in: fetch the chart chunk on idle so the first chart paints without waiting for it.
  useEffect(() => {
    if (!me.data) return
    const ric = window.requestIdleCallback ?? ((f: () => void) => window.setTimeout(f, 1200))
    ric(() => { preloadCharts() })
  }, [me.data])

  if (me.isLoading) return null
  const t = tr(SH)
  if (!me.data) return <Login onDone={() => qc.invalidateQueries()} />

  const logout = async () => { await api('/auth/logout', { method: 'POST' }); qc.setQueryData(['me'], null) }

  return (
    <div className="min-h-full">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded-full focus:bg-accent focus:px-4 focus:py-2 focus:text-on-accent">{t.skip}</a>
      <header className="sticky top-0 z-30 border-b bg-base/90 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-[1600px] min-w-0 items-center gap-1 px-3 sm:gap-3 sm:px-4 lg:px-6">
          <a href={href('overview')} className="flex shrink-0 items-center gap-2 font-semibold outline-none sm:gap-2.5">
            <span className="grid h-8 w-8 place-items-center rounded-[10px] bg-accent text-on-accent">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round"><path d="M5 16c3-9 11-9 14 0" /><circle cx="12" cy="17" r="1.6" fill="currentColor" /></svg>
            </span>
            <span className="text-[15px]">sbpanel</span>
            <span className="hidden rounded-full bg-overlay px-2 py-0.5 text-[11px] font-medium text-subtle sm:inline">sing-box</span>
          </a>
          <button type="button" onClick={() => setPalette(true)}
            className="ml-2 hidden h-8 w-72 cursor-pointer items-center gap-2 rounded-full border bg-surface px-3 text-[13px] text-muted outline-none transition-colors duration-(--dur-1) hover:border-hl-high focus-visible:ring-2 focus-visible:ring-accent md:flex">
            <IconSearch size={14} /><span className="truncate">{t.searchBox}</span><kbd className="ml-auto rounded bg-overlay px-1.5 text-[11px]">⌘K</kbd>
          </button>
          <div className="flex-1" />
          <UpdatePill />
          <SyncStatus />
          {drafts > 0 && (
            <a href={href('templates')} className="hidden h-8 items-center gap-1.5 rounded-full bg-warn/15 px-3 text-[13px] font-medium text-warn xl:flex">
              {t.drafts(drafts)}
            </a>
          )}
          <IconButton label={t.search} className="md:hidden" onPress={() => setPalette(true)}><IconSearch /></IconButton>
          <IconButton label={t.refresh} className="hidden sm:inline-flex" onPress={() => qc.invalidateQueries()}><IconRefresh /></IconButton>
          <span className="hidden text-[13px] text-subtle lg:inline">{me.data.username}</span>
          <FeedbackButton />
          <LangSwitch />
          <IconButton label={dark ? t.toLight : t.toDark} onPress={toggle}>{dark ? <IconSun /> : <IconMoon />}</IconButton>
          <IconButton label={t.logout} onPress={logout}><IconLogout /></IconButton>
        </div>
      </header>
      <div className="mx-auto flex max-w-[1600px]">
        <nav ref={navRef} aria-label={t.mainNav} className="sticky top-14 hidden h-[calc(100vh-56px)] w-[232px] shrink-0 flex-col gap-4 overflow-y-auto px-3 py-5 lg:flex">
          {bar && <span aria-hidden className={cx('pointer-events-none absolute top-0 left-0 z-10 w-[3px] rounded-full bg-accent', bar.ready && 'rp-slide')}
            style={{ transform: `translate(${bar.x}px, ${bar.y + 8}px)`, height: bar.h - 16 }} />}
          {groups.map((g) => (
            <div key={g.label}>
              <div className="px-3 pb-1.5 text-[11px] font-semibold tracking-wider text-muted">{g.label}</div>
              <ul className="flex flex-col gap-0.5">
                {g.items.map((it) => <NavLink key={it.id} it={it} active={it.id === current.id} badge={it.id === 'templates' && drafts > 0 ? drafts : undefined} />)}
              </ul>
            </div>
          ))}
          <div className="mt-auto flex flex-col gap-2">
            <button type="button" onClick={() => setHelp(true)} className="cursor-pointer px-3 text-left text-[11px] leading-4 text-muted outline-none hover:text-subtle focus-visible:text-text">
              <kbd className="rounded bg-overlay px-1">⌘K</kbd> {t.hintSearch} · <kbd className="rounded bg-overlay px-1">g</kbd>{t.hintJump} · <kbd className="rounded bg-overlay px-1">?</kbd> {t.hintKeys}
            </button>
            <PoweredBy />
          </div>
        </nav>
        <main id="main" className="min-w-0 flex-1 px-4 pt-5 pb-24 lg:px-6 lg:pb-10">
          <div key={current.id} className="animate-page-in"><PageBoundary>{current.el()}</PageBoundary></div>
        </main>
      </div>
      <nav aria-label={t.bottomNav} className="fixed inset-x-0 bottom-0 z-30 flex border-t bg-surface/95 pb-[env(safe-area-inset-bottom)] backdrop-blur lg:hidden">
        {allItems.filter((i) => i.mobile).map((it) => (
          <a key={it.id} href={href(it.id)} aria-current={it.id === current.id ? 'page' : undefined}
            className={cx('flex flex-1 flex-col items-center gap-0.5 py-2 text-[11px] outline-none transition-colors duration-(--dur-1) focus-visible:bg-overlay', it.id === current.id ? 'text-accent' : 'text-subtle')}>
            {it.icon}{it.label}
          </a>
        ))}
        <button type="button" onClick={() => setMore(true)} aria-haspopup="dialog" aria-expanded={more}
          className={cx('flex flex-1 cursor-pointer flex-col items-center gap-0.5 py-2 text-[11px] outline-none focus-visible:bg-overlay', !current.mobile ? 'text-accent' : 'text-subtle')}>
          <IconMenu />{t.more}
        </button>
      </nav>
      <ModalOverlay isOpen={more} onOpenChange={setMore} isDismissable className="fixed inset-0 z-50 bg-black/30 data-[entering]:animate-fade-in data-[exiting]:animate-fade-out lg:hidden">
        <Modal className="fixed inset-x-0 bottom-0 rounded-t-[20px] border-t bg-surface p-4 pb-[calc(16px+env(safe-area-inset-bottom))] outline-none data-[entering]:animate-sheet-in data-[exiting]:animate-sheet-out">
          <Dialog aria-label={t.morePages} className="outline-none">
            <div className="mb-3 flex justify-center" aria-hidden><span className="h-1 w-10 rounded-full bg-hl-high" /></div>
            <div className="grid grid-cols-4 gap-2">
              {allItems.map((it) => (
                <a key={it.id} href={href(it.id)} onClick={() => setMore(false)} aria-current={it.id === current.id ? 'page' : undefined}
                  className={cx('relative flex flex-col items-center gap-1 rounded-[12px] py-3 text-[12px] outline-none focus-visible:ring-2 focus-visible:ring-accent', it.id === current.id ? 'bg-overlay text-accent' : 'text-subtle')}>
                  {it.icon}{it.label}
                  {it.id === 'templates' && drafts > 0 && <span className="absolute top-1.5 right-3 rounded-full bg-warn/20 px-1.5 text-[10px] font-semibold text-warn">{drafts}</span>}
                </a>
              ))}
            </div>
            <div className="mt-3 border-t pt-3"><PoweredBy /></div>
          </Dialog>
        </Modal>
      </ModalOverlay>
      <Palette open={palette} onOpenChange={setPalette} />
      <ShortcutHelp open={help} onOpenChange={setHelp} />
    </div>
  )
}

function NavLink({ it, active, badge }: { it: NavItem; active: boolean; badge?: number }) {
  return (
    <li>
      <a href={href(it.id)} aria-current={active ? 'page' : undefined}
        className={cx('relative flex h-9 items-center gap-3 rounded-[8px] px-3 text-sm outline-none transition-colors duration-(--dur-2) focus-visible:ring-2 focus-visible:ring-accent',
          active ? 'bg-[color-mix(in_srgb,var(--accent)_10%,var(--overlay))] font-medium text-text' : 'text-subtle hover:bg-overlay hover:text-text')}>
        <span className={cx('transition-colors duration-(--dur-2)', active && 'text-accent')}>{it.icon}</span>{it.label}
        {badge !== undefined && <span className="ml-auto rounded-full bg-warn/20 px-1.5 text-[11px] font-semibold text-warn">{badge}</span>}
      </a>
    </li>
  )
}

const extLink = 'outline-none transition-colors duration-(--dur-1) focus-visible:ring-2 focus-visible:ring-accent'

/** The running panel version, shared with the Updates page's query. */
function usePanelVersion(): string | undefined {
  const q = useQuery({ queryKey: ['update'], queryFn: () => api<UpdateState>('/update'), refetchInterval: 10 * 60_000, retry: false })
  return q.data?.current || undefined
}

/** Header button: open a prefilled GitHub issue. */
function FeedbackButton() {
  const t = tr(SH)
  const v = usePanelVersion()
  return (
    <a href={issueUrl(v)} target="_blank" rel="noopener noreferrer" title={t.feedbackTitle} aria-label={t.feedback}
      className={cx('hidden h-8 shrink-0 items-center gap-1.5 rounded-full px-2 text-[13px] text-subtle hover:bg-overlay hover:text-text sm:inline-flex md:px-3', extLink)}>
      <IconFeedback size={16} /><span className="hidden xl:inline">{t.feedback}</span>
    </a>
  )
}

/** Sidebar / More-sheet footer: "Powered by sbpanel vX" → source repo, plus the feedback link. */
function PoweredBy() {
  const t = tr(SH)
  const v = usePanelVersion()
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 text-[11px] leading-4 text-muted">
      <a href={REPO_URL} target="_blank" rel="noopener noreferrer" title={t.sourceRepo} className={cx('inline-flex items-center gap-1 rounded hover:text-text', extLink)}>
        <IconGithub size={12} />{t.poweredBy} <span className="font-medium text-subtle">sbpanel</span>{v && <span className="font-mono">{v}</span>}
      </a>
      <a href={issueUrl(v)} target="_blank" rel="noopener noreferrer" title={t.feedbackTitle} className={cx('inline-flex items-center gap-1 rounded hover:text-text', extLink)}>
        <IconFeedback size={12} />{t.feedback}
      </a>
    </div>
  )
}

/** Header pill: a newer sbpanel release is out (for the panel, or only for some nodes). */
function UpdatePill() {
  const q = useQuery({ queryKey: ['update'], queryFn: () => api<UpdateState>('/update'), refetchInterval: 10 * 60_000, retry: false })
  const d = q.data
  const behind = d?.nodes.filter((n) => n.has_update).length ?? 0
  if (!d?.latest || !(d.has_update || behind)) return null
  const label = d.has_update ? tr(U).pill(d.latest.tag) : tr(U).pillNodes(behind, d.latest.tag)
  return (
    <a href={href('settings', { s: 'updates' })} title={label}
      className="flex h-8 shrink-0 items-center gap-1.5 rounded-full bg-accent/12 px-2.5 text-[13px] font-medium text-accent-text outline-none transition-colors duration-(--dur-2) hover:bg-accent/20 focus-visible:ring-2 focus-visible:ring-accent sm:px-3">
      <span className="h-2 w-2 rounded-full bg-current" /><span className="hidden sm:inline">{label}</span>
    </a>
  )
}

/** Header pill: are all nodes on the latest config? */
function SyncStatus() {
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<Node[]>('/nodes'), refetchInterval: 5000 })
  const ns = (nodes.data ?? []).filter((n) => n.enrolled)
  if (!nodes.data || ns.length === 0) return null
  const problems = ns.filter((n) => !n.online || n.apply_error)
  const syncing = ns.filter((n) => n.online && !n.apply_error && n.applied_revision < n.revision)
  const state = problems.length ? 'bad' : syncing.length ? 'sync' : 'ok'
  const t = tr(SH)
  const pill = cx('flex h-8 shrink-0 items-center gap-1.5 rounded-full px-2.5 text-[13px] font-medium outline-none transition-colors duration-(--dur-2) focus-visible:ring-2 focus-visible:ring-accent sm:px-3',
    state === 'bad' ? 'cursor-pointer bg-bad/12 text-bad hover:bg-bad/20' : state === 'sync' ? 'bg-accent/12 text-accent' : 'text-subtle')
  const label = state === 'bad' ? t.nodesBad(problems.length) : state === 'sync' ? t.syncing : t.synced
  const icon = state === 'bad' ? <span className="h-2 w-2 rounded-full bg-current" /> : state === 'sync' ? <Spinner size={12} /> : <span className="h-2 w-2 rounded-full bg-good" />
  if (state !== 'bad') {
    return <span className={pill} role="status" title={state === 'ok' ? t.syncedTitle(ns.length) : t.syncingTitle(syncing.length)}>{icon}<span key={state} className="hidden animate-content-in sm:inline">{label}</span></span>
  }
  return (
    <DialogTrigger>
      <RButton className={pill} aria-label={label}>{icon}<span key={state} className="max-w-[6.5rem] animate-content-in truncate sm:max-w-none">{label}</span></RButton>
      <Popover placement="bottom end" className={cx('w-[320px] max-w-[calc(100vw-24px)]', popoverCls)}>
        <Dialog aria-label={t.badNodes} className="p-2 outline-none">
          {({ close }) => (
            <>
              <div className="px-2 pt-1 pb-2 text-[13px] font-semibold">{t.badNodes}</div>
              <ul className="flex flex-col gap-1">
                {problems.map((n) => (
                  <li key={n.id}>
                    <a href={href('nodes')} onClick={close} className="block rounded-[10px] px-2 py-2 outline-none hover:bg-overlay focus-visible:bg-overlay">
                      <div className="flex items-center justify-between gap-2 text-sm">
                        <span className="truncate font-medium">{n.name}</span>
                        <span className="shrink-0 text-xs text-bad">{!n.online ? t.offline : t.applyFailed}</span>
                      </div>
                      {n.apply_error && <div className="mt-1 line-clamp-2 font-mono text-[11px] break-all text-muted">{n.apply_error}</div>}
                      {!n.online && <div className="mt-0.5 text-xs text-muted">{t.lastSeen(fullTime(n.last_seen))}</div>}
                    </a>
                  </li>
                ))}
              </ul>
              <a href={href('nodes')} onClick={close} className="mt-1 block rounded-[10px] px-2 py-1.5 text-[13px] text-accent hover:bg-overlay">{t.toNodes}</a>
            </>
          )}
        </Dialog>
      </Popover>
    </DialogTrigger>
  )
}

type Hit = { id: string; label: string; hint?: string; kind: 'page' | 'user' | 'node' | 'action'; to: string; token?: string }
const kindIcon = (h: Hit) => h.kind === 'page' ? (allItems.find((i) => 'p' + i.id === h.id)?.icon ?? <IconOverview />)
  : h.kind === 'user' ? <IconUsers /> : h.kind === 'node' ? <IconServer /> : h.id === 'a-2fa' ? <IconKey /> : <IconGear />
const sectionName = {
  get recent() { return tr(SH).secRecent }, get page() { return tr(SH).secPage }, get user() { return tr(SH).secUser },
  get node() { return tr(SH).secNode }, get more() { return tr(SH).secMore }, get action() { return tr(SH).secAction },
}

// Recent hits are stored without the subscription token (a user's secret must not outlive the session
// in localStorage); it is looked up from the live user list when the palette shows them.
const RECENT_KEY = 'sbp-recent'
function readRecent(): Hit[] {
  let raw: unknown
  try { raw = JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]') } catch { return [] }
  if (!Array.isArray(raw)) return []
  return raw.filter((h): h is Hit => !!h && typeof h.id === 'string' && typeof h.label === 'string' && typeof h.to === 'string' && h.to.startsWith('#/'))
    .map(({ token: _token, ...h }) => h)
}
// Drop tokens saved by older versions.
try { if (localStorage.getItem(RECENT_KEY)?.includes('"token"')) localStorage.setItem(RECENT_KEY, JSON.stringify(readRecent())) } catch { /* storage blocked */ }
function loadRecent(): Hit[] {
  const hs = readRecent()
  // Pages and actions were stored with the label of the language at the time; relabel them.
  const acts = actions()
  return hs.map((h) => h.kind === 'page' ? { ...h, label: allItems.find((i) => 'p' + i.id === h.id)?.label ?? h.label }
    : h.kind === 'action' ? { ...h, label: acts.find((x) => x.id === h.id)?.label ?? h.label }
      : h.kind === 'node' ? { ...h, hint: tr(SH).viewTraffic } : h)
}
function pushRecent(h: Hit) {
  const { token: _token, ...keep } = h
  try { localStorage.setItem(RECENT_KEY, JSON.stringify([keep, ...readRecent().filter((x) => x.id !== h.id)].slice(0, 5))) } catch { /* private mode */ }
}

// Built per call so labels follow the current language.
const actions = (): Hit[] => {
  const t = tr(SH)
  return [
    { id: 'a-newuser', label: t.aNewUser, kind: 'action', to: href('users', { new: 1 }) },
    { id: 'a-newnode', label: t.aNewNode, kind: 'action', to: href('nodes', { add: 1 }) },
    { id: 'a-expiring', label: t.aExpiring, kind: 'action', to: href('users', { f: 'expiring' }) },
    { id: 'a-2fa', label: t.a2fa, kind: 'action', to: href('settings', { s: 'account' }) },
    { id: 'a-apikey', label: t.aApiKey, kind: 'action', to: href('settings', { s: 'api' }) },
    { id: 'a-apidocs', label: t.aApiDocs, kind: 'action', to: href('settings', { view: 'api-docs' }) },
    { id: 'a-routing', label: t.aRouting, kind: 'action', to: href('subscriptions', { tab: 'routing' }) },
    { id: 'a-import', label: t.aImport, kind: 'action', to: href('settings', { s: 'import' }) },
    { id: 'a-backup', label: t.aBackup, kind: 'action', to: href('settings', { s: 'backup' }) },
  ]
}

function Palette({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const [q, setQ] = useState('')
  const [sel, setSel] = useState(0)
  const toast = useToast()
  const subUrl = useSubUrl()
  const users = useQuery({ queryKey: ['users'], queryFn: () => api<User[]>('/users'), enabled: open })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<Node[]>('/nodes'), enabled: open })
  useEffect(() => { if (open) { setQ(''); setSel(0) } }, [open])
  const sections = useMemo(() => {
    const s = q.trim().toLowerCase()
    const match = (...xs: (string | undefined)[]) => !s || xs.some((x) => x?.toLowerCase().includes(s))
    const page = (i: NavItem): Hit => ({ id: 'p' + i.id, label: i.label, kind: 'page', to: href(i.id) })
    const out: { key: keyof typeof sectionName; hits: Hit[] }[] = []
    if (!s) {
      const tokens = new Map((users.data ?? []).map((u) => ['u' + u.id, u.sub_token]))
      out.push({ key: 'recent', hits: loadRecent().map((h) => (h.kind === 'user' ? { ...h, token: tokens.get(h.id) } : h)) })
      out.push({ key: 'action', hits: actions().slice(0, 3) })
      out.push({ key: 'page', hits: allItems.map(page) })
    } else {
      // Pages matched by name rank first; pages matched only by keywords come after users and nodes.
      out.push({ key: 'page', hits: allItems.filter((i) => match(i.label, i.id)).map(page) })
      out.push({
        key: 'user', hits: (users.data ?? []).filter((u) => match(u.username, u.note, u.uuid)).slice(0, 8)
          .map((u) => ({ id: 'u' + u.id, label: u.username, hint: u.note || undefined, kind: 'user', to: href('users', { id: u.id }), token: u.sub_token })),
      })
      out.push({
        key: 'node', hits: (nodes.data ?? []).filter((n) => match(n.name, n.address, n.public_address)).slice(0, 5)
          .map((n) => ({ id: 'n' + n.id, label: n.name, hint: tr(SH).viewTraffic, kind: 'node', to: href('traffic', { node: n.id }) })),
      })
      out.push({ key: 'more', hits: allItems.filter((i) => !match(i.label, i.id) && match(i.words)).map(page) })
      out.push({ key: 'action', hits: actions().filter((h) => match(h.label)) })
    }
    return out.filter((x) => x.hits.length)
  }, [q, users.data, nodes.data, open])
  const flat = sections.flatMap((x) => x.hits.map((h) => ({ ...h, sec: x.key })))
  const cur = flat[sel]
  const [listRef, hl] = useIndicator<HTMLUListElement>('[aria-selected=true]', `${sel}:${flat.length}:${q}`)
  useEffect(() => { setSel(0) }, [q])
  useEffect(() => { listRef.current?.querySelector('[aria-selected=true]')?.scrollIntoView({ block: 'nearest' }) }, [sel, listRef])
  const pick = (h?: Hit) => {
    if (!h) return
    pushRecent({ id: h.id, label: h.label, hint: h.hint, kind: h.kind, to: h.to })
    location.hash = h.to
    onOpenChange(false)
  }
  const copySub = (h: Hit) => {
    if (!h.token) return
    copyText(subUrl({ sub_token: h.token })).then(() => toast('good', tr(SH).copiedSub(h.label)), () => toast('bad', tr(C).copyFailed))
  }
  let idx = -1
  const t = tr(SH)
  return (
    <ModalOverlay isOpen={open} onOpenChange={onOpenChange} isDismissable
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/30 p-4 pt-[12vh] backdrop-blur-[2px] data-[entering]:animate-fade-in data-[exiting]:animate-fade-out">
      <Modal className="w-full max-w-[560px] overflow-hidden rounded-[16px] border bg-surface shadow-[0_8px_24px_rgb(0_0_0/0.18)] outline-none data-[entering]:animate-palette-in data-[exiting]:animate-palette-out">
        <Dialog aria-label={t.search} className="outline-none">
          <div className="flex items-center gap-2 border-b px-4">
            <IconSearch size={16} className="text-muted" />
            <input autoFocus value={q} onChange={(e) => setQ(e.target.value)} placeholder={t.paletteInput} aria-label={t.search}
              role="combobox" aria-expanded aria-controls="palette-list" aria-activedescendant={cur?.id}
              onKeyDown={(e) => {
                if (e.key === 'ArrowDown') { e.preventDefault(); setSel((x) => Math.min(flat.length - 1, x + 1)) }
                if (e.key === 'ArrowUp') { e.preventDefault(); setSel((x) => Math.max(0, x - 1)) }
                if (e.key === 'Enter') { e.preventDefault(); pick(cur) }
                if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'c' && cur?.kind === 'user') {
                  const el = e.currentTarget
                  if (el.selectionStart === el.selectionEnd) { e.preventDefault(); copySub(cur) }
                }
              }}
              className="h-12 flex-1 bg-transparent text-[15px] outline-none placeholder:text-muted" />
          </div>
          <ul id="palette-list" role="listbox" ref={listRef} className="relative max-h-[50vh] overflow-y-auto p-1.5">
            {hl && <li aria-hidden className={cx('pointer-events-none absolute top-0 left-1.5 right-1.5 rounded-[10px] bg-overlay', hl.ready && 'rp-slide-fast')}
              style={{ transform: `translateY(${hl.y}px)`, height: hl.h }} />}
            {flat.length === 0 && <li className="px-3 py-6 text-center text-[13px] text-muted">{t.noResults}</li>}
            {sections.map((sec) => (
              <li key={sec.key} role="presentation">
                <div className="px-3 pt-2 pb-1 text-[11px] font-semibold tracking-wider text-muted">{sectionName[sec.key]}</div>
                <ul role="group" aria-label={sectionName[sec.key]}>
                  {sec.hits.map((h) => {
                    idx++
                    const i = idx
                    return (
                      <li key={sec.key + h.id} id={h.id} role="option" aria-selected={i === sel} onMouseMove={() => sel !== i && setSel(i)} onClick={() => pick(h)}
                        className="relative flex cursor-pointer items-center gap-3 rounded-[10px] px-3 py-2 text-sm">
                        <span className="text-subtle">{kindIcon(h)}</span>
                        <span className="min-w-0 flex-1 truncate">{h.label}</span>
                        {h.hint && <span className="max-w-[40%] truncate text-xs text-muted">{h.hint}</span>}
                        {h.kind === 'user' && i === sel && <kbd className="shrink-0 rounded bg-surface px-1.5 text-[11px] text-subtle">⌘C {t.copySub}</kbd>}
                      </li>
                    )
                  })}
                </ul>
              </li>
            ))}
          </ul>
          <div className="flex flex-wrap gap-x-3 gap-y-1 border-t px-4 py-2 text-[11px] text-muted">
            <span><kbd className="rounded bg-overlay px-1">↑↓</kbd> {t.kSelect}</span><span><kbd className="rounded bg-overlay px-1">↵</kbd> {t.kOpen}</span>
            <span><kbd className="rounded bg-overlay px-1">⌘C</kbd> {t.kCopy}</span><span><kbd className="rounded bg-overlay px-1">esc</kbd> {t.kClose}</span>
          </div>
        </Dialog>
      </Modal>
    </ModalOverlay>
  )
}

function ShortcutHelp({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const t = tr(SH)
  const rows: [string, string][] = [
    ['⌘K / Ctrl K', t.scPalette], ['/', t.scFocus], ['?', t.scHelp],
    ...allItems.map((i) => [`g ${i.key}`, t.scGo(i.label)] as [string, string]),
    ['j / k', t.scJK], ['Enter', t.scEnter], ['x', t.scX],
    ['⌘A', t.scAll], ['Esc', t.scEsc],
  ]
  return (
    <ModalDialog isOpen={open} onOpenChange={onOpenChange} title={t.shortcuts}>
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-[13px]">
        {rows.map(([k, v]) => <div key={k} className="contents"><dt><kbd className="rounded bg-overlay px-1.5 py-0.5 font-mono text-[12px]">{k}</kbd></dt><dd className="text-subtle">{v}</dd></div>)}
      </dl>
    </ModalDialog>
  )
}

/** Keeps a rendering error inside the page instead of blanking the whole app. */
class PageBoundary extends Component<{ children: ReactNode }, { err: Error | null }> {
  state = { err: null as Error | null }
  static getDerivedStateFromError(err: Error) { return { err } }
  render() {
    if (!this.state.err) return this.props.children
    return (
      <div className="rounded-[16px] border bg-surface p-6 text-[13px]">
        <div className="text-[15px] font-medium text-bad">{tr(SH).pageError}</div>
        <pre className="mt-2 font-mono text-xs break-all whitespace-pre-wrap text-muted">{this.state.err.message}</pre>
        <button type="button" onClick={() => this.setState({ err: null })} className="mt-3 h-8 cursor-pointer rounded-full bg-overlay px-4 text-sm font-medium hover:bg-hl-med">{tr(SH).retry}</button>
      </div>
    )
  }
}
