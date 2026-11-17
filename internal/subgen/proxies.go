package subgen

import (
	"net"
	"strings"
)

// Copied from sbpanel internal/master/sub.go (singboxOutbound, mihomoProxy).
func (e Endpoint) SingboxOutbound() map[string]any {
	o := map[string]any{"type": e.Type, "tag": e.Remark, "server": e.Address, "server_port": e.Port}
	tls := func() map[string]any {
		t := map[string]any{"enabled": true}
		if e.SNI != "" {
			t["server_name"] = e.SNI
		}
		if e.Insecure {
			t["insecure"] = true
		}
		if len(e.ALPN) > 0 {
			t["alpn"] = e.ALPN
		}
		if e.Security == "reality" {
			t["reality"] = map[string]any{"enabled": true, "public_key": e.PBK, "short_id": e.SID}
		}
		if e.FP != "" && (e.Security == "reality" || e.Type == "vless" || e.Type == "vmess" || e.Type == "trojan" || e.Type == "anytls") {
			t["utls"] = map[string]any{"enabled": true, "fingerprint": e.FP}
		}
		return t
	}
	transport := func() {
		switch e.Network {
		case "ws":
			t := map[string]any{"type": "ws", "path": e.Path}
			if len(e.transportHeaders()) > 0 {
				t["headers"] = e.transportHeaders()
			}
			if e.MaxEarlyData > 0 {
				t["max_early_data"] = e.MaxEarlyData
				if e.EarlyDataHeader != "" {
					t["early_data_header_name"] = e.EarlyDataHeader
				}
			}
			o["transport"] = t
		case "httpupgrade":
			t := map[string]any{"type": "httpupgrade", "path": e.Path, "host": e.Host}
			if len(e.Headers) > 0 {
				t["headers"] = e.transportHeaders()
			}
			o["transport"] = t
		case "http":
			t := map[string]any{"type": "http", "path": e.Path}
			if len(e.Headers) > 0 {
				t["headers"] = e.transportHeaders()
			}
			if e.Host != "" {
				t["host"] = []string{e.Host}
			}
			o["transport"] = t
		case "grpc":
			o["transport"] = map[string]any{"type": "grpc", "service_name": e.Service}
		}
	}
	switch e.Type {
	case "vless":
		o["uuid"] = e.UUID
		if e.Flow != "" {
			o["flow"] = e.Flow
		}
		if e.Security != "" {
			o["tls"] = tls()
		}
		transport()
	case "vmess":
		o["uuid"], o["security"], o["alter_id"] = e.UUID, "auto", 0
		if e.Security != "" {
			o["tls"] = tls()
		}
		transport()
	case "trojan":
		o["password"] = e.Password
		o["tls"] = tls()
		transport()
	case "shadowsocks":
		o["method"], o["password"] = e.SSMethod, e.Password
	case "hysteria2":
		if e.ObfsType != "" {
			o["obfs"] = map[string]any{"type": e.ObfsType, "password": e.ObfsPassword}
		}
		o["password"] = e.Password
		o["tls"] = tls()
	case "tuic":
		o["uuid"], o["password"], o["congestion_control"] = e.UUID, e.Password, e.CC
		o["tls"] = tls()
	case "anytls":
		o["password"] = e.Password
		o["tls"] = tls()
	}
	return o
}

// SingboxConfigOutbound is SingboxOutbound for a complete generated config: it may name
// the config's "local" DNS server to pick the address family for the server's hostname.
// (A bare outbound can't: other configs need not have that server.)
func (e Endpoint) SingboxConfigOutbound() map[string]any {
	o := e.SingboxOutbound()
	if st := e.ipStrategy(); st != "" {
		o["domain_resolver"] = map[string]any{"server": "local", "strategy": st}
	}
	return o
}

// ipStrategy is the sing-box strategy for reaching a hostname Address ("" = client default).
func (e Endpoint) ipStrategy() string {
	if net.ParseIP(strings.Trim(e.Address, "[]")) != nil {
		return ""
	}
	switch e.IPPrefer {
	case "ipv6":
		return "prefer_ipv6"
	case "ipv4":
		return "prefer_ipv4"
	}
	return ""
}

