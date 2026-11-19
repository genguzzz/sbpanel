package master

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type zf struct {
	name, body string
	mode       fs.FileMode
}

func makeZip(t *testing.T, files ...zf) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
		if f.mode != 0 {
			h.SetMode(f.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(f.body))
	}
	zw.Close()
	return buf.Bytes()
}

const testManifest = `{"api":1,"id":"neon","name":{"zh-CN":"霓虹"},"version":"1.2.0","author":"t","preview":"p.png",
 "settings":[{"type":"title","label":"x"},{"key":"accent","type":"color","default":"#ff0066"},
 {"key":"show","type":"switch","default":true},{"key":"mode","type":"select","default":"a","options":[{"value":"a","label":"A"},{"value":"b"}]},
 {"key":"clients","type":"multiselect","options":"$clients","default":["singbox"]},{"key":"footer","type":"text","maxLength":5}]}`

func neonZip(t *testing.T, prefix string) []byte {
	return makeZip(t,
		zf{name: prefix + "sbp-theme.json", body: testManifest},
		zf{name: prefix + "index.html", body: `<html><head><link rel="stylesheet" href="%SBP_ASSETS%/a.css"></head><body>NEON %SBP_DATA%</body></html>`},
		zf{name: prefix + "a.css", body: "body{}"},
		zf{name: prefix + "p.png", body: "png"},
		zf{name: prefix + "evil.html", body: "<script>1</script>"},
	)
}

