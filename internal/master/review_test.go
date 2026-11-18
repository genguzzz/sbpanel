package master

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"

	"sbpanel/internal/pb"
)

// update_mirror chooses the binary the next upgrade installs: only admin may change it.
func TestUpdateMirrorNeedsAdmin(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	sw, _ := mkKey(t, m, h, map[string]any{"name": "settings", "scopes": []string{"settings:write"}})
	admin, _ := mkKey(t, m, h, map[string]any{"name": "root", "scopes": []string{"admin"}})
	if w := call(h, "PUT", "/api/settings", map[string]string{"update_mirror": "https://evil.example/"}, bearer(sw)); w.Code != 403 {
		t.Fatalf("settings:write changed the update mirror: %d %s", w.Code, w.Body)
	}
	if v := settingOr(m.db, "update_mirror", ""); v != "" {
		t.Fatalf("mirror stored: %q", v)
	}
	// other settings, and resending the current (empty) mirror, still work for settings:write
	if w := call(h, "PUT", "/api/settings", map[string]string{"sub_title": "x", "update_mirror": ""}, bearer(sw)); w.Code != 200 {
		t.Fatalf("settings:write save: %d %s", w.Code, w.Body)
	}
	if w := call(h, "PUT", "/api/settings", map[string]string{"update_mirror": "javascript:x"}, bearer(admin)); w.Code != 400 {
		t.Fatalf("bad mirror accepted: %d", w.Code)
	}
	if w := call(h, "PUT", "/api/settings", map[string]string{"update_mirror": "http://ghfast.top/"}, bearer(admin)); w.Code != 400 {
		t.Fatalf("plain-HTTP mirror: %d", w.Code)
	}
	if w := call(h, "PUT", "/api/settings", map[string]string{"update_mirror": "https://ghfast.top/"}, bearer(admin)); w.Code != 200 {
		t.Fatalf("admin mirror: %d %s", w.Code, w.Body)
	}
	if w := call(h, "PUT", "/api/settings", map[string]string{"update_mirror": "https://ghfast.top/"}, sessionOpt(m)); w.Code != 200 {
		t.Fatalf("session mirror: %d %s", w.Code, w.Body)
	}
}

func TestWebhookURLValidatedAndMetadataBlocked(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	for _, v := range []string{"file:///etc/passwd", "gopher://x/", "http://", "not a url"} {
		if w := call(h, "PUT", "/api/settings", map[string]string{"webhook_url": v}, sessionOpt(m)); w.Code != 400 {
			t.Fatalf("webhook %q accepted: %d", v, w.Code)
		}
	}
	for _, ip := range []string{"169.254.169.254", "fe80::1", "0.0.0.0", "::", "100.100.100.200", "fd00:ec2::254", "224.0.0.1"} {
		if !blockedDialIP(net.ParseIP(ip)) {
			t.Fatalf("%s not blocked", ip)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.5", "192.168.1.2", "1.1.1.1", "2606:4700::1111"} {
		if blockedDialIP(net.ParseIP(ip)) {
			t.Fatalf("%s blocked", ip)
		}
	}
	// connect directly, whatever HTTP(S)_PROXY the test environment has
	saved := notifyClient
	notifyClient = guardedClient(5 * time.Second)
	notifyClient.Transport.(*http.Transport).Proxy = nil
	defer func() { notifyClient = saved }()
	setSetting(m.db, "webhook_url", "http://169.254.169.254/latest/meta-data/")
	if err := m.sendNotify("x"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("metadata webhook: %v", err)
	}
	// a local receiver still works
	got := make(chan bool, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got <- true }))
	defer srv.Close()
	setSetting(m.db, "webhook_url", srv.URL)
	if err := m.sendNotify("x"); err != nil {
		t.Fatalf("local webhook: %v", err)
	}
	<-got
}

// Paths that are not in clean form must not slip past the /dev, /proc ... checks.
func TestNodePathBypass(t *testing.T) {
	for _, p := range []string{"//dev/zero", "/./dev/zero", "/dev/./zero", "///proc/self/mem", "/dev", "/sys/.", "/etc/\rx"} {
		if !badNodePath(p) {
			t.Fatalf("%q allowed", p)
		}
	}
	for _, p := range []string{"/etc/ssl/a.pem", "/etc/letsencrypt/live/x/privkey.pem", "/devices.pem", "/runner/a.pem"} {
		if badNodePath(p) {
			t.Fatalf("%q rejected", p)
		}
	}
	_, _, err := (&inboundReq{Tag: "a", Type: "trojan", Options: json.RawMessage(`{"listen_port":1,"tls":{"enabled":true,"certificate_path":"//dev/zero","key_path":"/etc/k"}}`)}).validate()
	if err == nil {
		t.Fatal("inbound with //dev/zero accepted")
	}
}

// PATCH /api/certs/{id} used to accept any absolute path (e.g. /dev/zero) for node certificates.
func TestNodeCertPatchPaths(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	m.db.Exec(`INSERT INTO nodes(id,name,created_at) VALUES('n1','n1',1)`)
	w := call(h, "POST", "/api/certs/node", map[string]any{"node_id": "n1", "cert_path": "/etc/ssl/a.pem", "key_path": "/etc/ssl/a.key"}, sessionOpt(m))
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var out struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &out)
	for _, p := range []string{"/dev/zero", "//proc/self/environ", "/etc/../dev/zero", "/etc/ssl/"} {
		if w := call(h, "PATCH", "/api/certs/"+out.ID, map[string]any{"key_path": p}, sessionOpt(m)); w.Code != 400 {
			t.Fatalf("PATCH key_path %q: %d %s", p, w.Code, w.Body)
		}
	}
	if w := call(h, "PATCH", "/api/certs/"+out.ID, map[string]any{"key_path": "/etc/ssl/b.key"}, sessionOpt(m)); w.Code != 200 {
		t.Fatalf("PATCH good path: %d %s", w.Code, w.Body)
	}
}

