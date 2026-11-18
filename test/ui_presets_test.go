package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
)

// uiPreset is one entry of testdata/ui_presets.json, written by web/scripts/preset-fixture.ts from
// the same code the "New inbound" form runs (web/src/lib/presets.ts).
type uiPreset struct {
	ID      string         `json:"id"`
	Cert    string         `json:"cert"` // path | panel | acme | "" (no TLS certificate)
	Type    string         `json:"type"`
	UDP     bool           `json:"udp"`
	Options map[string]any `json:"options"`
	Client  map[string]any `json:"client"`
}

func loadUIPresets(t *testing.T) []uiPreset {
	t.Helper()
	b, err := os.ReadFile("testdata/ui_presets.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []uiPreset
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// fill replaces fixture placeholders in a JSON-shaped value.
func fill(v any, repl map[string]string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = fill(e, repl)
		}
	case []any:
		for i, e := range x {
			x[i] = fill(e, repl)
		}
	case string:
		if r, ok := repl[x]; ok {
			return r
		}
	}
	return v
}

// TestUIPresets deploys every inbound the "New inbound" form can create, exactly as the form builds
// it (each TLS preset with a node-file and with a panel certificate), and downloads through each one
// with the user's own subscription. Clients verify the certificate against the expected one for the
// domain (no "insecure"), so a wrong server name, ALPN or certificate wiring fails here.
// ACME variants are checked to be valid sing-box options (issuing needs the internet).
func TestUIPresets(t *testing.T) {
	e := newEnv(t)
	uid := e.createUser(map[string]any{"username": "ui"})
	nodePEM, err := os.ReadFile(e.cert)
	if err != nil {
		t.Fatal(err)
	}
	panelPEM, panelKey, _ := pemPair(t, "sb.test", 90)
	var up struct{ ID string }
	e.api.do("POST", "/api/certs/upload", map[string]any{"name": "ui-presets", "cert_pem": panelPEM, "key_pem": panelKey}, &up)
	var kp map[string]string
	e.api.do("POST", "/api/tools/reality-keypair", nil, &kp)
	repl := map[string]string{"@CERT": e.cert, "@KEY": e.key, "@CERT_ID": up.ID, "@PRIV": kp["private_key"], "@PUB": kp["public_key"], "@SID": kp["short_id"]}

	ports := portSet{}
	type deployed struct {
		uiPreset
		tag   string
		trust string // PEM the client trusts; "" for protocols without a certificate
	}
	var cases []deployed
	seen := map[string]bool{}
	for _, p := range loadUIPresets(t) {
		seen[p.ID] = true
		opts := fill(p.Options, repl).(map[string]any)
		client := fill(p.Client, repl).(map[string]any)
		if p.Cert == "acme" {
			t.Run(p.ID+"/acme", func(t *testing.T) { checkInboundOptions(t, p.Type, opts) })
			continue
		}
		opts["listen_port"] = ports.take(t, p.UDP)
		tlsOpts, _ := opts["tls"].(map[string]any)
		if r, ok := tlsOpts["reality"].(map[string]any); ok {
			// the form borrows the site's own handshake; here a local TLS 1.3 server stands in for it
			r["handshake"] = map[string]any{"server": "127.0.0.1", "server_port": realityTarget(t)}
		}
		d := deployed{uiPreset: p, tag: strings.TrimSuffix("ui-"+p.ID+"-"+p.Cert, "-")}
		switch p.Cert {
		case "path":
			d.trust = string(nodePEM)
		case "panel":
			d.trust = panelPEM
		}
		e.createInbound(d.tag, p.Type, opts, client)
		cases = append(cases, d)
	}
	for _, id := range []string{"vless-reality", "vmess-ws-tls", "vless-ws-tls", "vless-grpc-tls", "trojan-tls", "ss2022", "hysteria2", "tuic", "anytls"} {
		if !seen[id] {
			t.Errorf("fixture has no %s case: regenerate it with web/scripts/preset-fixture.ts", id)
		}
	}
	e.waitApplied()
	outs := e.subOutbounds(uid)
	target := blob(t)
	const size = 1 << 20
	for _, c := range cases {
		t.Run(c.tag, func(t *testing.T) {
			ob, ok := outs["local · "+c.tag]
			if !ok {
				t.Fatalf("no outbound in subscription (have %v)", keys(outs))
			}
			ob = trustOnly(t, ob, c.trust)
			proxy := client(t, ob)
			var err error
			for attempt := 0; attempt < 3; attempt++ { // QUIC handshakes can race the first request
				if _, err = fetch(proxy, target, size, "", 60*time.Second); err == nil {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if err != nil {
				t.Fatalf("download through %s: %v\noutbound: %s", c.tag, err, ob)
			}
		})
	}
}

// trustOnly makes a TLS outbound verify the server against exactly one certificate (and never
// skip verification); REALITY and plain outbounds are returned unchanged.
func trustOnly(t *testing.T, ob json.RawMessage, pem string) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ob, &m); err != nil {
		t.Fatal(err)
	}
	tls, _ := m["tls"].(map[string]any)
	if tls == nil || pem == "" {
		return ob
	}
	if tls["insecure"] == true {
		t.Fatalf("subscription marks the outbound insecure: %s", ob)
	}
	if tls["server_name"] != "sb.test" {
		t.Fatalf("outbound server_name %v, want sb.test", tls["server_name"])
	}
	tls["certificate"] = pem
	b, _ := json.Marshal(m)
	return b
}

// checkInboundOptions parses inbound options as sing-box would (users are injected by the panel).
func checkInboundOptions(t *testing.T, typ string, opts map[string]any) {
	t.Helper()
	clean := map[string]any{"type": typ, "tag": "x"}
	for k, v := range opts {
		if !strings.HasPrefix(k, "_") {
			clean[k] = v
		}
	}
	b, _ := json.Marshal(map[string]any{"inbounds": []any{clean}})
	var o option.Options
	if err := sjson.UnmarshalContext(include.Context(context.Background()), b, &o); err != nil {
		t.Fatalf("sing-box rejects %s options: %v\n%s", typ, err, b)
	}
	tls := opts["tls"].(map[string]any)
	acme, _ := tls["acme"].(map[string]any)
	if fmt.Sprint(acme["domain"]) != "[sb.test]" || tls["server_name"] != "sb.test" {
		t.Fatalf("acme for the wrong domain: %v", tls)
	}
}

// portSet hands out distinct ports that are free on every interface: the form's inbounds listen
// on "::", and all of them are bound together once the node applies the spec.
type portSet map[int]bool

func (ps portSet) take(t *testing.T, udp bool) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		l, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		ok := !ps[p]
		if ok && udp {
			u, err := net.ListenPacket("udp", fmt.Sprintf(":%d", p))
			if ok = err == nil; ok {
				u.Close()
			}
		}
		l.Close()
		if ok {
			ps[p] = true
			return p
		}
	}
	t.Fatal("no free port")
	return 0
}
