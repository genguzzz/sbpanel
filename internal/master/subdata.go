package master

// The public data contract for subscription pages (API v1): GET /s/{token}?format=json, also
// embedded in themed HTML as <script id="sbp-data" type="application/json">. Documented in
// docs/SUB-THEMES.md. Secrets only ever appear inside node "uri" strings.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type subClient struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Platforms []string `json:"platforms"`
	URL       string   `json:"url"`
	Download  string   `json:"download"`
	link      func(sub, name string) string
}

var subClients = []subClient{
	{ID: "singbox", Name: "sing-box", Platforms: []string{"ios", "android", "macos", "windows", "linux"}, Download: "https://sing-box.sagernet.org/clients/",
		link: func(s, n string) string {
			return "sing-box://import-remote-profile?url=" + url.QueryEscape(addQuery(s, "format=singbox")) + "#" + url.PathEscape(n)
		}},
	{ID: "mihomo", Name: "Clash Verge / mihomo", Platforms: []string{"windows", "macos", "linux", "android"}, Download: "https://github.com/clash-verge-rev/clash-verge-rev/releases",
		link: func(s, n string) string {
			return "clash://install-config?url=" + url.QueryEscape(addQuery(s, "format=mihomo")) + "&name=" + url.QueryEscape(n)
		}},
	{ID: "surge", Name: "Surge", Platforms: []string{"ios", "macos"}, Download: "https://apps.apple.com/app/id1442620678",
		link: func(s, n string) string {
			return "surge:///install-config?url=" + url.QueryEscape(addQuery(s, "format=surge"))
		}},
	{ID: "shadowrocket", Name: "Shadowrocket", Platforms: []string{"ios"}, Download: "https://apps.apple.com/app/id932747118",
		link: func(s, n string) string {
			return "shadowrocket://add/sub://" + base64.RawURLEncoding.EncodeToString([]byte(addQuery(s, "format=shadowrocket"))) + "?remark=" + url.QueryEscape(n)
		}},
	{ID: "hiddify", Name: "Hiddify", Platforms: []string{"ios", "android", "windows", "macos", "linux"}, Download: "https://github.com/hiddify/hiddify-app/releases",
		link: func(s, n string) string { return "hiddify://import/" + s + "#" + url.PathEscape(n) }},
	{ID: "stash", Name: "Stash", Platforms: []string{"ios", "macos"}, Download: "https://apps.apple.com/app/id1596063349",
		link: func(s, n string) string {
			return "stash://install-config?url=" + url.QueryEscape(addQuery(s, "format=mihomo")) + "&name=" + url.QueryEscape(n)
		}},
	{ID: "v2rayng", Name: "v2rayNG", Platforms: []string{"android"}, Download: "https://github.com/2dust/v2rayNG/releases",
		link: func(s, n string) string { return "v2rayng://install-config?url=" + url.QueryEscape(s) }},
	{ID: "nekobox", Name: "NekoBox", Platforms: []string{"android"}, Download: "https://github.com/MatsuriDayo/NekoBoxForAndroid/releases",
		link: func(s, n string) string {
			return "sn://subscription?url=" + url.QueryEscape(s) + "&name=" + url.QueryEscape(n)
		}},
}

type subSupportLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Icon  string `json:"icon,omitempty"`
}

type subRegion struct {
	Code string `json:"code"`
	Flag string `json:"flag"`
}

type subNodeJSON struct {
	ID        int        `json:"id"`
	Name      string     `json:"name"`
	Protocol  string     `json:"protocol"`
	Transport string     `json:"transport"`
	Security  string     `json:"security"`
	Flow      string     `json:"flow,omitempty"`
	Address   string     `json:"address"`
	Port      int        `json:"port"`
	SNI       string     `json:"sni,omitempty"`
	Region    *subRegion `json:"region"`
	Paused    bool       `json:"paused,omitempty"` // a traffic limit is used up: refused until it resets
	URI       string     `json:"uri"`
	QR        string     `json:"qr"`
}

// subUsage is this cycle's usage of one line (an inbound, however many hosts reach it).
type subUsage struct {
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	Up        int64  `json:"up"`
	Down      int64  `json:"down"`
	Used      int64  `json:"used"`
	Exhausted bool   `json:"exhausted"` // refused by the node until its traffic limit resets
}

