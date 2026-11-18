package master

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "sbm_session"

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeErr writes {"error": msg}. msg is English; it is translated to the caller's language
// (see i18n.go) when the catalog has it.
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": L(langOf(w), msg)})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	return dec.Decode(v)
}

func (m *Master) sign(payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// credGen fingerprints an admin's password hash and TOTP secret. Sessions carry it, so
// changing the password or two-factor setup (or renaming / deleting the admin) ends every
// existing session.
func (m *Master) credGen(user string) (string, bool) {
	var hash, totp string
	if m.db.QueryRow(`SELECT pass_hash, totp_secret FROM admins WHERE username=?`, user).Scan(&hash, &totp) != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(hash + "\x00" + totp))
	return hex.EncodeToString(sum[:8]), true
}

func (m *Master) newSession(user string) string {
	exp := time.Now().Add(7 * 24 * time.Hour).Unix()
	gen, _ := m.credGen(user)
	p := user + "|" + strconv.FormatInt(exp, 10) + "|" + gen
	return base64.RawURLEncoding.EncodeToString([]byte(p)) + "." + m.sign(p)
}

func (m *Master) checkSession(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || !hmac.Equal([]byte(m.sign(string(raw))), []byte(parts[1])) {
		return "", false
	}
	f := strings.SplitN(string(raw), "|", 3)
	if len(f) != 3 {
		return "", false
	}
	exp, _ := strconv.ParseInt(f[1], 10, 64)
	if exp < time.Now().Unix() {
		return "", false
	}
	if gen, ok := m.credGen(f[0]); !ok || !hmac.Equal([]byte(gen), []byte(f[2])) {
		return "", false
	}
	return f[0], true
}

// auth requires any authenticated caller. Scope checks, API keys, IP allowlists and rate limits
// live in guard() (apikeys.go); scopedMux applies it to every route via routeScopes (api_scopes.go).
func (m *Master) auth(h http.HandlerFunc) http.HandlerFunc { return m.guard(scopeAny, h) }

