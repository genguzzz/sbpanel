import { useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, nodeUsed, type Node, type Enroll, type Template, type TrafficMode } from '../lib/api'
import { bytes, parseSize, dateTime } from '../lib/fmt'
import { useRoute, go, replace, withParams } from '../lib/router'
import { useAction } from '../lib/actions'
import { ActionMenu, Button, Callout, Card, Confirm, CopyText, Empty, Field, ModalDialog, PageHeader, Presence, Segmented, Select, Skeleton, cx, useSticky } from '../ui/kit'
import { IconCheck, IconPlus } from '../ui/icons'
import { NodeCard } from './Overview'
import { NodeDrawer, type NodeTab } from './NodeDetail'
import { HealthDot, ProbeDrawer, useProbeResults, useRunProbe } from '../ui/probe'
import { tr } from '../i18n'
import { C } from '../i18n/common'
import { ND } from '../i18n/nodes'

const T = () => tr(ND)

export function NodesPage() {
  const { params } = useRoute()
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<Node[]>('/nodes'), refetchInterval: 5000 })
  const [wizard, setWizard] = useState<{ enroll?: Enroll; name?: string } | null>(() => (params.get('add') === '1' ? {} : null))
  useEffect(() => { if (params.get('add')) replace('nodes') }, [params])
  const [editing, setEditing] = useState<Node | null>(null)
  const [deleting, setDeleting] = useState<Node | null>(null)
  const delSticky = useSticky(deleting)
  const [specOf, setSpecOf] = useState<Node | null>(null)
  const probes = useProbeResults()
  const runProbe = useRunProbe()
  const [probeNode, setProbeNode] = useState<Node | null>(null)
  const probeSticky = useSticky(probeNode)
  const probeOf = (id: string) => (probes.data ?? []).filter((r) => r.node_id === id)
  const testNode = (n: Node) => { runProbe.mutate({ node_id: n.id }); setProbeNode(n) }

  const token = useAction({
    fn: (n: Node) => api<Enroll>(`/nodes/${n.id}/token`, { method: 'POST' }),
    onSuccess: (e, n) => setWizard({ enroll: e, name: n.name }),
  })
  const del = useAction({
    fn: (id: string) => api(`/nodes/${id}`, { method: 'DELETE' }),
    success: T().deleted, invalidate: [['nodes'], ['inbounds'], ['overview']],
  })

  const openId = params.get('id') ?? ''
  const openNode = nodes.data?.find((n) => n.id === openId)
  // a deleted (or mistyped) node closes the drawer instead of loading forever
  useEffect(() => { if (openId && nodes.data && !openNode) replace('nodes', withParams(params, { id: undefined })) }, [openId, nodes.data, openNode, params])
  const menu = (n: Node) => (
    <ActionMenu label={T().nodeActions} onAction={(k) => {
      if (k === 'probe') testNode(n)
      if (k === 'edit') setEditing(n)
      if (k === 'token') token.mutate(n)
      if (k === 'spec') setSpecOf(n)
      if (k === 'inbound') go('inbounds', { new: 1, node: n.id })
      if (k === 'delete') setDeleting(n)
    }} items={[
      { id: 'probe', label: T().testConn, isDisabled: n.inbounds === 0 }, { id: 'edit', label: tr(C).edit }, { id: 'inbound', label: T().createInbound },
      { id: 'token', label: n.enrolled ? T().reenroll : T().enrollCmd }, { id: 'spec', label: T().viewSpec }, 'sep',
      { id: 'delete', label: T().delete, danger: true },
    ]} />
  )

  return (
    <>
      <PageHeader title={T().title} subtitle={T().subtitle}
        actions={<Button variant="primary" onPress={() => setWizard({})}><IconPlus size={16} />{T().add}</Button>} />
      {nodes.isPending && !nodes.data && <div className="grid gap-4 md:grid-cols-2"><Skeleton className="h-64" /><Skeleton className="h-64" /></div>}
      {nodes.data?.length === 0 && (
        <Card><Empty title={T().empty} hint={T().emptyHint}
          action={<Button variant="primary" onPress={() => setWizard({})}>{T().add}</Button>} /></Card>
      )}
      <div className="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
        {nodes.data?.map((n) => (
          <NodeCard key={n.id} n={n} actions={<div className="flex shrink-0 items-center gap-0.5">
            {n.inbounds > 0 && <HealthDot results={probeOf(n.id)} running={runProbe.isPending && runProbe.variables?.node_id === n.id}
              onPress={() => probeOf(n.id).length ? setProbeNode(n) : testNode(n)} />}
            {menu(n)}
          </div>} />
        ))}
      </div>

      <NodeDrawer isOpen={!!openId} n={openNode} ids={(nodes.data ?? []).map((n) => n.id)} menu={openNode && menu(openNode)}
        tab={params.get('tab') === 'route' ? 'route' : 'status'} onTab={(t: NodeTab) => replace('nodes', withParams(params, { tab: t === 'status' ? undefined : t }))}
        onNav={(id) => replace('nodes', withParams(params, { id }))} onClose={() => go('nodes', withParams(params, { id: undefined, tab: undefined }))} />
      <ProbeDrawer isOpen={!!probeNode} onClose={() => setProbeNode(null)} title={T().probeTitle(probeSticky?.name ?? '')}
        results={probeSticky ? probeOf(probeSticky.id) : []} running={runProbe.isPending} onRetry={() => probeSticky && runProbe.mutate({ node_id: probeSticky.id })} />
      <Presence value={wizard}>
        {(w, open) => <AddNodeWizard isOpen={open} initial={w} onClose={() => setWizard(null)} />}
      </Presence>
      <Presence value={editing}>{(n, open) => <EditNode isOpen={open} n={n} onClose={() => setEditing(null)} />}</Presence>
      <Presence value={specOf}>{(n, open) => <SpecView isOpen={open} n={n} onClose={() => setSpecOf(null)} />}</Presence>
      <Confirm isOpen={!!deleting} onOpenChange={(v) => !v && setDeleting(null)} danger title={T().delete}
        confirmLabel={T().delete} onConfirm={() => deleting && del.mutate(deleting.id)}
        body={T().delBody(<b className="text-text">{delSticky?.name}</b>, delSticky?.inbounds ?? 0)} />
    </>
  )
}

