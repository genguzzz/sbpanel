package master

import (
	"bytes"
	"crypto/ecdh"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rsc.io/qr"

	"sbpanel/internal/subgen"
)

// subEntry is one client-facing endpoint: an inbound as reached through a host.
type subEntry subgen.Endpoint

type subUser struct {
	ID              int64
	Username, Token string
	UUID, Password  string
	Status          string
	Enabled         bool
	Up, Down, Limit int64
	ExpireAt        int64
	ResetStrategy   string
	CreatedAt       int64
	LastResetAt     int64
	DeviceLimit     int
	Blocked         map[int64]bool // inbounds a traffic limit has closed for this user
}

func (u subUser) active() bool { return u.Enabled && u.Status == "active" }

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func obj(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	return v
}

func strs(m map[string]any, k string) []string {
	switch v := m[k].(type) {
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

// realityPublicKey derives the client public key from the server private key.
func realityPublicKey(priv string) string {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(priv, "="))
	if err != nil {
		return ""
	}
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
}

// entryFromInbound fills protocol/TLS/transport fields from server-side options.
func entryFromInbound(typ string, opts map[string]any, client map[string]any) subEntry {
	e := subEntry{Type: typ, Network: "tcp", Flow: str(opts, "_flow"), FP: "chrome"}
	if p, ok := opts["listen_port"].(float64); ok {
		e.Port = int(p)
	}
	if tls := obj(opts, "tls"); tls != nil && tls["enabled"] == true {
		e.Security = "tls"
		e.SNI = str(tls, "server_name")
		e.ALPN = strs(tls, "alpn")
		if r := obj(tls, "reality"); r != nil && r["enabled"] == true {
			e.Security = "reality"
			e.PBK = realityPublicKey(str(r, "private_key"))
			if sids := strs(r, "short_id"); len(sids) > 0 {
				e.SID = sids[0]
			}
			e.ALPN = nil
		}
		if acme := obj(tls, "acme"); e.SNI == "" && acme != nil {
			if d := strs(acme, "domain"); len(d) > 0 {
				e.SNI = d[0]
			}
		}
	}
	if t := obj(opts, "transport"); t != nil {
		e.Network = str(t, "type")
		e.Path = str(t, "path")
		e.Service = str(t, "service_name")
		if v, ok := t["max_early_data"].(float64); ok {
			e.MaxEarlyData = int(v)
		}
		e.EarlyDataHeader = str(t, "early_data_header_name")
		e.Host = str(t, "host")
		if h := obj(t, "headers"); h != nil {
			if e.Host == "" {
				e.Host = str(h, "Host")
			}
			e.Headers = map[string]string{}
			for k, v := range h {
				if value, ok := v.(string); ok {
					e.Headers[k] = value
				}
			}
		}
		if hs := strs(t, "host"); e.Host == "" && len(hs) > 0 {
			e.Host = hs[0]
		}
	}
	if typ == "hysteria2" {
		if o := obj(opts, "obfs"); o != nil {
			e.ObfsType, e.ObfsPassword = str(o, "type"), str(o, "password")
		}
	}
	if typ == "shadowsocks" {
		e.SSMethod = str(opts, "method")
		e.Network = ""
	}
	if typ == "tuic" {
		e.CC = str(opts, "congestion_control")
		if e.CC == "" {
			e.CC = "bbr"
		}
	}
	if typ == "hysteria2" || typ == "tuic" {
		e.Network = ""
		if len(e.ALPN) == 0 {
			e.ALPN = []string{"h3"}
		}
	}
	// panel-side client overrides saved with the inbound
	if v := str(client, "fingerprint"); v != "" {
		e.FP = v
	}
	if v := str(client, "sni"); v != "" && e.SNI == "" {
		e.SNI = v
	}
	if v, ok := client["insecure"].(bool); ok {
		e.Insecure = v
	}
	return e
}

type hostRow struct {
	Remark, Address, SNI, Host, Path, ALPN, FP string
	Port                                       int
	Insecure                                   bool
}

// subEntries lists every endpoint the user may use, in node/inbound/host order.
// A negative user ID lists every enabled inbound (admin previews).
func (m *Master) subEntries(u subUser) ([]subEntry, error) {
	q := `SELECT DISTINCT i.id, i.type, i.tag, i.remark, i.options_json, i.client_json, n.name, n.public_address, n.address, n.client_ip, n.created_at, i.sort, i.port
		FROM inbounds i JOIN nodes n ON n.id=i.node_id JOIN group_inbounds gi ON gi.inbound_id=i.id JOIN user_groups ug ON ug.group_id=gi.group_id
		WHERE ug.user_id=? AND i.enabled=1 ORDER BY n.created_at, i.sort, i.port`
	if u.ID < 0 {
		q = `SELECT i.id, i.type, i.tag, i.remark, i.options_json, i.client_json, n.name, n.public_address, n.address, n.client_ip, n.created_at, i.sort, i.port
		FROM inbounds i JOIN nodes n ON n.id=i.node_id WHERE i.enabled=1 AND ?<0 ORDER BY n.created_at, i.sort, i.port`
	}
	rows, err := m.db.Query(q, u.ID)
	if err != nil {
		return nil, err
	}
	var ins []subInbound
	for rows.Next() {
		var x subInbound
		var created, srt, port int64
		rows.Scan(&x.id, &x.typ, &x.tag, &x.remark, &x.opts, &x.client, &x.node, &x.pub, &x.addr, &x.ipPrefer, &created, &srt, &port)
		ins = append(ins, x)
	}
	rows.Close()
	var out []subEntry
	for _, in := range ins {
		var o map[string]any
		if json.Unmarshal([]byte(in.opts), &o) == nil && isStatic(o) {
			continue // standalone entries are never handed to users
		}
		es, err := m.expandInbound(in, u.UUID, u.Password)
		if err != nil {
			return nil, err
		}
		out = append(out, es...)
	}
	// make remarks unique; clients key proxies by name
	seen := map[string]int{}
	for i := range out {
		r := out[i].Remark
		seen[r]++
		if seen[r] > 1 {
			out[i].Remark = fmt.Sprintf("%s %d", r, seen[r])
		}
	}
	return out, nil
}

// subInbound is one enabled inbound row as subscriptions (and the probe) see it.
type subInbound struct {
	id                                              int64
	typ, tag, remark, opts, client, node, pub, addr string
	ipPrefer                                        string
}

// expandInbound turns an inbound into its client endpoints (one per enabled host,
// or the node address + port) for the given credentials.
func (m *Master) expandInbound(in subInbound, uuid, password string) ([]subEntry, error) {
	var opts, client map[string]any
	json.Unmarshal([]byte(in.opts), &opts)
	json.Unmarshal([]byte(in.client), &client)
	base := entryFromInbound(in.typ, opts, client)
	base.UUID, base.Password = uuid, password
	if in.typ == "shadowsocks" {
		base.Password = ssClientPassword(base.SSMethod, str(opts, "password"), password)
	}
	if id := str(opts, "_cert"); id != "" && base.SNI == "" {
		var sans string
		m.db.QueryRow(`SELECT sans_json FROM certs WHERE id=?`, id).Scan(&sans)
		var ss []string
		json.Unmarshal([]byte(sans), &ss)
		base.SNI = primaryName(ss)
	}
	addr := in.pub
	if addr == "" {
		addr = in.addr
	}
	base.Address = addr
	base.IPPrefer = in.ipPrefer
	label := in.remark
	if label == "" {
		label = in.tag
	}
	base.Region = m.exitRegion(in.node, in.opts)
	name := in.node
	if base.Region != "" {
		name += " " + base.Region
	}
	base.Remark = name + " · " + label
	base.Node, base.Inbound, base.Line = in.node, in.id, base.Remark
	hosts, err := scanRows(m, `SELECT remark, address, port, sni, host, path, alpn, fingerprint, allow_insecure FROM hosts
		WHERE inbound_id=? AND enabled=1 ORDER BY sort, id`, []any{in.id}, func(s func(...any) error) (hostRow, error) {
		var h hostRow
		return h, s(&h.Remark, &h.Address, &h.Port, &h.SNI, &h.Host, &h.Path, &h.ALPN, &h.FP, &h.Insecure)
	})
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return []subEntry{base}, nil
	}
	var out []subEntry
	for _, h := range hosts {
		e := base
		if h.Remark != "" {
			e.Remark = name + " · " + h.Remark // keep "<node> <REGION> · " so every entry says where it exits
		}
		if h.Address != "" {
			e.Address = h.Address
		}
		if h.Port > 0 {
			e.Port = h.Port
		}
		if h.SNI != "" {
			e.SNI = h.SNI
		}
		if h.Host != "" {
			e.Host = h.Host
		}
		if h.Path != "" {
			e.Path = h.Path
		}
		if h.ALPN != "" {
			e.ALPN = strings.Split(h.ALPN, ",")
		}
		if h.FP != "" {
			e.FP = h.FP
		}
		if h.Insecure {
			e.Insecure = true
		}
		out = append(out, e)
	}
	return out, nil
}

