package master

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestInboundConnectionFieldExtraction(t *testing.T) {
	var opts map[string]any
	json.Unmarshal([]byte(`{"listen_port":30031,"tls":{"enabled":true,"server_name":"tls.example.com","alpn":["http/1.1"]},"transport":{"type":"ws","host":"explicit.example.com","path":"/vm","headers":{"Host":"header.example.com","X-Test":"test"},"max_early_data":2048,"early_data_header_name":"Sec-WebSocket-Protocol"}}`), &opts)
	e := entryFromInbound("vmess", opts, map[string]any{"insecure": true})
	if e.Host != "explicit.example.com" || e.Headers["X-Test"] != "test" || e.MaxEarlyData != 2048 || e.EarlyDataHeader != "Sec-WebSocket-Protocol" || e.ALPN[0] != "http/1.1" {
		t.Fatal("inbound required fields lost")
	}
	b, err := base64.StdEncoding.DecodeString(strings.SplitN(e.uri()[8:], "#", 2)[0])
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	json.Unmarshal(b, &v)
	if v["allowInsecure"] != true {
		t.Fatal("VMess verification mode lost")
	}
	var hy map[string]any
	json.Unmarshal([]byte(`{"listen_port":443,"obfs":{"type":"salamander","password":"test"}}`), &hy)
	h := entryFromInbound("hysteria2", hy, nil)
	u, _ := url.Parse(h.uri())
	if u.Query().Get("obfs") != "salamander" || u.Query().Get("obfs-password") != "test" {
		t.Fatal("Hysteria2 URI obfs lost")
	}
}

func TestShadowrocketFormatDetection(t *testing.T) {
	for _, tc := range []struct{ query, ua, want string }{{"", "Shadowrocket/3488", "shadowrocket"}, {"?format=shadowrocket", "other", "shadowrocket"}, {"?format=base64", "Shadowrocket/3488", "base64"}, {"?format=links", "Shadowrocket/3488", "links"}, {"", "Surge Mac/6", "surge"}, {"", "unknown", "base64"}} {
		r := httptest.NewRequest("GET", "https://example.com/s/test"+tc.query, nil)
		r.Header.Set("User-Agent", tc.ua)
		if got := detectFormat(r); got != tc.want {
			t.Errorf("%s %s => %s", tc.query, tc.ua, got)
		}
	}
	for _, c := range subClients {
		if c.ID == "shadowrocket" {
			u, _ := url.Parse(c.link("https://example.com/s/test?profile=standard", "test"))
			if u.Scheme != "shadowrocket" {
				t.Fatal("import link")
			}
			b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(u.Path, "/sub://"))
			if err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(string(b))
			if err != nil || target.Query().Get("format") != "shadowrocket" || target.Query().Get("profile") != "standard" {
				t.Fatal("import link lost explicit format or selected profile")
			}
		}
	}
}
