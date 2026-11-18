package subgen

import (
	"fmt"
	"regexp"
	"strings"
)

// Caps describes what a client can consume.
type Caps struct {
	Protocols  map[string]bool // vless, vmess, trojan, shadowsocks, hysteria2, tuic, anytls
	Reality    bool
	Transports map[string]bool // "", ws, grpc, http, httpupgrade
	SSMethods  map[string]bool // nil = any
}

func set(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

var ClientCaps = map[string]Caps{
	"mihomo":       {Protocols: set("vless", "vmess", "trojan", "shadowsocks", "hysteria2", "tuic", "anytls"), Reality: true, Transports: set("", "tcp", "ws", "grpc", "http", "httpupgrade")},
	"shadowrocket": {Protocols: set("vless", "vmess", "trojan", "shadowsocks", "hysteria2", "tuic", "anytls"), Reality: true, Transports: set("", "tcp", "ws", "grpc", "http", "httpupgrade")},
	"singbox":      {Protocols: set("vless", "vmess", "trojan", "shadowsocks", "hysteria2", "tuic", "anytls"), Reality: true, Transports: set("", "tcp", "ws", "grpc", "http", "httpupgrade")},
	// Surge 5: no VLESS, no REALITY, no gRPC/h2/httpupgrade; ws only for vmess/trojan.
	"surge": {Protocols: set("vmess", "trojan", "shadowsocks", "hysteria2", "tuic"), Transports: set("", "tcp", "ws"),
		SSMethods: set("aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305")},
}

// Supported reports whether client c can use endpoint e, with a reason if not.
func Supported(client string, e Endpoint) (bool, string) {
	c, ok := ClientCaps[client]
	if !ok {
		return true, ""
	}
	if e.ObfsType != "" && e.ObfsType != "salamander" {
		return false, fmt.Sprintf("%s does not export Hysteria2 obfuscation %s", client, e.ObfsType)
	}
	if e.Network != "" && e.Network != "tcp" && e.Type != "vless" && e.Type != "vmess" && e.Type != "trojan" {
		return false, fmt.Sprintf("%s does not export %s transport for %s", client, e.Network, e.Type)
	}
	if !c.Protocols[e.Type] {
		return false, fmt.Sprintf("%s doesn't support %s", client, e.Type)
	}
	if e.Security == "reality" && !c.Reality {
		return false, fmt.Sprintf("%s doesn't support REALITY", client)
	}
	if !c.Transports[e.Network] {
		return false, fmt.Sprintf("%s doesn't support transport %s", client, e.Network)
	}
	if e.Type == "shadowsocks" && c.SSMethods != nil && !c.SSMethods[e.SSMethod] {
		return false, fmt.Sprintf("%s doesn't support cipher %s", client, e.SSMethod)
	}
	return true, ""
}

// CGroup is a resolved group: concrete member names.
type CGroup struct {
	Group
	Out []string // member names as the client sees them (nodes, group names, DIRECT, REJECT)
}

// CRule is a resolved rule: rule set + target name.
type CRule struct {
	Set       RuleSet
	Target    string // client-visible name, "DIRECT" or "REJECT"
	ResolveIP bool
}

// Compiled is the client-specific, fully resolved model.
type Compiled struct {
	Client   string
	Nodes    []Endpoint
	Groups   []CGroup
	Rules    []CRule
	Final    string
	DNS      DNSPolicy
	Sets     map[string]RuleSet
	Mirror   string
	Warnings []string
}

// ipv6Node names the first node that prefers IPv6 for a hostname address.
func (c *Compiled) ipv6Node() string {
	for _, e := range c.Nodes {
		if e.ipStrategy() == "prefer_ipv6" {
			return e.Remark
		}
	}
	return ""
}

// dnsGeosite returns the geosite name behind a rule set id, for DNS policy.
func (c *Compiled) dnsGeosite(id string) string {
	rs, ok := c.Sets[id]
	if !ok || rs.Remote == nil {
		return ""
	}
	return Catalog[rs.Remote.Catalog].Geosite
}

func (c *Compiled) warn(f string, a ...any) { c.Warnings = append(c.Warnings, fmt.Sprintf(f, a...)) }

// Validate checks the profile is self-consistent (ids, references, cycles, regexes, URLs).
func Validate(p *Profile) error {
	total := 0
	for _, rs := range p.RuleSets {
		for _, m := range rs.Inline {
			total += len(m.Values)
		}
	}
	if total > MaxInlineValues {
		return fmt.Errorf("%d inline rules exceed the limit of %d; use remote rule sets instead", total, MaxInlineValues)
	}
	for c, ct := range p.Clients {
		if len(ct.Base) > MaxTemplate {
			return fmt.Errorf("The %s template exceeds 64 KB", c)
		}
	}
	if len(p.Groups) == 0 {
		return fmt.Errorf("At least one proxy group is required")
	}
	sets := map[string]bool{}
	for _, rs := range p.RuleSets {
		if rs.ID == "" || sets[rs.ID] {
			return fmt.Errorf("Rule set id is empty or duplicated: %q", rs.ID)
		}
		sets[rs.ID] = true
		if rs.Remote == nil && len(rs.Inline) == 0 {
			return fmt.Errorf("Rule set %s is empty", rs.ID)
		}
		if rs.Remote != nil {
			for k, u := range rs.Remote.URLs {
				if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
					return fmt.Errorf("Rule set %s: the %s URL must be http(s)", rs.ID, k)
				}
			}
			if rs.Remote.Catalog != "" {
				if _, ok := Catalog[rs.Remote.Catalog]; !ok {
					return fmt.Errorf("Rule set %s: unknown category %s", rs.ID, rs.Remote.Catalog)
				}
			}
		}
		for _, m := range rs.Inline {
			if !matcherTypes[m.Type] {
				return fmt.Errorf("Rule set %s: unknown match type %s", rs.ID, m.Type)
			}
			for _, v := range m.Values {
				if strings.ContainsAny(v, ",\n\r\"") {
					return fmt.Errorf("Rule set %s: value contains invalid characters: %q", rs.ID, v)
				}
				if m.Type == "domain_regex" {
					if _, err := regexp.Compile(v); err != nil {
						return fmt.Errorf("Rule set %s: bad regex %v", rs.ID, err)
					}
				}
			}
		}
	}
	if p.Mirror != "" && !((strings.HasPrefix(p.Mirror, "https://") || strings.HasPrefix(p.Mirror, "http://")) && strings.HasSuffix(p.Mirror, "/")) {
		return fmt.Errorf("The mirror prefix must be an http(s) URL ending in /")
	}
	groups := map[string]Group{}
	names := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, g := range p.Groups {
		if g.ID == "" || groups[g.ID].ID != "" || names[g.Name] || g.Name == "" {
			return fmt.Errorf("Group id/name is empty or duplicated: %q", g.ID)
		}
		if strings.ContainsAny(g.Name, ",=\n\"") {
			return fmt.Errorf("Group name %q contains invalid characters", g.Name)
		}
		switch g.Type {
		case "select", "url-test", "fallback", "load-balance":
		default:
			return fmt.Errorf("Group %s: unknown type %s", g.ID, g.Type)
		}
		groups[g.ID], names[g.Name] = g, true
		for _, m := range g.Members {
			for _, re := range []string{m.Include, m.Exclude} {
				if _, err := regexp.Compile(re); err != nil {
					return fmt.Errorf("Group %s: bad regex %v", g.ID, err)
				}
			}
		}
	}
	// cycles among group references
	state := map[string]int{}
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("Group reference cycle: %s", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, m := range groups[id].Members {
			if m.Kind == "group" {
				if _, ok := groups[m.Ref]; !ok {
					return fmt.Errorf("Group %s references missing group %s", id, m.Ref)
				}
				if err := visit(m.Ref); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		return nil
	}
	for id := range groups {
		if err := visit(id); err != nil {
			return err
		}
	}
	target := func(t string) bool { _, ok := groups[t]; return ok || t == "DIRECT" || t == "REJECT" }
	for _, r := range p.Rules {
		if !sets[r.RuleSet] {
			return fmt.Errorf("A route references missing rule set %s", r.RuleSet)
		}
		if !target(r.Target) {
			return fmt.Errorf("Route target doesn't exist: %s", r.Target)
		}
	}
	if p.Final == "REJECT" || !target(p.Final) {
		return fmt.Errorf("Invalid fallback target: %s", p.Final)
	}
	return nil
}

var matcherTypes = set("domain", "domain_suffix", "domain_keyword", "domain_regex", "ip_cidr", "geosite", "geoip", "process_name", "port", "network")

// Compile resolves a profile for one client against the user's endpoints.
func Compile(p *Profile, client string, eps []Endpoint) (*Compiled, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	c := &Compiled{Client: client, DNS: p.DNS}
	reserved := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, g := range p.Groups {
		reserved[g.Name] = true
	}
	// 1. capability filter + name sanitising/dedup
	seen := map[string]int{}
	for _, e := range eps {
		if ok, why := Supported(client, e); !ok {
			c.warn("Skipped node %s: %s", e.Remark, why)
			continue
		}
		n := strings.NewReplacer(",", " ", "=", "-", "\n", " ", "\r", " ", "\"", "'").Replace(strings.TrimSpace(e.Remark))
		if n == "" {
			n = e.Type
		}
		base := n
		for k := 2; reserved[n] || seen[n] > 0; k++ {
			n = fmt.Sprintf("%s %d", base, k)
		}
		seen[n]++
		e.Remark = n
		c.Nodes = append(c.Nodes, e)
	}
	// A node that must be reached over IPv6 needs the client's IPv6 switched on:
	// Mihomo's dns.ipv6=false drops AAAA answers and sing-box's ipv4_only strategy
	// never resolves them, so the per-node preference would silently become IPv4.
	if !c.DNS.IPv6 {
		if n := c.ipv6Node(); n != "" {
			c.DNS.IPv6 = true
			c.warn("IPv6 DNS enabled: node %s prefers IPv6", n)
		}
	}
	// 2. resolve groups (depth-first so referenced groups are known first)
	byID := map[string]Group{}
	for _, g := range p.Groups {
		byID[g.ID] = g
	}
	resolved := map[string][]string{} // id -> members; nil entry + present = dropped
	dropped := map[string]bool{}
	var resolve func(id string) []string
	resolve = func(id string) []string {
		if out, ok := resolved[id]; ok {
			return out
		}
		g := byID[id]
		var out []string
		dup := map[string]bool{}
		add := func(n string) {
			if !dup[n] {
				dup[n] = true
				out = append(out, n)
			}
		}
		hasNode := false
		for _, m := range g.Members {
			switch m.Kind {
			case "all", "filter":
				inc, _ := regexp.Compile(m.Include)
				var exc *regexp.Regexp
				if m.Exclude != "" {
					exc, _ = regexp.Compile(m.Exclude)
				}
				for _, e := range c.Nodes {
					hay := e.Remark + "\x00" + e.Node
					if m.Kind == "filter" && !inc.MatchString(hay) {
						continue
					}
					if exc != nil && exc.MatchString(hay) {
						continue
					}
					add(e.Remark)
					hasNode = true
				}
			case "group":
				sub := resolve(m.Ref)
				if dropped[m.Ref] {
					continue
				}
				add(byID[m.Ref].Name)
				if len(sub) > 0 {
					hasNode = true
				}
			case "direct":
				add("DIRECT")
			case "reject":
				add("REJECT")
			}
		}
		if !hasNode && g.HideIfEmpty {
			dropped[id] = true
			c.warn("Group %s has no usable nodes and was hidden", g.Name)
		}
		if len(out) == 0 {
			out = []string{"DIRECT"} // clients reject empty groups
			if !dropped[id] {
				c.warn("Group %s has no members; DIRECT was used as a placeholder", g.Name)
			}
		}
		// url-test/fallback/load-balance can't contain REJECT meaningfully; keep as-is but avoid groups-only DIRECT confusion.
		resolved[id] = out
		return out
	}
	for _, g := range p.Groups {
		out := resolve(g.ID)
		if !dropped[g.ID] {
			c.Groups = append(c.Groups, CGroup{Group: g, Out: out})
		}
	}
	name := func(t string) string {
		if t == "DIRECT" || t == "REJECT" {
			return t
		}
		if dropped[t] {
			return ""
		}
		return byID[t].Name
	}
	c.Final = name(p.Final)
	if c.Final == "" {
		c.Final = "DIRECT"
		c.warn("The fallback group was hidden; using DIRECT")
	}
	// 3. rules
	sets := map[string]RuleSet{}
	for _, rs := range p.RuleSets {
		sets[rs.ID] = rs
	}
	c.Sets = sets
	for _, r := range p.Rules {
		if r.Disabled {
			continue
		}
		t := name(r.Target)
		if t == "" {
			t = c.Final
			c.warn("The target group of rule %s was hidden; using the fallback %s", r.RuleSet, c.Final)
		}
		c.Rules = append(c.Rules, CRule{Set: sets[r.RuleSet], Target: t, ResolveIP: r.ResolveIP})
	}
	return c, nil
}