// ssClientPassword is what a Shadowsocks client needs: 2022 multi-user methods use
// "serverPSK:userPSK"; legacy methods use the user key alone.
func ssClientPassword(method, serverPSK, userPassword string) string {
	key := ssKey(userPassword, ssKeyLen(method))
	if strings.HasPrefix(method, "2022-") {
		return serverPSK + ":" + key
	}
	return key
}

func hostPort(h string, p int) string { return net.JoinHostPort(h, strconv.Itoa(p)) }

// ---------- share URIs ----------

func (e subEntry) uri() string {
	frag := "#" + url.PathEscape(e.Remark)
	q := url.Values{}
	addTLS := func() {
		switch e.Security {
		case "reality":
			q.Set("security", "reality")
			q.Set("pbk", e.PBK)
			if e.SID != "" {
				q.Set("sid", e.SID)
			}
		case "tls":
			q.Set("security", "tls")
			if len(e.ALPN) > 0 {
				q.Set("alpn", strings.Join(e.ALPN, ","))
			}
			if e.Insecure {
				q.Set("allowInsecure", "1")
			}
		default:
			q.Set("security", "none")
		}
		if e.SNI != "" {
			q.Set("sni", e.SNI)
		}
		if e.Security != "" && e.FP != "" {
			q.Set("fp", e.FP)
		}
	}
	addTransport := func() {
		q.Set("type", e.Network)
		switch e.Network {
		case "ws", "httpupgrade", "http":
			if e.Path != "" {
				q.Set("path", e.Path)
			}
			if e.Host != "" {
				q.Set("host", e.Host)
			}
		case "grpc":
			q.Set("serviceName", e.Service)
		}
	}
	switch e.Type {
	case "vless":
		q.Set("encryption", "none")
		if e.Flow != "" {
			q.Set("flow", e.Flow)
		}
		addTLS()
		addTransport()
		return "vless://" + e.UUID + "@" + hostPort(e.Address, e.Port) + "?" + q.Encode() + frag
	case "trojan":
		addTLS()
		addTransport()
		return "trojan://" + url.PathEscape(e.Password) + "@" + hostPort(e.Address, e.Port) + "?" + q.Encode() + frag
	case "vmess":
		m := map[string]any{"v": "2", "ps": e.Remark, "add": e.Address, "port": strconv.Itoa(e.Port), "id": e.UUID,
			"aid": "0", "scy": "auto", "net": e.Network, "type": "none", "host": e.Host, "path": e.Path, "tls": e.Security,
			"sni": e.SNI, "alpn": strings.Join(e.ALPN, ","), "fp": e.FP}
		if e.Insecure {
			m["allowInsecure"] = true
		}
		if e.Network == "grpc" {
			m["path"] = e.Service
		}
		b, _ := json.Marshal(m)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case "shadowsocks":
		ui := base64.RawURLEncoding.EncodeToString([]byte(e.SSMethod + ":" + e.Password))
		return "ss://" + ui + "@" + hostPort(e.Address, e.Port) + frag
	case "hysteria2":
		if e.ObfsType != "" {
			q.Set("obfs", e.ObfsType)
			q.Set("obfs-password", e.ObfsPassword)
		}
		if len(e.ALPN) > 0 {
			q.Set("alpn", strings.Join(e.ALPN, ","))
		}
		if e.SNI != "" {
			q.Set("sni", e.SNI)
		}
		if e.Insecure {
			q.Set("insecure", "1")
		}
		return "hysteria2://" + url.PathEscape(e.Password) + "@" + hostPort(e.Address, e.Port) + "/?" + q.Encode() + frag
	case "tuic":
		q.Set("congestion_control", e.CC)
		q.Set("alpn", strings.Join(e.ALPN, ","))
		if e.SNI != "" {
			q.Set("sni", e.SNI)
		}
		if e.Insecure {
			q.Set("allow_insecure", "1")
		}
		return "tuic://" + e.UUID + ":" + url.PathEscape(e.Password) + "@" + hostPort(e.Address, e.Port) + "?" + q.Encode() + frag
	case "anytls":
		if e.SNI != "" {
			q.Set("sni", e.SNI)
		}
		if e.Insecure {
			q.Set("insecure", "1")
		}
		return "anytls://" + url.PathEscape(e.Password) + "@" + hostPort(e.Address, e.Port) + "/?" + q.Encode() + frag
	}
	return ""
}

