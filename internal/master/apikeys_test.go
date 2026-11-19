package master

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type reqOpt func(*http.Request)

func bearer(tok string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}
func from(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }
func hdr(k, v string) reqOpt  { return func(r *http.Request) { r.Header.Set(k, v) } }

func call(h http.Handler, method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func sessionOpt(m *Master) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookie, Value: m.newSession("admin")}) }
}

// mkKey creates a key through the API as the logged-in admin.
func mkKey(t *testing.T, m *Master, h http.Handler, req map[string]any) (string, apiKey) {
	t.Helper()
	w := call(h, "POST", "/api/tokens", req, sessionOpt(m))
	if w.Code != 200 {
		t.Fatalf("create key %v: %d %s", req, w.Code, w.Body)
	}
	var out struct {
		Token string `json:"token"`
		Key   apiKey `json:"key"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if !strings.HasPrefix(out.Token, "sbp_") {
		t.Fatalf("token %q", out.Token)
	}
	return out.Token, out.Key
}

func TestRouteScopesComplete(t *testing.T) {
	m := newTestMaster(t)
	m.routes()
	mux := m.keyState().mux
	registered := map[string]bool{}
	for _, p := range mux.patterns {
		registered[p] = true
		if needsScope(p) {
			if _, ok := routeScopes[p]; !ok {
				t.Errorf("route %q has no scope in routeScopes (api_scopes.go)", p)
			}
			if _, ok := apiDocs[p]; !ok && p != "/api/" && p != "GET /mcp" && p != "DELETE /mcp" {
				t.Errorf("route %q is not documented in apiDocs (openapi.go)", p)
			}
		}
	}
	for p, s := range routeScopes {
		if !registered[p] {
			t.Errorf("routeScopes has %q but no such route is registered", p)
		}
		if s != scopePublic && s != scopeAny && s != scopeSession && s != scopeKey && !slices.Contains(AllScopes, s) {
			t.Errorf("route %q maps to unknown scope %q", p, s)
		}
	}
	for p := range apiDocs {
		if !registered[p] {
			t.Errorf("apiDocs documents %q but no such route is registered", p)
		}
	}
	// every GET under /api that needs auth is readable with the read scope (except key management,
	// backup and the admin's own sign-in credentials)
	for p, s := range routeScopes {
		if strings.HasPrefix(p, "GET /api/") && s != ScopeRead && s != scopeAny && p != "GET /api/tokens" && p != "GET /api/backup" && p != "GET /api/me/passkeys" {
			t.Errorf("GET route %q requires %q, expected read", p, s)
		}
	}
}

func TestOpenAPISpec(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	tok, _ := mkKey(t, m, h, map[string]any{"name": "docs", "scopes": []string{"users:write"}})
	w := call(h, "GET", "/api/openapi.json", nil, bearer(tok)) // any scope may read the spec
	if w.Code != 200 {
		t.Fatalf("openapi %d %s", w.Code, w.Body)
	}
	if call(h, "GET", "/api/openapi.json", nil).Code != 401 {
		t.Fatal("spec must require auth")
	}
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v", spec["openapi"])
	}
	for _, k := range []string{"info", "paths", "components", "servers", "security", "tags"} {
		if spec[k] == nil {
			t.Errorf("missing top-level %q", k)
		}
	}
	info := spec["info"].(map[string]any)
	if info["title"] == "" || info["version"] == "" {
		t.Fatal("info.title/version required")
	}
	paths := spec["paths"].(map[string]any)
	for _, p := range m.keyState().mux.patterns {
		method, path, ok := strings.Cut(p, " ")
		if !ok || !strings.HasPrefix(path, "/api/") {
			continue
		}
		ops, _ := paths[path].(map[string]any)
		op, _ := ops[strings.ToLower(method)].(map[string]any)
		if op == nil {
			t.Errorf("%s missing from OpenAPI spec", p)
			continue
		}
		if op["x-required-scope"] != routeScopes[p] || op["responses"] == nil || op["summary"] == "" {
			t.Errorf("%s: bad operation %v", p, op["x-required-scope"])
		}
	}
	// every $ref resolves
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for _, r := range regexpAll(w.Body.String(), `"#/components/schemas/([A-Za-z]+)"`) {
		if schemas[r] == nil {
			t.Errorf("dangling $ref %s", r)
		}
	}
}

func mustRe(s string) *regexp.Regexp { return regexp.MustCompile(s) }

func regexpAll(s, re string) []string {
	out := []string{}
	for _, m := range mustRe(re).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestScopedKeys(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	read, _ := mkKey(t, m, h, map[string]any{"name": "reader", "scopes": []string{"read"}, "description": "dashboards"})
	writer, _ := mkKey(t, m, h, map[string]any{"name": "writer", "scopes": []string{"users:write"}})
	admin, _ := mkKey(t, m, h, map[string]any{"name": "root", "scopes": []string{"admin"}})

	cases := []struct {
		tok, method, path string
		body              any
		want              int
	}{
		{read, "GET", "/api/users", nil, 200},
		{read, "GET", "/api/overview", nil, 200},
		{read, "GET", "/api/openapi.json", nil, 200},
		{read, "POST", "/api/users", map[string]any{"username": "x"}, 403},
		{read, "GET", "/api/tokens", nil, 403},
		{read, "POST", "/api/tokens", map[string]any{"name": "escalate", "scopes": []string{"admin"}}, 403},
		{read, "GET", "/api/backup", nil, 403},
		{read, "PUT", "/api/settings", map[string]string{"sub_title": "x"}, 403},
		{writer, "POST", "/api/users", map[string]any{"username": "via-writer"}, 200},
		{writer, "GET", "/api/users", nil, 403},
		{writer, "POST", "/api/groups", map[string]any{"name": "g"}, 403},
		{admin, "GET", "/api/tokens", nil, 200},
		{admin, "POST", "/api/groups", map[string]any{"name": "g"}, 200},
		{admin, "POST", "/api/me/password", map[string]any{"old": "a", "new": "bbbbbbbbb"}, 403}, // session only
		{"sbp_" + strings.Repeat("0", 48), "GET", "/api/users", nil, 401},
	}
	for _, c := range cases {
		if w := call(h, c.method, c.path, c.body, bearer(c.tok)); w.Code != c.want {
			t.Errorf("%s %s with %s…: %d want %d (%s)", c.method, c.path, c.tok[:10], w.Code, c.want, w.Body)
		}
	}
	// writes are attributed to the key
	var who string
	m.db.QueryRow(`SELECT admin FROM audit_log WHERE action='user.create' ORDER BY id DESC`).Scan(&who)
	if who != "token:writer" {
		t.Fatalf("audit actor %q", who)
	}
	// the key list never contains key material; last_used/last_ip are tracked
	w := call(h, "GET", "/api/tokens", nil, sessionOpt(m))
	if strings.Contains(w.Body.String(), read) || strings.Contains(w.Body.String(), "token_hash") {
		t.Fatal("key material in listing")
	}
	var keys []apiKey
	json.Unmarshal(w.Body.Bytes(), &keys)
	if len(keys) != 3 || keys[0].LastUsed == 0 || keys[0].LastIP != "192.0.2.1" || keys[0].Description != "dashboards" ||
		!slices.Equal(keys[0].Scopes, []string{"read"}) || keys[0].RateLimit != defaultRatePerMin {
		t.Fatalf("listing: %+v", keys)
	}
	// audit/log never stores the key
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%sbp_%' OR target LIKE '%sbp_%'`).Scan(&n)
	if n != 0 {
		t.Fatal("key material in audit log")
	}

	// validation
	for _, bad := range []map[string]any{
		{"name": "", "scopes": []string{"read"}},
		{"name": "a", "scopes": []string{}},
		{"name": "a", "scopes": []string{"root"}},
		{"name": "a", "scopes": []string{"read"}, "allow_ips": []string{"300.1.1.1"}},
		{"name": "a", "scopes": []string{"read"}, "expires_at": 1},
		{"name": "a", "scopes": []string{"read"}, "rate_limit": -1},
		{"name": "reader", "scopes": []string{"read"}}, // duplicate name
	} {
		if w := call(h, "POST", "/api/tokens", bad, sessionOpt(m)); w.Code != 400 {
			t.Errorf("bad key %v accepted: %d", bad, w.Code)
		}
	}

	// delete revokes immediately
	id := keys[0].ID
	if w := call(h, "DELETE", "/api/tokens/"+fmtID(id), nil, bearer(admin)); w.Code != 200 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := call(h, "GET", "/api/users", nil, bearer(read)); w.Code != 401 {
		t.Fatalf("deleted key still works: %d", w.Code)
	}
}