// subLimit is a traffic limit covering some of the user's lines.
type subLimit struct {
	Name      string   `json:"name"`
	Used      int64    `json:"used"`
	Total     int64    `json:"total"` // 0 = no limit for this user
	Exhausted bool     `json:"exhausted"`
	NextAt    int64    `json:"next_at"` // when it resets (0 = never)
	Lines     []string `json:"lines"`   // usage[].name entries it covers
}

type subReset struct {
	Strategy string `json:"strategy"`
	LastAt   int64  `json:"last_at"`
	NextAt   int64  `json:"next_at"`
}

type subUserJSON struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Enabled     bool     `json:"enabled"`
	Up          int64    `json:"up"`
	Down        int64    `json:"down"`
	Used        int64    `json:"used"`
	Total       int64    `json:"total"`
	ExpireAt    int64    `json:"expire_at"`
	DaysLeft    *int     `json:"days_left"`
	DeviceLimit int      `json:"device_limit"`
	Reset       subReset `json:"reset"`
}

type subSite struct {
	Title        string           `json:"title"`
	Logo         string           `json:"logo"`
	Announcement string           `json:"announcement"`
	Support      []subSupportLink `json:"support"`
}

type subSubscription struct {
	URL         string            `json:"url"`
	QR          string            `json:"qr"`
	UpdateHours int               `json:"update_hours"`
	Formats     map[string]string `json:"formats"`
}

type subThemeRef struct {
	ID       string         `json:"id"`
	Version  string         `json:"version"`
	Settings map[string]any `json:"settings"`
}

type subPageData struct {
	API          int             `json:"api"`
	GeneratedAt  int64           `json:"generated_at"`
	Site         subSite         `json:"site"`
	User         subUserJSON     `json:"user"`
	Subscription subSubscription `json:"subscription"`
	Clients      []subClient     `json:"clients"`
	Nodes        []subNodeJSON   `json:"nodes"`
	Usage        []subUsage      `json:"usage"`
	Limits       []subLimit      `json:"limits"`
	Theme        subThemeRef     `json:"theme"`
	// routing profiles the user may pick; selected_profile is the one in effect
	Profiles        []subProfileOption `json:"profiles"`
	SelectedProfile string             `json:"selected_profile"`
	// the visitor's language (see subLang): "en" or "zh", and the matching BCP 47 tag
	Lang   string `json:"lang"`
	Locale string `json:"locale"`
}

// subLang is the subscription page's language: ?lang=en|zh (the theme's switcher; the page is
// sandboxed, so the choice lives in the URL), else the browser's Accept-Language, else English.
func subLang(r *http.Request) string {
	if v := r.URL.Query().Get("lang"); v != "" {
		return langFromTag(v)
	}
	return acceptLang(r.Header.Get("Accept-Language"))
}

// nextReset is when the current usage period ends (0 for "none").
func nextReset(strategy string, created time.Time, t time.Time, loc *time.Location) int64 {
	start, ok := periodStart(strategy, created, t, loc)
	if !ok {
		return 0
	}
	switch strategy {
	case "day":
		return start.AddDate(0, 0, 1).Unix()
	case "week":
		return start.AddDate(0, 0, 7).Unix()
	case "month":
		return start.AddDate(0, 1, 0).Unix()
	case "month_rolling":
		nm := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
		return monthDay(nm.Year(), nm.Month(), created.In(loc).Day(), loc).Unix()
	}
	return 0
}

func (u subUser) statusKey() string {
	if !u.Enabled {
		return "disabled"
	}
	switch u.Status {
	case "limited", "expired":
		return u.Status
	}
	return "active"
}

var regionWords = []struct {
	code  string
	words []string
}{
	{"HK", []string{"香港", "hong kong", "hongkong", "hkg"}},
	{"TW", []string{"台湾", "臺灣", "台北", "taiwan", "taipei"}},
	{"JP", []string{"日本", "东京", "東京", "大阪", "japan", "tokyo", "osaka"}},
	{"KR", []string{"韩国", "韓國", "首尔", "korea", "seoul"}},
	{"SG", []string{"新加坡", "狮城", "singapore"}},
	{"US", []string{"美国", "美國", "洛杉矶", "圣何塞", "西雅图", "纽约", "硅谷", "united states", "los angeles", "san jose", "seattle", "new york", "usa"}},
	{"GB", []string{"英国", "伦敦", "united kingdom", "london", " uk"}},
	{"DE", []string{"德国", "法兰克福", "germany", "frankfurt"}},
	{"FR", []string{"法国", "巴黎", "france", "paris"}},
	{"NL", []string{"荷兰", "阿姆斯特丹", "netherlands", "amsterdam"}},
	{"CA", []string{"加拿大", "canada", "toronto"}},
	{"AU", []string{"澳大利亚", "澳洲", "悉尼", "australia", "sydney"}},
	{"RU", []string{"俄罗斯", "莫斯科", "russia", "moscow"}},
	{"IN", []string{"印度", "india", "mumbai"}},
	{"TR", []string{"土耳其", "turkey", "istanbul"}},
	{"MY", []string{"马来西亚", "malaysia"}},
	{"TH", []string{"泰国", "thailand", "bangkok"}},
	{"VN", []string{"越南", "vietnam"}},
	{"PH", []string{"菲律宾", "philippines"}},
	{"CN", []string{"中国", "上海", "北京", "广州", "深圳", "china", "shanghai"}},
}