// ---------- sing-box ----------

// singboxOutbound and mihomoProxy live in subgen so the routing generators share them.

func singboxConfig(es []subEntry) []byte {
	tags := make([]string, 0, len(es))
	outs := []any{}
	for _, e := range es {
		tags = append(tags, e.Remark)
	}
	if len(tags) == 0 {
		tags = []string{"direct"}
	}
	outs = append(outs,
		map[string]any{"type": "selector", "tag": "proxy", "outbounds": append([]string{"auto"}, tags...), "default": "auto"},
		map[string]any{"type": "urltest", "tag": "auto", "outbounds": tags, "url": "https://www.gstatic.com/generate_204", "interval": "3m"},
	)
	for _, e := range es {
		outs = append(outs, subgen.Endpoint(e).SingboxConfigOutbound())
	}
	outs = append(outs, map[string]any{"type": "direct", "tag": "direct"})
	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"},
				map[string]any{"type": "local", "tag": "local"},
			},
			"final": "remote",
		},
		"inbounds": []any{
			map[string]any{"type": "tun", "tag": "tun-in", "address": []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}, "auto_route": true, "strict_route": true},
			map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080},
		},
		"outbounds": outs,
		"route": map[string]any{
			"rules": []any{
				map[string]any{"action": "sniff"},
				map[string]any{"protocol": "dns", "action": "hijack-dns"},
				map[string]any{"ip_is_private": true, "outbound": "direct"},
			},
			"final":                   "proxy",
			"auto_detect_interface":   true,
			"default_domain_resolver": "local",
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return b
}

