package subgen

// Built-in client templates. Admins clone and edit them per profile.
const MihomoBase = `mixed-port: {{opt.mixed_port}}
allow-lan: {{opt.allow_lan}}
mode: rule
log-level: {{opt.log_level}}
ipv6: {{opt.ipv6}}
unified-delay: true
tcp-concurrent: true
external-controller: {{opt.controller}}
profile:
  store-selected: true
  store-fake-ip: true
sniffer:
  enable: true
  sniff:
    HTTP: {ports: [80, 8080-8880], override-destination: true}
    TLS: {ports: [443, 8443]}
    QUIC: {ports: [443, 8443]}
{{dns}}
{{proxies}}
{{proxy_groups}}
{{rule_providers}}
{{rules}}
`

const SingboxBase = `{
  "log": {"level": "{{opt.log_level}}", "timestamp": true},
  "dns": {{dns}},
  "inbounds": [
    {"type": "tun", "tag": "tun-in", "address": ["172.19.0.1/30", "fdfe:dcba:9876::1/126"], "auto_route": true, "strict_route": true, "stack": "{{opt.tun_stack}}"},
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": {{opt.mixed_port}}}
  ],
  "outbounds": {{outbounds}},
  "route": {
    "rules": {{route_rules}},
    "rule_set": {{rule_set}},
    "final": {{final}},
    "auto_detect_interface": true,
    "default_domain_resolver": "local"
  },
  "experimental": {
    "cache_file": {"enabled": true, "store_fakeip": false},
    "clash_api": {"external_controller": "{{opt.controller}}", "default_mode": "rule"}
  }
}
`

const SurgeBase = `#!MANAGED-CONFIG {{sub_url}} interval={{opt.update_interval}} strict=false

[General]
loglevel = notify
{{dns}}
skip-proxy = 127.0.0.1, 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, 100.64.0.0/10, localhost, *.local
exclude-simple-hostnames = true
ipv6 = {{opt.ipv6}}
internet-test-url = {{opt.test_url}}
proxy-test-url = {{opt.test_url}}

{{proxies}}

{{proxy_groups}}

{{rules}}
`

func cat(id, name, key string) RuleSet {
	return RuleSet{ID: id, Name: name, Remote: &Source{Catalog: key}}
}

func region(id, name, re string) Group {
	return Group{ID: id, Name: name, Type: "url-test", HideIfEmpty: true, Members: []Member{{Kind: "filter", Include: re}}}
}

func clients() map[string]ClientTemplate {
	return map[string]ClientTemplate{
		"mihomo":  {Enabled: true, Base: MihomoBase, Options: map[string]string{}},
		"singbox": {Enabled: true, Base: SingboxBase, Options: map[string]string{}},
		"surge":   {Enabled: true, Base: SurgeBase, Options: map[string]string{}},
	}
}

func defaultDNS(direct ...string) DNSPolicy {
	return DNSPolicy{IPv6: true, Mode: "fake-ip", Domestic: []string{"https://223.5.5.5/dns-query"}, Remote: []string{"https://1.1.1.1/dns-query"},
		DirectSets: append([]string{}, direct...)}
}