// Anonymous gRPC callers may reach Enroll (rate-limited) but nothing else, before the message is read.
func TestHubTapRequiresClientCert(t *testing.T) {
	h := newHub(newTestMaster(t))
	anon := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{}}})
	if _, err := h.tap(anon, &tap.Info{FullMethodName: pb.Enroll_Enroll_FullMethodName}); err != nil {
		t.Fatalf("enroll refused: %v", err)
	}
	for _, ctx := range []context.Context{context.Background(), anon} {
		if _, err := h.tap(ctx, &tap.Info{FullMethodName: pb.NodeHub_FetchSpec_FullMethodName}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anonymous FetchSpec: %v", err)
		}
	}
	leaf := &x509.Certificate{}
	withCert := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}}})
	if _, err := h.tap(withCert, &tap.Info{FullMethodName: pb.NodeHub_FetchSpec_FullMethodName}); err != nil {
		t.Fatalf("verified client refused: %v", err)
	}
}

// Cookie-authenticated writes from another site (sibling subdomains included) are refused.
func TestCrossSiteWritesRefused(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	body := map[string]string{"sub_title": "x"}
	for _, site := range []string{"cross-site", "same-site"} {
		if w := call(h, "PUT", "/api/settings", body, sessionOpt(m), hdr("Sec-Fetch-Site", site)); w.Code != 403 {
			t.Fatalf("%s write: %d", site, w.Code)
		}
	}
	if w := call(h, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": "x"}, hdr("Sec-Fetch-Site", "cross-site")); w.Code != 403 {
		t.Fatalf("cross-site login: %d", w.Code)
	}
	for _, site := range []string{"same-origin", "none", ""} {
		if w := call(h, "PUT", "/api/settings", body, sessionOpt(m), hdr("Sec-Fetch-Site", site)); w.Code != 200 {
			t.Fatalf("%q write: %d %s", site, w.Code, w.Body)
		}
	}
	if w := call(h, "GET", "/api/settings", nil, sessionOpt(m), hdr("Sec-Fetch-Site", "cross-site")); w.Code != 200 {
		t.Fatalf("cross-site read: %d", w.Code)
	}
	key, _ := mkKey(t, m, h, map[string]any{"name": "k", "scopes": []string{"settings:write"}})
	if w := call(h, "PUT", "/api/settings", body, bearer(key), hdr("Sec-Fetch-Site", "cross-site")); w.Code != 200 {
		t.Fatalf("bearer write: %d %s", w.Code, w.Body)
	}
}

func TestLoginLimiterIPv6AndOverflow(t *testing.T) {
	var l loginLimiter
	for i := 0; i < loginMaxIP; i++ {
		l.fail("2001:db8:1:2::"+strings.Repeat("a", 1+i%3), "u"+string(rune('a'+i)))
	}
	if l.blocked("2001:db8:1:2:ffff::1", "other") == 0 {
		t.Fatal("addresses in one /64 are counted separately")
	}
	if l.blocked("2001:db8:1:3::1", "other") != 0 {
		t.Fatal("another /64 blocked")
	}
	var l2 loginLimiter
	for i := 0; i < loginMaxUser; i++ {
		l2.fail("198.51.100."+string(rune('0'+i%10)), "admin")
	}
	for k := 0; len(l2.fails) <= 100_000; k++ {
		l2.fails["ip:x"+string(rune(k))+strings.Repeat("y", k%7)] = nil
		if k > 200_000 {
			break
		}
	}
	l2.fail("203.0.113.9", "someone")
	if l2.blocked("203.0.113.10", "admin") == 0 {
		t.Fatal("overflow reset the per-username lockout")
	}
}

func TestLogLimitClamped(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	for i := 0; i < 3; i++ {
		addEvent(m.db, "x", 0, "", "e")
	}
	for _, p := range []string{"/api/events?limit=-1", "/api/audit?limit=-1", "/api/events?limit=999999999"} {
		if w := call(h, "GET", p, nil, sessionOpt(m)); w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/?limit=-1", nil)
	if qLimit(r, 100) != 100 {
		t.Fatal("negative limit kept")
	}
	r = httptest.NewRequest("GET", "/?limit=20", nil)
	if qLimit(r, 100) != 20 {
		t.Fatal("valid limit dropped")
	}
}

func TestEnrollCommandShellSafe(t *testing.T) {
	m := newTestMaster(t)
	m.db.Exec(`INSERT INTO nodes(id,name,created_at) VALUES('n1','n1',1)`)
	r := httptest.NewRequest("POST", "/api/nodes/n1/token", nil)
	r.Host = "a$(id)b"
	info, err := m.enrollInfo(r, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if cmd := info["command"].(string); strings.ContainsAny(cmd, "$()`;|&") {
		t.Fatalf("shell syntax in command: %s", cmd)
	}
	r.Host = "panel.example.com:8080"
	info, _ = m.enrollInfo(r, "n1")
	if !strings.Contains(info["command"].(string), "panel.example.com:") {
		t.Fatalf("normal host dropped: %s", info["command"])
	}
}