func flagOf(code string) string {
	if len(code) != 2 {
		return ""
	}
	if code == "TW" {
		return "🏝️" // iOS with a mainland-China region renders 🇹🇼 as a blank box
	}
	r := []rune{rune(0x1F1E6 + int(code[0]-'A')), rune(0x1F1E6 + int(code[1]-'A'))}
	return string(r)
}

// guessRegion reads a leading flag emoji or a city/country keyword from the remark.
func guessRegion(remark string) *subRegion {
	rs := []rune(remark)
	for i := 0; i+1 < len(rs); i++ {
		a, b := rs[i], rs[i+1]
		if a >= 0x1F1E6 && a <= 0x1F1FF && b >= 0x1F1E6 && b <= 0x1F1FF {
			code := string([]rune{'A' + (a - 0x1F1E6), 'A' + (b - 0x1F1E6)})
			return &subRegion{code, string([]rune{a, b})}
		}
	}
	low := " " + strings.ToLower(remark)
	for _, rw := range regionWords {
		for _, w := range rw.words {
			if strings.Contains(low, w) {
				return &subRegion{rw.code, flagOf(rw.code)}
			}
		}
	}
	return nil
}

func (e subEntry) nodeJSON(i int, qr string) subNodeJSON {
	transport := e.Network
	security := e.Security
	switch e.Type {
	case "hysteria2", "hysteria", "tuic":
		transport, security = "quic", "tls"
	}
	if transport == "" {
		transport = "tcp"
	}
	if security == "" {
		security = "none"
	}
	return subNodeJSON{ID: i, Name: e.Remark, Protocol: e.Type, Transport: transport, Security: security, Flow: e.Flow,
		Address: e.Address, Port: e.Port, SNI: e.SNI, Region: entryRegion(e), URI: e.uri(), QR: qr}
}

func (m *Master) subSite() subSite {
	s := subSite{
		Title:        settingOr(m.db, "sub_title", "sbpanel"),
		Logo:         settingOr(m.db, "sub_logo_url", ""),
		Announcement: settingOr(m.db, "sub_announcement", ""),
		Support:      []subSupportLink{},
	}
	if v, ok := getSetting(m.db, "sub_support_links"); ok && v != "" {
		json.Unmarshal([]byte(v), &s.Support)
	}
	return s
}

func (m *Master) clientsFor(link, name string, allow map[string]bool) []subClient {
	out := []subClient{}
	for _, c := range subClients {
		if allow != nil && !allow[c.ID] {
			continue
		}
		c.URL = c.link(link, name)
		out = append(out, c)
	}
	return out
}

func (m *Master) allowedClients() map[string]bool {
	v := settingOr(m.db, "sub_clients", "")
	if v == "" {
		return nil
	}
	out := map[string]bool{}
	for _, id := range strings.Split(v, ",") {
		out[strings.TrimSpace(id)] = true
	}
	return out
}

// addQuery appends a query parameter to a URL that may already carry one (?profile=).
func addQuery(u, kv string) string {
	if strings.Contains(u, "?") {
		return u + "&" + kv
	}
	return u + "?" + kv
}

func subFormats(link string) map[string]string {
	out := map[string]string{"auto": link}
	for _, f := range []string{"base64", "singbox", "mihomo", "surge", "shadowrocket", "links", "json"} {
		out[f] = addQuery(link, "format="+f)
	}
	return out
}

// subProfileOption is one entry of the public 配置模板 dropdown, with every link
// pre-rendered (themes run sandboxed and can't draw QR codes themselves).
type subProfileOption struct {
	subProfileJSON
	URL     string            `json:"url"`
	QR      string            `json:"qr"`
	Formats map[string]string `json:"formats"`
	Clients map[string]string `json:"clients"` // client id -> import deep link
}

