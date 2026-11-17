package subgen

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ShadowrocketSubscription emits the structured proxies list used by Sub-Store's
// Shadowrocket producer, retaining fields that generic VMess share URIs lose.
// It is a node subscription, not a replacement for the user's routing profile.
func ShadowrocketSubscription(es []Endpoint) *Result {
	r := &Result{Client: "shadowrocket", ContentType: "text/yaml; charset=utf-8", Ext: ".yaml"}
	var b strings.Builder
	b.WriteString("proxies:\n")
	count := 0
	for _, e := range es {
		if ok, reason := Supported("shadowrocket", e); !ok {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: %s", e.Remark, reason))
			continue
		}
		p := e.ShadowrocketProxy()
		data, _ := json.Marshal(p)
		b.WriteString("  - ")
		b.Write(data)
		b.WriteByte('\n')
		count++
	}
	if len(r.Warnings) > 0 {
		warnings, _ := json.Marshal(r.Warnings)
		b.WriteString("# Skipped endpoints: ")
		b.Write(warnings)
		b.WriteByte('\n')
	}
	if count == 0 {
		r.Body = []byte("proxies: []\n")
	} else {
		r.Body = []byte(b.String())
	}
	return r
}

// Shadowrocket accepts the Clash-shaped structured list. Differences follow
// Sub-Store's Shadowrocket producer: "client-fingerprint" stays as is (Shadowrocket's
// "fingerprint" is a certificate pin, not a uTLS profile), Hysteria2 keeps
// obfs + obfs-password (current Shadowrocket), and WebSocket paths default to "/".
func (e Endpoint) ShadowrocketProxy() map[string]any {
	p := e.MihomoProxy()
	switch e.Type {
	case "trojan", "tuic", "hysteria2", "anytls":
		delete(p, "tls")
	}
	if ws, ok := p["ws-opts"].(map[string]any); ok && ws["path"] == "" {
		ws["path"] = "/"
	}
	if e.Type == "tuic" {
		p["version"] = 5
	}
	return p
}