func (e Endpoint) MihomoProxy() map[string]any {
	p := map[string]any{"name": e.Remark, "server": e.Address, "port": e.Port, "udp": true}
	tlsCommon := func() {
		if e.SNI != "" {
			p["servername"] = e.SNI
		}
		if e.Insecure {
			p["skip-cert-verify"] = true
		}
		if len(e.ALPN) > 0 {
			p["alpn"] = e.ALPN
		}
		if e.FP != "" {
			p["client-fingerprint"] = e.FP
		}
		if e.Security == "reality" {
			p["reality-opts"] = map[string]any{"public-key": e.PBK, "short-id": e.SID}
		}
	}
	network := func() {
		if e.Network != "" {
			p["network"] = e.Network
		}
		switch e.Network {
		case "ws", "httpupgrade":
			ws := map[string]any{"path": e.Path}
			if len(e.transportHeaders()) > 0 {
				ws["headers"] = e.transportHeaders()
			}
			if e.MaxEarlyData > 0 {
				ws["max-early-data"] = e.MaxEarlyData
				if e.EarlyDataHeader != "" {
					ws["early-data-header-name"] = e.EarlyDataHeader
				}
			}
			if e.Network == "httpupgrade" {
				p["network"] = "ws"
				ws["v2ray-http-upgrade"] = true
			}
			p["ws-opts"] = ws
		case "grpc":
			p["grpc-opts"] = map[string]any{"grpc-service-name": e.Service}
		case "http":
			p["network"] = "h2"
			h := map[string]any{"path": e.Path}
			if e.Host != "" {
				h["host"] = []string{e.Host}
			}
			p["h2-opts"] = h
		}
	}
	switch e.Type {
	case "vless":
		p["type"], p["uuid"] = "vless", e.UUID
		if e.Flow != "" {
			p["flow"] = e.Flow
		}
		if e.Security != "" {
			p["tls"] = true
			tlsCommon()
		}
		network()
	case "vmess":
		p["type"], p["uuid"], p["alterId"], p["cipher"] = "vmess", e.UUID, 0, "auto"
		if e.Security != "" {
			p["tls"] = true
			tlsCommon()
		}
		network()
	case "trojan":
		p["type"], p["password"] = "trojan", e.Password
		tlsCommon()
		if s, ok := p["servername"]; ok {
			delete(p, "servername")
			p["sni"] = s
		}
		network()
	case "shadowsocks":
		p["type"], p["cipher"], p["password"] = "ss", e.SSMethod, e.Password
	case "hysteria2":
		if e.ObfsType != "" {
			p["obfs"], p["obfs-password"] = e.ObfsType, e.ObfsPassword
		}
		p["type"], p["password"] = "hysteria2", e.Password
		tlsCommon()
		delete(p, "client-fingerprint")
		if s, ok := p["servername"]; ok {
			delete(p, "servername")
			p["sni"] = s
		}
	case "tuic":
		p["type"], p["uuid"], p["password"], p["congestion-controller"] = "tuic", e.UUID, e.Password, e.CC
		tlsCommon()
		delete(p, "client-fingerprint")
		if s, ok := p["servername"]; ok {
			delete(p, "servername")
			p["sni"] = s
		}
	case "anytls":
		p["type"], p["password"] = "anytls", e.Password
		tlsCommon()
		if s, ok := p["servername"]; ok {
			delete(p, "servername")
			p["sni"] = s
		}
	}
	switch e.ipStrategy() {
	case "prefer_ipv6":
		p["ip-version"] = "ipv6-prefer"
	case "prefer_ipv4":
		p["ip-version"] = "ipv4-prefer"
	}
	return p
}

// transportHeaders returns a fresh map so host overrides never mutate sibling endpoints.
func (e Endpoint) transportHeaders() map[string]string {
	h := make(map[string]string, len(e.Headers)+1)
	for k, v := range e.Headers {
		h[k] = v
	}
	if e.Host != "" {
		for k := range h {
			if strings.EqualFold(k, "Host") {
				delete(h, k)
			}
		}
		h["Host"] = e.Host
	}
	return h
}