// routes registers the HTTP surface. Every /api and /mcp pattern needs an entry in routeScopes
// (api_scopes.go) and an operation in apiDocs (openapi.go); tests enforce both.
func (m *Master) routes() http.Handler {
	mux := m.newScopedMux()
	a := m.auth
	mux.HandleFunc("POST /api/auth/login", m.apiLogin)
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/auth/me", a(m.apiMe))
	mux.HandleFunc("POST /api/me/password", a(m.apiChangePassword))
	mux.HandleFunc("POST /api/me/totp/setup", a(m.apiTOTPSetup))
	mux.HandleFunc("POST /api/me/totp/enable", a(m.apiTOTPEnable))
	mux.HandleFunc("POST /api/me/totp/disable", a(m.apiTOTPDisable))
	mux.HandleFunc("GET /api/tokens", a(m.apiTokens))
	mux.HandleFunc("POST /api/tokens", a(m.apiCreateToken))
	mux.HandleFunc("DELETE /api/tokens/{id}", a(m.apiDeleteToken))
	mux.HandleFunc("GET /api/scopes", a(m.apiScopes))
	mux.HandleFunc("GET /api/openapi.json", a(m.apiOpenAPI))
	mux.HandleFunc("POST /mcp", m.mcpHandler(mux))
	mux.HandleFunc("GET /mcp", m.mcpNoStream)
	mux.HandleFunc("DELETE /mcp", m.mcpNoStream)
	mux.HandleFunc("GET /api/audit", a(m.apiAudit))
	mux.HandleFunc("GET /api/settings", a(m.apiSettings))
	mux.HandleFunc("PUT /api/settings", a(m.apiSaveSettings))
	mux.HandleFunc("GET /api/backup", a(m.apiBackup))
	mux.HandleFunc("POST /api/notify/test", a(m.apiNotifyTest))
	mux.HandleFunc("POST /api/import/3xui", a(m.apiImport3XUI))
	mux.HandleFunc("GET /api/update", a(m.apiUpdate))
	mux.HandleFunc("POST /api/update/check", a(m.apiUpdateCheck))
	mux.HandleFunc("POST /api/update/master", a(m.apiUpdateMaster))
	mux.HandleFunc("POST /api/update/nodes", a(m.apiUpdateNodes))

	mux.HandleFunc("GET /api/overview", a(m.apiOverview))
	mux.HandleFunc("GET /api/traffic/series", a(m.apiSeries))
	mux.HandleFunc("GET /api/traffic/top", a(m.apiTop))
	mux.HandleFunc("GET /api/events", a(m.apiEvents))
	mux.HandleFunc("GET /api/online", a(m.apiOnline))

	mux.HandleFunc("GET /api/nodes", a(m.apiNodes))
	mux.HandleFunc("POST /api/nodes", a(m.apiCreateNode))
	mux.HandleFunc("PATCH /api/nodes/{id}", a(m.apiUpdateNode))
	mux.HandleFunc("DELETE /api/nodes/{id}", a(m.apiDeleteNode))
	mux.HandleFunc("POST /api/nodes/{id}/token", a(m.apiNodeToken))
	mux.HandleFunc("GET /api/nodes/{id}/spec", a(m.apiNodeSpec))
	mux.HandleFunc("GET /api/nodes/{id}/conns", a(m.apiNodeConns))
	mux.HandleFunc("GET /api/nodes/{id}/metrics", a(m.apiNodeMetrics))
	mux.HandleFunc("GET /api/nodes/{id}/route", a(m.apiNodeRoute))
	mux.HandleFunc("PUT /api/nodes/{id}/route", a(m.apiSetNodeRoute))
	mux.HandleFunc("DELETE /api/nodes/{id}/route", a(m.apiResetNodeRoute))
	mux.HandleFunc("POST /api/nodes/{id}/conns/close", a(m.apiCloseConns))

	mux.HandleFunc("GET /api/inbounds", a(m.apiInbounds))
	mux.HandleFunc("POST /api/inbounds", a(m.apiCreateInbound))
	mux.HandleFunc("PATCH /api/inbounds/{id}", a(m.apiUpdateInbound))
	mux.HandleFunc("DELETE /api/inbounds/{id}", a(m.apiDeleteInbound))
	mux.HandleFunc("POST /api/tools/reality-keypair", a(m.apiRealityKeypair))
	mux.HandleFunc("GET /api/tools/dns-check", a(m.apiDNSCheck))

	mux.HandleFunc("GET /api/groups", a(m.apiGroups))
	mux.HandleFunc("POST /api/groups", a(m.apiCreateGroup))
	mux.HandleFunc("PATCH /api/groups/{id}", a(m.apiUpdateGroup))
	mux.HandleFunc("DELETE /api/groups/{id}", a(m.apiDeleteGroup))

	mux.HandleFunc("GET /api/hosts", a(m.apiHosts))
	mux.HandleFunc("POST /api/hosts", a(m.apiSaveHost))
	mux.HandleFunc("PUT /api/hosts/{id}", a(m.apiSaveHost))
	mux.HandleFunc("DELETE /api/hosts/{id}", a(m.apiDeleteHost))

	mux.HandleFunc("GET /api/templates", a(m.apiTemplates))
	mux.HandleFunc("POST /api/templates", a(m.apiCreateTemplate))
	mux.HandleFunc("PATCH /api/templates/{id}", a(m.apiSaveTemplateDraft))
	mux.HandleFunc("POST /api/templates/{id}/apply", a(m.apiApplyTemplate))
	mux.HandleFunc("DELETE /api/templates/{id}", a(m.apiDeleteTemplate))

	mux.HandleFunc("GET /api/routing/catalog", a(m.apiRoutingCatalog))
	mux.HandleFunc("GET /api/routing/nodes", a(m.apiRoutingNodes))
	mux.HandleFunc("GET /api/routing/profiles", a(m.apiProfiles))
	mux.HandleFunc("POST /api/routing/profiles", a(m.apiCreateProfile))
	mux.HandleFunc("GET /api/routing/profiles/{id}", a(m.apiProfile))
	mux.HandleFunc("PATCH /api/routing/profiles/{id}", a(m.apiUpdateProfile))
	mux.HandleFunc("DELETE /api/routing/profiles/{id}", a(m.apiDeleteProfile))
	mux.HandleFunc("PUT /api/routing/profiles/{id}/draft", a(m.apiSaveProfileDraft))
	mux.HandleFunc("DELETE /api/routing/profiles/{id}/draft", a(m.apiDiscardProfileDraft))
	mux.HandleFunc("POST /api/routing/profiles/{id}/preview", a(m.apiPreviewProfile))
	mux.HandleFunc("POST /api/routing/profiles/{id}/publish", a(m.apiPublishProfile))
	mux.HandleFunc("GET /api/routing/profiles/{id}/history", a(m.apiProfileHistory))
	mux.HandleFunc("POST /api/routing/profiles/{id}/history/{hid}/restore", a(m.apiRestoreProfile))

	mux.HandleFunc("GET /api/users", a(m.apiUsers))
	mux.HandleFunc("POST /api/users", a(m.apiCreateUser))
	mux.HandleFunc("POST /api/users/bulk", a(m.apiBulkUsers))
	mux.HandleFunc("POST /api/users/batch", a(m.apiBatchUsers))
	mux.HandleFunc("GET /api/users/{id}", a(m.apiUser))
	mux.HandleFunc("GET /api/users/{id}/sub", a(m.apiUserSub))
	mux.HandleFunc("PATCH /api/users/{id}", a(m.apiUpdateUser))
	mux.HandleFunc("DELETE /api/users/{id}", a(m.apiDeleteUser))
	mux.HandleFunc("POST /api/users/{id}/kick", a(m.apiKickUser))
	mux.HandleFunc("PUT /api/users/{id}/limits/{rule}", a(m.apiSetUserLimit))

	mux.HandleFunc("GET /api/limits", a(m.apiLimits))
	mux.HandleFunc("POST /api/limits", a(m.apiCreateLimit))
	mux.HandleFunc("PATCH /api/limits/{id}", a(m.apiUpdateLimit))
	mux.HandleFunc("GET /api/limits/{id}/users", a(m.apiLimitUsers))
	mux.HandleFunc("DELETE /api/limits/{id}", a(m.apiDeleteLimit))

	// subscriptions (public, token in path); /sub/ keeps 3x-ui links working
	mux.HandleFunc("GET /s/{token}", m.handleSub)
	mux.HandleFunc("GET /sub/{token}", m.handleSub)
	mux.HandleFunc("GET /s/_t/{ref}/{path...}", m.handleThemeAsset)

	// subscription-page themes (admin)
	mux.HandleFunc("GET /api/sub-themes", a(m.apiSubThemes))
	mux.HandleFunc("POST /api/sub-themes", a(m.apiUploadSubTheme))
	mux.HandleFunc("PUT /api/sub-themes/active", a(m.apiActivateSubTheme))
	mux.HandleFunc("PUT /api/sub-themes/{id}/settings", a(m.apiSaveSubThemeSettings))
	mux.HandleFunc("DELETE /api/sub-themes/{id}", a(m.apiDeleteSubTheme))
	mux.HandleFunc("GET /api/sub-themes/{id}/preview", a(m.apiPreviewSubTheme))

	m.certRoutes(mux, a) // domains, certificates, DNS, connectivity probe (certroutes.go)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, 404, "not found") })
	if m.cfg.Web != nil {
		mux.Handle("/", spaHandler(m.cfg.Web, m.uiBootScript))
	}
	return securityHeaders(withLang(rejectOpaqueOrigin(mux)))
}