func TestThemeZipValidation(t *testing.T) {
	good := neonZip(t, "")
	if _, _, err := inspectThemeZip(good); err != nil {
		t.Fatalf("valid zip rejected: %v", err)
	}
	if mf, _, err := inspectThemeZip(neonZip(t, "neon-1.2.0/")); err != nil || mf.ID != "neon" {
		t.Fatalf("single wrapping folder should be accepted: %v", err)
	}
	idx := zf{name: "index.html", body: "x"}
	cases := map[string][]byte{
		"not a zip":       []byte("hello"),
		"no manifest":     makeZip(t, idx),
		"zip slip":        makeZip(t, zf{name: "sbp-theme.json", body: testManifest}, idx, zf{name: "p.png"}, zf{name: "../../etc/x", body: "x"}),
		"absolute":        makeZip(t, zf{name: "sbp-theme.json", body: testManifest}, idx, zf{name: "p.png"}, zf{name: "/etc/x", body: "x"}),
		"backslash":       makeZip(t, zf{name: "sbp-theme.json", body: testManifest}, idx, zf{name: "p.png"}, zf{name: "a\\..\\..\\x", body: "x"}),
		"dotfile":         makeZip(t, zf{name: "sbp-theme.json", body: testManifest}, idx, zf{name: "p.png"}, zf{name: ".htaccess", body: "x"}),
		"symlink":         makeZip(t, zf{name: "sbp-theme.json", body: testManifest}, idx, zf{name: "p.png"}, zf{name: "l", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}),
		"reserved id":     makeZip(t, zf{name: "sbp-theme.json", body: strings.Replace(testManifest, `"neon"`, `"default"`, 1)}, idx, zf{name: "p.png"}),
		"bad id":          makeZip(t, zf{name: "sbp-theme.json", body: strings.Replace(testManifest, `"neon"`, `"../x"`, 1)}, idx, zf{name: "p.png"}),
		"missing entry":   makeZip(t, zf{name: "sbp-theme.json", body: testManifest}, zf{name: "p.png"}),
		"missing preview": makeZip(t, zf{name: "sbp-theme.json", body: testManifest}, idx),
		"bad api":         makeZip(t, zf{name: "sbp-theme.json", body: strings.Replace(testManifest, `"api":1`, `"api":9`, 1)}, idx, zf{name: "p.png"}),
		"bad setting":     makeZip(t, zf{name: "sbp-theme.json", body: strings.Replace(testManifest, `"type":"switch"`, `"type":"script"`, 1)}, idx, zf{name: "p.png"}),
	}
	for name, z := range cases {
		if _, _, err := inspectThemeZip(z); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var many []zf
	many = append(many, zf{name: "sbp-theme.json", body: testManifest}, idx, zf{name: "p.png"})
	for i := 0; i < themeMaxFiles; i++ {
		many = append(many, zf{name: "f/" + itoa(i), body: "x"})
	}
	if _, _, err := inspectThemeZip(makeZip(t, many...)); err == nil {
		t.Error("too many files accepted")
	}
}

func TestThemeSettingsCoercion(t *testing.T) {
	var mf themeManifest
	if err := json.Unmarshal([]byte(testManifest), &mf); err != nil {
		t.Fatal(err)
	}
	th := &subTheme{themeManifest: mf}
	m := newTestMaster(t)
	got := m.themeSettings(th, nil)
	if got["accent"] != "#ff0066" || got["show"] != true || got["mode"] != "a" || got["footer"] != "" {
		t.Fatalf("defaults: %#v", got)
	}
	if _, err := validateThemeSettings(th, map[string]any{"accent": "red"}); err == nil {
		t.Error("bad colour accepted")
	}
	if _, err := validateThemeSettings(th, map[string]any{"mode": "zzz"}); err == nil {
		t.Error("bad select accepted")
	}
	if _, err := validateThemeSettings(th, map[string]any{"clients": []any{"nope"}}); err == nil {
		t.Error("unknown client accepted")
	}
	if _, err := validateThemeSettings(th, map[string]any{"footer": "123456"}); err == nil {
		t.Error("maxLength ignored")
	}
	out, err := validateThemeSettings(th, map[string]any{"show": false, "unknown": 1, "clients": []any{"hiddify"}})
	if err != nil || out["show"] != false || out["unknown"] != nil {
		t.Fatalf("valid settings: %v %#v", err, out)
	}
}

func seedSubUser(t *testing.T, m *Master, name, status string, enabled bool) string {
	t.Helper()
	tok := "tok" + name
	_, err := m.db.Exec(`INSERT INTO users(username, sub_token, uuid, password, status, enabled, traffic_limit, reset_strategy, created_at, last_reset_at, used_up, used_down)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, name, tok, "11111111-1111-4111-8111-111111111111", "pw", status, enabled, 100<<30, "month", now()-86400*40, now()-86400*3, 1<<30, 2<<30)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func do(h http.Handler, method, target string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSubThemeLifecycle(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	tok := seedSubUser(t, m, "alice", "active", true)
	setSetting(m.db, "sub_announcement", "维护通知 </script><b>")

	// default theme renders, with sandbox CSP and the data embedded safely
	w := do(h, "GET", "/s/"+tok+"?format=html", nil, nil)
	if w.Code != 200 {
		t.Fatalf("html %d", w.Code)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("csp: %s", csp)
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("referrer policy")
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="sbp-data"`) || strings.Contains(body, "%SBP_") || !strings.Contains(body, "<script nonce=") {
		t.Fatal("placeholders not replaced or assets not inlined")
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-") || !strings.Contains(body, `nonce="`+strings.Split(strings.Split(csp, "'nonce-")[1], "'")[0]+`"`) {
		t.Fatal("inline scripts must carry the CSP nonce")
	}
	if strings.Count(body, "</script>") != strings.Count(body, "<script") {
		t.Fatal("announcement broke out of the data script")
	}

	// JSON contract
	w = do(h, "GET", "/s/"+tok+"?format=json", nil, nil)
	var d subPageData
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
		t.Fatalf("json %d %v", w.Code, err)
	}
	if d.API != 1 || d.User.Name != "alice" || d.User.Status != "active" || d.User.Used != 3<<30 || d.User.Reset.Strategy != "month" ||
		d.User.Reset.NextAt <= time.Now().Unix() || d.Subscription.QR == "" || len(d.Clients) != len(subClients) || d.Theme.ID != "default" {
		t.Fatalf("contract: %+v", d)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("json must be readable from the sandboxed page")
	}
	if strings.Contains(w.Body.String(), "11111111-1111") {
		t.Fatal("uuid leaked outside node uris")
	}

	// default theme asset
	w = do(h, "GET", "/s/_t/default@"+builtinTheme().Version+"/app.js", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") ||
		!strings.HasPrefix(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("asset %d %v", w.Code, w.Header())
	}
	for _, p := range []string{"/s/_t/default@1/../sub.go", "/s/_t/default@1/%2e%2e/x", "/s/_t/nope@1/app.js", "/s/_t/default@1/.hidden"} {
		if w := do(h, "GET", p, nil, nil); w.Code == 200 {
			t.Errorf("%s -> 200 %q", p, w.Body.String()[:min(40, w.Body.Len())])
		}
	}

	// upload (as an authenticated admin), activate, render
	cookie := sessionCookie + "=" + m.newSession("admin")
	var mb bytes.Buffer
	mw := multipart.NewWriter(&mb)
	fw, _ := mw.CreateFormFile("file", "neon.zip")
	fw.Write(neonZip(t, ""))
	mw.Close()
	auth := map[string]string{"Cookie": cookie, "Content-Type": mw.FormDataContentType()}
	if w = do(h, "POST", "/api/sub-themes", mb.Bytes(), auth); w.Code != 200 {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	js := map[string]string{"Cookie": cookie, "Content-Type": "application/json"}
	if w = do(h, "PUT", "/api/sub-themes/active", []byte(`{"id":"neon"}`), js); w.Code != 200 {
		t.Fatalf("activate %d %s", w.Code, w.Body)
	}
	if w = do(h, "PUT", "/api/sub-themes/neon/settings", []byte(`{"mode":"b","footer":"hi"}`), js); w.Code != 200 {
		t.Fatalf("settings %d %s", w.Code, w.Body)
	}
	if w = do(h, "PUT", "/api/sub-themes/neon/settings", []byte(`{"mode":"zz"}`), js); w.Code != 400 {
		t.Fatalf("bad settings %d", w.Code)
	}
	w = do(h, "GET", "/s/"+tok+"?format=html", nil, nil)
	if !strings.Contains(w.Body.String(), "NEON") || !strings.Contains(w.Body.String(), `"mode":"b"`) || !strings.Contains(w.Body.String(), "<style>body{}</style>") {
		t.Fatalf("custom theme not rendered: %s", w.Body)
	}
	// a theme .html asset opened directly is sandboxed too
	if w = do(h, "GET", "/s/_t/neon@1.2.0/evil.html", nil, nil); !strings.HasPrefix(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("theme html asset not sandboxed")
	}
	var list []themeInfo
	w = do(h, "GET", "/api/sub-themes", nil, js)
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 2 || !list[1].Active || list[1].PreviewURL == nil || *list[1].PreviewURL != "/s/_t/neon@1.2.0/p.png" || list[1].Settings["footer"] != "hi" {
		t.Fatalf("list: %+v", list)
	}
	if w = do(h, "GET", "/api/sub-themes/neon/preview?state=limited", nil, js); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatalf("preview %d", w.Code)
	}
	var uid int64
	m.db.QueryRow(`SELECT id FROM users WHERE sub_token=?`, tok).Scan(&uid)
	if w = do(h, "GET", "/api/sub-themes/neon/preview?user="+itoa(int(uid)), nil, js); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"alice"`) {
		t.Fatalf("preview as user %d", w.Code)
	}
	if w = do(h, "GET", "/api/sub-themes/neon/preview?user=99999", nil, js); w.Code != 404 {
		t.Fatalf("preview unknown user %d", w.Code)
	}

	// a broken active theme falls back to the built-in one
	os.Remove(filepath.Join(m.themesDir(), "neon", "index.html"))
	if w = do(h, "GET", "/s/"+tok+"?format=html", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"default"`) {
		t.Fatalf("fallback: %d", w.Code)
	}

	// delete the active theme: default becomes active, default cannot be deleted
	if w = do(h, "DELETE", "/api/sub-themes/default", nil, js); w.Code != 400 {
		t.Fatalf("delete default %d", w.Code)
	}
	if w = do(h, "DELETE", "/api/sub-themes/neon", nil, js); w.Code != 200 {
		t.Fatalf("delete %d", w.Code)
	}
	if m.activeThemeID() != "default" {
		t.Fatal("active theme not reset")
	}
	if _, err := os.Stat(filepath.Join(m.themesDir(), "neon")); !os.IsNotExist(err) {
		t.Fatal("theme dir left behind")
	}
}

