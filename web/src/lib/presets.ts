// Inbound presets of the "New inbound" form: what each protocol choice turns into (sing-box inbound
// options plus client hints for subscriptions). Pure, with no imports, so that
// web/scripts/preset-fixture.ts can run it under Node and the Go end-to-end test (test/ui_presets_test.go)
// can deploy exactly what the form produces and push traffic through it.

/** Where an inbound's TLS certificate comes from. */
export type CertChoice = { src: 'panel' | 'path' | 'acme'; certId: string; certPath: string; keyPath: string; email: string }

export type PresetInput = {
  port: number
  /** TLS presets: the inbound's domain (certificate name, client SNI). REALITY: the site to borrow. */
  sni: string
  /** REALITY only: host:port of the borrowed TLS 1.3 site; empty = sni:443 */
  handshake: string
  /** WebSocket path or gRPC service name */
  path: string
} & CertChoice

export type RealityKeypair = { private_key: string; public_key: string; short_id: string }

export type PresetId = 'vless-reality' | 'vmess-ws-tls' | 'vless-ws-tls' | 'vless-grpc-tls' | 'trojan-tls' | 'ss2022' | 'hysteria2' | 'tuic' | 'anytls'

export type PresetDef = {
  id: PresetId
  label: string
  type: string
  /** needs a domain and a certificate for it */
  tls: boolean
  udp: boolean
  /** works behind a CDN such as Cloudflare's proxy (WebSocket / gRPC over TLS) */
  cdn: boolean
  /** which extra field the form shows */
  path?: 'ws' | 'grpc'
  build: (o: PresetInput, keypair: () => Promise<RealityKeypair>, randomKey: () => string) => Promise<{ options: Record<string, any>; client: Record<string, any> }>
}

export const ACME_DIR = '/var/lib/sbnode/acme'

/** TLS block for a certificate choice: panel certificate (distributed by the master), files on the node, or node-side ACME. */
export function tlsFor(o: Pick<PresetInput, 'sni'> & CertChoice): Record<string, any> {
  if (o.src === 'acme') return { enabled: true, server_name: o.sni, acme: { domain: [o.sni], ...(o.email ? { email: o.email } : {}), data_directory: ACME_DIR } }
  if (o.src === 'panel') return { enabled: true, server_name: o.sni }
  return { enabled: true, server_name: o.sni, certificate_path: o.certPath, key_path: o.keyPath }
}

/** Rewrites inbound options for a certificate choice (the panel hint _cert lives at the top level). */
export function applyCertChoice(opts: Record<string, any>, c: CertChoice): Record<string, any> {
  const o: Record<string, any> = { ...opts, tls: { ...(opts.tls ?? {}), enabled: true } }
  delete o._cert
  delete o.tls.certificate_path; delete o.tls.key_path; delete o.tls.acme; delete o.tls.certificate; delete o.tls.key
  if (c.src === 'panel') o._cert = c.certId
  else if (c.src === 'path') { o.tls.certificate_path = c.certPath; o.tls.key_path = c.keyPath }
  else o.tls.acme = { domain: [o.tls.server_name ?? ''], ...(c.email ? { email: c.email } : {}), data_directory: ACME_DIR }
  return o
}

export function choiceOf(opts: Record<string, any>): CertChoice {
  const t = opts.tls ?? {}
  if (opts._cert) return { src: 'panel', certId: opts._cert, certPath: '', keyPath: '', email: '' }
  if (t.acme) return { src: 'acme', certId: '', certPath: '', keyPath: '', email: t.acme.email ?? '' }
  return { src: 'path', certId: '', certPath: t.certificate_path ?? '', keyPath: t.key_path ?? '', email: '' }
}

const tlsInbound = (o: PresetInput, extra: Record<string, any> = {}) => {
  const tls = { ...tlsFor(o), ...extra }
  return { listen: '::', listen_port: o.port, tls, ...(o.src === 'panel' ? { _cert: o.certId } : {}) }
}

