// Package subgen turns one generic routing profile into client configs
// (mihomo YAML, sing-box JSON, Surge conf). The model is client-agnostic;
// everything client-specific lives in ClientTemplate.
package subgen

// Matcher is one generic match condition.
// Types: domain, domain_suffix, domain_keyword, domain_regex, ip_cidr,
// geosite, geoip, process_name, port, network.
type Matcher struct {
	Type      string   `json:"type"`
	Values    []string `json:"values"`
	NoResolve bool     `json:"no_resolve,omitempty"` // ip matchers: don't resolve domains to test
}

// Source is a remote rule list. Either a catalogue reference (Catalog="netflix")
// that the generator maps to a per-client URL, or explicit per-client URLs.
type Source struct {
	Catalog  string            `json:"catalog,omitempty"`  // key in Catalog
	Behavior string            `json:"behavior,omitempty"` // domain | ipcidr | classical
	URLs     map[string]string `json:"urls,omitempty"`     // client -> url ("mihomo","singbox","surge")
	Interval int               `json:"interval,omitempty"` // seconds, default 86400
}

// RuleSet is a named list of matchers: inline items and/or a remote source.
type RuleSet struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Inline []Matcher `json:"inline,omitempty"`
	Remote *Source   `json:"remote,omitempty"`
}

// Member of a policy group.
//
//	all            every eligible node
//	filter         nodes whose name/node/tags match Include and not Exclude (regex)
//	group          another group by id
//	direct/reject  built-in policies
type Member struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref,omitempty"`
	Include string `json:"include,omitempty"`
	Exclude string `json:"exclude,omitempty"`
}

// Group is a policy group. Type: select | url-test | fallback | load-balance.
type Group struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"` // shown in clients
	Type      string   `json:"type"`
	Members   []Member `json:"members"`
	TestURL   string   `json:"test_url,omitempty"`
	Interval  int      `json:"interval,omitempty"`
	Tolerance int      `json:"tolerance,omitempty"`
	// HideIfEmpty drops the group (and rules/members pointing at it) when it
	// resolves to no nodes, instead of padding it with DIRECT.
	HideIfEmpty bool `json:"hide_if_empty,omitempty"`
}

// Rule routes a rule set to a target: a group id, "DIRECT" or "REJECT".
type Rule struct {
	RuleSet  string `json:"rule_set"`
	Target   string `json:"target"`
	Disabled bool   `json:"disabled,omitempty"`
	// ResolveIP: for the IP part of this rule set, resolve domains first
	// (mihomo: omit no-resolve; sing-box: insert a resolve action; Surge: default).
	// Off = IP matchers only see connections that already target an IP.
	ResolveIP bool `json:"resolve_ip,omitempty"`
}

// DNSPolicy is a client-neutral DNS hint set.
type DNSPolicy struct {
	Mode       string   `json:"mode"`        // fake-ip | real-ip
	Domestic   []string `json:"domestic"`    // resolvers for direct/CN domains
	Remote     []string `json:"remote"`      // resolvers through the proxy
	DirectSets []string `json:"direct_sets"` // rule set ids whose domains use Domestic
	IPv6       bool     `json:"ipv6"`
}

// ClientTemplate is the per-client base config with placeholders plus options.
type ClientTemplate struct {
	Enabled bool              `json:"enabled"`
	Base    string            `json:"base"`    // text with {{placeholders}}
	Options map[string]string `json:"options"` // {{opt.key}} values: ports, tun, log level...
}

// Profile = generic routing model + per-client templates.
type Profile struct {
	ID       string    `json:"id,omitempty"`
	Name     string    `json:"name,omitempty"`
	RuleSets []RuleSet `json:"rule_sets"`
	Groups   []Group   `json:"groups"`
	Rules    []Rule    `json:"rules"`
	Final    string    `json:"final"`
	DNS      DNSPolicy `json:"dns"`
	// Mirror is prepended to GitHub-hosted rule URLs (e.g. "https://ghfast.top/"),
	// for clients that can't reach GitHub directly.
	Mirror  string                    `json:"mirror,omitempty"`
	Clients map[string]ClientTemplate `json:"clients"` // "mihomo","singbox","surge"
}

// Endpoint is the shared client-facing connection model. The master defines
// subEntry from this type so extraction and exporters cannot drift structurally.
// Node is the panel node name, used by group filters together with Remark.
type Endpoint struct {
	Remark                 string
	Type                   string
	Address                string
	Port                   int
	UUID                   string
	Password               string
	Flow                   string
	Security               string // "", "tls", "reality"
	SNI                    string
	ALPN                   []string
	FP                     string
	Insecure               bool
	PBK, SID               string
	Network                string // tcp, ws, grpc, http, httpupgrade
	Path, Host             string
	Service                string // grpc service name
	SSMethod               string
	CC                     string // tuic congestion control
	Node                   string
	Region                 string            // ISO code of where traffic exits ("" = unknown)
	Inbound                int64             // panel inbound id (usage and limits)
	Line                   string            // "<node> <REGION> · <inbound>" before host remarks
	Headers                map[string]string // client transport headers
	MaxEarlyData           int
	EarlyDataHeader        string
	ObfsType, ObfsPassword string // Hysteria2 obfuscation, never the server masquerade
	IPPrefer               string // "ipv6" / "ipv4": which family the client should try first for a hostname Address
}

// Meta is per-request context available to templates.
type Meta struct {
	User      string
	SubURL    string // the subscription URL (Surge managed config)
	Title     string
	UpdateHrs int
	Mirror    string // global rule-URL mirror prefix; the profile's own Mirror wins
}
