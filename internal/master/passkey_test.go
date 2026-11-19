package master

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
)

const passkeyTestHost = "panel.example.com"

var passkeyRP = virtualwebauthn.RelyingParty{Name: "sbpanel", ID: passkeyTestHost, Origin: "https://" + passkeyTestHost}

// passkeyOpt makes the request look like it came in over HTTPS for passkeyTestHost.
func passkeyOpt(r *http.Request) {
	r.Host = passkeyTestHost
	r.Header.Set("X-Forwarded-Proto", "https")
}

func adminSession(m *Master, user string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookie, Value: m.newSession(user)}) }
}

type passkeyBegin struct {
	Session string          `json:"session"`
	Options json.RawMessage `json:"options"`
}

func beginPasskey(t *testing.T, h http.Handler, path string, opts ...reqOpt) passkeyBegin {
	t.Helper()
	w := call(h, "POST", path, nil, append([]reqOpt{passkeyOpt}, opts...)...)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var b passkeyBegin
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.Session == "" || len(b.Options) == 0 {
		t.Fatalf("%s: bad response %s", path, w.Body)
	}
	return b
}

// registerPasskey adds a passkey for user through the HTTP handlers and returns the
// authenticator (holding the credential, user handle set) and the stored id.
func registerPasskey(t *testing.T, m *Master, h http.Handler, user, name string) (virtualwebauthn.Authenticator, string) {
	t.Helper()
	b := beginPasskey(t, h, "/api/me/passkeys/register/begin", adminSession(m, user))
	opts, err := virtualwebauthn.ParseAttestationOptions(string(b.Options))
	if err != nil {
		t.Fatal(err)
	}
	if opts.RelyingPartyID != passkeyTestHost {
		t.Fatalf("rp id %q", opts.RelyingPartyID)
	}
	auth := virtualwebauthn.NewAuthenticator()
	cred := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	resp := virtualwebauthn.CreateAttestationResponse(passkeyRP, auth, cred, *opts)
	w := call(h, "POST", "/api/me/passkeys/register/finish",
		map[string]any{"session": b.Session, "name": name, "credential": json.RawMessage(resp)}, passkeyOpt, adminSession(m, user))
	if w.Code != 200 {
		t.Fatalf("register finish: %d %s", w.Code, w.Body)
	}
	var p passkeyInfo
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.ID == "" || p.Name != name {
		t.Fatalf("register finish: %s", w.Body)
	}
	auth.Options.UserHandle = []byte(opts.UserID)
	auth.AddCredential(cred)
	return auth, p.ID
}

// assertPasskey runs a passkey sign-in with auth's first credential.
func assertPasskey(t *testing.T, h http.Handler, auth virtualwebauthn.Authenticator) (passkeyBegin, string, *httptest.ResponseRecorder) {
	t.Helper()
	b := beginPasskey(t, h, "/api/auth/passkey/begin")
	opts, err := virtualwebauthn.ParseAssertionOptions(string(b.Options))
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.AllowCredentials) != 0 {
		t.Fatalf("discoverable login must not list credentials: %v", opts.AllowCredentials)
	}
	resp := virtualwebauthn.CreateAssertionResponse(passkeyRP, auth, auth.Credentials[0], *opts)
	w := call(h, "POST", "/api/auth/passkey/finish", map[string]any{"session": b.Session, "credential": json.RawMessage(resp)}, passkeyOpt)
	return b, resp, w
}