// rejectOpaqueOrigin refuses state-changing /api calls from an opaque origin ("Origin: null"), i.e.
// from sandboxed documents such as subscription-page themes. Defense in depth on top of SameSite.
func rejectOpaqueOrigin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("Origin") == "null" && strings.HasPrefix(r.URL.Path, "/api/") {
				writeErr(w, http.StatusForbidden, "forbidden origin")
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// uiBootScript is injected into index.html so the server-wide appearance default applies before
// first paint (the inline boot script in index.html reads window.__SBP_UI).
func (m *Master) uiBootScript() string {
	v, err := validateAppearance(settingOr(m.db, "ui_appearance", ""))
	if err != nil || v == "" {
		v = "{}"
	}
	return "<script>window.__SBP_UI=" + v + "</script>"
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

// spaHandler serves built assets and falls back to index.html for client routes.
func spaHandler(fsys fs.FS, inject func() string) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := fsys.Open(p); err == nil {
				f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			http.Error(w, "web ui not built", 404)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if inject != nil {
			b = bytes.Replace(b, []byte("<!--sbp-ui-->"), []byte(inject()), 1)
		}
		w.Write(b)
	})
}

func (m *Master) apiLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password, Code string }
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	req.Username = truncate(req.Username, 64)
	ip := clientIP(r)
	if wait := m.logins.blocked(ip, req.Username); wait > 0 {
		writeErr(w, 429, T(langOf(w), "Too many failed sign-ins; try again in %d minutes", int(wait.Minutes())+1))
		return
	}
	var hash, totp string
	err := m.db.QueryRow(`SELECT pass_hash, totp_secret FROM admins WHERE username=?`, req.Username).Scan(&hash, &totp)
	if err != nil {
		hash = dummyHash // same bcrypt cost whether or not the user exists
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil || err != nil {
		m.logins.fail(ip, req.Username)
		time.Sleep(500 * time.Millisecond)
		m.db.Exec(`INSERT INTO audit_log(ts, admin, action, target, ip) VALUES(?,?,?,?,?)`, now(), req.Username, "login.failed", "", ip)
		writeErr(w, 401, "Wrong username or password")
		return
	}
	if totp != "" {
		if req.Code == "" {
			writeJSON(w, 401, map[string]any{"error": T(langOf(w), "Enter your two-factor code"), "need_totp": true})
			return
		}
		if !m.totpUse(req.Username, totp, req.Code, time.Now()) {
			m.logins.fail(ip, req.Username)
			time.Sleep(500 * time.Millisecond)
			m.db.Exec(`INSERT INTO audit_log(ts, admin, action, target, ip) VALUES(?,?,?,?,?)`, now(), req.Username, "login.failed_2fa", "", ip)
			writeJSON(w, 401, map[string]any{"error": T(langOf(w), "Incorrect two-factor code"), "need_totp": true})
			return
		}
	}
	m.logins.ok(ip)
	m.db.Exec(`INSERT INTO audit_log(ts, admin, action, target, ip) VALUES(?,?,?,?,?)`, now(), req.Username, "login", "", ip)
	setSetting(m.db, "admin_lang", reqLang(r)) // notifications follow the admin's UI language
	m.setSession(w, r, req.Username)
	writeJSON(w, 200, map[string]string{"username": req.Username})
}

