package subgen

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestStructuredSubscriptionsPreserveConnectionFields(t *testing.T) {
	e := Endpoint{Remark: "zouter", Type: "vmess", Address: "zouter.example.com", Port: 30031, UUID: "11111111-1111-4111-8111-111111111111", Security: "tls", SNI: "tls.example.com", ALPN: []string{"http/1.1"}, FP: "chrome", Insecure: true, Network: "ws", Path: "/vm", Host: "override.example.com", Headers: map[string]string{"Host": "old.example.com", "X-Test": "value"}, MaxEarlyData: 2048, EarlyDataHeader: "Sec-WebSocket-Protocol", IPPrefer: "ipv6"}
	sb := e.SingboxConfigOutbound()
	tr := sb["transport"].(map[string]any)
	if tr["max_early_data"] != 2048 || tr["early_data_header_name"] != "Sec-WebSocket-Protocol" {
		t.Fatal("sing-box early data lost")
	}
	h := tr["headers"].(map[string]string)
	if h["Host"] != e.Host || h["X-Test"] != "value" || e.Headers["Host"] != "old.example.com" {
		t.Fatal("headers lost or source mutated")
	}
	for _, get := range []func() map[string]any{e.MihomoProxy, e.ShadowrocketProxy} {
		p := get()
		if p["ip-version"] != "ipv6-prefer" || p["server"] != e.Address || p["servername"] != e.SNI || p["skip-cert-verify"] != true {
			t.Fatal("TLS identity or family lost")
		}
		ws := p["ws-opts"].(map[string]any)
		if ws["path"] != "/vm" || ws["max-early-data"] != 2048 {
			t.Fatal("WebSocket fields lost")
		}
	}
	r := ShadowrocketSubscription([]Endpoint{e})
	var cfg struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(r.Body, &cfg); err != nil || len(cfg.Proxies) != 1 {
		t.Fatal("invalid Shadowrocket list", err)
	}
	if cfg.Proxies[0]["ip-version"] != "ipv6-prefer" || cfg.Proxies[0]["client-fingerprint"] != "chrome" || cfg.Proxies[0]["fingerprint"] != nil {
		t.Fatal("Shadowrocket fields missing")
	}
	// IPv4/default and literal endpoints must not inherit IPv6 preference.
	e.IPPrefer = "ipv4"
	if e.ShadowrocketProxy()["ip-version"] != "ipv4-prefer" {
		t.Fatal("IPv4 preference lost")
	}
	e.IPPrefer = ""
	if _, ok := e.ShadowrocketProxy()["ip-version"]; ok {
		t.Fatal("default overridden")
	}
	e.IPPrefer = "ipv6"
	e.Address = "2001:db8::1"
	if _, ok := e.ShadowrocketProxy()["ip-version"]; ok {
		t.Fatal("literal has unnecessary preference")
	}
}

func TestHysteriaObfuscationAcrossClients(t *testing.T) {
	e := Endpoint{Remark: "hy2", Type: "hysteria2", Address: "h.example.com", Port: 443, Password: "user-secret", Security: "tls", SNI: "s.example.com", ALPN: []string{"h3"}, ObfsType: "salamander", ObfsPassword: "obfs-secret"}
	o := e.SingboxOutbound()
	if o["obfs"].(map[string]any)["password"] != e.ObfsPassword {
		t.Fatal("sing-box obfs lost")
	}
	p := e.MihomoProxy()
	if p["obfs"] != "salamander" || p["obfs-password"] != e.ObfsPassword {
		t.Fatal("Mihomo obfs lost")
	}
	p = e.ShadowrocketProxy()
	if p["obfs"] != "salamander" || p["obfs-password"] != e.ObfsPassword {
		t.Fatal("Shadowrocket obfs lost")
	}
	if !strings.Contains(e.surgeProxy(), "salamander-password=obfs-secret") || !strings.Contains(e.surgeProxy(), "alpn=h3") {
		t.Fatal("Surge obfs or ALPN lost")
	}
	e.ObfsType = "unknown"
	r := ShadowrocketSubscription([]Endpoint{e})
	if len(r.Warnings) != 1 || string(r.Body) != "proxies: []\n" {
		t.Fatal("unsupported obfs silently downgraded")
	}
}

func TestSurgeRetainsALPNAndCustomHeaders(t *testing.T) {
	e := Endpoint{Remark: "ws", Type: "vmess", Address: "ws.example.com", Port: 443, Security: "tls", Network: "ws", Path: "/vm", ALPN: []string{"h2", "http/1.1"}, Headers: map[string]string{"X-Test": "test"}}
	s := e.surgeProxy()
	if !strings.Contains(s, `alpn="h2,http/1.1"`) || !strings.Contains(s, `ws-headers=X-Test:"test"`) {
		t.Fatal("Surge fields lost")
	}
	// Early data is a client-side optimisation; sing-box servers accept plain upgrades,
	// so Surge keeps the node and simply connects without it.
	e.MaxEarlyData = 2048
	if ok, _ := Supported("surge", e); !ok {
		t.Fatal("early data must not drop the node from Surge")
	}
	tu := Endpoint{Remark: "tu", Type: "tuic", Address: "t.example.com", Port: 443, UUID: "u", Password: "p", Security: "tls"}
	if !strings.Contains(tu.surgeProxy(), "alpn=h3") {
		t.Fatal("Surge TUIC lost its default ALPN")
	}
}

func TestIPv6DefaultsAndExplicitOverride(t *testing.T) {
	p := StandardPreset()
	if !p.DNS.IPv6 {
		t.Fatal("built-in DNS should support IPv6")
	}
	r, err := Render("surge", p, SampleEndpoints(), Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), "ipv6 = true") {
		t.Fatal("Surge IPv6 default disabled")
	}
	ct := p.Clients["surge"]
	ct.Options = map[string]string{"ipv6": "false"}
	p.Clients["surge"] = ct
	r, err = Render("surge", p, SampleEndpoints(), Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), "ipv6 = false") {
		t.Fatal("explicit operator override ignored")
	}
}
