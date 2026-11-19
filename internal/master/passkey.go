package master

// Passkey (WebAuthn) sign-in. A passkey is a discoverable credential created with user
// verification required, so it is a full two-factor sign-in on its own and skips TOTP.
// Ceremony state (the challenge) lives in memory; the relying party is derived from each
// request's host, so the panel works under whatever name it is opened with.

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	passkeyCeremonyTTL = 5 * time.Minute
	passkeyCeremonyMax = 1000 // pending ceremonies kept in memory
	passkeyNameMax     = 64
	passkeyLoginUser   = "passkey" // audit name before the admin is known
)

// ---------- ceremony state ----------

type passkeyCeremony struct {
	kind  string // "register" or "login"
	admin string // register: the admin the passkey is for
	data  webauthn.SessionData
	exp   time.Time
}

// passkeyCeremonies holds begun registrations and sign-ins until they finish: single use,
// passkeyCeremonyTTL, at most passkeyCeremonyMax entries. The zero value is ready to use.
type passkeyCeremonies struct {
	mu sync.Mutex
	m  map[string]*passkeyCeremony
}

func (c *passkeyCeremonies) put(e *passkeyCeremony) string {
	b := make([]byte, 32)
	rand.Read(b)
	id := base64.RawURLEncoding.EncodeToString(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*passkeyCeremony{}
	}
	t := time.Now()
	for k, v := range c.m {
		if t.After(v.exp) {
			delete(c.m, k)
		}
	}
	for len(c.m) >= passkeyCeremonyMax {
		// full of live ceremonies: drop the oldest, so a flood can't lock everyone out for good
		var oldest string
		for k, v := range c.m {
			if oldest == "" || v.exp.Before(c.m[oldest].exp) {
				oldest = k
			}
		}
		delete(c.m, oldest)
	}
	e.exp = t.Add(passkeyCeremonyTTL)
	c.m[id] = e
	return id
}

// take returns the ceremony and forgets it (a session id works once, whatever the outcome).
func (c *passkeyCeremonies) take(id, kind string) *passkeyCeremony {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[id]
	delete(c.m, id)
	if e == nil || e.kind != kind || time.Now().After(e.exp) {
		return nil
	}
	return e
}

// ---------- relying party ----------

// passkeyUser adapts an admin to webauthn.User. The user handle is the admin's row id, which
// survives a rename.
type passkeyUser struct {
	id    int64
	name  string
	creds []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return passkeyHandle(u.id) }
func (u *passkeyUser) WebAuthnName() string                       { return u.name }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.name }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func passkeyHandle(id int64) []byte { return []byte("sbp-admin:" + strconv.FormatInt(id, 10)) }

func passkeyID(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

// requestScheme mirrors how the session cookie's Secure flag is decided.
func requestScheme(r *http.Request) string {
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		return "https"
	}
	return "http"
}

// relyingParty builds the WebAuthn configuration for the host this request came in on:
// RP ID = the hostname, origin = scheme://host[:port].
func (m *Master) relyingParty(r *http.Request) (*webauthn.WebAuthn, error) {
	host := r.Host
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	if name == "" {
		return nil, errors.New("no host")
	}
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL}
	return webauthn.New(&webauthn.Config{
		RPID:          name,
		RPDisplayName: settingOr(m.db, "sub_title", "sbpanel"),
		RPOrigins:     []string{requestScheme(r) + "://" + host},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts:              webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
}

// passkeyAdmin loads an admin with their passkeys.
func (m *Master) passkeyAdmin(username string) (*passkeyUser, error) {
	u := &passkeyUser{name: username}
	if err := m.db.QueryRow(`SELECT id FROM admins WHERE username=?`, username).Scan(&u.id); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(`SELECT credential_json FROM passkeys WHERE admin=? ORDER BY created_at, id`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		u.creds = append(u.creds, c)
	}
	return u, rows.Err()
}

func passkeyUnavailable(w http.ResponseWriter) {
	writeErr(w, 400, "Passkeys are not available at this address; open the panel over HTTPS or localhost")
}

// ---------- account endpoints (session only) ----------

type passkeyInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
}

func (m *Master) apiPasskeys(w http.ResponseWriter, r *http.Request) {
	out, err := scanRows(m, `SELECT id, name, created_at, last_used_at FROM passkeys WHERE admin=? ORDER BY created_at, id`,
		[]any{actor(r)}, func(s func(...any) error) (passkeyInfo, error) {
			var p passkeyInfo
			return p, s(&p.ID, &p.Name, &p.CreatedAt, &p.LastUsedAt)
		})
	if errf(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (m *Master) apiPasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	wa, err := m.relyingParty(r)
	if err != nil {
		passkeyUnavailable(w)
		return
	}
	u, err := m.passkeyAdmin(actor(r))
	if errf(w, err) {
		return
	}
	creation, data, err := wa.BeginRegistration(u,
		webauthn.WithExclusions(webauthn.Credentials(u.creds).CredentialDescriptors()),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired))
	if errf(w, err) {
		return
	}
	id := m.passkeyState.put(&passkeyCeremony{kind: "register", admin: u.name, data: *data})
	writeJSON(w, 200, map[string]any{"session": id, "options": creation.Response})
}