func TestKeyExpiryIPAndRateLimit(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()

	exp, k := mkKey(t, m, h, map[string]any{"name": "short", "scopes": []string{"read"}, "expires_in_days": 1})
	if w := call(h, "GET", "/api/users", nil, bearer(exp)); w.Code != 200 {
		t.Fatalf("fresh key %d", w.Code)
	}
	m.db.Exec(`UPDATE api_tokens SET expires_at=? WHERE id=?`, now()-1, k.ID)
	if w := call(h, "GET", "/api/users", nil, bearer(exp)); w.Code != 401 || !strings.Contains(w.Body.String(), "API key expired") {
		t.Fatalf("expired key %d %s", w.Code, w.Body)
	}
	// the message follows the caller's language (X-Lang, else Accept-Language)
	if w := call(h, "GET", "/api/users", nil, bearer(exp), hdr("X-Lang", "zh")); w.Code != 401 || !strings.Contains(w.Body.String(), "过期") || w.Header().Get("Content-Language") != "zh-CN" {
		t.Fatalf("expired key (zh) %d %s", w.Code, w.Body)
	}
	if w := call(h, "GET", "/api/users", nil, bearer(exp), hdr("Accept-Language", "zh-TW,zh;q=0.9,en;q=0.8")); !strings.Contains(w.Body.String(), "过期") {
		t.Fatalf("expired key (Accept-Language zh-TW) %s", w.Body)
	}

	ipk, k2 := mkKey(t, m, h, map[string]any{"name": "lan", "scopes": []string{"read"}, "allow_ips": []string{"10.0.0.0/8", "2001:db8::1"}})
	if !slices.Equal(k2.AllowIPs, []string{"10.0.0.0/8", "2001:db8::1"}) {
		t.Fatalf("allow list %v", k2.AllowIPs)
	}
	for _, c := range []struct {
		opts []reqOpt
		want int
	}{
		{[]reqOpt{from("10.1.2.3:5555")}, 200},
		{[]reqOpt{from("[2001:db8::1]:5555")}, 200},
		{[]reqOpt{from("192.0.2.7:5555")}, 403},
		{[]reqOpt{from("127.0.0.1:5555"), hdr("X-Forwarded-For", "10.9.9.9")}, 200},  // trusted proxy on loopback
		{[]reqOpt{from("127.0.0.1:5555"), hdr("X-Forwarded-For", "192.0.2.9")}, 403}, // proxied client outside list
		{[]reqOpt{from("192.0.2.7:5555"), hdr("X-Forwarded-For", "10.9.9.9")}, 403},  // spoofed header ignored
	} {
		if w := call(h, "GET", "/api/users", nil, append(c.opts, bearer(ipk))...); w.Code != c.want {
			t.Errorf("ip case %v: %d want %d", c.want, w.Code, c.want)
		}
	}

	rl, _ := mkKey(t, m, h, map[string]any{"name": "slow", "scopes": []string{"read"}, "rate_limit": 6, "rate_burst": 2})
	for i := 0; i < 2; i++ {
		if w := call(h, "GET", "/api/users", nil, bearer(rl)); w.Code != 200 || w.Header().Get("X-RateLimit-Limit") != "6" {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := call(h, "GET", "/api/users", nil, bearer(rl))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || w.Header().Get("Retry-After") == "0" {
		t.Fatalf("rate limit: %d retry=%q", w.Code, w.Header().Get("Retry-After"))
	}
	// sessions are not rate limited, other keys have their own bucket
	if w := call(h, "GET", "/api/users", nil, sessionOpt(m)); w.Code != 200 {
		t.Fatal("session limited")
	}
}

func TestLegacyTokensBecomeAdmin(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	tok := "sbp_" + strings.Repeat("ab", 24)
	// a row as written before v5 (only the original columns)
	if _, err := m.db.Exec(`INSERT INTO api_tokens(name, token_hash, prefix, created_at) VALUES('old', ?, ?, ?)`, hashToken(tok), tok[:10], now()); err != nil {
		t.Fatal(err)
	}
	if w := call(h, "GET", "/api/tokens", nil, bearer(tok)); w.Code != 200 {
		t.Fatalf("legacy token lost admin: %d", w.Code)
	}
	// old clients that only send {name} still get a full-access token
	w := call(h, "POST", "/api/tokens", map[string]any{"name": "ci"}, sessionOpt(m))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"scopes":["admin"]`) {
		t.Fatalf("name-only create: %d %s", w.Code, w.Body)
	}
}

// The spec's text follows the caller's language; the English spec has no Chinese left.
func TestOpenAPILocalized(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	tok, _ := mkKey(t, m, h, map[string]any{"name": "docs", "scopes": []string{"read"}})
	op := func(lang string) (string, map[string]any) {
		w := call(h, "GET", "/api/openapi.json", nil, bearer(tok), hdr("X-Lang", lang))
		var spec map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
			t.Fatalf("%s spec: %v", lang, err)
		}
		return w.Body.String(), spec["paths"].(map[string]any)["/api/certs"].(map[string]any)["get"].(map[string]any)
	}
	enBody, en := op("en")
	if en["summary"] != "List certificates (with the inbounds using them)" || !strings.HasPrefix(en["description"].(string), "Requires scope `read`.") {
		t.Fatalf("en op = %v / %v", en["summary"], en["description"])
	}
	for _, r := range enBody {
		if r >= 0x4e00 && r <= 0x9fff {
			t.Fatalf("Chinese text in the English spec near %q", enBody[strings.IndexRune(enBody, r)-40:strings.IndexRune(enBody, r)+20])
		}
	}
	_, zh := op("zh")
	if zh["summary"] != "证书列表（含使用的入站）" || !strings.HasPrefix(zh["description"].(string), "需要权限：`read`。") {
		t.Fatalf("zh op = %v / %v", zh["summary"], zh["description"])
	}
}