const stepNames = () => [T().step1, T().step2, T().step3]

/** Add-node wizard: name → commands → live detection, all in one dialog. */
function AddNodeWizard({ isOpen, initial, onClose }: { isOpen: boolean; initial: { enroll?: Enroll; name?: string }; onClose: () => void }) {
  const qc = useQueryClient()
  const [step, setStep] = useState(initial.enroll ? 2 : 1)
  const [name, setName] = useState(initial.name ?? '')
  const [pubAddr, setPubAddr] = useState('')
  const [enroll, setEnroll] = useState<Enroll | null>(initial.enroll ?? null)
  const [how, setHow] = useState<'script' | 'docker' | 'manual'>('script')
  const [now, setNow] = useState(() => Date.now() / 1000)
  useEffect(() => { const t = setInterval(() => setNow(Date.now() / 1000), 1000); return () => clearInterval(t) }, [])
  const create = useAction({
    fn: () => api<Enroll>('/nodes', { method: 'POST', body: { name, public_address: pubAddr } }),
    invalidate: [['nodes']], onSuccess: (e) => { setEnroll(e); setStep(2) },
  })
  const regen = useAction({ fn: () => api<Enroll>(`/nodes/${enroll!.node_id}/token`, { method: 'POST' }), success: T().newToken, onSuccess: setEnroll })
  // Snapshot of the node before enrolling, so "re-enroll" of an existing node waits for a fresh certificate.
  const [baseline] = useState(() => initial.enroll ? qc.getQueryData<Node[]>(['nodes'])?.find((n) => n.id === initial.enroll!.node_id)?.cert_expire ?? 0 : 0)
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<Node[]>('/nodes'), refetchInterval: step >= 2 && isOpen ? 2000 : 5000 })
  const n = enroll ? nodes.data?.find((x) => x.id === enroll.node_id) : undefined
  const enrolled = !!n?.enrolled && (n.cert_expire !== baseline || !baseline)
  const online = enrolled && !!n?.online
  const applied = online && n!.applied_revision >= n!.revision && !n!.apply_error
  useEffect(() => { if (step === 2 && enrolled) setStep(3) }, [step, enrolled])
  const expired = !!enroll && now > enroll.expires_at && !enrolled
  const dir = '/var/lib/sbnode'
  const scriptCmd = enroll ? `curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- node \\\n  --master ${enroll.master} --token ${enroll.token} --ca-sha256 ${enroll.ca_sha256}` : ''
  const image = 'ghcr.io/genguzzz/sbpanel'
  const dockerCmd = enroll ? `docker run -d --name sbnode --restart unless-stopped --network host --cap-add NET_ADMIN \\\n  -e SBN_MASTER=${enroll.master} -e SBN_TOKEN=${enroll.token} \\\n  -e SBN_CA_SHA256=${enroll.ca_sha256} \\\n  -v sbnode:/var/lib/sbnode ${image} node` : ''
  const manualCmd = enroll ? `sbnode ${enroll.command.replace(/^sbnode /, '')} --dir ${dir}\nsbnode run --dir ${dir}` : ''

  const footer = step === 1 ? <>
    <Button onPress={onClose}>{tr(C).cancel}</Button>
    <Button variant="primary" isPending={create.isPending} isDisabled={!name.trim()} onPress={() => create.mutate()}>{T().nextCmd}</Button>
  </> : step === 2 ? <>
    <Button variant="ghost" className="mr-auto" onPress={onClose}>{T().later}</Button>
    <Button variant="primary" onPress={() => setStep(3)}>{T().ranIt}</Button>
  </> : <>
    {!applied && <Button variant="ghost" className="mr-auto" onPress={() => setStep(2)}>{T().showCmd}</Button>}
    {applied ? <>
      <Button onPress={onClose}>{T().done}</Button>
      <Button variant="primary" onPress={() => { onClose(); go('inbounds', { new: 1, node: enroll!.node_id }) }}>{T().createInbound}</Button>
    </> : <Button onPress={onClose}>{T().closeBg}</Button>}
  </>

  return (
    <ModalDialog wide isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={initial.enroll ? T().enrollX(initial.name ?? T().nodeWord) : T().add} footer={footer}>
      <ol className="mb-5 flex items-center gap-2 text-[13px]" aria-label={T().steps}>
        {stepNames().map((s, i) => {
          const k = i + 1
          const done = k < step || (k === 3 && applied)
          return (
            <li key={s} className="flex min-w-0 items-center gap-2" aria-current={k === step ? 'step' : undefined}>
              {i > 0 && <span className={cx('h-px w-4 shrink-0 transition-colors duration-(--dur-2) sm:w-8', k <= step ? 'bg-accent' : 'bg-hl-med')} />}
              <span className={cx('grid h-6 w-6 shrink-0 place-items-center rounded-full text-[12px] font-semibold transition-colors duration-(--dur-2)',
                done ? 'bg-good text-on-accent' : k === step ? 'bg-accent text-on-accent' : 'bg-overlay text-muted')}>
                {done ? <IconCheck size={13} strokeWidth={3} className="animate-check" /> : k}
              </span>
              <span className={cx('truncate', k === step ? 'font-medium text-text' : 'hidden text-muted sm:inline')}>{s}</span>
            </li>
          )
        })}
      </ol>
      <div key={step} className="animate-content-in">
        {step === 1 && (
          <form className="flex flex-col gap-4" onSubmit={(e) => { e.preventDefault(); if (name.trim()) create.mutate() }}>
            <Field label={T().name} value={name} onChange={setName} placeholder={T().namePh} autoFocus />
            <Field label={T().pubAddr} value={pubAddr} onChange={setPubAddr} placeholder={T().pubPh} description={T().pubHint} />
            <button type="submit" hidden />
          </form>
        )}
        {step === 2 && enroll && (
          <div className="flex flex-col gap-4 text-[13px]">
            <Segmented label={T().method} value={how} onChange={setHow} options={[{ id: 'script', label: T().script }, { id: 'docker', label: T().docker }, { id: 'manual', label: T().manual }]} />
            {how === 'script' ? (
              <div key="s" className="flex animate-content-in flex-col gap-2">
                <p className="text-subtle">{T().scriptIntro()}</p>
                <Code text={scriptCmd} />
                <p className="text-xs text-muted">{T().scriptNote()}</p>
              </div>
            ) : how === 'docker' ? (
              <div key="d" className="flex animate-content-in flex-col gap-2">
                <p className="text-subtle">{T().dockerIntro}</p>
                <Code text={dockerCmd} />
                <p className="text-xs text-muted">{T().dockerNote()}</p>
              </div>
            ) : (
              <div key="m" className="flex animate-content-in flex-col gap-2">
                <p className="text-subtle">{T().manualIntro}</p>
                <Code text={manualCmd} />
              </div>
            )}
            {expired ? (
              <Callout tone="bad"><span className="flex flex-wrap items-center justify-between gap-2">{T().expired}<Button size="sm" variant="primary" isPending={regen.isPending} onPress={() => regen.mutate()}>{T().regen}</Button></span></Callout>
            ) : (
              <p className="text-xs text-muted">{T().tokenNote(dateTime(enroll.expires_at), Math.max(0, Math.round((enroll.expires_at - now) / 60)))}</p>
            )}
            <div className="rounded-[10px] bg-overlay px-3 py-2 text-xs text-subtle">
              {T().masterInfo(<span className="font-mono text-text">{enroll.master}</span>, <span className="font-mono break-all text-text">{enroll.ca_sha256}</span>, <span className="font-mono">--public-grpc host:port</span>)}
            </div>
            <div className="flex items-center gap-2 text-xs text-muted"><Pulse />{T().waiting}</div>
          </div>
        )}
        {step === 3 && enroll && (
          <div className="flex flex-col gap-1">
            <Check state={enrolled ? 'done' : expired ? 'fail' : 'wait'} wait={T().wEnroll} done={T().dEnroll} fail={T().fEnroll}
              action={expired ? <Button size="sm" variant="primary" isPending={regen.isPending} onPress={() => { regen.mutate(); setStep(2) }}>{T().regen}</Button> : undefined} />
            <Check state={online ? 'done' : enrolled ? 'wait' : 'idle'} wait={T().wConnect} done={T().dConnect(n?.info?.singbox ?? '')} />
            <Check state={applied ? 'done' : n?.apply_error && online ? 'fail' : online ? 'wait' : 'idle'} wait={T().wApply} done={T().dApply} fail={T().fApply(n?.apply_error ?? '')} />
            {applied && <p className="mt-3 animate-content-in text-[13px] text-subtle">{T().noInbound}</p>}
          </div>
        )}
      </div>
    </ModalDialog>
  )
}