func (m *Master) apiPasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session    string          `json:"session"`
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	c := m.passkeyState.take(req.Session, "register")
	if c == nil || c.admin != actor(r) {
		writeErr(w, 400, "The passkey request expired; please try again")
		return
	}
	wa, err := m.relyingParty(r)
	if err != nil {
		passkeyUnavailable(w)
		return
	}
	u, err := m.passkeyAdmin(c.admin)
	if errf(w, err) {
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		writeErr(w, 400, "Passkey verification failed")
		return
	}
	cred, err := wa.CreateCredential(u, c.data, parsed)
	if err != nil {
		writeErr(w, 400, "Passkey verification failed")
		return
	}
	name := truncate(strings.TrimSpace(req.Name), passkeyNameMax)
	if name == "" {
		name = "Passkey"
	}
	raw, err := json.Marshal(cred)
	if errf(w, err) {
		return
	}
	p := passkeyInfo{ID: passkeyID(cred.ID), Name: name, CreatedAt: now()}
	res, err := m.db.Exec(`INSERT INTO passkeys(id, admin, name, credential_json, created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO NOTHING`, p.ID, u.name, p.Name, string(raw), p.CreatedAt)
	if errf(w, err) {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, 409, "This passkey is already registered")
		return
	}
	m.renewOwnSession(w, r)
	m.audit(r, "admin.passkey_add", actor(r), name)
	writeJSON(w, 200, p)
}

func (m *Master) apiDeletePasskey(w http.ResponseWriter, r *http.Request) {
	var name string
	err := m.db.QueryRow(`DELETE FROM passkeys WHERE id=? AND admin=? RETURNING name`, r.PathValue("id"), actor(r)).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "Passkey not found")
		return
	}
	if errf(w, err) {
		return
	}
	m.renewOwnSession(w, r)
	m.audit(r, "admin.passkey_remove", actor(r), name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- sign-in (public) ----------

// passkeyLimitKey is the login-limiter "username" for passkey attempts whose admin isn't known:
// one per client address, so failures from some clients never lock passkey sign-in for everyone.
func passkeyLimitKey(ip string) string { return passkeyLoginUser + "@" + limiterIP(ip) }

func (m *Master) apiPasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if wait := m.logins.blocked(clientIP(r), passkeyLimitKey(clientIP(r))); wait > 0 {
		writeErr(w, 429, T(langOf(w), "Too many failed sign-ins; try again in %d minutes", int(wait.Minutes())+1))
		return
	}
	wa, err := m.relyingParty(r)
	if err != nil {
		passkeyUnavailable(w)
		return
	}
	assertion, data, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if errf(w, err) {
		return
	}
	id := m.passkeyState.put(&passkeyCeremony{kind: "login", data: *data})
	writeJSON(w, 200, map[string]any{"session": id, "options": assertion.Response})
}

func (m *Master) apiPasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if wait := m.logins.blocked(ip, passkeyLimitKey(ip)); wait > 0 {
		writeErr(w, 429, T(langOf(w), "Too many failed sign-ins; try again in %d minutes", int(wait.Minutes())+1))
		return
	}
	var req struct {
		Session    string          `json:"session"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	c := m.passkeyState.take(req.Session, "login")
	if c == nil {
		writeErr(w, 400, "The passkey request expired; please try again")
		return
	}
	// failures count against the admin once the credential is known, else against this client
	who, limitKey := passkeyLoginUser, passkeyLimitKey(ip)
	fail := func() {
		m.logins.fail(ip, limitKey)
		time.Sleep(500 * time.Millisecond)
		m.db.Exec(`INSERT INTO audit_log(ts, admin, action, target, ip) VALUES(?,?,?,?,?)`, now(), who, "login.failed", "passkey", ip)
		writeErr(w, 401, "Passkey sign-in failed")
	}
	wa, err := m.relyingParty(r)
	if err != nil {
		passkeyUnavailable(w)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
	if err != nil {
		fail()
		return
	}
	credID := passkeyID(parsed.RawID)
	_, cred, err := wa.ValidatePasskeyLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		var admin string
		if err := m.db.QueryRow(`SELECT admin FROM passkeys WHERE id=?`, passkeyID(rawID)).Scan(&admin); err != nil {
			return nil, errors.New("unknown credential")
		}
		who, limitKey = admin, admin
		u, err := m.passkeyAdmin(admin)
		if err != nil {
			return nil, err
		}
		if string(userHandle) != string(u.WebAuthnID()) {
			return nil, errors.New("user handle mismatch")
		}
		return u, nil
	}, c.data, parsed)
	if err != nil || cred.Authenticator.CloneWarning {
		// a clone warning means the signature counter went backwards: a copied authenticator
		fail()
		return
	}
	raw, err := json.Marshal(cred)
	if errf(w, err) {
		return
	}
	m.db.Exec(`UPDATE passkeys SET credential_json=?, last_used_at=? WHERE id=?`, string(raw), now(), credID)
	m.completeLogin(w, r, who, "login.passkey")
}
