import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { FileTrigger } from 'react-aria-components'
import { api, apiUpload, SUB_CLIENTS, type SiteSettings, type SubSupportLink, type SubTheme, type SubThemeField, type User } from '../../lib/api'
import { href } from '../../lib/router'
import { useAction } from '../../lib/actions'
import {
  Badge, Button, Callout, Card, CheckList, Confirm, Field, IconButton, ModalDialog, Presence, Segmented, Select, Skeleton, Switch, cx, useSticky,
} from '../../ui/kit'
import { IconPlus, IconTrash } from '../../ui/icons'
import { getLang, tr, useT } from '../../i18n'
import { P } from '../../i18n/subpage'

const icons = () => { const t = tr(P); return [{ id: 'link', label: t.iLink }, { id: 'telegram', label: 'Telegram' }, { id: 'chat', label: t.iChat }, { id: 'book', label: t.iBook }, { id: 'mail', label: t.iMail }] }

/** Subscriptions → Subscription page: what users see when they open their link in a browser. */
export function SubPageTab() {
  const [previewId, setPreviewId] = useState('')
  const t = useT(P)
  const themes = useQuery({ queryKey: ['sub-themes'], queryFn: () => api<SubTheme[]>('/sub-themes') })
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => api<SiteSettings>('/settings') })
  const list = themes.data ?? []
  const preview = list.find((x) => x.id === previewId) ?? list.find((x) => x.active)
  // Bump the preview whenever saved content or theme data changes.
  const version = `${themes.dataUpdatedAt}-${settings.dataUpdatedAt}`
  return (
    <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_440px]">
      <div className="flex min-w-0 flex-col gap-4">
        <Callout>
          {t.intro(<span className="font-mono">docs/SUB-THEMES.md</span>, (l) => <a href={href('settings', { s: 'appearance' })} className="font-medium text-accent hover:underline">{l}</a>)}
        </Callout>
        <SiteContent />
        <Themes list={list} loading={themes.isLoading} previewId={preview?.id} onPreview={setPreviewId} />
      </div>
      {preview && <Preview theme={preview} version={version} />}
    </div>
  )
}

function SiteContent() {
  const s = useQuery({ queryKey: ['settings'], queryFn: () => api<SiteSettings>('/settings') })
  const [ann, setAnn] = useState('')
  const t = useT(P)
  const [logo, setLogo] = useState('')
  const [links, setLinks] = useState<SubSupportLink[]>([])
  const [clients, setClients] = useState<string[]>([])
  useEffect(() => {
    if (!s.data) return
    setAnn(s.data.sub_announcement ?? '')
    setLogo(s.data.sub_logo_url ?? '')
    try { setLinks(JSON.parse(s.data.sub_support_links || '[]') ?? []) } catch { setLinks([]) }
    setClients(s.data.sub_clients ? s.data.sub_clients.split(',') : SUB_CLIENTS.map((c) => c.id))
  }, [s.data])
  const save = useAction({
    fn: () => api('/settings', {
      method: 'PUT', body: {
        sub_announcement: ann, sub_logo_url: logo,
        sub_support_links: JSON.stringify(links.filter((l) => l.label.trim() || l.url.trim())),
        // all selected = no allow-list, so clients added later show up automatically
        sub_clients: clients.length === SUB_CLIENTS.length ? '' : SUB_CLIENTS.filter((c) => clients.includes(c.id)).map((c) => c.id).join(','),
      },
    }),
    success: t.contentSaved, invalidate: [['settings'], ['sub-themes']],
  })
  const setLink = (i: number, p: Partial<SubSupportLink>) => setLinks(links.map((l, j) => (j === i ? { ...l, ...p } : l)))
  return (
    <div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-1 2xl:grid-cols-2">
      <Card title={t.content} action={<Button size="sm" variant="primary" isPending={save.isPending} onPress={() => save.mutate()}>{t.save}</Button>}>
        <div className="flex flex-col gap-4">
          <Field label={t.ann} multiline rows={4} value={ann} onChange={setAnn} placeholder={t.annPh} description={t.annDesc} />
          <Field label={t.logo} value={logo} onChange={setLogo} placeholder="https://…/logo.png" description={t.logoDesc} />
          <div className="flex flex-col gap-2">
            <span className="text-[13px] font-medium text-subtle">{t.help}</span>
            {links.length === 0 && <span className="text-[13px] text-muted">{t.noLinks}</span>}
            {links.map((l, i) => (
              <div key={i} className="grid grid-cols-[128px_minmax(0,1fr)_auto] items-center gap-2 rounded-[10px] border bg-base p-2.5">
                <Select hideLabel label={t.icon} value={l.icon || 'link'} onChange={(v) => setLink(i, { icon: v })} options={icons()} />
                <Field hideLabel label={t.name} value={l.label} onChange={(v) => setLink(i, { label: v })} placeholder={t.namePh} />
                <IconButton label={t.removeLink} onPress={() => setLinks(links.filter((_, j) => j !== i))}><IconTrash size={15} /></IconButton>
                <Field hideLabel label={t.link} className="col-span-3" mono value={l.url} onChange={(v) => setLink(i, { url: v })} placeholder="https:// · mailto: · tg://" />
              </div>
            ))}
            <div><Button size="sm" isDisabled={links.length >= 8} onPress={() => setLinks([...links, { label: '', url: '', icon: 'link' }])}><IconPlus size={14} />{t.add}</Button></div>
          </div>
        </div>
      </Card>
      <Card title={t.clients} action={<Button size="sm" variant="primary" isPending={save.isPending} onPress={() => save.mutate()}>{t.save}</Button>}>
        <CheckList label={t.showOnPage} options={SUB_CLIENTS} value={clients} onChange={setClients} />
        <p className="mt-3 text-xs text-muted">{t.clientsHint}</p>
      </Card>
    </div>
  )
}

