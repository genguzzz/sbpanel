package master

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type actorKey struct{}

// actor is who performed a request: an admin session or an API token.
func actor(r *http.Request) string {
	if v, ok := r.Context().Value(actorKey{}).(string); ok {
		return v
	}
	return "system"
}

func (m *Master) audit(r *http.Request, action, target, detail string) {
	ip := ""
	if r != nil {
		ip = clientIP(r)
	}
	who := "system"
	if r != nil {
		who = actor(r)
	}
	m.db.Exec(`INSERT INTO audit_log(ts, admin, action, target, detail, ip) VALUES(?,?,?,?,?,?)`, now(), who, action, target, truncate(detail, 500), ip)
}

func withActor(r *http.Request, who string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorKey{}, who))
}

// ---------- TOTP (RFC 6238, SHA-1, 6 digits, 30 s) ----------

func totpCode(secret string, t time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(t.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	s := mac.Sum(nil)
	off := s[len(s)-1] & 0x0f
	v := binary.BigEndian.Uint32(s[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}

func totpValid(secret, code string, t time.Time) bool {
	_, ok := totpStep(secret, code, t)
	return ok
}

// totpStep checks a code against the previous, current and next 30 s step and returns the
// step it matched.
func totpStep(secret, code string, t time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	for _, d := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		at := t.Add(d)
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, at)), []byte(code)) == 1 {
			return at.Unix() / 30, true
		}
	}
	return 0, false
}

// totpUse accepts a code once: a step already used to sign in (a sniffed or replayed code)
// is rejected.
func (m *Master) totpUse(user, secret, code string, t time.Time) bool {
	step, ok := totpStep(secret, code, t)
	if !ok {
		return false
	}
	m.logins.mu.Lock()
	defer m.logins.mu.Unlock()
	if m.logins.totp == nil {
		m.logins.totp = map[string]int64{}
	}
	if step <= m.logins.totp[user] {
		return false
	}
	m.logins.totp[user] = step
	return true
}

// dummyHash is compared against when the username doesn't exist, so a failed sign-in takes
// the same time either way (no username probing).
var dummyHash = func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	return string(h)
}()

// loginLimiter counts failed sign-ins per client IP and per username in a sliding window.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
	totp  map[string]int64 // last TOTP step used per admin
}

const (
	loginWindow  = 15 * time.Minute
	loginMaxIP   = 10 // failures per IP per window
	loginMaxUser = 20 // failures per username per window (from any IP)
)

func (l *loginLimiter) recent(k string, now time.Time) []time.Time {
	ts := l.fails[k]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > loginWindow {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, k)
	} else {
		l.fails[k] = ts
	}
	return ts
}

// blocked returns how long the caller must wait, or 0.
func (l *loginLimiter) blocked(ip, user string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil {
		return 0
	}
	now := time.Now()
	var wait time.Duration
	for _, c := range []struct {
		k   string
		max int
	}{{"ip:" + limiterIP(ip), loginMaxIP}, {"user:" + user, loginMaxUser}} {
		if ts := l.recent(c.k, now); len(ts) >= c.max {
			if w := loginWindow - now.Sub(ts[len(ts)-c.max]); w > wait {
				wait = w
			}
		}
	}
	return wait
}

func (l *loginLimiter) fail(ip, user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil {
		l.fails = map[string][]time.Time{}
	}
	if len(l.fails) > 100_000 {
		// bound memory under a distributed attack, but drop only the per-IP counters: wiping
		// everything would also reset the per-username lockout the attack is running into
		for k := range l.fails {
			if strings.HasPrefix(k, "ip:") {
				delete(l.fails, k)
			}
		}
	}
	now := time.Now()
	for _, k := range []string{"ip:" + limiterIP(ip), "user:" + user} {
		ts := append(l.recent(k, now), now)
		if len(ts) > loginMaxUser {
			ts = ts[len(ts)-loginMaxUser:]
		}
		l.fails[k] = ts
	}
}

func (l *loginLimiter) ok(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, "ip:"+limiterIP(ip))
}

