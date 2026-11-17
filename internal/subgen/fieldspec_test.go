package subgen

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fieldFixtures covers every protocol/transport the panel exports, with every
// relevant field populated, a hostname address and an IPv6 preference.
func fieldFixtures() []Endpoint {
	base := func(e Endpoint) Endpoint {
		e.Address, e.Port, e.IPPrefer = "node.example.com", 443, "ipv6"
		if e.Security != "" {
			e.SNI, e.Insecure = "sni.example.com", true
			if e.Security == "tls" && e.Type != "shadowsocks" {
				e.ALPN = []string{"h2", "http/1.1"}
			}
		}
		if e.FP == "" {
			e.FP = "chrome" // the panel sets it for every inbound
		}
		e.Node, e.Region, e.Inbound, e.Line = "n", "US", 1, e.Remark
		return e
	}
	u := "11111111-1111-4111-8111-111111111111"
	return []Endpoint{
		base(Endpoint{Remark: "vmess-ws", Type: "vmess", UUID: u, Security: "tls", Network: "ws", Path: "/vm", Host: "h.example.com",
			Headers: map[string]string{"X-Test": "v"}, MaxEarlyData: 2048, EarlyDataHeader: "Sec-WebSocket-Protocol"}),
		base(Endpoint{Remark: "vmess-h2", Type: "vmess", UUID: u, Security: "tls", Network: "http", Path: "/h2", Host: "h.example.com"}),
		base(Endpoint{Remark: "vless-reality", Type: "vless", UUID: u, Flow: "xtls-rprx-vision", Security: "reality", Network: "tcp",
			PBK: "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0", SID: "6ba85179e30d4fc2"}),
		base(Endpoint{Remark: "vless-grpc", Type: "vless", UUID: u, Security: "tls", Network: "grpc", Service: "svc"}),
		base(Endpoint{Remark: "vless-upgrade", Type: "vless", UUID: u, Security: "tls", Network: "httpupgrade", Path: "/up", Host: "h.example.com",
			Headers: map[string]string{"X-Test": "v"}}),
		base(Endpoint{Remark: "trojan-ws", Type: "trojan", Password: "pw", Security: "tls", Network: "ws", Path: "/tr", Host: "h.example.com"}),
		base(Endpoint{Remark: "ss", Type: "shadowsocks", SSMethod: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA=="}),
		base(Endpoint{Remark: "hy2", Type: "hysteria2", Password: "pw", Security: "tls", ALPN: []string{"h3"}, ObfsType: "salamander", ObfsPassword: "ob"}),
		base(Endpoint{Remark: "tuic", Type: "tuic", UUID: u, Password: "pw", Security: "tls", ALPN: []string{"h3"}, CC: "bbr"}),
		base(Endpoint{Remark: "anytls", Type: "anytls", Password: "pw", Security: "tls"}),
	}
}

func TestFieldSpecCoversEndpoint(t *testing.T) {
	rows := map[string]FieldSpec{}
	for _, s := range FieldSpecs {
		if _, dup := rows[s.Field]; dup {
			t.Fatalf("duplicate FieldSpec row %s", s.Field)
		}
		rows[s.Field] = s
	}
	et := reflect.TypeOf(Endpoint{})
	for i := 0; i < et.NumField(); i++ {
		f := et.Field(i).Name
		s, ok := rows[f]
		if !ok {
			t.Errorf("Endpoint.%s has no FieldSpecs row: decide how every client carries it (fieldspec.go)", f)
			continue
		}
		delete(rows, f)
		if s.Meta != "" {
			continue
		}
		for _, c := range SpecClients {
			cr, ok := s.Clients[c]
			if !ok {
				t.Errorf("Endpoint.%s: no %s entry in FieldSpecs", f, c)
			} else if len(cr.Keys) == 0 && cr.Not == "" && cr.Implicit == "" {
				t.Errorf("Endpoint.%s/%s: give Keys, Implicit or Not", f, c)
			}
		}
	}
	for f := range rows {
		t.Errorf("FieldSpecs row %s names no Endpoint field", f)
	}
}

// node renders one endpoint the way each exporter does, normalised to JSON values.
func renderNode(client string, e Endpoint) (map[string]any, string) {
	var v map[string]any
	switch client {
	case "singbox":
		v = e.SingboxConfigOutbound()
	case "mihomo":
		v = e.MihomoProxy()
	case "shadowrocket":
		v = e.ShadowrocketProxy()
	case "surge":
		return nil, e.surgeProxy()
	}
	b, _ := json.Marshal(v)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out, ""
}

func lookup(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = mm[p]; !ok {
			return nil, false
		}
	}
	return cur, !reflect.ValueOf(cur).IsZero()
}

func TestFieldSpecCarried(t *testing.T) {
	for _, e := range fieldFixtures() {
		ev := reflect.ValueOf(e)
		for _, client := range SpecClients {
			if ok, _ := Supported(client, e); !ok {
				continue // filtered with a warning; covered by capability tests
			}
			m, line := renderNode(client, e)
			for _, s := range FieldSpecs {
				if s.Meta != "" {
					continue
				}
				fv := ev.FieldByName(s.Field)
				if fv.IsZero() || (s.Field == "Network" && fv.String() == "tcp") {
					continue
				}
				c := s.Clients[client]
				if c.Not != "" || c.Implicit != "" || c.Except[e.Type] != "" || c.Except["net:"+e.Network] != "" {
					continue
				}
				found := false
				for _, key := range c.Keys {
					if client == "surge" {
						if key == "*" {
							found = found || strings.Contains(line, fmt.Sprint(fv.Interface()))
						} else {
							found = found || strings.Contains(line, " "+key+"=") || strings.Contains(line, ","+key+"=")
						}
						continue
					}
					got, ok := lookup(m, key)
					if !ok {
						continue
					}
					// a header map must keep every header, not only Host
					if hm, isMap := fv.Interface().(map[string]string); isMap {
						gm, _ := got.(map[string]any)
						for hk := range hm {
							if _, ok := gm[hk]; !ok {
								t.Errorf("%s/%s: header %s lost under %s", e.Remark, client, hk, key)
							}
						}
					}
					found = true
				}
				if !found {
					t.Errorf("%s/%s: field %s not carried (want one of %v)\n%v%s", e.Remark, client, s.Field, c.Keys, m, line)
				}
			}
		}
	}
}

// The sing-box column is checked against sing-box itself: unknown keys fail.
func TestFieldFixturesSingboxStrict(t *testing.T) {
	r := mustRender(t, "singbox", StandardPreset(), fieldFixtures())
	boxCheck(t, r.Body)
	for _, c := range []string{"mihomo", "surge"} {
		mustRender(t, c, StandardPreset(), fieldFixtures())
	}
}

// A saved profile with IPv6 off must not cancel a node's IPv6 preference.
func TestIPv6PreferOverridesGlobalOff(t *testing.T) {
	p := StandardPreset()
	p.DNS.IPv6 = false
	for _, c := range []string{"mihomo", "surge", "singbox"} {
		ct := p.Clients[c]
		ct.Options = map[string]string{"ipv6": "false"}
		p.Clients[c] = ct
	}
	eps := []Endpoint{{Remark: "zouter", Type: "vmess", Address: "z.example.com", Port: 1, UUID: "u", Security: "tls", Network: "ws", Path: "/vm", IPPrefer: "ipv6"}}
	want := map[string][]string{"mihomo": {"ipv6: true", "ip-version"}, "surge": {"ipv6 = true", "ip-version=prefer-v6"}, "singbox": {`"prefer_ipv4"`, `"prefer_ipv6"`}}
	for c, subs := range want {
		r, err := Render(c, p, eps, Meta{})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range subs {
			if !strings.Contains(string(r.Body), s) {
				t.Errorf("%s: missing %q", c, s)
			}
		}
		if strings.Contains(string(r.Body), "ipv6: false") || strings.Contains(string(r.Body), "ipv6 = false") || strings.Contains(string(r.Body), "ipv4_only") {
			t.Errorf("%s still disables IPv6\n%s", c, r.Body)
		}
		if len(r.Warnings) == 0 {
			t.Errorf("%s: override not reported", c)
		}
	}
	// without an IPv6 node the operator's choice stands
	eps[0].IPPrefer = ""
	if r, _ := Render("mihomo", p, eps, Meta{}); !strings.Contains(string(r.Body), "ipv6: false") {
		t.Error("explicit IPv6 off ignored")
	}
}