// ---------- mihomo / clash ----------

func (e subEntry) mihomoProxy() map[string]any { return subgen.Endpoint(e).MihomoProxy() }

// yq renders a scalar as a JSON string, which YAML accepts verbatim.
func yq(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func mihomoConfig(es []subEntry) []byte {
	var b bytes.Buffer
	b.WriteString("mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: warning\nipv6: true\n")
	b.WriteString("dns:\n  enable: true\n  enhanced-mode: fake-ip\n  nameserver:\n    - https://1.1.1.1/dns-query\n    - https://223.5.5.5/dns-query\n")
	b.WriteString("proxies:\n")
	names := make([]string, 0, len(es))
	for _, e := range es {
		j, _ := json.Marshal(e.mihomoProxy()) // JSON flow mapping is valid YAML
		b.WriteString("  - " + string(j) + "\n")
		names = append(names, e.Remark)
	}
	if len(es) == 0 {
		b.WriteString("  []\n")
	}
	list := func(xs []string) string {
		q := make([]string, len(xs))
		for i, x := range xs {
			q[i] = yq(x)
		}
		return "[" + strings.Join(q, ", ") + "]"
	}
	b.WriteString("proxy-groups:\n")
	sel := append([]string{"自动选择"}, names...)
	if len(names) == 0 {
		sel = []string{"DIRECT"}
	}
	b.WriteString("  - {name: " + yq("节点选择") + ", type: select, proxies: " + list(sel) + "}\n")
	auto := names
	if len(auto) == 0 {
		auto = []string{"DIRECT"}
	}
	b.WriteString("  - {name: " + yq("自动选择") + ", type: url-test, url: \"https://www.gstatic.com/generate_204\", interval: 300, tolerance: 50, proxies: " + list(auto) + "}\n")
	b.WriteString("rules:\n")
	for _, r := range []string{"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve", "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve", "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve", "MATCH,节点选择"} {
		b.WriteString("  - " + yq(r) + "\n")
	}
	return b.Bytes()
}

// ---------- HTTP ----------

func detectFormat(r *http.Request) string {
	switch f := strings.ToLower(r.URL.Query().Get("format")); f {
	case "base64", "links", "singbox", "sing-box", "clash", "mihomo", "surge", "shadowrocket", "html", "json":
		if f == "sing-box" {
			return "singbox"
		}
		if f == "clash" {
			return "mihomo"
		}
		return f
	}
	ua := strings.ToLower(r.UserAgent())
	switch {
	case strings.Contains(ua, "shadowrocket"):
		return "shadowrocket"
	case strings.Contains(ua, "surge"):
		return "surge"
	case strings.Contains(ua, "clash"), strings.Contains(ua, "mihomo"), strings.Contains(ua, "stash"), strings.Contains(ua, "nyanpasu"):
		return "mihomo"
	case strings.Contains(ua, "sing-box"), strings.Contains(ua, "sfa"), strings.Contains(ua, "sfi"), strings.Contains(ua, "sfm"), strings.Contains(ua, "sft"):
		return "singbox"
	case strings.Contains(ua, "mozilla") && strings.Contains(r.Header.Get("Accept"), "text/html"):
		return "html"
	}
	return "base64"
}

func (m *Master) loadSubUser(token string) (subUser, error) {
	var u subUser
	err := m.db.QueryRow(`SELECT id, username, sub_token, uuid, password, status, enabled, used_up, used_down, traffic_limit, expire_at, reset_strategy,
		created_at, last_reset_at, device_limit
		FROM users WHERE sub_token=?`, token).Scan(&u.ID, &u.Username, &u.Token, &u.UUID, &u.Password, &u.Status, &u.Enabled,
		&u.Up, &u.Down, &u.Limit, &u.ExpireAt, &u.ResetStrategy, &u.CreatedAt, &u.LastResetAt, &u.DeviceLimit)
	return u, err
}

// clientIP is the visitor's address. Behind a local reverse proxy (loopback peer) it trusts
// X-Real-IP, which the proxy sets, and otherwise the right-most X-Forwarded-For entry (the
// one the proxy appended); the left-most entries come from the client and can be forged.
func clientIP(r *http.Request) string {
	if !isLoopback(r.RemoteAddr) {
		return hostOnly(r.RemoteAddr)
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" && net.ParseIP(xr) != nil {
		return xr
	}
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		parts := strings.Split(xf, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
			return ip
		}
	}
	return hostOnly(r.RemoteAddr)
}