function Themes({ list, loading, previewId, onPreview }: { list: SubTheme[]; loading: boolean; previewId?: string; onPreview: (id: string) => void }) {
  const qc = useQueryClient()
  const t = useT(P)
  const [editing, setEditing] = useState<SubTheme | null>(null)
  const [deleting, setDeleting] = useState<SubTheme | null>(null)
  const [uploadErr, setUploadErr] = useState('')
  const activate = useAction({ fn: (id: string) => api('/sub-themes/active', { method: 'PUT', body: { id } }), success: t.activated, invalidate: [['sub-themes']] })
  const del = useAction({ fn: (id: string) => api(`/sub-themes/${id}`, { method: 'DELETE' }), success: t.themeDeleted, invalidate: [['sub-themes']] })
  const upload = useAction({
    fn: (f: File) => apiUpload<SubTheme>('/sub-themes', f),
    success: (x) => t.installed(x.name, x.version), invalidate: [['sub-themes']],
    onSuccess: (x) => { setUploadErr(''); onPreview(x.id) },
  })
  const delSticky = useSticky(deleting)
  useEffect(() => { if (upload.error) setUploadErr((upload.error as Error).message) }, [upload.error])

  return (
    <Card title={t.themes} action={
      <FileTrigger acceptedFileTypes={['.zip', 'application/zip']} onSelect={(fl) => { const f = fl?.[0]; if (f) { setUploadErr(''); upload.mutate(f) } }}>
        <Button size="sm" variant="primary" isPending={upload.isPending}><IconPlus size={14} />{t.uploadTheme}</Button>
      </FileTrigger>
    }>
      {uploadErr && <div className="mb-3"><Callout tone="bad"><b>{t.uploadFailed}</b>{uploadErr}</Callout></div>}
      {loading && <Skeleton className="h-32" />}
      <div className="grid gap-3 sm:grid-cols-2 2xl:grid-cols-3">
        {list.map((th) => (
          <article key={th.id} className={cx('flex min-w-0 flex-col overflow-hidden rounded-[12px] border bg-base', th.active && 'border-accent ring-1 ring-accent')}>
            <button type="button" onClick={() => onPreview(th.id)} aria-label={t.previewX(th.name)} aria-pressed={previewId === th.id}
              className="relative aspect-[16/10] cursor-pointer overflow-hidden bg-overlay outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-inset">
              <Thumb theme={th} />
              {previewId === th.id && <span className="absolute top-2 left-2 rounded-full bg-surface/90 px-2 py-0.5 text-[11px] font-medium text-subtle">{t.previewing}</span>}
            </button>
            <div className="flex flex-1 flex-col gap-2 p-3">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{th.name} <span className="font-mono text-xs text-muted">{th.version}</span></div>
                  <div className="truncate text-xs text-muted">{th.author || '—'}{th.builtin && t.builtin}</div>
                </div>
                {th.active && <Badge tone="good" dot>{t.inUse}</Badge>}
              </div>
              {th.description && <p className="line-clamp-2 text-xs text-subtle">{th.description}</p>}
              <div className="mt-auto flex flex-wrap gap-2 pt-1">
                {!th.active && <Button size="sm" variant="primary" isPending={activate.isPending && activate.variables === th.id} onPress={() => activate.mutate(th.id)}>{t.activate}</Button>}
                <Button size="sm" isDisabled={th.schema.every((f) => f.type === 'title')} onPress={() => setEditing(th)}>{t.themeSettings}</Button>
                {!th.builtin && <Button size="sm" variant="danger" onPress={() => setDeleting(th)}>{t.delete}</Button>}
              </div>
            </div>
          </article>
        ))}
      </div>
      <p className="mt-3 text-xs text-muted">{t.pkgHint(<span className="font-mono">sbp-theme.json</span>, <span className="font-mono">index.html</span>)}</p>
      <Presence value={editing}>{(th, open) => <ThemeSettings theme={th} isOpen={open} onClose={() => setEditing(null)} onSaved={() => qc.invalidateQueries({ queryKey: ['sub-themes'] })} />}</Presence>
      <Confirm isOpen={!!deleting} onOpenChange={(v) => !v && setDeleting(null)} danger title={t.deleteTheme} confirmLabel={t.deleteTheme}
        onConfirm={() => deleting && del.mutate(deleting.id)}
        body={t.deleteBody(<b className="text-text">{delSticky?.name}</b>, !!delSticky?.active)} />
    </Card>
  )
}