export const PRESETS: PresetDef[] = [
  {
    id: 'vless-reality', label: 'VLESS + REALITY', type: 'vless', tls: false, udp: false, cdn: false,
    build: async (o, keypair) => {
      const kp = await keypair()
      const [hs, hp] = splitHostPort(o.handshake)
      return {
        options: {
          listen: '::', listen_port: o.port, _flow: 'xtls-rprx-vision',
          tls: { enabled: true, server_name: o.sni, reality: { enabled: true, handshake: { server: hs || o.sni, server_port: hp || 443 }, private_key: kp.private_key, short_id: [kp.short_id] } },
        },
        client: { security: 'reality', sni: o.sni, public_key: kp.public_key, short_id: kp.short_id, flow: 'xtls-rprx-vision', fingerprint: 'chrome' },
      }
    },
  },
  {
    id: 'vmess-ws-tls', label: 'VMess + WS + TLS', type: 'vmess', tls: true, udp: false, cdn: true, path: 'ws',
    build: async (o) => ({
      options: { ...tlsInbound(o), transport: { type: 'ws', path: wsPath(o.path) } },
      client: { security: 'tls', sni: o.sni, network: 'ws', path: wsPath(o.path) },
    }),
  },
  {
    id: 'vless-ws-tls', label: 'VLESS + WS + TLS', type: 'vless', tls: true, udp: false, cdn: true, path: 'ws',
    build: async (o) => ({
      options: { ...tlsInbound(o), transport: { type: 'ws', path: wsPath(o.path) } },
      client: { security: 'tls', sni: o.sni, network: 'ws', path: wsPath(o.path) },
    }),
  },
  {
    id: 'vless-grpc-tls', label: 'VLESS + gRPC + TLS', type: 'vless', tls: true, udp: false, cdn: true, path: 'grpc',
    build: async (o) => ({
      options: { ...tlsInbound(o, { alpn: ['h2'] }), transport: { type: 'grpc', service_name: grpcName(o.path) } },
      client: { security: 'tls', sni: o.sni, network: 'grpc', service: grpcName(o.path) },
    }),
  },
  {
    id: 'trojan-tls', label: 'Trojan + TLS', type: 'trojan', tls: true, udp: false, cdn: false,
    build: async (o) => ({ options: tlsInbound(o), client: { security: 'tls', sni: o.sni } }),
  },
  {
    id: 'ss2022', label: 'Shadowsocks 2022', type: 'shadowsocks', tls: false, udp: false, cdn: false,
    build: async (o, _kp, randomKey) => ({ options: { listen: '::', listen_port: o.port, network: 'tcp', method: '2022-blake3-aes-128-gcm', password: randomKey() }, client: {} }),
  },
  {
    id: 'hysteria2', label: 'Hysteria2', type: 'hysteria2', tls: true, udp: true, cdn: false,
    build: async (o) => ({ options: tlsInbound(o, { alpn: ['h3'] }), client: { security: 'tls', sni: o.sni } }),
  },
  {
    id: 'tuic', label: 'TUIC v5', type: 'tuic', tls: true, udp: true, cdn: false,
    build: async (o) => ({ options: { ...tlsInbound(o, { alpn: ['h3'] }), congestion_control: 'bbr' }, client: { security: 'tls', sni: o.sni } }),
  },
  {
    id: 'anytls', label: 'AnyTLS', type: 'anytls', tls: true, udp: false, cdn: false,
    build: async (o) => ({ options: tlsInbound(o), client: { security: 'tls', sni: o.sni } }),
  },
]

export const wsPath = (p: string) => '/' + p.trim().replace(/^\/+/, '')
export const grpcName = (p: string) => p.trim().replace(/^\/+/, '') || 'grpc'

/** "host:port" → [host, port]; a missing or bad port is 0. IPv6 needs brackets. */
export function splitHostPort(s: string): [string, number] {
  const v = s.trim()
  const m = /^\[([^\]]+)\](?::(\d+))?$/.exec(v) ?? /^([^:]*)(?::(\d+))?$/.exec(v)
  if (!m) return [v, 0]
  const p = Number(m[2] ?? 0)
  return [m[1], Number.isInteger(p) && p > 0 && p < 65536 ? p : 0]
}

/** A DNS name with at least two labels (not an IP address). */
export function isHostname(h: string): boolean {
  const v = h.trim().replace(/\.$/, '')
  if (!v || v.length > 253 || !v.includes('.') || /^[\d.]+$/.test(v) || v.includes(':')) return false
  return v.split('.').every((l) => /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/i.test(l))
}