func isLoopback(addr string) bool {
	ip := net.ParseIP(hostOnly(addr))
	return ip != nil && ip.IsLoopback()
}

func (m *Master) subBaseURL(r *http.Request) string {
	if v, ok := getSetting(m.db, "sub_base_url"); ok && v != "" {
		return strings.TrimRight(v, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (m *Master) subURL(r *http.Request, token string) string {
	return m.subBaseURL(r) + "/s/" + token
}

func (m *Master) handleSub(w http.ResponseWriter, r *http.Request) {
	u, err := m.loadSubUser(r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	format := detectFormat(r)
	m.db.Exec(`INSERT INTO sub_access(ts, user_id, ip, ua, format) VALUES(?,?,?,?,?)`, now(), u.ID, clientIP(r), truncate(r.UserAgent(), 300), format)
	m.db.Exec(`UPDATE users SET sub_last_at=?, sub_last_ua=? WHERE id=?`, now(), truncate(r.UserAgent(), 300), u.ID)

	var all, entries []subEntry
	if u.active() {
		if all, err = m.subEntries(u); err != nil {
			http.Error(w, "internal error", 500)
			m.log.Printf("sub %d: %v", u.ID, err)
			return
		}
	}
	// lines a traffic limit closed for this user stay in the subscription (the node refuses
	// them until it resets); the page marks them as used up
	u.Blocked = m.blockedInbounds(u.ID)
	entries = all
	title := settingOr(m.db, "sub_title", "sbpanel")
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Subscription-Userinfo", fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", u.Up, u.Down, u.Limit, u.ExpireAt))
	h.Set("Profile-Update-Interval", settingOr(m.db, "sub_update_hours", "12"))
	h.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
	h.Set("Profile-Web-Page-Url", m.subURL(r, u.Token))
	// One case per output format. Formats that need extra rendering live in their own files.
	switch format {
	case "html":
		m.subPage(w, r, u, all)
	case "json":
		h.Set("Content-Type", "application/json; charset=utf-8")
		h.Set("Access-Control-Allow-Origin", "*") // themed pages run in an opaque origin
		json.NewEncoder(w).Encode(m.subData(r, u, all, m.activeTheme()))
	case "singbox", "mihomo", "surge":
		// routing profile (internal/subgen); legacy generators if none applies or it fails
		if res, ok := m.renderProfile(r, format, u, entries); ok {
			h.Set("Content-Type", res.ContentType)
			h.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(title)+res.Ext)
			writeSub(w, r, res.Body)
			return
		}
		switch format {
		case "singbox":
			h.Set("Content-Type", "application/json; charset=utf-8")
			h.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(title)+".json")
			writeSub(w, r, singboxConfig(entries))
		case "mihomo":
			h.Set("Content-Type", "text/yaml; charset=utf-8")
			h.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(title)+".yaml")
			writeSub(w, r, mihomoConfig(entries))
		default: // surge has no legacy generator: node list
			h.Set("Content-Type", "text/plain; charset=utf-8")
			writeSub(w, r, []byte(base64.StdEncoding.EncodeToString([]byte(subLinks(entries)))))
		}
	case "shadowrocket":
		res := subgen.ShadowrocketSubscription(toEndpoints(entries))
		h.Set("Content-Type", res.ContentType)
		if len(res.Warnings) > 0 {
			warnings, _ := json.Marshal(res.Warnings)
			h.Set("Subscription-Warnings", base64.StdEncoding.EncodeToString(warnings))
		}
		writeSub(w, r, res.Body)
	case "links":
		h.Set("Content-Type", "text/plain; charset=utf-8")
		writeSub(w, r, []byte(subLinks(entries)))
	default: // base64
		h.Set("Content-Type", "text/plain; charset=utf-8")
		writeSub(w, r, []byte(base64.StdEncoding.EncodeToString([]byte(subLinks(entries)))))
	}
}