// inlinePrivate matches LAN/loopback without any download.
func inlinePrivate() RuleSet {
	return RuleSet{ID: "lan", Name: "局域网", Inline: []Matcher{
		{Type: "domain_suffix", Values: []string{"local", "lan", "localhost"}},
		{Type: "ip_cidr", NoResolve: true, Values: []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10"}},
	}}
}

func basicGroups() []Group {
	return []Group{
		{ID: "proxy", Name: "🚀 节点选择", Type: "select", Members: []Member{{Kind: "group", Ref: "auto"}, {Kind: "all"}, {Kind: "direct"}}},
		{ID: "auto", Name: "♻️ 自动选择", Type: "url-test", Members: []Member{{Kind: "all"}}},
	}
}

// homeNodes matches home-broadband (residential) node names.
const homeNodes = `(?i)家宽|家庭|住宅|\bhome\b|residential`

// Preset is a built-in starting point offered when creating a profile.
type Preset struct {
	Slug, Name, Description string
	Build                   func() *Profile
}

var Presets = []Preset{
	{"standard", "标准分流", "OpenAI、Claude、其他 AI、流媒体、广告拦截、国内直连，其余走代理", StandardPreset},
	{"global", "全局代理", "除局域网外全部走代理", GlobalPreset},
	{"bypass-cn", "绕过大陆", "中国大陆域名与 IP 直连，其余走代理", BypassCNPreset},
	{"minimal", "极简", "只有节点选择和自动选择，不下载任何规则", MinimalPreset},
}

func PresetBySlug(slug string) *Profile {
	for _, p := range Presets {
		if p.Slug == slug {
			return p.Build()
		}
	}
	return nil
}

func GlobalPreset() *Profile {
	return &Profile{RuleSets: []RuleSet{inlinePrivate()}, Groups: basicGroups(),
		Rules: []Rule{{RuleSet: "lan", Target: "DIRECT"}}, Final: "proxy", DNS: defaultDNS(), Clients: clients()}
}

func BypassCNPreset() *Profile {
	return &Profile{RuleSets: []RuleSet{inlinePrivate(), cat("cn", "中国大陆", "cn")}, Groups: basicGroups(),
		Rules: []Rule{{RuleSet: "lan", Target: "DIRECT"}, {RuleSet: "cn", Target: "DIRECT", ResolveIP: true}},
		Final: "proxy", DNS: defaultDNS("cn"), Clients: clients()}
}

func MinimalPreset() *Profile {
	p := GlobalPreset()
	p.DNS = DNSPolicy{Mode: "fake-ip", Remote: []string{"https://1.1.1.1/dns-query"}}
	return p
}

// StandardPreset is 「标准分流」: OpenAI, Claude and other AI each get a group (Claude
// lists home-broadband nodes first), streaming, ads, CN direct, and a "🐟 final" fallback.
func StandardPreset() *Profile {
	regions := []Member{{Kind: "group", Ref: "hk"}, {Kind: "group", Ref: "tw"}, {Kind: "group", Ref: "jp"}, {Kind: "group", Ref: "sg"}, {Kind: "group", Ref: "us"}}
	sel := func(id, name string, head []Member, tail ...Member) Group {
		ms := append(append([]Member{}, head...), tail...)
		return Group{ID: id, Name: name, Type: "select", Members: ms}
	}
	proxy := []Member{{Kind: "group", Ref: "proxy"}}
	return &Profile{
		RuleSets: []RuleSet{
			inlinePrivate(),
			cat("ads", "广告", "ads"),
			cat("openai", "OpenAI", "openai"),
			cat("anthropic", "Claude", "anthropic"),
			cat("ai", "其他 AI", "ai"),
			cat("netflix", "Netflix", "netflix"), cat("disney", "Disney+", "disney"), cat("youtube", "YouTube", "youtube"),
			cat("hbo", "HBO", "hbo"), cat("primevideo", "Prime Video", "primevideo"), cat("spotify", "Spotify", "spotify"),
			cat("tiktok", "TikTok", "tiktok"), cat("bahamut", "巴哈姆特", "bahamut"),
			cat("telegram", "Telegram", "telegram"), cat("apple", "Apple", "apple"), cat("microsoft", "Microsoft", "microsoft"),
			cat("google", "Google", "google"),
			cat("global", "海外常用", "global"),
			cat("cn", "中国大陆", "cn"),
		},
		Groups: []Group{
			sel("proxy", "🚀 节点选择", append([]Member{{Kind: "group", Ref: "fallback"}, {Kind: "group", Ref: "auto"}}, regions...), Member{Kind: "all"}, Member{Kind: "direct"}),
			// the first healthy node in order; reorder its members to set the priority (e.g. one filter per node)
			{ID: "fallback", Name: "🛟 故障切换", Type: "fallback", Members: []Member{{Kind: "all", Exclude: homeNodes}}},
			{ID: "auto", Name: "♻️ 自动选择", Type: "url-test", Members: []Member{{Kind: "all"}}},
			sel("openai", "🤖 OpenAI", []Member{{Kind: "group", Ref: "us"}, {Kind: "group", Ref: "jp"}, {Kind: "group", Ref: "sg"}, {Kind: "group", Ref: "tw"}}, proxy...),
			// Claude: home-broadband nodes (residential IPs) first, listed directly
			sel("claude", "🟣 Claude", []Member{{Kind: "filter", Include: homeNodes}, {Kind: "group", Ref: "us"}, {Kind: "group", Ref: "jp"}}, proxy...),
			sel("ai", "✨ 其他 AI", []Member{{Kind: "group", Ref: "us"}, {Kind: "group", Ref: "jp"}, {Kind: "group", Ref: "sg"}, {Kind: "group", Ref: "tw"}}, proxy...),
			sel("media", "📺 流媒体", proxy, regions...),
			sel("tg", "📲 Telegram", proxy, regions...),
			sel("apple", "🍎 Apple", []Member{{Kind: "direct"}}, proxy...),
			sel("ms", "Ⓜ️ Microsoft", []Member{{Kind: "direct"}}, proxy...),
			sel("ads", "🛑 广告拦截", []Member{{Kind: "reject"}, {Kind: "direct"}}),
			sel("final", "🐟 final", proxy, Member{Kind: "direct"}),
			region("hk", "🇭🇰 香港", `(?i)港|🇭🇰|\bhk\b|hong ?kong`),
			// not 🇹🇼: iOS with a mainland-China region renders that flag as a blank box
			region("tw", "🏝️ 台湾", `(?i)台|🇹🇼|\btw\b|taiwan`),
			region("jp", "🇯🇵 日本", `(?i)日本|🇯🇵|\bjp\b|japan|tokyo|东京|大阪`),
			region("sg", "🇸🇬 新加坡", `(?i)新加坡|狮城|🇸🇬|\bsg\b|singapore`),
			region("us", "🇺🇸 美国", `(?i)美国|🇺🇸|\bus\b|united states|los angeles|san jose|洛杉矶|圣何塞`),
		},
		Rules: []Rule{
			{RuleSet: "lan", Target: "DIRECT"},
			{RuleSet: "ads", Target: "ads"},
			{RuleSet: "openai", Target: "openai"},
			{RuleSet: "anthropic", Target: "claude"},
			{RuleSet: "ai", Target: "ai"},
			{RuleSet: "netflix", Target: "media"}, {RuleSet: "disney", Target: "media"}, {RuleSet: "youtube", Target: "media"},
			{RuleSet: "hbo", Target: "media"}, {RuleSet: "primevideo", Target: "media"}, {RuleSet: "spotify", Target: "media"},
			{RuleSet: "tiktok", Target: "media"}, {RuleSet: "bahamut", Target: "tw"},
			{RuleSet: "telegram", Target: "tg"},
			{RuleSet: "apple", Target: "apple"}, {RuleSet: "microsoft", Target: "ms"},
			{RuleSet: "google", Target: "proxy"}, {RuleSet: "global", Target: "proxy"},
			{RuleSet: "cn", Target: "DIRECT", ResolveIP: true},
		},
		Final:   "final",
		DNS:     defaultDNS("cn"),
		Clients: clients(),
	}
}