func TestSubJSONInactiveUser(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	tok := seedSubUser(t, m, "bob", "limited", true)
	var d subPageData
	json.Unmarshal(do(h, "GET", "/s/"+tok+"?format=json", nil, nil).Body.Bytes(), &d)
	if d.User.Status != "limited" || len(d.Nodes) != 0 {
		t.Fatalf("%+v", d.User)
	}
}

func TestOpaqueOriginRejected(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	cookie := map[string]string{"Cookie": sessionCookie + "=" + m.newSession("admin"), "Content-Type": "application/json", "Origin": "null"}
	if w := do(h, "PUT", "/api/settings", []byte(`{"sub_title":"x"}`), cookie); w.Code != 403 {
		t.Fatalf("Origin: null write -> %d", w.Code)
	}
	if w := do(h, "POST", "/api/auth/login", []byte(`{}`), map[string]string{"Origin": "null"}); w.Code != 403 {
		t.Fatalf("Origin: null login -> %d", w.Code)
	}
	delete(cookie, "Origin")
	if w := do(h, "PUT", "/api/settings", []byte(`{"sub_title":"x"}`), cookie); w.Code != 200 {
		t.Fatalf("same-origin write -> %d %s", w.Code, w.Body)
	}
}

func TestSubSiteSettingsValidation(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	js := map[string]string{"Cookie": sessionCookie + "=" + m.newSession("admin"), "Content-Type": "application/json"}
	bad := []string{
		`{"sub_logo_url":"javascript:alert(1)"}`,
		`{"sub_support_links":"[{\"label\":\"x\",\"url\":\"javascript:alert(1)\"}]"}`,
		`{"sub_support_links":"not json"}`,
		`{"sub_clients":"singbox,evil"}`,
		`{"ui_appearance":"{\"palette\":\"hacker\"}"}`,
		`{"ui_appearance":"{\"accent\":{\"light\":{\"accent\":\"red\"},\"dark\":{}}}"}`,
	}
	for _, b := range bad {
		if w := do(h, "PUT", "/api/settings", []byte(b), js); w.Code != 400 {
			t.Errorf("%s -> %d", b, w.Code)
		}
	}
	good := `{"sub_support_links":"[{\"label\":\"群组\",\"url\":\"https://t.me/x\",\"icon\":\"telegram\"}]","sub_clients":"singbox,hiddify","ui_appearance":"{\"palette\":\"nord\",\"mode\":\"dark\"}"}`
	if w := do(h, "PUT", "/api/settings", []byte(good), js); w.Code != 200 {
		t.Fatalf("good -> %d %s", w.Code, w.Body)
	}
	if s := m.subSite(); len(s.Support) != 1 || s.Support[0].Label != "群组" {
		t.Fatalf("support: %+v", s.Support)
	}
	if cs := m.clientsFor("https://x/s/t", "u", m.allowedClients()); len(cs) != 2 {
		t.Fatalf("client allow-list: %d", len(cs))
	}
	// the appearance default is injected into index.html before first paint
	spa := spaHandler(fstest.MapFS{"index.html": {Data: []byte(`<head><!--sbp-ui--></head>`)}}, m.uiBootScript)
	w := do(spa, "GET", "/users", nil, nil)
	if !strings.Contains(w.Body.String(), `window.__SBP_UI={"mode":"dark","palette":"nord"}`) {
		t.Fatalf("boot injection: %s", w.Body)
	}
}

