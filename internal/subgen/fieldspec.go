package subgen

// Field carriage matrix: the single place that records, for every Endpoint field,
// how each client format carries it. TestFieldSpecCoversEndpoint fails when an
// Endpoint field has no row (or a row misses a client), and TestFieldSpecCarried
// renders protocol fixtures through every exporter and fails when a field the
// matrix says is carried is missing from the output. So a new protocol field can't
// be added to the model and silently dropped by one client again.
//
// None of these clients publishes a machine-readable schema (Surge and Shadowrocket
// are closed source, Mihomo and sing-box define options as Go structs). The
// sing-box column is additionally checked against sing-box's own option parser
// (unknown keys are rejected) in TestFieldFixturesSingboxStrict. References:
//   sing-box  https://sing-box.sagernet.org/configuration/outbound/
//   Mihomo    https://wiki.metacubex.one/config/proxies/
//   Surge     https://manual.nssurge.com/policy/proxy.html
//   Shadowrocket: Sub-Store's producer (closest public reference)
//   https://github.com/sub-store-org/Sub-Store/blob/master/backend/src/core/proxy-utils/producers/shadowrocket.js

// SpecClients are the exporters the matrix covers.
var SpecClients = []string{"singbox", "mihomo", "shadowrocket", "surge"}

// Carry is how one client carries one field.
//   - Keys: output key paths (dotted, for structured formats; "key=" tokens for
//     Surge, where "*" means the value itself appears in the line). Any one present
//     satisfies the check (alternatives per transport).
//   - Implicit: carried without a key of its own (e.g. TLS implied by the protocol).
//   - Not: the client cannot express it; the reason is the documentation.
//   - Except: protocol ("hysteria2") or transport ("net:http") where the client
//     legitimately drops the field, with the reason.
type Carry struct {
	Keys     []string
	Implicit string
	Not      string
	Except   map[string]string
}

// FieldSpec is one Endpoint field. Meta marks panel-only fields never sent to clients.
type FieldSpec struct {
	Field   string
	Meta    string
	Clients map[string]Carry
}

func k(keys ...string) Carry { return Carry{Keys: keys} }

// clashLike gives the Mihomo and Shadowrocket columns (Shadowrocket reuses the
// Mihomo proxy shape, see ShadowrocketProxy).
func clashLike(c Carry) map[string]Carry { return map[string]Carry{"mihomo": c, "shadowrocket": c} }

func row(field string, singbox Carry, clash Carry, surge Carry) FieldSpec {
	m := clashLike(clash)
	m["singbox"], m["surge"] = singbox, surge
	return FieldSpec{Field: field, Clients: m}
}

var (
	quicNoUTLS = map[string]string{"hysteria2": "QUIC protocols have no uTLS", "tuic": "QUIC protocols have no uTLS", "shadowsocks": "no TLS layer"}
	tlsImplied = map[string]string{"trojan": "TLS implied by the protocol", "hysteria2": "TLS implied by the protocol", "tuic": "TLS implied by the protocol", "anytls": "TLS implied by the protocol"}
)

var FieldSpecs = []FieldSpec{
	row("Remark", k("tag"), k("name"), k("*")),
	row("Type", k("type"), k("type"), Carry{Implicit: "proxy type keyword (ss, vmess, trojan, hysteria2, tuic-v5)"}),
	row("Address", k("server"), k("server"), k("*")),
	row("Port", k("server_port"), k("port"), k("*")),
	row("UUID", k("uuid"), k("uuid"), k("username", "uuid")),
	row("Password", k("password"), k("password"), k("password")),
	row("Flow", k("flow"), k("flow"), Carry{Not: "Surge has no VLESS"}),
	row("Security", k("tls.enabled"), Carry{Keys: []string{"tls", "reality-opts"}, Except: tlsImplied},
		Carry{Keys: []string{"tls"}, Except: tlsImplied}),
	row("SNI", k("tls.server_name"), k("servername", "sni"), k("sni")),
	row("ALPN", k("tls.alpn"), k("alpn"), k("alpn")),
	row("FP", Carry{Keys: []string{"tls.utls.fingerprint"}, Except: quicNoUTLS}, Carry{Keys: []string{"client-fingerprint"}, Except: quicNoUTLS},
		Carry{Not: "Surge has no uTLS fingerprint profiles"}),
	row("Insecure", k("tls.insecure"), k("skip-cert-verify"), k("skip-cert-verify")),
	row("PBK", k("tls.reality.public_key"), k("reality-opts.public-key"), Carry{Not: "Surge has no REALITY (node skipped)"}),
	row("SID", k("tls.reality.short_id"), k("reality-opts.short-id"), Carry{Not: "Surge has no REALITY (node skipped)"}),
	row("Network", k("transport.type"), k("network"), k("ws")),
	row("Path", k("transport.path"), k("ws-opts.path", "h2-opts.path"), k("ws-path")),
	row("Host", k("transport.headers.Host", "transport.host"), k("ws-opts.headers.Host", "h2-opts.host"), k("ws-headers")),
	row("Service", k("transport.service_name"), k("grpc-opts.grpc-service-name"), Carry{Not: "Surge has no gRPC (node skipped)"}),
	row("SSMethod", k("method"), k("cipher"), k("encrypt-method")),
	row("CC", k("congestion_control"), k("congestion-controller"), Carry{Not: "Surge's TUIC has no congestion-control option"}),
	row("Headers", k("transport.headers"), Carry{Keys: []string{"ws-opts.headers"}, Except: map[string]string{"net:http": "Mihomo h2-opts has no custom headers"}},
		k("ws-headers")),
	row("MaxEarlyData", k("transport.max_early_data"), k("ws-opts.max-early-data"),
		Carry{Not: "Surge has no WebSocket early data; it connects with a plain upgrade, which the server accepts"}),
	row("EarlyDataHeader", k("transport.early_data_header_name"), k("ws-opts.early-data-header-name"),
		Carry{Not: "Surge has no WebSocket early data"}),
	row("ObfsType", k("obfs.type"), k("obfs"), Carry{Implicit: "salamander-password implies salamander"}),
	row("ObfsPassword", k("obfs.password"), k("obfs-password"), k("salamander-password")),
	row("IPPrefer", k("domain_resolver.strategy"), k("ip-version"), k("ip-version")),
	{Field: "Node", Meta: "panel node name for group filters"},
	{Field: "Region", Meta: "exit region for names and filters"},
	{Field: "Inbound", Meta: "panel inbound id for usage accounting"},
	{Field: "Line", Meta: "display line before host remarks"},
}
