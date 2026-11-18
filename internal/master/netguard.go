package master

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Outbound requests to URLs that panel users configure (webhooks) must not reach the cloud
// metadata service or other link-local endpoints of the master's host: a settings:write key
// could otherwise point the panel at http://169.254.169.254/. LAN and loopback stay allowed
// (self-hosted webhook receivers are common).

// metadataIPs are cloud metadata / host agent addresses outside the link-local ranges.
var metadataIPs = []net.IP{
	net.ParseIP("100.100.100.200"), // Alibaba Cloud
	net.ParseIP("168.63.129.16"),   // Azure WireServer
	net.ParseIP("fd00:ec2::254"),   // AWS IMDS over IPv6
}

// blockedDialIP reports addresses a user-configured outbound request may not connect to.
func blockedDialIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, m := range metadataIPs {
		if m.Equal(ip) {
			return true
		}
	}
	return false
}

// guardedDialer checks the resolved address of every connection (redirects and DNS
// rebinding included).
func guardedDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if blockedDialIP(net.ParseIP(host)) {
			return fmt.Errorf("connections to %s are not allowed", host)
		}
		return nil
	}}
}

// guardedClient is an HTTP client for user-configured URLs.
func guardedClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = guardedDialer(10 * time.Second).DialContext
	return &http.Client{Timeout: timeout, Transport: tr}
}