func TestGuessRegionAndReset(t *testing.T) {
	for in, want := range map[string]string{"🇯🇵 东京": "JP", "香港 HKT": "HK", "Los Angeles 01": "US", "备用": "", "法兰克福 TUIC": "DE"} {
		r := guessRegion(in)
		if (r == nil && want != "") || (r != nil && r.Code != want) {
			t.Errorf("%s -> %+v", in, r)
		}
	}
	loc := time.UTC
	created := time.Date(2026, 1, 31, 10, 0, 0, 0, loc)
	now := time.Date(2026, 2, 15, 12, 0, 0, 0, loc)
	cases := map[string]time.Time{
		"day": time.Date(2026, 2, 16, 0, 0, 0, 0, loc), "week": time.Date(2026, 2, 16, 0, 0, 0, 0, loc),
		"month": time.Date(2026, 3, 1, 0, 0, 0, 0, loc), "month_rolling": time.Date(2026, 2, 28, 0, 0, 0, 0, loc),
		"month:10": time.Date(2026, 3, 10, 0, 0, 0, 0, loc), "month:20": time.Date(2026, 2, 20, 0, 0, 0, 0, loc),
		"month:31": time.Date(2026, 2, 28, 0, 0, 0, 0, loc),
	}
	for s, want := range cases {
		if got := nextReset(s, created, now, loc); got != want.Unix() {
			t.Errorf("%s: %v want %v", s, time.Unix(got, 0).UTC(), want)
		}
	}
	if nextReset("none", created, now, loc) != 0 {
		t.Error("none")
	}
}

