package master

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
)

func TestCheckDNS(t *testing.T) {
	m := newTestMaster(t)
	records := map[string][]string{
		"node.example.com":  {"203.0.113.10"},
		"good.example.com":  {"203.0.113.10"},
		"dual.example.com":  {"203.0.113.10", "198.51.100.7"},
		"other.example.com": {"198.51.100.7"},
		"self.example.com":  {"192.0.2.44"},
		"cdn.example.com":   {"104.16.1.1"},
	}
	oldLookup, oldLocal := lookupIP, localIPs
	t.Cleanup(func() { lookupIP, localIPs = oldLookup, oldLocal })
	lookupIP = func(_ context.Context, h string) ([]net.IP, error) {
		r, ok := records[h]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: h, IsNotFound: true}
		}
		var ips []net.IP
		for _, s := range r {
			ips = append(ips, net.ParseIP(s))
		}
		return ips, nil
	}
	localIPs = func() []net.IP { return []net.IP{net.ParseIP("192.0.2.44")} }
	for _, n := range []struct{ id, pub, addr string }{
		{"byname", "node.example.com", "10.0.0.5"}, // public address is a host name; connects from a private network
		{"byip", "", "203.0.113.10"},               // no public address; connects from a public IP
		{"local", "", "127.0.0.1"},                 // runs on the master's machine
		{"nothing", "", "10.0.0.9"},                // nothing usable
	} {
		if _, err := m.db.Exec(`INSERT INTO nodes(id, name, public_address, address, created_at) VALUES(?,?,?,?,0)`, n.id, n.id, n.pub, n.addr); err != nil {
			t.Fatal(err)
		}
	}
	m.db.Exec(`INSERT INTO domains(fqdn, proxied, created_at) VALUES('cdn.example.com', 1, 0)`)
	cases := []struct {
		host, node, status string
		proxied            bool
	}{
		{"good.example.com", "byname", "ok", false},
		{"GOOD.example.com.", "byip", "ok", false},
		{"dual.example.com", "byip", "partial", false},
		{"other.example.com", "byname", "mismatch", false},
		{"missing.example.com", "byname", "none", false},
		{"self.example.com", "local", "ok", false},
		{"good.example.com", "nothing", "unknown", false},
		{"cdn.example.com", "byip", "mismatch", true},
	}
	for _, c := range cases {
		got := m.checkDNS(context.Background(), c.host, c.node)
		if got.Status != c.status || got.Proxied != c.proxied {
			t.Errorf("%s on %s: %+v, want %s proxied=%v", c.host, c.node, got, c.status, c.proxied)
		}
	}
	if got := m.checkDNS(context.Background(), "good.example.com", "byname"); !slices.Equal(got.NodeIPs, []string{"203.0.113.10"}) || !slices.Equal(got.IPs, []string{"203.0.113.10"}) {
		t.Errorf("addresses: %+v", got)
	}
	for _, bad := range []string{"", "1.2.3.4", "::1", "nodot", "-a.example.com", "a b.example.com"} {
		w := httptest.NewRecorder()
		m.apiDNSCheck(w, httptest.NewRequest(http.MethodGet, "/api/tools/dns-check?node=byip&host="+url.QueryEscape(bad), nil))
		if w.Code != 400 {
			t.Errorf("host %q: status %d, want 400", bad, w.Code)
		}
	}
}