func (m *Master) setSession(w http.ResponseWriter, r *http.Request, user string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: m.newSession(user), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge: 7 * 86400,
	})
}

// renewOwnSession keeps the admin who just changed their password / two-factor setup signed
// in on this device; their other sessions end (credGen changed).
func (m *Master) renewOwnSession(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(sessionCookie); err == nil {
		m.setSession(w, r, actor(r))
	}
}

func qInt(r *http.Request, k string, d int64) int64 {
	if v, err := strconv.ParseInt(r.URL.Query().Get(k), 10, 64); err == nil {
		return v
	}
	return d
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, errors.New("bad id")
	}
	return id, nil
}

func scanRows[T any](m *Master, q string, args []any, fn func(scan func(...any) error) (T, error)) ([]T, error) {
	rows, err := m.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := fn(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m *Master) apiEvents(w http.ResponseWriter, r *http.Request) {
	limit := qInt(r, "limit", 100)
	type ev struct {
		TS     int64  `json:"ts"`
		Kind   string `json:"kind"`
		UserID int64  `json:"user_id"`
		User   string `json:"user"`
		NodeID string `json:"node_id"`
		Node   string `json:"node"`
		Detail string `json:"detail"`
	}
	out, err := scanRows(m, `SELECT e.ts, e.kind, e.user_id, COALESCE(u.username,''), e.node_id, COALESCE(n.name,''), e.detail
		FROM events e LEFT JOIN users u ON u.id=e.user_id LEFT JOIN nodes n ON n.id=e.node_id
		ORDER BY e.id DESC LIMIT ?`, []any{limit}, func(s func(...any) error) (ev, error) {
		var e ev
		return e, s(&e.TS, &e.Kind, &e.UserID, &e.User, &e.NodeID, &e.Node, &e.Detail)
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	lang := reqLang(r)
	for i := range out {
		out[i].Detail = L(lang, out[i].Detail) // also maps legacy Chinese rows back to English
	}
	writeJSON(w, 200, out)
}

func errf(w http.ResponseWriter, err error) bool {
	if err != nil {
		writeErr(w, 500, err.Error())
		return true
	}
	return false
}

func fmtID(n int64) string { return fmt.Sprint(n) }