// profileOptions renders the dropdown for a user; base is the plain subscription
// link. The assigned profile uses the plain link, others add ?profile=<slug>.
func (m *Master) profileOptions(u subUser, base, req string) ([]subProfileOption, string, string) {
	ps, selected := m.selectableProfiles(u, req)
	assigned, _ := m.assignedProfile(u.ID)
	link := base
	out := []subProfileOption{}
	for _, p := range ps {
		l := base
		if p.Slug != assigned.Slug {
			l = addQuery(base, "profile="+url.QueryEscape(p.Slug))
		}
		if p.Slug == selected {
			link = l
		}
		o := subProfileOption{subProfileJSON: p, URL: l, Formats: subFormats(l), Clients: map[string]string{}}
		if len(ps) > 1 {
			o.QR = string(qrDataURI(l))
		}
		for _, c := range m.clientsFor(l, u.Username, m.allowedClients()) {
			o.Clients[c.ID] = c.URL
		}
		out = append(out, o)
	}
	return out, selected, link
}

func (m *Master) subData(r *http.Request, u subUser, es []subEntry, t *subTheme) subPageData {
	profiles, selected, link := m.profileOptions(u, m.subURL(r, u.Token), r.URL.Query().Get("profile"))
	hours, _ := strconv.Atoi(settingOr(m.db, "sub_update_hours", "12"))
	uj := subUserJSON{Name: u.Username, Status: u.statusKey(), Enabled: u.Enabled, Up: u.Up, Down: u.Down, Used: u.Up + u.Down,
		Total: u.Limit, ExpireAt: u.ExpireAt, DeviceLimit: u.DeviceLimit,
		Reset: subReset{Strategy: nz(u.ResetStrategy, "none"), LastAt: u.LastResetAt}}
	if u.ExpireAt > 0 {
		d := int((u.ExpireAt - time.Now().Unix() + 86399) / 86400)
		uj.DaysLeft = &d
	}
	uj.Reset.NextAt = nextReset(u.ResetStrategy, time.Unix(u.CreatedAt, 0), time.Now(), m.loc)
	nodes := []subNodeJSON{}
	for i, e := range es {
		n := e.nodeJSON(i, string(qrDataURI(e.uri())))
		n.Paused = u.Blocked[e.Inbound]
		nodes = append(nodes, n)
	}
	usage, limits := m.subUsage(u, es, uj.Reset.NextAt)
	return subPageData{
		API: themeAPIVersion, GeneratedAt: time.Now().Unix(), Site: m.subSite(), User: uj,
		Subscription: subSubscription{URL: link, QR: string(qrDataURI(link)), UpdateHours: hours, Formats: subFormats(link)},
		Clients:      m.clientsFor(link, u.Username, m.allowedClients()), Nodes: nodes, Usage: usage, Limits: limits,
		Theme:    subThemeRef{ID: t.ID, Version: t.Version, Settings: m.themeSettings(t, nil)},
		Profiles: profiles, SelectedProfile: selected,
		Lang: subLang(r), Locale: contentLanguage(subLang(r)),
	}.localizeProfiles()
}

// localizeProfiles shows untouched built-in profile names in the page language (presetText).
func (d subPageData) localizeProfiles() subPageData {
	for i := range d.Profiles {
		d.Profiles[i].Name, d.Profiles[i].Description = presetText(d.Lang, d.Profiles[i].Name), presetText(d.Lang, d.Profiles[i].Description)
	}
	return d
}

