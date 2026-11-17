package subgen

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// ---------------- Surge ----------------

func surgeQuote(v string) string {
	if strings.ContainsAny(v, ", =\"") {
		return `"` + strings.ReplaceAll(v, `"`, ``) + `"`
	}
	return v
}

func (e Endpoint) surgeProxy() string {
	kv := []string{}
	add := func(k, v string) {
		if v != "" {
			kv = append(kv, k+"="+surgeQuote(v))
		}
	}
	tls := func() {
		add("sni", e.SNI)
		add("alpn", strings.Join(e.ALPN, ","))
		if e.Insecure {
			add("skip-cert-verify", "true")
		}
	}
	ws := func() {
		if e.Network == "ws" {
			add("ws", "true")
			add("ws-path", or(e.Path, "/"))
			h := e.transportHeaders()
			if len(h) > 0 {
				keys := make([]string, 0, len(h))
				for k := range h {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, k+":"+strconv.Quote(h[k]))
				}
				kv = append(kv, "ws-headers="+strings.Join(parts, "|"))
			}
		}
	}
	head := ""
	srv := e.Address + ", " + strconv.Itoa(e.Port)
	switch e.Type {
	case "shadowsocks":
		head = "ss, " + srv
		add("encrypt-method", e.SSMethod)
		add("password", e.Password)
		add("udp-relay", "true")
	case "vmess":
		head = "vmess, " + srv
		add("username", e.UUID)
		add("vmess-aead", "true")
		if e.Security == "tls" {
			add("tls", "true")
			tls()
		}
		ws()
	case "trojan":
		head = "trojan, " + srv
		add("password", e.Password)
		tls()
		ws()
	case "hysteria2":
		head = "hysteria2, " + srv
		if e.ObfsType == "salamander" {
			add("salamander-password", e.ObfsPassword)
		}
		add("password", e.Password)
		tls()
	case "tuic":
		head = "tuic-v5, " + srv
		add("uuid", e.UUID)
		add("password", e.Password)
		if len(e.ALPN) == 0 {
			add("alpn", "h3") // Surge's TUIC needs an ALPN; sing-box's TUIC server speaks h3
		}
		tls()
	}
	switch e.ipStrategy() {
	case "prefer_ipv6":
		add("ip-version", "prefer-v6")
	case "prefer_ipv4":
		add("ip-version", "prefer-v4")
	}
	s := e.Remark + " = " + head
	if len(kv) > 0 {
		s += ", " + strings.Join(kv, ", ")
	}
	return s
}

var surgeType = map[string]string{"domain": "DOMAIN", "domain_suffix": "DOMAIN-SUFFIX", "domain_keyword": "DOMAIN-KEYWORD",
	"geoip": "GEOIP", "process_name": "PROCESS-NAME", "port": "DEST-PORT"}

func surgeSections(c *Compiled, opts map[string]string) map[string]string {
	var px, gs, rs strings.Builder
	px.WriteString("[Proxy]\n")
	for _, e := range c.Nodes {
		px.WriteString(e.surgeProxy() + "\n")
	}
	gs.WriteString("[Proxy Group]\n")
	for _, g := range c.Groups {
		typ := g.Type
		line := g.Name + " = " + typ + ", " + strings.Join(g.Out, ", ")
		if g.Type != "select" {
			line += fmt.Sprintf(", url=%s, interval=%d", or(g.TestURL, opts["test_url"]), orI(g.Interval, 300))
			if g.Type == "url-test" {
				line += fmt.Sprintf(", tolerance=%d", orI(g.Tolerance, 50))
			}
		}
		gs.WriteString(line + "\n")
	}
	rs.WriteString("[Rule]\n")
	for _, r := range c.Rules {
		if r.Set.Remote != nil {
			refs := remotesFor("surge", r.Set, c.Mirror)
			geo := ""
			if r.Set.Remote.URLs["surge"] == "" {
				geo = Catalog[r.Set.Remote.Catalog].SurgeGeoIP
			}
			if len(refs) == 0 && geo == "" {
				c.warn("Rule set %s has no Surge source; skipped", r.Set.Name)
			}
			for _, ref := range refs {
				rs.WriteString("RULE-SET," + ref.URL + "," + r.Target + "\n")
			}
			if geo != "" {
				rs.WriteString("GEOIP," + geo + "," + r.Target + "\n")
			}
		}
		for _, m := range r.Set.Inline {
			for _, v := range m.Values {
				switch m.Type {
				case "ip_cidr":
					t := "IP-CIDR"
					if strings.Contains(v, ":") {
						t = "IP-CIDR6"
					}
					s := t + "," + v + "," + r.Target
					if m.NoResolve {
						s += ",no-resolve"
					}
					rs.WriteString(s + "\n")
				case "domain_regex", "geosite", "network":
					c.warn("Surge doesn't support %s matching; skipped %s", m.Type, v)
				case "geoip":
					rs.WriteString("GEOIP," + strings.ToUpper(v) + "," + r.Target + "\n")
				default:
					rs.WriteString(surgeType[m.Type] + "," + v + "," + r.Target + "\n")
				}
			}
		}
	}
	rs.WriteString("FINAL," + c.Final + ",dns-failed\n")
	// Surge: dns-server takes plain IPs only; DoH/DoQ go to encrypted-dns-server.
	plain := []string{"system"}
	var enc []string
	for _, s := range append(append([]string{}, c.DNS.Domestic...), c.DNS.Remote...) {
		if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "quic://") {
			enc = append(enc, s)
		} else if ip := strings.TrimPrefix(s, "udp://"); net.ParseIP(ip) != nil {
			plain = append(plain, ip)
		}
	}
	if len(plain) == 1 {
		plain = append(plain, "223.5.5.5", "119.29.29.29") // bootstrap for encrypted DNS
	}
	dns := "dns-server = " + strings.Join(plain, ", ") + "\n"
	if len(enc) > 0 {
		dns += "encrypted-dns-server = " + strings.Join(enc, ", ") + "\n"
	}
	px.WriteString("\n")
	gs.WriteString("\n")
	return map[string]string{"proxies": px.String(), "proxy_groups": gs.String(), "rules": rs.String(), "dns": dns}
}
