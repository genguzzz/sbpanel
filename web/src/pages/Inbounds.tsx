import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type Group, type Inbound, type Node } from '../lib/api'
import { useAction, useApplyTracker } from '../lib/actions'
import { useRoute, replace } from '../lib/router'
import { Badge, Button, Callout, Card, CheckList, Confirm, Empty, Field, IconButton, ModalDialog, PageHeader, Presence, Segmented, Select, Skeleton, Spinner, Switch, cx, useSticky } from '../ui/kit'
import { IconEdit, IconPlus, IconTrash } from '../ui/icons'
import { HealthDot, IconProbe, ProbeDrawer, useProbeResults, useRunProbe, type ProbeTarget } from '../ui/probe'
import { certLabel, covers, matchCert, useCerts, useDomains, type Cert } from '../lib/certs'
import { href } from '../lib/router'
import { PRESETS, applyCertChoice, choiceOf, grpcName, isHostname, splitHostPort, type CertChoice, type PresetId, type RealityKeypair } from '../lib/presets'
import { tr } from '../i18n'
import { C } from '../i18n/common'
import { IB } from '../i18n/inbounds'

const T = () => tr(IB)

export type { CertChoice } from '../lib/presets'
export { applyCertChoice } from '../lib/presets'

/** Card text for each preset (the definitions live in lib/presets.ts). */
const presetText = (id: PresetId): { desc: string; tags: string[] } => {
  const t = T()
  switch (id) {
    case 'vless-reality': return { desc: t.dReality, tags: [] }
    case 'vmess-ws-tls': case 'vless-ws-tls': return { desc: t.dWs, tags: [t.needsDomain, t.needsCert] }
    case 'vless-grpc-tls': return { desc: t.dGrpc, tags: [t.needsDomain, t.needsCert] }
    case 'trojan-tls': return { desc: t.dTrojan, tags: [t.needsDomain, t.needsCert] }
    case 'ss2022': return { desc: t.dSs, tags: [] }
    case 'hysteria2': return { desc: t.dHy2, tags: ['UDP', t.needsDomain, t.needsCert] }
    case 'tuic': return { desc: t.dTuic, tags: ['UDP', t.needsDomain, t.needsCert] }
    case 'anytls': return { desc: t.dAnytls, tags: [t.needsDomain, t.needsCert] }
  }
}

const randPort = () => 20000 + Math.floor(Math.random() * 40000)
const randomKey = () => { const k = new Uint8Array(16); crypto.getRandomValues(k); return btoa(String.fromCharCode(...k)) }
const realityKeypair = () => api<RealityKeypair>('/tools/reality-keypair', { method: 'POST' })

/** UDP / TCP use of an inbound, for port clash checks. */
function transportsOf(type: string, opts: Record<string, any>): Array<'tcp' | 'udp'> {
  if (type === 'hysteria2' || type === 'tuic') return ['udp']
  if (type === 'shadowsocks') return opts.network === 'tcp' ? ['tcp'] : opts.network === 'udp' ? ['udp'] : ['tcp', 'udp']
  return ['tcp']
}