func TestPasskeyRegisterAndSignIn(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	auth, id := registerPasskey(t, m, h, "admin", "MacBook")

	// listed, and excluded when adding another one
	w := call(h, "GET", "/api/me/passkeys", nil, sessionOpt(m))
	var list []passkeyInfo
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 1 || list[0].ID != id || list[0].Name != "MacBook" || list[0].LastUsedAt != 0 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	b := beginPasskey(t, h, "/api/me/passkeys/register/begin", sessionOpt(m))
	opts, _ := virtualwebauthn.ParseAttestationOptions(string(b.Options))
	if !auth.Credentials[0].IsExcludedForAttestation(*opts) {
		t.Fatal("existing passkey not in excludeCredentials")
	}
	var sel struct {
		AuthenticatorSelection struct {
			ResidentKey      string `json:"residentKey"`
			UserVerification string `json:"userVerification"`
		} `json:"authenticatorSelection"`
	}
	json.Unmarshal(b.Options, &sel)
	if sel.AuthenticatorSelection.ResidentKey != "required" || sel.AuthenticatorSelection.UserVerification != "required" {
		t.Fatalf("authenticatorSelection: %s", b.Options)
	}

	// sign in: a working session cookie, no TOTP needed even with TOTP on
	m.db.Exec(`UPDATE admins SET totp_secret=? WHERE username='admin'`, newTOTPSecret())
	sess, resp, w := assertPasskey(t, h, auth)
	if w.Code != 200 {
		t.Fatalf("passkey finish: %d %s", w.Code, w.Body)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly {
		t.Fatalf("no secure session cookie: %v", w.Result().Cookies())
	}
	w = call(h, "GET", "/api/auth/me", nil, func(r *http.Request) { r.AddCookie(cookie) })
	var me struct{ Username string }
	json.Unmarshal(w.Body.Bytes(), &me)
	if w.Code != 200 || me.Username != "admin" {
		t.Fatalf("session from passkey: %d %s", w.Code, w.Body)
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='login.passkey' AND admin='admin'`).Scan(&n)
	if n != 1 {
		t.Fatalf("login.passkey audit rows: %d", n)
	}
	var used int64
	m.db.QueryRow(`SELECT last_used_at FROM passkeys WHERE id=?`, id).Scan(&used)
	if used == 0 {
		t.Fatal("last_used_at not updated")
	}

	// the same ceremony works once
	w = call(h, "POST", "/api/auth/passkey/finish", map[string]any{"session": sess.Session, "credential": json.RawMessage(resp)}, passkeyOpt)
	if w.Code != 400 {
		t.Fatalf("reused ceremony: %d %s", w.Code, w.Body)
	}

	// an expired ceremony is refused
	b = beginPasskey(t, h, "/api/auth/passkey/begin")
	aopts, _ := virtualwebauthn.ParseAssertionOptions(string(b.Options))
	m.passkeyState.mu.Lock()
	m.passkeyState.m[b.Session].exp = time.Now().Add(-time.Second)
	m.passkeyState.mu.Unlock()
	resp = virtualwebauthn.CreateAssertionResponse(passkeyRP, auth, auth.Credentials[0], *aopts)
	w = call(h, "POST", "/api/auth/passkey/finish", map[string]any{"session": b.Session, "credential": json.RawMessage(resp)}, passkeyOpt)
	if w.Code != 400 {
		t.Fatalf("expired ceremony: %d %s", w.Code, w.Body)
	}

	// a signature counter that goes backwards (a cloned authenticator) is refused
	auth.Credentials[0].Counter = 5
	if _, _, w := assertPasskey(t, h, auth); w.Code != 200 {
		t.Fatalf("counter 5: %d %s", w.Code, w.Body)
	}
	auth.Credentials[0].Counter = 3
	if _, _, w := assertPasskey(t, h, auth); w.Code != 401 {
		t.Fatalf("cloned authenticator accepted: %d %s", w.Code, w.Body)
	}
}

func TestPasskeyUnknownCredential(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	auth, _ := registerPasskey(t, m, h, "admin", "Key")
	// a credential the panel never saw, claiming the admin's user handle
	stranger := virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{UserHandle: auth.Options.UserHandle})
	stranger.AddCredential(virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2))
	if _, _, w := assertPasskey(t, h, stranger); w.Code != 401 {
		t.Fatalf("unknown credential: %d %s", w.Code, w.Body)
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='login.failed' AND target='passkey'`).Scan(&n)
	if n != 1 {
		t.Fatalf("login.failed audit rows: %d", n)
	}
	// without user verification the assertion is refused too
	auth.Options.UserNotVerified = true
	if _, _, w := assertPasskey(t, h, auth); w.Code != 401 {
		t.Fatalf("assertion without user verification: %d %s", w.Code, w.Body)
	}
}

func TestPasskeyOwnership(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	if err := m.ResetAdmin("bob", "pw-bob-12345"); err != nil {
		t.Fatal(err)
	}
	_, bobID := registerPasskey(t, m, h, "bob", "Bob's key")
	_, ownID := registerPasskey(t, m, h, "admin", "Mine")

	// admin can't see or delete bob's passkey
	w := call(h, "GET", "/api/me/passkeys", nil, sessionOpt(m))
	var list []passkeyInfo
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != ownID {
		t.Fatalf("admin sees: %s", w.Body)
	}
	if w := call(h, "DELETE", "/api/me/passkeys/"+bobID, nil, sessionOpt(m)); w.Code != 404 {
		t.Fatalf("delete another admin's passkey: %d %s", w.Code, w.Body)
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM passkeys WHERE id=?`, bobID).Scan(&n)
	if n != 1 {
		t.Fatal("bob's passkey was deleted")
	}

	// a registration ceremony begun by bob can't be finished by admin
	b := beginPasskey(t, h, "/api/me/passkeys/register/begin", adminSession(m, "bob"))
	opts, _ := virtualwebauthn.ParseAttestationOptions(string(b.Options))
	resp := virtualwebauthn.CreateAttestationResponse(passkeyRP, virtualwebauthn.NewAuthenticator(), virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2), *opts)
	if w := call(h, "POST", "/api/me/passkeys/register/finish", map[string]any{"session": b.Session, "credential": json.RawMessage(resp)}, passkeyOpt, sessionOpt(m)); w.Code != 400 {
		t.Fatalf("finished another admin's ceremony: %d %s", w.Code, w.Body)
	}

	// API keys can't manage passkeys
	tok, _ := mkKey(t, m, h, map[string]any{"name": "ci", "scopes": []string{ScopeAdmin}})
	if w := call(h, "GET", "/api/me/passkeys", nil, hdr("Authorization", "Bearer "+tok)); w.Code != 403 {
		t.Fatalf("API key listed passkeys: %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/api/me/passkeys/register/begin", nil, passkeyOpt, hdr("Authorization", "Bearer "+tok)); w.Code != 403 {
		t.Fatalf("API key began a registration: %d %s", w.Code, w.Body)
	}

	// deleting your own works, is audited, and ends other sessions
	old := m.newSession("admin")
	if w := call(h, "DELETE", "/api/me/passkeys/"+ownID, nil, sessionOpt(m)); w.Code != 200 {
		t.Fatalf("delete own passkey: %d %s", w.Code, w.Body)
	}
	m.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='admin.passkey_remove' AND admin='admin'`).Scan(&n)
	if n != 1 {
		t.Fatalf("admin.passkey_remove audit rows: %d", n)
	}
	if w := call(h, "GET", "/api/auth/me", nil, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookie, Value: old}) }); w.Code != 401 {
		t.Fatalf("session survived passkey removal: %d", w.Code)
	}

	// renaming keeps bob's passkeys; a password reset clears them
	if err := m.RenameAdmin("bob", "robert"); err != nil {
		t.Fatal(err)
	}
	m.db.QueryRow(`SELECT COUNT(*) FROM passkeys WHERE admin='robert'`).Scan(&n)
	if n != 1 {
		t.Fatalf("passkeys after rename: %d", n)
	}
	m.ResetAdmin("robert", "pw-robert-123")
	m.db.QueryRow(`SELECT COUNT(*) FROM passkeys`).Scan(&n)
	if n != 0 {
		t.Fatalf("passkeys after reset: %d", n)
	}
}

func TestPasskeyCeremonyBounds(t *testing.T) {
	var c passkeyCeremonies
	for i := 0; i < passkeyCeremonyMax+10; i++ {
		c.put(&passkeyCeremony{kind: "login"})
	}
	if len(c.m) > passkeyCeremonyMax {
		t.Fatalf("%d ceremonies kept", len(c.m))
	}
	id := c.put(&passkeyCeremony{kind: "register", admin: "admin"})
	if c.take(id, "login") != nil || c.take(id, "register") != nil {
		t.Fatal("wrong-kind take must consume the ceremony")
	}
}

// Failed passkey sign-ins with an unknown credential lock out only the client that made them.
func TestPasskeyLimitPerClient(t *testing.T) {
	var l loginLimiter
	a, b := "203.0.113.7", "198.51.100.9"
	for i := 0; i < loginMaxUser+5; i++ {
		l.fail(a, passkeyLimitKey(a))
	}
	if l.blocked(a, passkeyLimitKey(a)) == 0 {
		t.Fatal("the failing client should be blocked")
	}
	if w := l.blocked(b, passkeyLimitKey(b)); w != 0 {
		t.Fatalf("another client is blocked for %v", w)
	}
}