// limiterIP is the key failed sign-ins are counted under: the address itself, or its /64 for
// IPv6 (one host usually holds a whole /64, so per-address counting is no limit at all).
func limiterIP(ip string) string {
	a := net.ParseIP(ip)
	if a == nil || a.To4() != nil {
		return ip
	}
	return (&net.IPNet{IP: a.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
}

func newTOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

// ---------- account endpoints ----------

func (m *Master) apiMe(w http.ResponseWriter, r *http.Request) {
	who := actor(r)
	var totp string
	m.db.QueryRow(`SELECT totp_secret FROM admins WHERE username=?`, who).Scan(&totp)
	out := map[string]any{"username": who, "totp": totp != "", "token": strings.HasPrefix(who, "token:")}
	if p := principalFrom(r); p != nil && !p.session {
		out["scopes"] = p.scopes
	} else {
		out["scopes"] = []string{ScopeAdmin}
	}
	writeJSON(w, 200, out)
}

func (m *Master) apiChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Old, New string }
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(req.New) < 8 {
		writeErr(w, 400, "The new password needs at least 8 characters")
		return
	}
	var hash string
	if err := m.db.QueryRow(`SELECT pass_hash FROM admins WHERE username=?`, actor(r)).Scan(&hash); err != nil ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Old)) != nil {
		writeErr(w, 400, "The current password is incorrect")
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	m.db.Exec(`UPDATE admins SET pass_hash=? WHERE username=?`, string(h), actor(r))
	m.renewOwnSession(w, r)
	m.audit(r, "admin.password", actor(r), "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (m *Master) apiTOTPSetup(w http.ResponseWriter, r *http.Request) {
	sec := newTOTPSecret()
	setSetting(m.db, "totp_pending:"+actor(r), sec)
	issuer := settingOr(m.db, "sub_title", "sbpanel")
	uri := "otpauth://totp/" + url.PathEscape(issuer+":"+actor(r)) + "?secret=" + sec + "&issuer=" + url.QueryEscape(issuer)
	writeJSON(w, 200, map[string]any{"secret": sec, "uri": uri, "qr": string(qrDataURI(uri))})
}

func (m *Master) apiTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var req struct{ Code string }
	readJSON(r, &req)
	sec, ok := getSetting(m.db, "totp_pending:"+actor(r))
	if !ok || !totpValid(sec, req.Code, time.Now()) {
		writeErr(w, 400, "Incorrect verification code")
		return
	}
	m.db.Exec(`UPDATE admins SET totp_secret=? WHERE username=?`, sec, actor(r))
	m.renewOwnSession(w, r)
	m.db.Exec(`DELETE FROM settings WHERE k=?`, "totp_pending:"+actor(r))
	m.audit(r, "admin.totp_enable", actor(r), "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (m *Master) apiTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	readJSON(r, &req)
	var hash string
	if err := m.db.QueryRow(`SELECT pass_hash FROM admins WHERE username=?`, actor(r)).Scan(&hash); err != nil ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		writeErr(w, 400, "Incorrect password")
		return
	}
	m.db.Exec(`UPDATE admins SET totp_secret='' WHERE username=?`, actor(r))
	m.renewOwnSession(w, r)
	m.audit(r, "admin.totp_disable", actor(r), "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (m *Master) apiAudit(w http.ResponseWriter, r *http.Request) {
	type row struct {
		TS     int64  `json:"ts"`
		Admin  string `json:"admin"`
		Action string `json:"action"`
		Target string `json:"target"`
		Detail string `json:"detail"`
		IP     string `json:"ip"`
	}
	out, err := scanRows(m, `SELECT ts, admin, action, target, detail, ip FROM audit_log ORDER BY id DESC LIMIT ?`, []any{qLimit(r, 200)},
		func(s func(...any) error) (row, error) {
			var x row
			return x, s(&x.TS, &x.Admin, &x.Action, &x.Target, &x.Detail, &x.IP)
		})
	if errf(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

// ResetAdmin sets a new password (creating the admin if needed) and clears 2FA and passkeys.
// RenameAdmin changes an admin's username (keeps password and 2FA).
func (m *Master) RenameAdmin(from, to string) error {
	res, err := m.db.Exec(`UPDATE admins SET username=? WHERE username=?`, to, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no admin named %q", from)
	}
	_, err = m.db.Exec(`UPDATE passkeys SET admin=? WHERE admin=?`, to, from)
	return err
}

func (m *Master) ResetAdmin(user, password string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`INSERT INTO admins(username, pass_hash, created_at) VALUES(?,?,?)
		ON CONFLICT(username) DO UPDATE SET pass_hash=excluded.pass_hash, totp_secret=''`, user, string(h), now())
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`DELETE FROM passkeys WHERE admin=?`, user)
	return err
}