/** Theme thumbnail from the manifest's preview image; a drawn placeholder when absent or broken. */
function Thumb({ theme }: { theme: SubTheme }) {
  const [broken, setBroken] = useState(false)
  const t = useT(P)
  if (theme.preview_url && !broken) {
    return <img src={theme.preview_url} alt="" onError={() => setBroken(true)} className="h-full w-full object-cover object-top" loading="lazy" />
  }
  return (
    <span aria-hidden className="flex h-full flex-col gap-2 bg-[linear-gradient(135deg,var(--overlay),var(--hl-low))] p-4 text-left">
      <span className="flex items-center gap-2"><span className="grid h-6 w-6 place-items-center rounded-[7px] bg-accent text-[11px] font-bold text-on-accent">{theme.name.slice(0, 1)}</span><span className="h-2 w-16 rounded-full bg-hl-high" /></span>
      <span className="flex flex-1 gap-2">
        <span className="flex-[2] rounded-[8px] border bg-surface p-2"><span className="block h-2 w-1/2 rounded-full bg-hl-high" /><span className="mt-2 block h-6 w-6 rounded-full border-[3px] border-accent" /></span>
        <span className="flex-1 rounded-[8px] border bg-surface p-2"><span className="block h-2 w-3/4 rounded-full bg-hl-high" /><span className="mt-2 block h-3 rounded-[4px] bg-accent/70" /></span>
      </span>
      <span className="text-[11px] text-muted">{broken ? t.thumbBroken : t.thumbNone} · {t.clickPreview}</span>
    </span>
  )
}

const samples = () => { const t = tr(P); return [{ id: 's:active', label: t.sActive }, { id: 's:limited', label: t.sLimited }, { id: 's:expired', label: t.sExpired }] }

function Preview({ theme, version }: { theme: SubTheme; version: string }) {
  const users = useQuery({ queryKey: ['users'], queryFn: () => api<User[]>('/users') })
  const [who, setWho] = useState('s:active')
  const t = useT(P)
  const [wide, setWide] = useState(false)
  const [scheme, setScheme] = useState<'auto' | 'light' | 'dark'>('auto')
  const src = useMemo(() => {
    const q = new URLSearchParams(who.startsWith('s:') ? { state: who.slice(2) } : { user: who })
    q.set('v', version)
    q.set('lang', getLang())
    return `/api/sub-themes/${encodeURIComponent(theme.id)}/preview?${q}`
  }, [theme.id, who, version])
  const options = [...samples(), ...(users.data ?? []).slice(0, 200).map((u) => ({ id: String(u.id), label: `${u.username}${u.status !== 'active' || !u.enabled ? t.unavailable : ''}` }))]
  return (
    <Card title={<>{t.livePreview} <span className="text-[13px] font-normal text-muted">{theme.name}</span></>} className="xl:sticky xl:top-20 xl:self-start">
      <div className="mb-3 flex flex-col gap-2">
        <Select hideLabel label={t.previewUser} value={who} onChange={setWho} options={options} />
        <div className="flex flex-wrap gap-2">
          <Segmented label={t.width} value={wide ? 'w' : 'n'} onChange={(v) => setWide(v === 'w')} options={[{ id: 'n', label: t.phone }, { id: 'w', label: t.desktop }]} />
          <Segmented label={t.scheme} value={scheme} onChange={setScheme} options={[{ id: 'auto', label: t.followPanel }, { id: 'light', label: t.light }, { id: 'dark', label: t.dark }]} />
        </div>
      </div>
      <div className={cx('mx-auto overflow-hidden rounded-[16px] border bg-base', !wide && 'max-w-[377px]')}>
        {/* The page is served with a CSP sandbox (opaque origin). Its prefers-color-scheme follows this element's color-scheme. */}
        <div className={cx(wide && 'overflow-x-auto')}>
          <iframe key={src + scheme} title={t.previewTitle(theme.name)} src={src} referrerPolicy="no-referrer"
            sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals allow-top-navigation-to-custom-protocols" style={scheme === 'auto' ? undefined : { colorScheme: scheme }}
            className={cx('block h-[680px] border-0', wide ? 'w-[1100px] max-w-none origin-top-left' : 'w-full')} />
        </div>
      </div>
      <p className="mt-2 text-xs text-muted">{who.startsWith('s:') ? t.sampleNote : t.realNote}{t.autoRefresh}</p>
    </Card>
  )
}