// previewData is a synthetic subscription for the admin theme preview (no real token or user).
func (m *Master) previewData(r *http.Request, t *subTheme, override map[string]any, state string) subPageData {
	now := time.Now()
	u := subUser{Username: "demo", Token: "preview", UUID: "00000000-0000-4000-8000-000000000000", Password: "preview-password",
		Status: "active", Enabled: true, Up: 3 << 30, Down: 54 << 30, Limit: 100 << 30, ExpireAt: now.Add(23 * 24 * time.Hour).Unix(),
		ResetStrategy: "month", CreatedAt: now.AddDate(0, -3, 0).Unix(), LastResetAt: now.AddDate(0, 0, -7).Unix(), DeviceLimit: 3}
	switch state {
	case "limited":
		u.Status, u.Up, u.Down = "limited", 0, 100<<30
	case "expired":
		u.Status, u.ExpireAt = "expired", now.Add(-24*time.Hour).Unix()
	}
	es := []subEntry{
		{Remark: "🇺🇸 Los Angeles CN2", Type: "vless", Address: "la.example.com", Port: 443, UUID: u.UUID, Flow: "xtls-rprx-vision", Security: "reality", SNI: "www.microsoft.com", FP: "chrome", PBK: "preview", Network: "tcp"},
		{Remark: "Tokyo IIJ", Type: "vmess", Address: "tyo.example.com", Port: 443, UUID: u.UUID, Security: "tls", SNI: "tyo.example.com", Network: "ws", Path: "/ws"},
		{Remark: "Hong Kong HKT", Type: "trojan", Address: "hk.example.com", Port: 8443, Password: u.Password, Security: "tls", SNI: "hk.example.com", Network: "grpc", Service: "grpc"},
		{Remark: "Singapore Hysteria2", Type: "hysteria2", Address: "sg.example.com", Port: 20443, Password: u.Password, Security: "tls", SNI: "sg.example.com"},
	}
	if u.Status != "active" {
		es = nil
	}
	link := "https://sub.example.com/s/preview"
	d := subPageData{API: themeAPIVersion, GeneratedAt: now.Unix(), Site: m.subSite(),
		User: subUserJSON{Name: u.Username, Status: u.statusKey(), Enabled: true, Up: u.Up, Down: u.Down, Used: u.Up + u.Down, Total: u.Limit,
			ExpireAt: u.ExpireAt, DeviceLimit: u.DeviceLimit, Reset: subReset{Strategy: u.ResetStrategy, LastAt: u.LastResetAt,
				NextAt: nextReset(u.ResetStrategy, time.Unix(u.CreatedAt, 0), now, m.loc)}},
		Subscription: subSubscription{URL: link, QR: string(qrDataURI(link)), UpdateHours: 12, Formats: subFormats(link)},
		Clients:      m.clientsFor(link, u.Username, m.allowedClients()), Nodes: []subNodeJSON{},
		Theme: subThemeRef{ID: t.ID, Version: t.Version, Settings: m.themeSettings(t, override)},
		Lang:  subLang(r), Locale: contentLanguage(subLang(r))}
	d.Profiles, d.SelectedProfile, _ = m.profileOptions(subUser{ID: -1, Username: u.Username}, link, "")
	d = d.localizeProfiles()
	if u.ExpireAt > 0 {
		dl := int((u.ExpireAt - now.Unix() + 86399) / 86400)
		d.User.DaysLeft = &dl
	}
	for i, e := range es {
		d.Nodes = append(d.Nodes, e.nodeJSON(i, string(qrDataURI(e.uri()))))
	}
	d.Usage, d.Limits = []subUsage{}, []subLimit{}
	if es != nil {
		d.Usage = []subUsage{
			{Name: "🇺🇸 Los Angeles CN2", Protocol: "vless", Up: 2 << 30, Down: 31 << 30, Used: 33 << 30},
			{Name: "Tokyo IIJ", Protocol: "vmess", Up: 1 << 29, Down: 15 << 30, Used: 31 << 29},
			{Name: "Hong Kong HKT", Protocol: "trojan", Up: 1 << 29, Down: 8 << 30, Used: 17 << 29},
			{Name: "Singapore Hysteria2", Protocol: "hysteria2", Used: 0},
		}
		d.Limits = []subLimit{{Name: "Tokyo home line", Used: 31 << 29, Total: 50 << 30, NextAt: d.User.Reset.NextAt, Lines: []string{"Tokyo IIJ"}}}
	}
	return d
}

// ---------- site settings validation (sub page) ----------

func validateSupportLinks(v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", nil
	}
	var ls []subSupportLink
	if err := json.Unmarshal([]byte(v), &ls); err != nil {
		return "", fmt.Errorf("Invalid support links")
	}
	if len(ls) > 8 {
		return "", fmt.Errorf("At most 8 support links")
	}
	for i := range ls {
		l := &ls[i]
		l.Label, l.URL = strings.TrimSpace(l.Label), strings.TrimSpace(l.URL)
		if l.Label == "" || utf8.RuneCountInString(l.Label) > 40 {
			return "", fmt.Errorf("Support link %d: the label is missing or too long", i+1)
		}
		low := strings.ToLower(l.URL)
		if !(strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "mailto:") || strings.HasPrefix(low, "tg://")) || len(l.URL) > 500 {
			return "", fmt.Errorf("Support link %d must start with https://, mailto: or tg://", i+1)
		}
		switch l.Icon {
		case "", "telegram", "book", "mail", "link", "chat":
		default:
			l.Icon = "link"
		}
	}
	b, _ := json.Marshal(ls)
	return string(b), nil
}