export function InboundsPage() {
  const { params } = useRoute()
  const inbounds = useQuery({ queryKey: ['inbounds'], queryFn: () => api<Inbound[]>('/inbounds') })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<Node[]>('/nodes') })
  const groups = useQuery({ queryKey: ['groups'], queryFn: () => api<Group[]>('/groups') })
  const [editing, setEditing] = useState<Inbound | 'new' | null>(() => (params.get('new') === '1' ? 'new' : null))
  const [preNode] = useState(() => params.get('node') ?? '')
  useEffect(() => { if (params.get('new')) replace('inbounds') }, [params])
  const [deleting, setDeleting] = useState<Inbound | null>(null)
  const delSticky = useSticky(deleting)
  const toggle = useAction({
    fn: (i: Inbound) => api(`/inbounds/${i.id}`, { method: 'PATCH', body: { enabled: !i.enabled } }),
    apply: true, success: (_, i) => T().toggled(i.tag, i.enabled), invalidate: [['inbounds'], ['nodes']],
  })
  const del = useAction({
    fn: (id: number) => api(`/inbounds/${id}`, { method: 'DELETE' }),
    apply: true, success: T().deleted, invalidate: [['inbounds'], ['nodes'], ['groups']],
  })

  const probes = useProbeResults()
  const runProbe = useRunProbe()
  const [probeView, setProbeView] = useState<{ title: string; target: ProbeTarget } | null>(null)
  const probeSticky = useSticky(probeView)
  const probeOf = (id: number) => (probes.data ?? []).filter((r) => r.inbound_id === id)
  const runningFor = (t: ProbeTarget) => runProbe.isPending && (runProbe.variables?.all || runProbe.variables?.inbound_id === t.inbound_id && !!t.inbound_id)
  const test = (title: string, target: ProbeTarget, open = true) => { runProbe.mutate(target); if (open) setProbeView({ title, target }) }
  const viewResults = (() => {
    const t = probeSticky?.target
    if (!t) return []
    return (probes.data ?? []).filter((r) => t.all || r.inbound_id === t.inbound_id)
  })()

  const byNode = new Map<string, Inbound[]>()
  for (const i of inbounds.data ?? []) byNode.set(i.node_id, [...(byNode.get(i.node_id) ?? []), i])

  return (
    <>
      <PageHeader title={T().title} subtitle={T().subtitle}
        actions={<>
          <Button isDisabled={!inbounds.data?.length} isPending={runProbe.isPending && !!runProbe.variables?.all} onPress={() => test(T().allTitle, { all: true })}>{T().testAll}</Button>
          <Button variant="primary" isDisabled={!nodes.data?.length} onPress={() => setEditing('new')}><IconPlus size={16} />{T().newInbound}</Button>
        </>} />
      {inbounds.isLoading && <Skeleton className="h-48" />}
      {inbounds.data?.length === 0 && (
        <Card><Empty title={T().empty} hint={nodes.data?.length ? T().emptyHint : T().emptyNoNode}
          action={nodes.data?.length ? <Button variant="primary" onPress={() => setEditing('new')}>{T().newInbound}</Button> : undefined} /></Card>
      )}
      <div className="flex flex-col gap-4">
        {(nodes.data ?? []).filter((n) => byNode.has(n.id)).map((n) => (
          <Card key={n.id} title={<span>{n.name} <span className="ml-1 text-[13px] font-normal text-subtle">{n.address}</span></span>} pad={false}>
            <div className="overflow-x-auto">
            <table className="w-full text-[13px] whitespace-nowrap">
              <thead className="text-left text-xs text-subtle">
                <tr className="border-b"><th className="w-0 py-2 pr-0 pl-3 font-medium"><span className="sr-only">{T().conn}</span></th><th className="px-3 py-2 font-medium">{T().colTag}</th><th className="px-3 py-2 font-medium">{T().colProto}</th>
                  <th className="px-3 py-2 font-medium">{T().colPort}</th><th className="hidden px-3 py-2 font-medium md:table-cell">{T().colTransport}</th>
                  <th className="px-3 py-2 font-medium">{T().colEnabled}</th><th className="px-5 py-2" /></tr>
              </thead>
              <tbody>
                {byNode.get(n.id)!.map((i) => (
                  <tr key={i.id} className="border-b last:border-0 hover:bg-hl-low">
                    <td className="py-2.5 pr-0 pl-3"><HealthDot results={probeOf(i.id)} running={runningFor({ inbound_id: i.id })} onPress={() => probeOf(i.id).length ? setProbeView({ title: T().connOf(i.tag), target: { inbound_id: i.id } }) : test(T().connOf(i.tag), { inbound_id: i.id })} /></td>
                    <td className="px-3 py-2.5 font-medium">{i.tag}{i.remark && <div className="text-xs font-normal text-muted">{i.remark}</div>}</td>
                    <td className="px-3 py-2.5"><Badge tone="accent">{i.type}</Badge></td>
                    <td className="px-3 py-2.5 font-mono tabular">{i.port}</td>
                    <td className="hidden px-3 py-2.5 text-subtle md:table-cell">{describe(i)}</td>
                    <td className="px-3 py-2.5"><Switch label={T().enableX(i.tag)} isSelected={i.enabled} isDisabled={toggle.isPending && toggle.variables?.id === i.id} onChange={() => toggle.mutate(i)} /></td>
                    <td className="px-5 py-2.5 text-right whitespace-nowrap">
                      <IconButton label={T().testX(i.tag)} isDisabled={!i.enabled} onPress={() => test(T().connOf(i.tag), { inbound_id: i.id })}><IconProbe /></IconButton>
                      <IconButton label={tr(C).edit} onPress={() => setEditing(i)}><IconEdit size={16} /></IconButton>
                      <IconButton label={tr(C).delete} onPress={() => setDeleting(i)}><IconTrash size={16} /></IconButton>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            </div>
          </Card>
        ))}
      </div>
      <Presence value={nodes.data ? editing : null}>
        {(e, open) => <InboundEditor isOpen={open} nodes={nodes.data ?? []} groups={groups.data ?? []} inbounds={inbounds.data ?? []} value={e === 'new' ? null : e} preNode={preNode} onClose={() => setEditing(null)} />}
      </Presence>
      <ProbeDrawer isOpen={!!probeView} onClose={() => setProbeView(null)} title={probeSticky?.title ?? ''} results={viewResults}
        running={!!probeSticky && runProbe.isPending} onRetry={() => probeSticky && runProbe.mutate(probeSticky.target)} />
      <Confirm isOpen={!!deleting} onOpenChange={(v) => !v && setDeleting(null)} danger title={T().delTitle} confirmLabel={T().delTitle}
        onConfirm={() => deleting && del.mutate(deleting.id)} body={T().delBody(<b className="text-text">{delSticky?.tag}</b>, delSticky?.node ?? '', delSticky?.port ?? '')} />
    </>
  )
}

function describe(i: Inbound): string {
  const o = i.options as any
  const parts: string[] = []
  if (o.transport?.type) parts.push(o.transport.type + (o.transport.path ? ' ' + o.transport.path : ''))
  if (o.tls?.reality?.enabled) parts.push('REALITY · ' + (o.tls.server_name ?? ''))
  else if (o.tls?.enabled) parts.push('TLS' + (o.tls.server_name ? ' · ' + o.tls.server_name : ''))
  if (o.method) parts.push(o.method)
  return parts.join(' · ') || 'tcp'
}

type DnsCheck = { host: string; ips: string[]; node_ips: string[]; status: 'ok' | 'partial' | 'mismatch' | 'none' | 'unknown'; proxied: boolean; error?: string }

function useDebounced<V>(v: V, ms: number): V {
  const [d, setD] = useState(v)
  useEffect(() => { const t = setTimeout(() => setD(v), ms); return () => clearTimeout(t) }, [v, ms])
  return d
}

/** Field error for the TLS domain, or '' when it is usable. */
function domainError(d: string): string {
  const v = d.trim()
  if (!v) return T().domainNeeded
  if (/^[\d.]+$/.test(v) || v.includes(':')) return T().domainNotIp
  return isHostname(v) ? '' : T().domainBad
}

/**
 * Whether the domain resolves to the node, checked from the master: the most common reason a new
 * TLS inbound "doesn't work" is a domain pointing somewhere else.
 */
function DnsStatus({ host, nodeId, nodeName, cdn, protocol, acme }: { host: string; nodeId: string; nodeName: string; cdn: boolean; protocol: string; acme: boolean }) {
  const h = useDebounced(host.trim().toLowerCase(), 500)
  const ok = !!nodeId && isHostname(h)
  const q = useQuery({
    queryKey: ['dns-check', h, nodeId], enabled: ok, staleTime: 30_000, retry: false,
    queryFn: () => api<DnsCheck>(`/tools/dns-check?host=${encodeURIComponent(h)}&node=${encodeURIComponent(nodeId)}`),
  })
  if (!ok) return null
  if (q.isPending || h !== host.trim().toLowerCase()) return <Callout><span className="flex items-center gap-2"><Spinner size={12} />{T().dnsChecking}</span></Callout>
  if (q.isError) return <Callout tone="warn">{T().dnsError((q.error as Error).message)}</Callout>
  const d = q.data
  const t = T()
  const list = (a: string[]) => a.join(', ')
  let tone: 'good' | 'warn' | 'neutral' | 'bad' = 'warn'
  let text: string
  if (d.proxied) { tone = cdn ? 'neutral' : 'warn'; text = cdn ? t.dnsProxied : t.dnsProxiedNoCdn(protocol) }
  else if (d.status === 'ok') { tone = 'good'; text = t.dnsOk(list(d.ips), nodeName) }
  else if (d.status === 'partial') text = t.dnsPartial(list(d.ips.filter((ip) => !d.node_ips.includes(ip))), nodeName)
  else if (d.status === 'mismatch') text = t.dnsMismatch(list(d.ips), nodeName, list(d.node_ips))
  else if (d.status === 'unknown') { tone = 'neutral'; text = t.dnsUnknown(nodeName) }
  else text = d.error ? t.dnsError(d.error) : t.dnsNone(d.host)
  const acmeBad = acme && (d.proxied || d.status !== 'ok')
  return (
    <Callout tone={acmeBad ? 'bad' : tone}>
      <div data-testid="dns-status" data-status={d.proxied ? 'proxied' : d.status}>{text}</div>
      {acmeBad && <div className="mt-1">{t.dnsAcmeBad}</div>}
      {tone !== 'good' && (
        <div className="mt-1 flex gap-3 text-xs">
          <button type="button" className="cursor-pointer underline-offset-2 hover:underline" onClick={() => q.refetch()}>{t.recheck}</button>
          <a className="underline-offset-2 hover:underline" href={href('certs')}>{t.dnsManage}</a>
        </div>
      )}
    </Callout>
  )
}

function InboundEditor({ nodes, groups, inbounds, value, onClose, isOpen, preNode }: { nodes: Node[]; groups: Group[]; inbounds: Inbound[]; value: Inbound | null; onClose: () => void; isOpen: boolean; preNode?: string }) {
  const qc = useQueryClient()
  const track = useApplyTracker()
  const [nodeId, setNodeId] = useState(value?.node_id ?? (nodes.some((n) => n.id === preNode) ? preNode! : nodes[0]?.id ?? ''))
  const node = nodes.find((n) => n.id === nodeId)
  const [preset, setPreset] = useState<PresetId>('vless-reality')
  const p = PRESETS.find((x) => x.id === preset)!
  const [tag, setTag] = useState(value?.tag ?? '')
  const [tagTouched, setTagTouched] = useState(!!value)
  const [remark, setRemark] = useState(value?.remark ?? '')
  const region0 = String((value?.options as Record<string, unknown> | undefined)?._region ?? '')
  const [region, setRegion] = useState(region0)
  const [port, setPort] = useState(String(value?.port ?? randPort()))
  // REALITY borrows someone else's site; TLS presets need the admin's own domain. Kept apart so
  // switching protocols never turns www.microsoft.com into "your" domain.
  const [site, setSite] = useState('www.microsoft.com')
  const [handshake, setHandshake] = useState('')
  const [domain, setDomain] = useState('')
  const [domainTouched, setDomainTouched] = useState(false)
  const [ws, setWs] = useState('/ws')
  const [grpc, setGrpc] = useState('grpc')
  const certs = useCerts()
  const domains = useDomains()
  const [cert, setCertRaw] = useState<CertChoice>({ src: 'path', certId: '', certPath: '', keyPath: '', email: '' })
  const [certTouched, setCertTouched] = useState(false)
  const setCert = (c: CertChoice) => { setCertRaw(c); setCertTouched(true); setJsonTouched(false) }
  const [gids, setGids] = useState<number[]>(value?.group_ids ?? groups.filter((g) => g.is_default).map((g) => g.id))
  const [json, setJson] = useState(value ? JSON.stringify(value.options, null, 2) : '')
  const [client, setClient] = useState(value ? JSON.stringify(value.client, null, 2) : '{}')
  // For new inbounds the JSON is generated from the basic fields at save time unless the admin edited it by hand.
  const [jsonTouched, setJsonTouched] = useState(!!value)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  // errors show once a field was edited, or after a save attempt
  const [tried, setTried] = useState(false)
  const [type, setType] = useState(value?.type ?? p.type)
  const portN = Number(port)
  const t = T()

  useEffect(() => { if (!value && !tagTouched) setTag(`${p.id}-${port}`) }, [p, port, value, tagTouched])
  // suggest the domain bound to the node under Domains & certificates
  useEffect(() => {
    if (value || domainTouched) return
    setDomain(domains.data?.find((x) => x.node_id === nodeId)?.fqdn ?? '')
  }, [value, domainTouched, nodeId, domains.data])
  // and a panel certificate covering it
  useEffect(() => {
    if (value || certTouched || !certs.data) return
    const m = isHostname(domain) ? matchCert(certs.data, domain.trim(), nodeId) : undefined
    setCertRaw((c) => m ? { ...c, src: 'panel', certId: m.id } : { ...c, src: c.src === 'panel' ? 'path' : c.src, certId: '' })
  }, [value, certTouched, certs.data, domain, nodeId])
  useEffect(() => { if (!value && !jsonTouched) setType(p.type) }, [p, value, jsonTouched])

  // field errors (new inbounds built from the form; hand-edited JSON is taken as is)
  const errs = useMemo(() => {
    const e: Record<string, string> = {}
    if (value) {
      if (inbounds.some((i) => i.id !== value.id && i.tag === tag.trim())) e.tag = t.tagUsed
      return e
    }
    if (inbounds.some((i) => i.tag === tag.trim())) e.tag = t.tagUsed
    if (jsonTouched) return e
    if (!Number.isInteger(portN) || portN < 1 || portN > 65535) e.port = t.portRange
    else {
      const mine = transportsOf(p.type, { network: p.id === 'ss2022' ? 'tcp' : undefined })
      const clash = inbounds.find((i) => i.node_id === nodeId && i.port === portN && transportsOf(i.type, i.options as Record<string, any>).some((x) => mine.includes(x)))
      if (clash) e.port = t.portUsed(clash.tag)
    }
    if (p.tls) {
      const de = domainError(domain)
      if (de) e.domain = de
      if (cert.src === 'path' && (!cert.certPath.trim() || !cert.keyPath.trim())) e.cert = t.certPathNeeded
      if (cert.src === 'panel') {
        const sel = certs.data?.find((c) => c.id === cert.certId)
        if (!sel) e.cert = t.pickCert
        else if (!de && !covers(sel.sans, domain.trim())) e.cert = t.certPanelBad(domain.trim())
      }
    }
    if (p.id === 'vless-reality') {
      if (!isHostname(site)) e.site = t.realityBad
      const [h, hp] = splitHostPort(handshake)
      if (handshake.trim() && (!h || !hp)) e.handshake = t.handshakeBad
    }
    if (p.path === 'ws' && !/^\/?[\x21-\x7e]*$/.test(ws.trim())) e.path = t.pathBad
    if (p.path === 'grpc' && !/^[A-Za-z0-9._-]+$/.test(grpcName(grpc))) e.path = t.grpcBad
    return e
  }, [value, inbounds, tag, jsonTouched, portN, p, nodeId, domain, cert, certs.data, site, handshake, ws, grpc, t])
  const hasErr = Object.keys(errs).length > 0

  const build = async () => {
    const first = Object.values(errs)[0]
    if (first) throw new Error(first)
    const r = await p.build({ port: portN, sni: p.tls ? domain.trim().toLowerCase() : site.trim(), handshake: handshake.trim(), path: p.path === 'grpc' ? grpc : ws, ...cert }, realityKeypair, randomKey)
    return r
  }
  const preview = async () => {
    try { const r = await build(); setJson(JSON.stringify(r.options, null, 2)); setClient(JSON.stringify(r.client, null, 2)); setErr('') } catch (e) { setErr((e as Error).message) }
  }

  const save = async () => {
    setErr('')
    if (hasErr) { setTried(true); return }
    setBusy(true)
    try {
      let options: unknown, cl: unknown
      if (!value && !jsonTouched) { const r = await build(); options = r.options; cl = r.client }
      else {
        try { options = JSON.parse(json); cl = JSON.parse(client || '{}') } catch (e) { throw new Error(t.badJson((e as Error).message)) }
      }
      if (options && typeof options === 'object') {
        const o = options as Record<string, unknown>
        if (region) o._region = region; else delete o._region
      }
      if (value) await api(`/inbounds/${value.id}`, { method: 'PATCH', body: { tag, type, options, client: cl, remark, group_ids: gids } })
      else await api('/inbounds', { method: 'POST', body: { node_id: nodeId, tag, type, options, client: cl, remark, group_ids: gids } })
      for (const k of ['groups', 'inbounds', 'nodes', 'hosts']) qc.invalidateQueries({ queryKey: [k] })
      track({ text: value ? t.savedX(tag) : t.createdX(tag) })
      onClose()
    } catch (e) { setErr((e as Error).message) } finally { setBusy(false) }
  }

  const dirty = !value ? (jsonTouched || remark !== '') : (json !== JSON.stringify(value.options, null, 2) || tag !== value.tag || remark !== value.remark || region !== region0 || gids.join() !== value.group_ids.join())
  const shown = (k: string) => !!errs[k] && (tried || (k === 'domain' ? domainTouched : k === 'cert' ? certTouched : true))
  const fieldErr = (k: string) => (shown(k) ? { isInvalid: true, errorMessage: errs[k] } : {})
  return (
    <ModalDialog wide isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={value ? t.editX(value.tag) : t.newInbound} dirty={dirty}
      footer={<>{err && <span className="mr-auto max-w-full self-center text-[13px] break-all text-bad">{err}</span>}<Button onPress={onClose}>{tr(C).cancel}</Button>
        <Button variant="primary" isPending={busy} isDisabled={!tag.trim() || (tried && hasErr) || (!!value && !json)} onPress={save}>{t.saveDeploy}</Button></>}>
      <div className="flex flex-col gap-5">
        {!value && (
          <>
            <Select label={tr(C).node} className="sm:w-64" value={nodeId} onChange={setNodeId} options={nodes.map((n) => ({ id: n.id, label: n.name }))} />
            <fieldset>
              <legend className="mb-2 text-[13px] font-medium text-subtle">{t.protocol}</legend>
              <div role="radiogroup" aria-label={t.presets} className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                {PRESETS.map((x) => {
                  const pt = presetText(x.id)
                  return (
                    <button key={x.id} type="button" role="radio" aria-checked={x.id === preset} onClick={() => { setPreset(x.id); setJsonTouched(false) }}
                      className={cx('flex cursor-pointer flex-col gap-1 rounded-[12px] border p-3 text-left outline-none transition-[border-color,background-color,box-shadow] duration-(--dur-1) focus-visible:ring-2 focus-visible:ring-accent',
                        x.id === preset ? 'border-accent bg-[color-mix(in_srgb,var(--accent)_8%,var(--surface))] shadow-[inset_0_0_0_1px_var(--accent)]' : 'bg-base hover:border-hl-high')}>
                      <span className="flex items-center gap-2">
                        <span className={cx('grid h-6 min-w-6 place-items-center rounded-[6px] px-1 font-mono text-[10px] font-semibold uppercase', x.id === preset ? 'bg-accent text-on-accent' : 'bg-overlay text-subtle')}>{x.type.slice(0, 2)}</span>
                        <span className="text-sm font-medium">{x.label}</span>
                      </span>
                      <span className="text-xs text-subtle">{pt.desc}</span>
                      {pt.tags.length > 0 && <span className="flex flex-wrap gap-1">{pt.tags.map((g) => <span key={g} className={cx('rounded-full px-1.5 text-[11px]', g === 'UDP' ? 'bg-iris/15 text-iris' : 'bg-warn/12 text-warn')}>{g}</span>)}</span>}
                    </button>
                  )
                })}
              </div>
            </fieldset>
            <div key={preset} className="grid animate-content-in gap-3 sm:grid-cols-2">
              <Field label={t.listenPort} value={port} onChange={(v) => { setPort(v); setJsonTouched(false) }} inputMode="numeric" {...fieldErr('port')} />
              {p.tls && <Field label={t.domain} value={domain} onChange={(v) => { setDomain(v); setDomainTouched(true); setJsonTouched(false) }} isRequired
                placeholder="proxy.example.com" description={t.domainHint} {...fieldErr('domain')} />}
              {p.id === 'vless-reality' && <Field label={t.realitySite} value={site} onChange={(v) => { setSite(v); setJsonTouched(false) }} description={t.sniReality} {...fieldErr('site')} />}
              {p.tls && !errs.domain && <div className="sm:col-span-2"><DnsStatus host={domain} nodeId={nodeId} nodeName={node?.name ?? ''} cdn={p.cdn} protocol={p.label} acme={cert.src === 'acme'} /></div>}
              {p.id === 'vless-reality' && <Field label={t.handshake} value={handshake} onChange={(v) => { setHandshake(v); setJsonTouched(false) }} placeholder={t.handshakeAuto(site.trim() || 'www.microsoft.com')} description={t.handshakeHint} {...fieldErr('handshake')} />}
              {p.path === 'ws' && <Field label={t.wsPath} value={ws} onChange={(v) => { setWs(v); setJsonTouched(false) }} mono {...fieldErr('path')} />}
              {p.path === 'grpc' && <Field label={t.grpcName} value={grpc} onChange={(v) => { setGrpc(v); setJsonTouched(false) }} mono {...fieldErr('path')} />}
              {p.tls && <div className="sm:col-span-2"><CertSourcePicker value={cert} onChange={setCert} certs={certs.data ?? []} nodeId={nodeId} sni={domain.trim()} error={shown('cert') ? errs.cert : undefined} /></div>}
            </div>
          </>
        )}
        {value && <EditCertSource json={json} setJson={(j) => { setJson(j); setJsonTouched(true) }} certs={certs.data ?? []} nodeId={value.node_id} nodeName={value.node} type={value.type} />}
        <div className="grid gap-3 sm:grid-cols-[1fr_1fr_9rem]">
          <Field label={t.tag} value={tag} onChange={(v) => { setTag(v); setTagTouched(true) }} mono {...fieldErr('tag')} />
          <Field label={t.remark} value={remark} onChange={setRemark} placeholder={t.optional} />
          <Field label={t.exitRegion} value={region} onChange={(v) => setRegion(v.toUpperCase().slice(0, 2))} placeholder={t.optional} description={t.exitRegionHint} />
        </div>
        <CheckList label={t.groups} value={gids} onChange={setGids}
          options={groups.map((g) => ({ id: g.id, label: g.name, hint: t.usersN(g.users) }))} />
        <details className="group rounded-[12px] border" onToggle={(e) => { if ((e.currentTarget as HTMLDetailsElement).open && !value && !jsonTouched) preview() }}>
          <summary className="flex cursor-pointer list-none items-center justify-between px-4 py-2.5 text-[13px] font-medium text-subtle outline-none hover:text-text focus-visible:ring-2 focus-visible:ring-accent [&::-webkit-details-marker]:hidden">
            {t.advanced}{jsonTouched && !value && <span className="ml-2 text-xs font-normal text-warn">{t.manual}</span>}
            <span className="ml-auto transition-transform duration-(--dur-2) group-open:rotate-180">▾</span>
          </summary>
          <div className="flex animate-content-in flex-col gap-3 border-t p-4">
            <div className="grid gap-3 sm:grid-cols-3"><Field label={t.type} value={type} onChange={(v) => { setType(v); setJsonTouched(true) }} mono /></div>
            <Field label={t.options} multiline rows={14} mono value={json} onChange={(v) => { setJson(v); setJsonTouched(true) }}
              description={t.optionsHint} />
            <Field label={t.client} multiline rows={4} mono value={client} onChange={(v) => { setClient(v); setJsonTouched(true) }} />
            {!value && <div><Button size="sm" onPress={preview}>{t.regen}</Button></div>}
          </div>
        </details>
      </div>
    </ModalDialog>
  )
}

/** Certificate source: panel certificate (select), files on the node, or node-side ACME. */
function CertSourcePicker({ value, onChange, certs, nodeId, sni, error }: { value: CertChoice; onChange: (c: CertChoice) => void; certs: Cert[]; nodeId: string; sni: string; error?: string }) {
  const usable = certs.filter((c) => c.has_material && (c.source !== 'node' || c.node_id === nodeId) || c.id === value.certId)
  const sel = certs.find((c) => c.id === value.certId)
  const mismatch = value.src === 'panel' && sel && sni && !covers(sel.sans, sni)
  const le = isHostname(sni) ? sni : 'example.com'
  return (
    <fieldset className="flex flex-col gap-3 rounded-[12px] border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <legend className="text-[13px] font-medium text-subtle">{T().certSource}</legend>
        <Segmented label={T().certSource} value={value.src} onChange={(src) => onChange({ ...value, src, certId: src === 'panel' ? (value.certId || matchCert(certs, sni, nodeId)?.id || usable[0]?.id || '') : value.certId })}
          options={[{ id: 'panel', label: T().srcPanel }, { id: 'path', label: T().srcPath }, { id: 'acme', label: T().srcAcme }]} />
      </div>
      {value.src === 'panel' && (usable.length ? (
        <>
          <Select label={T().cert} value={value.certId} onChange={(certId) => onChange({ ...value, certId })} placeholder={T().chooseCert}
            options={usable.map((c) => ({ id: c.id, label: certLabel(c) }))}
            description={T().certHint} />
          {mismatch && <span className="text-xs text-bad">{T().certMismatch(sni)}</span>}
        </>
      ) : (
        <p className="text-[13px] text-subtle">{T().noCerts}<a className="text-accent underline-offset-2 hover:underline" href={href('certs')}>{T().goCerts}</a></p>
      ))}
      {value.src === 'path' && (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={T().certPath} value={value.certPath} onChange={(certPath) => onChange({ ...value, certPath })} mono placeholder={`/etc/letsencrypt/live/${le}/fullchain.pem`} isInvalid={!!error && !value.certPath.trim()} />
          <Field label={T().keyPath} value={value.keyPath} onChange={(keyPath) => onChange({ ...value, keyPath })} mono placeholder={`/etc/letsencrypt/live/${le}/privkey.pem`} isInvalid={!!error && !value.keyPath.trim()} />
          <p className="text-xs text-muted sm:col-span-2">{T().pathNote}</p>
        </div>
      )}
      {value.src === 'acme' && (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={T().acmeEmail} value={value.email} onChange={(email) => onChange({ ...value, email })} placeholder={T().optional} />
          <p className="self-end text-xs text-muted">{T().acmeEmailNote}</p>
        </div>
      )}
      {error && !mismatch && <span className="text-xs text-bad" role="alert">{error}</span>}
    </fieldset>
  )
}

/** For existing inbounds: edits the certificate part of the JSON in place, and checks the domain's DNS. */
function EditCertSource({ json, setJson, certs, nodeId, nodeName, type }: { json: string; setJson: (j: string) => void; certs: Cert[]; nodeId: string; nodeName: string; type: string }) {
  let opts: Record<string, any> | null = null
  try { opts = JSON.parse(json) } catch { /* invalid JSON: the JSON editor shows it */ }
  if (!opts?.tls?.enabled || opts.tls.reality?.enabled) return null
  const cur = choiceOf(opts)
  const onChange = (c: CertChoice) => setJson(JSON.stringify(applyCertChoice(opts!, c), null, 2))
  const sni: string = opts.tls.server_name ?? ''
  const cdn = ['ws', 'grpc', 'httpupgrade'].includes(opts.transport?.type)
  return (
    <div className="flex flex-col gap-3">
      {isHostname(sni) && <DnsStatus host={sni} nodeId={nodeId} nodeName={nodeName} cdn={cdn} protocol={type} acme={cur.src === 'acme'} />}
      <CertSourcePicker value={cur} onChange={onChange} certs={certs} nodeId={nodeId} sni={sni} />
    </div>
  )
}