function Pulse() {
  return <span className="relative inline-flex h-2.5 w-2.5 shrink-0"><span className="absolute inset-0 animate-pulse-ring rounded-full" /><span className="relative h-2.5 w-2.5 rounded-full bg-accent" /></span>
}

function Check({ state, wait, done, fail, action }: { state: 'idle' | 'wait' | 'done' | 'fail'; wait: string; done: string; fail?: string; action?: React.ReactNode }) {
  return (
    <div className={cx('flex items-center gap-3 rounded-[10px] px-3 py-2.5 text-sm transition-colors duration-(--dur-2)', state === 'wait' && 'bg-accent/6')}>
      <span className="grid h-6 w-6 shrink-0 place-items-center">
        {state === 'done' ? <span key="d" className="grid h-6 w-6 animate-check place-items-center rounded-full bg-good text-on-accent"><IconCheck size={14} strokeWidth={3} /></span>
          : state === 'fail' ? <span key="f" className="grid h-6 w-6 animate-check place-items-center rounded-full bg-bad/15 text-bad">!</span>
            : state === 'wait' ? <Pulse /> : <span className="h-2 w-2 rounded-full bg-hl-high" />}
      </span>
      <span key={state} className={cx('min-w-0 flex-1 animate-content-in break-all', state === 'idle' && 'text-muted', state === 'fail' && 'text-bad', state === 'done' && 'font-medium')}>
        {state === 'done' ? done : state === 'fail' ? fail : wait}
      </span>
      {action}
    </div>
  )
}

