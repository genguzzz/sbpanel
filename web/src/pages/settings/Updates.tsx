import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type NodeUpdate, type Settings, type UpdateState } from '../../lib/api'
import { useAction } from '../../lib/actions'
import { ago, dateTime } from '../../lib/fmt'
import { Badge, Button, Callout, Card, Confirm, CopyText, Field, RelTime, Spinner, Switch, cx, useToast } from '../../ui/kit'
import { useT } from '../../i18n'
import { C } from '../../i18n/common'
import { U } from '../../i18n/updates'

const INSTALL_UPGRADE = 'curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- upgrade master'
const DOCKER_UPGRADE = 'docker compose pull && docker compose up -d --force-recreate'

export const updateQuery = (busy: boolean) => ({
  queryKey: ['update'],
  queryFn: () => api<UpdateState>('/update'),
  refetchInterval: busy ? 2000 : 60_000,
  retry: busy ? 30 : 1,
})

type Ask = { kind: 'panel' } | { kind: 'panel-rollback' } | { kind: 'nodes'; ids: string[] | null } | { kind: 'node-rollback'; id: string; name: string }

/** Settings → Updates: the panel's own version and every node's, with upgrade and rollback. */
export function UpdatesSettings() {
  const t = useT(U)
  const c = useT(C)
  const toast = useToast()
  const qc = useQueryClient()
  // set while the panel restarts: the version we started from and when
  const [watch, setWatch] = useState<{ from: string; since: number } | null>(null)
  const [ask, setAsk] = useState<Ask | null>(null)
  const q = useQuery({ ...updateQuery(true), refetchInterval: (qq) => {
    const d = qq.state.data
    const nodeBusy = d?.nodes.some((n) => n.job || n.status?.state === 'downloading' || n.status?.state === 'applying')
    return watch || d?.running || (d?.job && d.job.state !== 'failed') || nodeBusy ? 2000 : 60_000
  } })
  const d = q.data
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => api<Settings & { update_check?: string; update_mirror?: string }>('/settings') })
  const [auto, setAuto] = useState(true)
  const [mirror, setMirror] = useState('')
  useEffect(() => {
    if (!settings.data) return
    setAuto(settings.data.update_check !== '0')
    setMirror(settings.data.update_mirror ?? '')
  }, [settings.data])

  // the restart is over once the version changed, or a result newer than our request is in
  const reloaded = useRef(false)
  useEffect(() => {
    if (!watch || !d) return
    if (d.current !== watch.from && !reloaded.current) {
      reloaded.current = true
      toast('good', t.reloading)
      setTimeout(() => location.reload(), 1500)
      return
    }
    if (d.job?.state === 'failed' || (d.last && d.last.started >= watch.since - 5 && !d.running && !d.job)) setWatch(null)
  }, [d, watch])

  const check = useAction({ fn: () => api<UpdateState>('/update/check', { method: 'POST' }), onSuccess: (r) => qc.setQueryData(['update'], r) })
  const panel = useAction({
    fn: (rollback: boolean) => api('/update/master', { method: 'POST', body: { rollback } }),
    onSuccess: () => { setWatch({ from: d?.current ?? '', since: Math.floor(Date.now() / 1000) }); qc.invalidateQueries({ queryKey: ['update'] }) },
  })
  const nodes = useAction({
    fn: (b: { ids?: string[]; outdated?: boolean; rollback?: boolean }) => api<{ nodes: { name: string; ok: boolean; reason?: string }[] }>('/update/nodes', { method: 'POST', body: b }),
    onSuccess: (r) => {
      const bad = r.nodes.filter((n) => !n.ok)
      if (bad.length) toast('bad', bad.map((n) => `${n.name}: ${n.reason}`).join('\n'))
      if (bad.length < r.nodes.length) toast('good', t.sent)
      qc.invalidateQueries({ queryKey: ['update'] })
    },
  })
  const save = useAction({
    fn: () => api('/settings', { method: 'PUT', body: { update_check: auto ? '1' : '0', update_mirror: mirror.trim() } }),
    success: t.saved, invalidate: [['settings'], ['update']],
  })

  if (!d) return <Card title={t.panel}><div className="flex h-24 items-center justify-center"><Spinner /></div></Card>
  const latest = d.latest
  const dev = !d.release
  const offer = latest && latest.tag !== d.current && (d.has_update || dev)
  const busyPanel = !!watch || d.running || (d.job && d.job.state !== 'failed')
  const outdated = d.nodes.filter((n) => n.online && n.supported && latest && n.version !== latest.tag && (n.has_update || !n.release))

  return (
    <div className="flex flex-col gap-4">
      <Card title={t.panel} action={<Button size="sm" isPending={check.isPending} onPress={() => check.mutate()}>{t.checkNow}</Button>}>
        <div className="flex flex-col gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Info label={t.current}>
              <span className="font-mono text-[15px]">{d.current}</span>
              {dev && <Badge>{t.devBuild}</Badge>}
              {!dev && latest && !d.has_update && <Badge tone="good">{t.upToDate}</Badge>}
            </Info>
            <Info label={t.latest} sub={d.check_error ? <span className="text-bad">{t.checkFailed}: {d.check_error}</span>
              : d.checked_at ? <RelTime ts={d.checked_at} text={t.checked(ago(d.checked_at))} /> : t.never}>
              {latest ? <>
                <a href={latest.url} target="_blank" rel="noreferrer" className="font-mono text-[15px] text-accent-text hover:underline">{latest.tag}</a>
                {d.has_update && <Badge tone="accent" dot>{t.newVersion(latest.tag)}</Badge>}
                {!!latest.published && <span className="text-xs text-muted">{dateTime(Date.parse(latest.published) / 1000)}</span>}
              </> : <span className="text-muted">—</span>}
            </Info>
          </div>

          {busyPanel && (
            <Callout tone="neutral">
              <span className="flex items-center gap-2"><Spinner size={13} />
                {d.job?.state === 'downloading' ? t.downloading(d.job.to) : d.running || d.job?.state === 'applying' ? t.applying : t.restarting}</span>
            </Callout>
          )}
          {!busyPanel && d.job?.state === 'failed' && <Callout tone="bad"><b>{t.failed}</b>: {d.job.error}</Callout>}
          {!busyPanel && d.last && <ResultLine r={d.last} />}

          {!d.supported ? (
            <Callout tone="warn">
              <div className="font-medium">{t.unsupported}</div>
              <div className="mt-1 text-[13px]">{d.container ? t.containerHint : t.unsupportedHint}</div>
              <CopyText text={d.container ? DOCKER_UPGRADE : INSTALL_UPGRADE} className="mt-2" />
            </Callout>
          ) : (offer || d.can_rollback) && (
            <div className="flex flex-wrap gap-2">
              {offer && <Button variant="primary" isDisabled={!!busyPanel} isPending={panel.isPending} onPress={() => setAsk({ kind: 'panel' })}>
                {d.has_update ? t.upgradeTo(latest!.tag) : t.install(latest!.tag)}</Button>}
              {d.can_rollback && <Button isDisabled={!!busyPanel} onPress={() => setAsk({ kind: 'panel-rollback' })}>{t.rollback}</Button>}
            </div>
          )}

          {latest?.notes && (
            <details className="group rounded-[10px] border px-3 py-2">
              <summary className="cursor-pointer text-[13px] font-medium text-subtle outline-none select-none">{t.notes} · {latest.tag}</summary>
              <pre className="mt-2 max-h-72 overflow-auto font-sans text-[13px] leading-5 whitespace-pre-wrap text-subtle">{latest.notes}</pre>
            </details>
          )}

          <div className="flex flex-col gap-3 border-t pt-4">
            <div className="flex flex-col gap-1"><Switch isSelected={auto} onChange={setAuto}>{t.auto}</Switch><p className="text-xs text-muted">{t.autoHint}</p></div>
            <Field label={t.mirror} description={t.mirrorHint} value={mirror} onChange={setMirror} placeholder="https://ghproxy.example/" mono />
            <div><Button size="sm" variant="primary" isPending={save.isPending} onPress={() => save.mutate()}>{c.save}</Button></div>
          </div>
        </div>
      </Card>

      <Card title={t.nodes} pad={false} action={outdated.length > 0 && latest && (
        <Button size="sm" variant="primary" onPress={() => setAsk({ kind: 'nodes', ids: null })}>{t.upgradeAll(outdated.length)}</Button>
      )}>
        {d.nodes.length === 0 ? <div className="py-10 text-center text-[13px] text-muted">{t.noNodes}</div> : (
          <div className="overflow-x-auto">
            <table className="w-full text-[13px]">
              <thead className="text-left text-xs text-subtle">
                <tr className="border-b">
                  <th className="px-5 py-2 font-medium">{t.colNode}</th>
                  <th className="px-3 py-2 font-medium">{t.colVersion}</th>
                  <th className="px-3 py-2 font-medium">{t.colStatus}</th>
                  <th className="px-5 py-2" />
                </tr>
              </thead>
              <tbody>
                {d.nodes.map((n) => (
                  <tr key={n.id} className="border-b align-top last:border-0">
                    <td className="px-5 py-2.5"><div className="font-medium">{n.name}</div><div className="text-xs text-muted">{n.arch || '—'}</div></td>
                    <td className="px-3 py-2.5 whitespace-nowrap">
                      <span className="font-mono">{n.version || '—'}</span>
                      {n.has_update && latest && <span className="ml-2"><Badge tone="accent">{latest.tag}</Badge></span>}
                    </td>
                    <td className="px-3 py-2.5"><NodeStatus n={n} /></td>
                    <td className="px-5 py-2.5">
                      <div className="flex justify-end gap-2">
                        {n.online && n.supported && latest && n.version !== latest.tag && (
                          <Button size="sm" isDisabled={nodeBusy(n)} onPress={() => setAsk({ kind: 'nodes', ids: [n.id] })}>{t.upgrade}</Button>
                        )}
                        {n.online && n.status?.can_rollback && (
                          <Button size="sm" isDisabled={nodeBusy(n)} onPress={() => setAsk({ kind: 'node-rollback', id: n.id, name: n.name })}>{t.rollback}</Button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Confirm isOpen={!!ask} onOpenChange={(v) => !v && setAsk(null)}
        title={!ask ? '' : ask.kind === 'panel' ? t.confirmPanel(latest?.tag ?? '') : ask.kind === 'panel-rollback' ? t.confirmPanelRollback
          : ask.kind === 'nodes' ? t.confirmNodes(ask.ids?.length ?? outdated.length, latest?.tag ?? '') : t.confirmNodeRollback(ask.name)}
        body={!ask ? '' : ask.kind === 'panel' ? t.confirmPanelBody : ask.kind === 'panel-rollback' ? t.confirmPanelRollbackBody
          : ask.kind === 'nodes' ? t.confirmNodesBody : t.confirmNodeRollbackBody}
        danger={ask?.kind === 'panel-rollback' || ask?.kind === 'node-rollback'}
        confirmLabel={ask?.kind === 'panel-rollback' || ask?.kind === 'node-rollback' ? t.rollback : t.upgrade}
        onConfirm={() => {
          if (!ask) return
          if (ask.kind === 'panel') panel.mutate(false)
          else if (ask.kind === 'panel-rollback') panel.mutate(true)
          else if (ask.kind === 'nodes') nodes.mutate(ask.ids ? { ids: ask.ids } : { outdated: true })
          else nodes.mutate({ ids: [ask.id], rollback: true })
          setAsk(null)
        }} />
    </div>
  )
}

const nodeBusy = (n: NodeUpdate) => !!(n.job && n.job.state !== 'failed') || n.status?.state === 'downloading' || n.status?.state === 'applying'

function Info({ label, sub, children }: { label: string; sub?: ReactNode; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-[13px] text-subtle">{label}</div>
      <div className="mt-1 flex flex-wrap items-center gap-2">{children}</div>
      {sub && <div className="mt-1 text-xs text-muted">{sub}</div>}
    </div>
  )
}

function ResultLine({ r }: { r: { state: string; from: string; to: string; rollback?: boolean; error?: string; finished: number } }) {
  const t = useT(U)
  const ok = r.state === 'upgraded'
  const head = ok ? (r.rollback ? t.rollbackDone(r.from, r.to) : t.upgraded(r.from, r.to)) : r.state === 'rolled_back' ? t.rolledBack(r.from, r.to) : t.failed
  return (
    <Callout tone={ok ? 'good' : r.state === 'rolled_back' ? 'warn' : 'bad'}>
      <div className="flex flex-wrap items-baseline gap-x-2"><b>{head}</b>{r.finished > 0 && <RelTime ts={r.finished} text={ago(r.finished)} className="text-xs opacity-80" />}</div>
      {r.error && (
        <details className="mt-1">
          <summary className="cursor-pointer text-xs">{t.details}</summary>
          <pre className="mt-1 max-h-60 overflow-auto font-mono text-[11.5px] leading-4 whitespace-pre-wrap">{r.error}</pre>
        </details>
      )}
    </Callout>
  )
}

function NodeStatus({ n }: { n: NodeUpdate }) {
  const t = useT(U)
  if (!n.online) return <span className="text-muted">{t.offline}</span>
  if (n.job && n.job.state !== 'failed') return <span className="flex items-center gap-1.5 text-accent-text"><Spinner size={12} />{n.job.state === 'sent' ? t.nSent : t.nPreparing}</span>
  if (n.job?.state === 'failed') return <span className="text-bad" title={n.job.error}>{t.nFailed}: <span className="text-xs">{n.job.error}</span></span>
  const s = n.status
  if (s?.state === 'downloading' || s?.state === 'applying') {
    return <span className="flex items-center gap-1.5 text-accent-text"><Spinner size={12} />{s.state === 'downloading' ? t.nDownloading : t.nApplying} <span className="font-mono text-xs">{s.to}</span></span>
  }
  if (s?.state) {
    const tone = s.state === 'upgraded' ? 'text-good' : s.state === 'rolled_back' ? 'text-warn' : 'text-bad'
    return (
      <div className="min-w-0 max-w-[420px]">
        <span className={cx('font-medium', tone)}>{s.state === 'upgraded' ? t.nUpgraded : s.state === 'rolled_back' ? t.nRolledBack : t.nFailed}</span>
        <span className="ml-1.5 font-mono text-xs text-muted">{s.from} → {s.to}</span>
        {!!s.finished && <RelTime ts={s.finished} text={` · ${ago(s.finished)}`} className="text-xs text-muted" />}
        {s.error && <details><summary className="cursor-pointer text-xs text-subtle">{t.details}</summary><pre className="mt-1 max-h-48 overflow-auto font-mono text-[11px] leading-4 whitespace-pre-wrap text-subtle">{s.error}</pre></details>}
      </div>
    )
  }
  if (!n.supported) return <span className="text-xs text-muted">{n.container ? t.needsPull : t.needsInstall}</span>
  return <span className="text-muted">—</span>
}
