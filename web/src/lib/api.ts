import { getLang } from '../i18n'

export class ApiError extends Error {
  status: number
  data: any
  constructor(status: number, msg: string, data?: any) {
    super(msg)
    this.status = status
    this.data = data
  }
}

export async function api<T>(path: string, init?: { method?: string; body?: unknown }): Promise<T> {
  const res = await fetch('/api' + path, {
    method: init?.method ?? 'GET',
    headers: init?.body !== undefined ? { 'Content-Type': 'application/json', 'X-Lang': getLang() } : { 'X-Lang': getLang() },
    body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
    credentials: 'same-origin',
  })
  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/auth/')) window.dispatchEvent(new Event('sbp:unauthorized'))
    throw new ApiError(res.status, data?.error ?? res.statusText, data)
  }
  return data as T
}

/** Like api() but also returns the response headers (e.g. X-Precision on /traffic/top). */
export async function apiRaw<T>(path: string): Promise<{ data: T; headers: Headers }> {
  const res = await fetch('/api' + path, { credentials: 'same-origin', headers: { 'X-Lang': getLang() } })
  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) {
    if (res.status === 401) window.dispatchEvent(new Event('sbp:unauthorized'))
    throw new ApiError(res.status, data?.error ?? res.statusText, data)
  }
  return { data: data as T, headers: res.headers }
}

export type SysInfo = {
  cpu_pct?: number; mem_used?: number; mem_total?: number; load1?: number; uptime?: number
  net_rx?: number; net_tx?: number; disk_used?: number; disk_total?: number
}

/** One process's own footprint; cpu_pct is a share of the whole machine (all cores = 100). */
export type ProcInfo = { cpu_pct?: number; rss?: number; heap?: number; goroutines?: number; threads?: number; started?: number }

export type NodeMetricPoint = {
  ts: number; cpu: number; mem_used: number; mem_total: number; proc_cpu: number | null; proc_rss: number | null
  rate_up: number; rate_down: number; nic_rx: number; nic_tx: number; conns: number
}
export type NodeMetrics = {
  from: number; to: number; step: number; points: NodeMetricPoint[]
  traffic: [number, number, number][]; master?: { ts: number; cpu: number; rss: number }[]
}

export type TrafficMode = 'both' | 'out' | 'in' | 'max'
export type ServerUsage = { tracked: boolean; rx: number; tx: number; adj: number; used: number; period_start: number; period_end: number }

/** The node's usage against its cap: the machine's metered traffic once the node reports it, else user traffic. */
export const nodeUsed = (n: Node) => (n.server?.tracked ? n.server.used : n.month_up + n.month_down)

export type Node = {
  id: string; name: string; address: string; online: boolean; enrolled: boolean
  revision: number; applied_revision: number; apply_error: string; last_seen: number; cert_expire: number
  info: { version?: string; singbox?: string; hostname?: string; os?: string; arch?: string }
  public_address: string; region?: string; template_id: number
  traffic_limit: number; reset_day: number; inbounds: number; sys?: SysInfo
  /** what traffic_limit is compared with; server: the machine's metered uplink this billing period */
  traffic_mode: TrafficMode; server?: ServerUsage
  /** address family clients try first for the node's hostname; '' = client default */
  client_ip: '' | 'ipv6' | 'ipv4'
  /** the sbnode process; master: the sbmaster process when the panel runs on this machine */
  proc?: ProcInfo; master?: ProcInfo
  rate_up: number; rate_down: number; active_conns: number; spool_backlog: number; online_users: number
  month_up: number; month_down: number; today_up: number; today_down: number
}

export type Inbound = {
  id: number; node_id: string; node: string; tag: string; type: string; port: number
  options: Record<string, unknown>; client: Record<string, unknown>; enabled: boolean; remark: string
  sort: number; group_ids: number[]
}

export type User = {
  id: number; username: string; sub_token: string; uuid: string; password?: string; status: string; enabled: boolean
  traffic_limit: number; expire_at: number; reset_strategy: string; note: string
  used_up: number; used_down: number; life_up: number; life_down: number; created_at: number; online_ips: number
  device_limit: number; speed_up: number; speed_down: number; last_reset_at: number; sub_last_at: number; sub_last_ua: string
  group_ids: number[]; sub_profile_id?: number
  /** traffic limit presets on the user; traffic_limit 0 = the preset's size */
  limits?: { id: number; traffic_limit: number }[]; limits_exhausted?: number
}

export type LimitUser = { user_id: number; username: string; enabled: boolean; traffic_limit: number; override: boolean; up: number; down: number; used: number; exhausted: boolean; reaches: boolean }

export type Group = { id: number; name: string; remark: string; is_default: boolean; inbound_ids: number[]; users: number; sub_profile_id?: number }

export type Limit = {
  id: number; name: string; note: string; traffic_limit: number; period: 'user' | 'month'; reset_day: number; enabled: boolean
  inbound_ids: number[]; created_at: number; period_start: number; next_reset: number; users: number; limited: number; used: number; overrides: number
}

export type UserLimit = {
  id: number; name: string; traffic_limit: number; default_limit: number; override: boolean; up: number; down: number; used: number
  exhausted: boolean; period: 'user' | 'month'; next_reset: number; inbound_ids: number[]
}

export type InboundUse = { inbound_id: number; node: string; tag: string; remark: string; type: string; up: number; down: number; blocked: boolean }

export type Host = {
  id: number; inbound_id: number; remark: string; address: string; port: number; sni: string; host: string; path: string
  alpn: string; fingerprint: string; allow_insecure: boolean; sort: number; enabled: boolean
}

