package subgen

import (
	"strings"
	"testing"
)

// A line break in any endpoint value must not start a new line in a Surge profile.
func TestSurgeProxyLineBreaks(t *testing.T) {
	e := Endpoint{Remark: "a\r\n[Script]", Type: "trojan", Address: "h.example.com\n[General]", Port: 443,
		Password: "p\nw", Security: "tls", SNI: "s\r\nx", Network: "ws", Path: "/p\n[Rule]"}
	if s := e.surgeProxy(); strings.ContainsAny(s, "\r\n") {
		t.Fatalf("line break in proxy line: %q", s)
	}
}