func subLinks(es []subEntry) string {
	lines := make([]string, 0, len(es))
	for _, e := range es {
		lines = append(lines, e.uri())
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func settingOr(db *sql.DB, k, d string) string {
	if v, ok := getSetting(db, k); ok && v != "" {
		return v
	}
	return d
}

func qrDataURI(s string) template.URL {
	c, err := qr.Encode(s, qr.M)
	if err != nil {
		return ""
	}
	c.Scale = 6
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(c.PNG()))
}

func fmtBytesGo(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB", "TB"} {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return fmt.Sprintf("%.2f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.2f PB", f)
}

// subPage renders the active subscription theme (see subtheme.go) with the data embedded.
func (m *Master) subPage(w http.ResponseWriter, r *http.Request, u subUser, es []subEntry) {
	t := m.activeTheme()
	if err := renderTheme(w, t, m.subData(r, u, es, t), "'none'"); err != nil && !t.builtin {
		m.log.Printf("sub theme %s: %v", t.ID, err)
		bt := builtinTheme()
		renderTheme(w, bt, m.subData(r, u, es, bt), "'none'")
	}
}

// exitRegion is where an inbound's traffic leaves: its own "_region" (relays that
// exit elsewhere), else its node's region.
func (m *Master) exitRegion(node, optsJSON string) string {
	var o struct {
		Region string `json:"_region"`
	}
	json.Unmarshal([]byte(optsJSON), &o)
	if rg, ok := normRegion(o.Region); ok && rg != "" {
		return rg
	}
	var rg string
	m.db.QueryRow(`SELECT region FROM nodes WHERE name=?`, node).Scan(&rg)
	return rg
}

// normRegion upper-cases a two-letter country code; "" is valid (unset).
func normRegion(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	if len(s) != 2 || s[0] < 'A' || s[0] > 'Z' || s[1] < 'A' || s[1] > 'Z' {
		return "", false
	}
	return s, true
}