export type Template = { id: number; name: string; json: string; draft: string; has_draft: boolean; updated_at: number; nodes: string[] }

export type SubInfo = {
  url: string; qr: string; active: boolean; links: { remark: string; type: string; uri: string }[]
  access: { ts: number; ip: string; ua: string; format: string }[]
  formats: Record<'base64' | 'singbox' | 'mihomo' | 'page', string>
}

export type Settings = {
  sub_base_url: string; sub_title: string; sub_update_hours: string; tg_chat: string; tg_token_set: boolean
  webhook_url: string; notify_events: string; device_policy: string; ca_sha256: string; grpc_listen: string
  public_grpc: string; keep_minute_days: number; keep_hour_days: number; keep_dim_days: number; tz: string
}

export type Conn = {
  id: number; user: string; username: string; inbound: string; outbound: string; network: string; host: string
  port: number; src_ip: string; rule: string; start: number; up: number; down: number
}

export type AuditRow = { ts: number; admin: string; action: string; target: string; detail: string; ip: string }
export type ApiToken = { id: number; name: string; prefix: string; created_at: number; last_used: number }

export type ImportReport = {
  dry_run: boolean; users: number; updated: number; skipped: string[] | null; groups: string[] | null; warnings: string[] | null
  inbounds: { tag: string; type: string; port: number; users: number; remark: string }[] | null
}

export type TopRow = { key: string; label: string; up: number; down: number; conns: number; users: number }

export type Series = { key: string; label: string; up: number; down: number; points: [number, number, number][] }
export type SeriesResp = { step: number; from: number; to: number; group: string; series: Series[] }

export type Overview = {
  today_up: number; today_down: number; month_up: number; month_down: number
  rate_up: number; rate_down: number; active_conns: number
  users: number; active_users: number; online_users: number; online_ips: number
  nodes_total: number; nodes_online: number; nodes: Node[]
  /** the sbmaster process itself */
  master?: ProcInfo | null
  top_users: TopRow[]; top_domains: TopRow[]; top_outbounds: TopRow[]
}

export type Event = { ts: number; kind: string; user_id: number; user: string; node_id: string; node: string; detail: string }

export type Enroll = { node_id: string; token: string; expires_at: number; master: string; ca_sha256: string; command: string }

// ---- subscription-page themes (settings → subscription page) ----

export type SubThemeField = {
  key?: string; type: 'title' | 'text' | 'textarea' | 'url' | 'color' | 'number' | 'switch' | 'select' | 'multiselect'
  label: string; help: string; default?: unknown; maxLength?: number; min?: number; max?: number
  options?: { value: string; label: string }[]
}

export type SubTheme = {
  id: string; name: string; version: string; author: string; description: string; homepage: string; license: string
  builtin: boolean; active: boolean; preview_url: string | null; schema: SubThemeField[]; settings: Record<string, unknown>
}

export type SubSupportLink = { label: string; url: string; icon?: string }

export type SiteSettings = Settings & {
  sub_announcement: string; sub_logo_url: string; sub_support_links: string; sub_clients: string; ui_appearance: string
}

/** The clients the server knows how to deep-link (mirrors subClients in internal/master/subdata.go). */
export const SUB_CLIENTS = [
  { id: 'singbox', label: 'sing-box' }, { id: 'mihomo', label: 'Clash Verge / mihomo' }, { id: 'shadowrocket', label: 'Shadowrocket' },
  { id: 'hiddify', label: 'Hiddify' }, { id: 'stash', label: 'Stash' }, { id: 'v2rayng', label: 'v2rayNG' }, { id: 'nekobox', label: 'NekoBox' },
]

/** Multipart upload to /api (the JSON helper above cannot send files). */
export async function apiUpload<T>(path: string, file: File): Promise<T> {
  const fd = new FormData()
  fd.append('file', file)
  const res = await fetch('/api' + path, { method: 'POST', body: fd, credentials: 'same-origin', headers: { 'X-Lang': getLang() } })
  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) {
    if (res.status === 401) window.dispatchEvent(new Event('sbp:unauthorized'))
    throw new ApiError(res.status, data?.error ?? res.statusText, data)
  }
  return data as T
}

// ---- panel & node upgrades (GET /api/update) ----

export type Release = { tag: string; name: string; notes: string; url: string; published: string }
export type UpgradeResult = { state: 'upgraded' | 'rolled_back' | 'failed'; from: string; to: string; rollback?: boolean; error?: string; started: number; finished: number }
/** What the master is doing before the helper / node takes over. */
export type UpgradeJob = { state: 'downloading' | 'applying' | 'preparing' | 'sent' | 'failed'; to: string; error?: string; at: number }
export type NodeUpgradeStatus = {
  state?: 'downloading' | 'applying' | 'upgraded' | 'rolled_back' | 'failed'; from?: string; to?: string; error?: string
  started?: number; finished?: number; can_rollback?: boolean; rollback?: boolean
}
export type NodeUpdate = { id: string; name: string; online: boolean; version: string; arch: string; release: boolean; supported: boolean; container?: boolean; has_update: boolean; status?: NodeUpgradeStatus; job?: UpgradeJob }
export type UpdateState = {
  current: string; release: boolean; latest: Release | null; has_update: boolean; checked_at: number; check_error?: string; check_enabled: boolean; mirror: string
  supported: boolean; unsupported?: string; container?: boolean; running: boolean; job?: UpgradeJob; last?: UpgradeResult; can_rollback: boolean
  nodes: NodeUpdate[]
}