function Code({ text }: { text: string }) {
  return (
    <div className="flex items-start justify-between gap-3 rounded-[10px] border bg-base px-3 py-2.5">
      <code className="min-w-0 font-mono text-[12.5px] break-all whitespace-pre-wrap">{text}</code>
      <CopyText text={text} display={T().copy} className="shrink-0" />
    </div>
  )
}

function EditNode({ n, onClose, isOpen }: { n: Node; onClose: () => void; isOpen: boolean }) {
  const [name, setName] = useState(n.name)
  const [pub, setPub] = useState(n.public_address)
  const [region, setRegion] = useState(n.region ?? '')
  const [tpl, setTpl] = useState(String(n.template_id))
  const templates = useQuery({ queryKey: ['templates'], queryFn: () => api<Template[]>('/templates') })
  const [limit, setLimit] = useState(n.traffic_limit ? (n.traffic_limit / 1024 ** 3).toString() : '')
  const [day, setDay] = useState(String(n.reset_day || 1))
  const [mode, setMode] = useState<TrafficMode>(n.traffic_mode || 'both')
  const [used, setUsed] = useState('')
  const [cip, setCip] = useState<string>(n.client_ip || 'auto')
  const save = useAction({
    fn: () => {
      const l = limit.trim() ? parseSize(limit) : 0
      if (Number.isNaN(l)) throw new Error(T().badLimit)
      const u = used.trim() ? parseSize(used) : undefined
      if (Number.isNaN(u)) throw new Error(T().badUsed)
      const rd = Math.min(28, Math.max(1, Number(day) || 1))
      // the correction applies to the period of the reset day being saved, so it goes in a second request
      return api(`/nodes/${n.id}`, { method: 'PATCH', body: { name, public_address: pub, region: region.trim().toUpperCase(), template_id: Number(tpl), traffic_limit: l, reset_day: rd, traffic_mode: mode, client_ip: cip === 'auto' ? '' : cip } })
        .then(() => u !== undefined ? api(`/nodes/${n.id}`, { method: 'PATCH', body: { traffic_used: u } }) : undefined)
    },
    apply: Number(tpl) !== n.template_id, success: T().savedX(name), invalidate: [['nodes'], ['templates']], onSuccess: onClose,
  })
  return (
    <ModalDialog isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={T().editX(n.name)}
      footer={<><Button onPress={onClose}>{tr(C).cancel}</Button><Button variant="primary" isPending={save.isPending} onPress={() => save.mutate()}>{tr(C).save}</Button></>}>
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-4">
          <Field label={T().nameShort} value={name} onChange={setName} />
          <Select label={T().template} value={tpl} onChange={setTpl} options={(templates.data ?? []).map((t) => ({ id: String(t.id), label: t.name }))} />
        </div>
        <div className="grid gap-4 sm:grid-cols-[1fr_9rem]">
          <Field label={T().pubAddrShort} value={pub} onChange={setPub} placeholder={n.address || T().pubPh2} description={T().pubHint2} />
          <Field label={T().region} value={region} onChange={(v) => setRegion(v.toUpperCase().slice(0, 2))} placeholder="US" description={T().regionHint} />
        </div>
        <Select label={T().clientIp} value={cip} onChange={setCip} description={T().clientIpHint}
          options={['auto', 'ipv6', 'ipv4'].map((id) => ({ id, label: T().clientIpOpts(id) }))} />
        <div className="grid grid-cols-2 gap-4">
          <Field label={T().limit} value={limit} onChange={setLimit} placeholder={T().unlimited} description={n.traffic_limit ? T().current(bytes(n.traffic_limit)) : T().limitHint} />
          <Field label={T().resetDay} value={day} onChange={setDay} description="1–28" />
        </div>
        <div className="grid grid-cols-2 gap-4">
          <Select label={T().mode} value={mode} onChange={setMode} description={T().modeHint}
            options={(['both', 'out', 'in', 'max'] as const).map((id) => ({ id, label: T().modeOpts(id) }))} />
          <Field label={T().used} value={used} onChange={setUsed} placeholder={(nodeUsed(n) / 1024 ** 3).toFixed(2)}
            description={T().usedHint(bytes(nodeUsed(n)))} />
        </div>
      </div>
    </ModalDialog>
  )
}

function SpecView({ n, onClose, isOpen }: { n: Node; onClose: () => void; isOpen: boolean }) {
  const spec = useQuery({ queryKey: ['spec', n.id], queryFn: () => api<unknown>(`/nodes/${n.id}/spec`) })
  return (
    <ModalDialog wide isOpen={isOpen} onOpenChange={(v) => !v && onClose()} title={T().specTitle(n.name, n.revision)}>
      <pre className="max-h-[60vh] overflow-auto rounded-[10px] border bg-base p-3 font-mono text-[12px] leading-5">
        {spec.data ? JSON.stringify(decodeSpec(spec.data), null, 2) : T().loading}
      </pre>
    </ModalDialog>
  )
}

// protojson renders bytes as base64; decode the embedded sing-box JSON for display.
function decodeSpec(s: any) {
  const dec = (b?: string) => { try { return b ? JSON.parse(atob(b)) : undefined } catch { return b } }
  return {
    revision: s.revision,
    skeleton: dec(s.skeletonJson),
    inbounds: (s.inbounds ?? []).map((i: any) => ({ ...dec(i.optionsJson), type: i.type, tag: i.tag, users: dec(i.usersJson) })),
  }
}