func validateClientList(v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", nil
	}
	known := map[string]bool{}
	for _, c := range subClients {
		known[c.ID] = true
	}
	var out []string
	for _, id := range strings.Split(v, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !known[id] {
			return "", fmt.Errorf("Unknown client: %s", id)
		}
		out = append(out, id)
	}
	return strings.Join(out, ","), nil
}

// ---------- panel appearance default ----------

type accentSet struct {
	Accent     string `json:"accent"`
	AccentText string `json:"accentText"`
	OnAccent   string `json:"onAccent"`
}

type uiAppearance struct {
	Mode    string `json:"mode,omitempty"`
	Palette string `json:"palette,omitempty"`
	Radius  string `json:"radius,omitempty"`
	Density string `json:"density,omitempty"`
	Accent  *struct {
		Light  accentSet `json:"light"`
		Dark   accentSet `json:"dark"`
		Source string    `json:"source,omitempty"` // the picked colour, re-resolved per palette
	} `json:"accent,omitempty"`
}

func validateAppearance(v string) (string, error) {
	if strings.TrimSpace(v) == "" {
		return "", nil
	}
	var a uiAppearance
	if err := json.Unmarshal([]byte(v), &a); err != nil {
		return "", fmt.Errorf("Invalid appearance settings")
	}
	in := func(s string, ok ...string) bool {
		for _, o := range ok {
			if s == o {
				return true
			}
		}
		return false
	}
	if !in(a.Mode, "", "system", "light", "dark") || !in(a.Palette, "", "neutral", "rosepine", "catppuccin", "nord") ||
		!in(a.Radius, "", "default", "sharp", "round") || !in(a.Density, "", "default", "compact") {
		return "", fmt.Errorf("Appearance settings contain invalid values")
	}
	if a.Accent != nil {
		if a.Accent.Source != "" && !hexColorRe.MatchString(a.Accent.Source) {
			return "", fmt.Errorf("Invalid accent color")
		}
		for _, c := range []string{a.Accent.Light.Accent, a.Accent.Light.AccentText, a.Accent.Light.OnAccent, a.Accent.Dark.Accent, a.Accent.Dark.AccentText, a.Accent.Dark.OnAccent} {
			if !hexColorRe.MatchString(c) {
				return "", fmt.Errorf("Invalid accent color")
			}
		}
	}
	b, _ := json.Marshal(a)
	return string(b), nil
}

// entryRegion prefers the configured exit region and falls back to guessing from the remark.
func entryRegion(e subEntry) *subRegion {
	if e.Region != "" {
		return &subRegion{e.Region, flagOf(e.Region)}
	}
	return guessRegion(e.Remark)
}

// subUsage is the per-line usage and the traffic limits shown on the subscription page.
func (m *Master) subUsage(u subUser, es []subEntry, userNext int64) ([]subUsage, []subLimit) {
	usage, limits := []subUsage{}, []subLimit{}
	if u.ID <= 0 {
		return usage, limits
	}
	use := map[int64][2]int64{}
	rows, err := m.db.Query(`SELECT inbound_id, up, down FROM inbound_usage WHERE user_id=?`, u.ID)
	if err == nil {
		for rows.Next() {
			var id, up, down int64
			rows.Scan(&id, &up, &down)
			use[id] = [2]int64{up, down}
		}
		rows.Close()
	}
	name := map[int64]string{}
	for _, e := range es {
		if _, ok := name[e.Inbound]; ok {
			continue
		}
		name[e.Inbound] = e.Line
		v := use[e.Inbound]
		usage = append(usage, subUsage{Name: e.Line, Protocol: e.Type, Up: v[0], Down: v[1], Used: v[0] + v[1], Exhausted: u.Blocked[e.Inbound]})
	}
	sort.SliceStable(usage, func(i, j int) bool { return usage[i].Used > usage[j].Used })
	for _, l := range m.userLimits(u.ID) {
		sl := subLimit{Name: l.Name, Used: l.Used, Total: l.TrafficLimit, Exhausted: l.Exhausted, NextAt: l.NextReset, Lines: []string{}}
		if l.Period == periodUser {
			sl.NextAt = userNext
		}
		for _, id := range l.InboundIDs {
			if n, ok := name[id]; ok {
				sl.Lines = append(sl.Lines, n)
			}
		}
		limits = append(limits, sl)
	}
	return usage, limits
}