func TestInlineAssets(t *testing.T) {
	th := &subTheme{themeManifest: themeManifest{ID: "x", Version: "1"}, fsys: fstest.MapFS{
		"a.css":   {Data: []byte("body{color:red}")},
		"bg.css":  {Data: []byte("body{background:url(img/bg.png)}")},
		"evil.js": {Data: []byte("x='</script><script>alert(1)'")},
		"ok.js":   {Data: []byte("var a=1")},
	}}
	page := `<link rel="stylesheet" href="%SBP_ASSETS%/a.css"><link rel="stylesheet" href="%SBP_ASSETS%/bg.css">` +
		`<script src="%SBP_ASSETS%/ok.js" defer></script><script src="%SBP_ASSETS%/evil.js"></script><script src="%SBP_ASSETS%/../x.js"></script>`
	out := inlineAssets(th, page, "N")
	for _, want := range []string{"<style>body{color:red}</style>", `href="%SBP_ASSETS%/bg.css"`, `<script nonce="N">var a=1</script>`,
		`src="%SBP_ASSETS%/evil.js"`, `src="%SBP_ASSETS%/../x.js"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

func TestBuiltinThemeValid(t *testing.T) {
	bt := builtinTheme()
	if err := bt.validate(func(p string) bool { _, err := fs.Stat(bt.fsys, p); return err == nil }); err != nil {
		t.Fatal(err)
	}
	b, _ := fs.ReadFile(bt.fsys, bt.entry())
	if strings.Count(string(b), "%SBP_DATA%") != 1 {
		t.Fatal("entry must contain %SBP_DATA% exactly once")
	}
}

func TestThemePreviewImagesAndFraming(t *testing.T) {
	m := newTestMaster(t)
	h := m.routes()
	js := map[string]string{"Cookie": sessionCookie + "=" + m.newSession("admin"), "Content-Type": "application/json"}
	var list []themeInfo
	json.Unmarshal(do(h, "GET", "/api/sub-themes", nil, js).Body.Bytes(), &list)
	if len(list) == 0 {
		t.Fatal("no themes")
	}
	for _, ti := range list {
		if ti.PreviewURL == nil {
			t.Errorf("%s: no preview_url", ti.ID)
			continue
		}
		w := do(h, "GET", *ti.PreviewURL, nil, nil)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
			t.Errorf("%s preview %s -> %d %s", ti.ID, *ti.PreviewURL, w.Code, w.Header().Get("Content-Type"))
		}
	}
	// the preview page may be framed by the panel itself, and stays sandboxed
	w := do(h, "GET", "/api/sub-themes/default/preview?state=active", nil, js)
	csp := w.Header().Get("Content-Security-Policy")
	if w.Code != 200 || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(csp, "frame-ancestors 'self'") || !strings.HasPrefix(csp, "sandbox ") {
		t.Fatalf("preview headers: %d %v", w.Code, w.Header())
	}
	// everything else keeps DENY
	for _, p := range []string{"/api/sub-themes", "/api/settings"} {
		if w := do(h, "GET", p, nil, js); w.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s X-Frame-Options = %q", p, w.Header().Get("X-Frame-Options"))
		}
	}
	tok := seedSubUser(t, m, "carol", "active", true)
	w = do(h, "GET", "/s/"+tok+"?format=html", nil, nil)
	if w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("public page must not be frameable: %v", w.Header())
	}
}