function ThemeSettings({ theme, isOpen, onClose, onSaved }: { theme: SubTheme; isOpen: boolean; onClose: () => void; onSaved: () => void }) {
  const [v, setV] = useState<Record<string, unknown>>(theme.settings)
  const t = useT(P)
  const dirty = JSON.stringify(v) !== JSON.stringify(theme.settings)
  const save = useAction({
    fn: () => api(`/sub-themes/${encodeURIComponent(theme.id)}/settings`, { method: 'PUT', body: v }),
    success: t.settingsSaved, onSuccess: () => { onSaved(); onClose() },
  })
  return (
    <ModalDialog isOpen={isOpen} onOpenChange={(o) => !o && onClose()} title={t.settingsTitle(theme.name)} wide dirty={dirty}
      footer={<><Button onPress={onClose}>{t.cancel}</Button><Button variant="primary" isPending={save.isPending} isDisabled={!dirty} onPress={() => save.mutate()}>{t.save}</Button></>}>
      <div className="flex flex-col gap-4">
        {theme.schema.map((f, i) => <SchemaField key={f.key ?? `t${i}`} f={f} value={f.key ? v[f.key] : undefined} onChange={(x) => f.key && setV({ ...v, [f.key]: x })} />)}
      </div>
    </ModalDialog>
  )
}

/** Renders one manifest setting (see SubThemeField). */
function SchemaField({ f, value, onChange }: { f: SubThemeField; value: unknown; onChange: (v: unknown) => void }) {
  const help = f.help || undefined
  switch (f.type) {
    case 'title':
      return <h3 className="-mb-1 border-t pt-4 text-[13px] font-semibold first:border-0 first:pt-0">{f.label}</h3>
    case 'switch':
      return <div className="flex flex-col gap-1"><Switch isSelected={!!value} onChange={onChange}>{f.label}</Switch>{help && <span className="pl-11 text-xs text-muted">{help}</span>}</div>
    case 'select':
      return <Select label={f.label} description={help} value={String(value ?? '')} onChange={onChange} options={(f.options ?? []).map((o) => ({ id: o.value, label: o.label }))} />
    case 'multiselect':
      return <div><CheckList label={f.label} options={(f.options ?? []).map((o) => ({ id: o.value, label: o.label }))} value={(value as string[]) ?? []} onChange={onChange} />{help && <p className="mt-1 text-xs text-muted">{help}</p>}</div>
    case 'color': {
      const s = String(value ?? '')
      return (
        <div className="flex items-end gap-2">
          <input type="color" aria-label={tr(P).pickColor(f.label)} value={/^#[0-9a-f]{6}$/i.test(s) ? s : '#1f6a88'} onChange={(e) => onChange(e.target.value)}
            className="h-9 w-11 shrink-0 cursor-pointer rounded-[10px] border bg-base p-1" />
          <Field className="flex-1" label={f.label} description={help} mono value={s} onChange={onChange} placeholder={tr(P).colorPh} />
        </div>
      )
    }
    case 'number':
      return <Field label={f.label} description={help} inputMode="decimal" value={value == null ? '' : String(value)} onChange={(x) => onChange(x === '' ? 0 : Number(x))} />
    case 'textarea':
      return <Field label={f.label} description={help} multiline rows={4} value={String(value ?? '')} onChange={onChange} />
    default:
      return <Field label={f.label} description={help} value={String(value ?? '')} onChange={onChange} placeholder={f.type === 'url' ? 'https://…' : undefined} />
  }
}
