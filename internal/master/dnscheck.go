package master

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
)

// lookupIP resolves a host name (test hook).
var lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// localIPs are this machine's global addresses (test hook): a node connected over loopback runs
// on the master's machine, so these are its public addresses too.
var localIPs = func() []net.IP {
	as, _ := net.InterfaceAddrs()
	var out []net.IP
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok && n.IP.IsGlobalUnicast() && !n.IP.IsPrivate() {
			out = append(out, n.IP)
		}
	}
	return out
}

// dnsCheck says whether a domain resolves to a node.
type dnsCheck struct {
	Host    string   `json:"host"`
	IPs     []string `json:"ips"`      // what the domain resolves to (from the master)
	NodeIPs []string `json:"node_ips"` // the node's known public addresses
	// ok: every address is the node's · partial: some are · mismatch: none are ·
	// none: no A/AAAA record · unknown: the node's address isn't known
	Status  string `json:"status"`
	Proxied bool   `json:"proxied"` // a panel-managed record behind the Cloudflare proxy (CDN)
	Error   string `json:"error,omitempty"`
}

// nodePublicIPs collects the addresses clients can reach a node at: its public address (an IP or
// a host name) and the address it connects to the master from, if that is public.
func (m *Master) nodePublicIPs(ctx context.Context, nodeID string) []string {
	var pub, addr string
	m.db.QueryRow(`SELECT public_address, address FROM nodes WHERE id=?`, nodeID).Scan(&pub, &addr)
	var ips []net.IP
	if pub = strings.TrimSpace(pub); pub != "" {
		if ip := net.ParseIP(pub); ip != nil {
			ips = append(ips, ip)
		} else if r, err := lookupIP(ctx, pub); err == nil {
			ips = append(ips, r...)
		}
	}
	if ip := net.ParseIP(addr); ip != nil {
		switch {
		case ip.IsLoopback():
			ips = append(ips, localIPs()...)
		case !ip.IsPrivate() && !ip.IsLinkLocalUnicast():
			ips = append(ips, ip)
		}
	}
	var out []string
	for _, ip := range ips {
		if s := ip.String(); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func (m *Master) checkDNS(ctx context.Context, host, nodeID string) dnsCheck {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	out := dnsCheck{Host: host, IPs: []string{}, NodeIPs: m.nodePublicIPs(ctx, nodeID)}
	if out.NodeIPs == nil {
		out.NodeIPs = []string{}
	}
	var proxied bool
	if m.db.QueryRow(`SELECT proxied FROM domains WHERE fqdn=?`, host).Scan(&proxied) == nil {
		out.Proxied = proxied
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := lookupIP(ctx, host)
	if err != nil {
		out.Status = "none"
		if de := (*net.DNSError)(nil); !errors.As(err, &de) || !de.IsNotFound {
			out.Error = err.Error()
		}
		return out
	}
	hit := 0
	for _, ip := range r {
		s := ip.String()
		if !slices.Contains(out.IPs, s) {
			out.IPs = append(out.IPs, s)
			if slices.Contains(out.NodeIPs, s) {
				hit++
			}
		}
	}
	switch {
	case len(out.IPs) == 0:
		out.Status = "none"
	case len(out.NodeIPs) == 0:
		out.Status = "unknown"
	case hit == len(out.IPs):
		out.Status = "ok"
	case hit > 0:
		out.Status = "partial"
	default:
		out.Status = "mismatch"
	}
	return out
}

// GET /api/tools/dns-check?host=&node=
func (m *Master) apiDNSCheck(w http.ResponseWriter, r *http.Request) {
	host, node := r.URL.Query().Get("host"), r.URL.Query().Get("node")
	if !validHostname(host) {
		writeErr(w, 400, "host must be a domain name")
		return
	}
	writeJSON(w, 200, m.checkDNS(r.Context(), host, node))
}

// validHostname: letters, digits and hyphens in dot-separated labels, at least two labels, not an IP.
func validHostname(h string) bool {
	h = strings.TrimSuffix(strings.TrimSpace(h), ".")
	if len(h) == 0 || len(h) > 253 || net.ParseIP(h) != nil || !strings.Contains(h, ".") {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
