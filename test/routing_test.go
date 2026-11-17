package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	"gopkg.in/yaml.v3"
)

// TestRoutingProfiles: subscriptions rendered from routing profiles for Clash, sing-box
// and Surge user agents, ?profile= selection, assignment precedence, draft/publish,
// ETag revalidation, and real traffic through a sing-box client running the full
// generated profile config.
func TestRoutingProfiles(t *testing.T) {
	e := newEnv(t)
	e.createInbound("plain", "vless", map[string]any{"listen": "127.0.0.1", "listen_port": freePort(t)}, nil)
	e.createInbound("ss", "shadowsocks", map[string]any{"listen": "127.0.0.1", "listen_port": freePort(t),
		"method": "2022-blake3-aes-128-gcm", "password": "dGVzdHRlc3R0ZXN0dGVzdA=="}, nil)
	uid := e.createUser(map[string]any{"username": "router"})
	e.waitApplied()
	var s struct {
		URL string `json:"url"`
	}
	e.api.do("GET", fmt.Sprintf("/api/users/%d/sub", uid), nil, &s)

	get := func(q, ua string, hdr ...string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest("GET", s.URL+q, nil)
		req.Header.Set("User-Agent", ua)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}

	// default profile 标准分流 for each client
	_, b := get("", "clash-verge/v2.2.3")
	var mh struct {
		Proxies       []map[string]any          `yaml:"proxies"`
		ProxyGroups   []map[string]any          `yaml:"proxy-groups"`
		RuleProviders map[string]map[string]any `yaml:"rule-providers"`
		Rules         []string                  `yaml:"rules"`
	}
	if err := yaml.Unmarshal(b, &mh); err != nil {
		t.Fatalf("mihomo yaml: %v\n%s", err, b)
	}
	if len(mh.Proxies) != 2 || mh.RuleProviders["ai-site"] == nil || mh.Rules[len(mh.Rules)-1] != "MATCH,🐟 final" {
		t.Fatalf("mihomo standard: %d proxies, providers %d, last %q", len(mh.Proxies), len(mh.RuleProviders), mh.Rules[len(mh.Rules)-1])
	}
	resp, b := get("", "SFA/1.12.0 (Android)")
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") || !strings.Contains(string(b), `"type": "selector"`) {
		t.Fatalf("sing-box: %s", resp.Header.Get("Content-Type"))
	}
	singboxParses(t, b)
	resp, b = get("", "Surge iOS/3101")
	sb := string(b)
	if !strings.Contains(sb, "[Proxy Group]") || !strings.Contains(sb, "local · ss = ss, ") || strings.Contains(sb, "vless") {
		t.Fatalf("surge:\n%s", sb)
	}
	if !strings.Contains(sb, "#!MANAGED-CONFIG "+s.URL) {
		t.Fatal("surge managed-config header")
	}
	// Shadowrocket gets a structured node subscription, never a full rule profile.
	if _, b = get("", "Shadowrocket/2070"); !strings.HasPrefix(string(b), "proxies:") || strings.Contains(string(b), "proxy-groups:") {
		t.Fatal("shadowrocket must get structured nodes")
	}
	if _, b = get("?format=base64", "Shadowrocket/2070"); strings.HasPrefix(string(b), "proxies:") || len(b) == 0 {
		t.Fatal("explicit legacy format must win")
	}
	if _, b = get("?format=shadowrocket", "other"); !strings.HasPrefix(string(b), "proxies:") {
		t.Fatal("explicit Shadowrocket format")
	}

	// ETag revalidation
	resp, _ = get("", "clash-verge/v2.2.3")
	tag := resp.Header.Get("ETag")
	if resp2, b2 := get("", "clash-verge/v2.2.3", "If-None-Match", tag); tag == "" || resp2.StatusCode != 304 || len(b2) != 0 {
		t.Fatalf("etag %q: %d", tag, resp2.StatusCode)
	}

	// ?profile= picks a user-selectable profile
	if _, b = get("?profile=minimal", "mihomo/1.19"); strings.Contains(string(b), "rule-providers") || !strings.Contains(string(b), "MATCH,🚀 节点选择") {
		t.Fatalf("?profile=minimal:\n%s", b)
	}

	var profiles []struct {
		ID         int64  `json:"id"`
		Slug       string `json:"slug"`
		IsDefault  bool   `json:"is_default"`
		Selectable bool   `json:"selectable"`
	}
	e.api.do("GET", "/api/routing/profiles", nil, &profiles)
	if len(profiles) != 4 || !profiles[0].IsDefault || profiles[0].Slug != "standard" {
		t.Fatalf("seeded profiles %+v", profiles)
	}
	ids := map[string]int64{}
	for _, p := range profiles {
		ids[p.Slug] = p.ID
	}
	// non-selectable profiles can't be picked via ?profile=
	e.api.expect(200, "PATCH", fmt.Sprintf("/api/routing/profiles/%d", ids["global"]), map[string]any{"selectable": false})
	if _, b = get("?profile=global", "mihomo/1.19"); !strings.Contains(string(b), "rule-providers") {
		t.Fatal("non-selectable profile must fall back to the assigned one")
	}

	// group assignment beats the default; user override beats the group
	var groups []struct{ ID int64 }
	e.api.do("GET", "/api/groups", nil, &groups)
	e.api.expect(200, "PATCH", fmt.Sprintf("/api/groups/%d", groups[0].ID), map[string]any{"sub_profile_id": ids["bypass-cn"]})
	selected := func() string {
		_, b := get("?format=json", "")
		var d struct {
			Profiles []struct {
				Slug string `json:"slug"`
				URL  string `json:"url"`
				QR   string `json:"qr"`
			} `json:"profiles"`
			Selected string `json:"selected_profile"`
		}
		json.Unmarshal(b, &d)
		for _, p := range d.Profiles {
			if p.Slug == "global" {
				t.Fatal("non-selectable profile offered on the page")
			}
			if p.QR == "" || (p.Slug != d.Selected && !strings.Contains(p.URL, "?profile="+p.Slug)) {
				t.Fatalf("profile option %+v", p)
			}
		}
		return d.Selected
	}
	if got := selected(); got != "bypass-cn" {
		t.Fatalf("group assignment: selected %q", got)
	}

	// a custom profile without LAN rules: everything goes through the panel's node
	var created struct{ ID int64 }
	e.api.do("POST", "/api/routing/profiles", map[string]any{"name": "测试", "slug": "e2e", "preset": "minimal"}, &created)
	draft := map[string]any{
		"rule_sets": []any{map[string]any{"id": "blk", "name": "blk", "inline": []any{map[string]any{"type": "domain", "values": []string{"blocked.invalid"}}}}},
		"groups": []any{
			map[string]any{"id": "proxy", "name": "代理", "type": "select", "members": []any{map[string]any{"kind": "filter", "include": "vless|plain"}, map[string]any{"kind": "all"}}},
			map[string]any{"id": "x", "name": "循环", "type": "select", "members": []any{map[string]any{"kind": "group", "ref": "x"}}},
		},
		"rules": []any{map[string]any{"rule_set": "blk", "target": "REJECT"}}, "final": "proxy",
		"dns":     map[string]any{"mode": "real-ip", "remote": []string{}, "domestic": []string{}},
		"clients": map[string]any{"singbox": map[string]any{"enabled": true, "base": `{"outbounds": {{outbounds}}, "route": {"rules": {{route_rules}}, "rule_set": {{rule_set}}, "final": {{final}}}, "inbounds": [{"type":"tun","tag":"tun-in","address":["172.19.0.1/30"],"auto_route":true}]}`}},
	}
	path := fmt.Sprintf("/api/routing/profiles/%d", created.ID)
	if msg := e.api.expect(400, "PUT", path+"/draft", map[string]any{"draft": draft}); !strings.Contains(msg, "reference cycle") {
		t.Fatalf("cycle accepted: %s", msg)
	}
	e.api.lang = "zh"
	if msg := e.api.expect(400, "PUT", path+"/draft", map[string]any{"draft": draft}); !strings.Contains(msg, "循环") {
		t.Fatalf("cycle accepted (zh): %s", msg)
	}
	e.api.lang = ""
	draft["groups"] = draft["groups"].([]any)[:1]
	e.api.expect(200, "PUT", path+"/draft", map[string]any{"draft": draft})
	var preview struct {
		Body          string   `json:"body"`
		Error         string   `json:"error"`
		PublishedBody string   `json:"published_body"`
		Warnings      []string `json:"warnings"`
	}
	e.api.do("POST", path+"/preview", map[string]any{"client": "singbox", "user_id": uid}, &preview)
	if preview.Error != "" || !strings.Contains(preview.Body, `"代理"`) || strings.Contains(preview.PublishedBody, `"代理"`) {
		t.Fatalf("preview: %+v", preview)
	}
	e.api.expect(200, "PATCH", fmt.Sprintf("/api/users/%d", uid), map[string]any{"sub_profile_id": created.ID})
	// draft is not live yet
	if _, b = get("", "sing-box 1.14.4"); strings.Contains(string(b), `"代理"`) {
		t.Fatal("unpublished draft leaked into the subscription")
	}
	e.api.expect(200, "POST", path+"/publish", nil)
	if got := selected(); got != "e2e" {
		t.Fatalf("user override: selected %q", got)
	}
	var hist []struct{ ID int64 }
	e.api.do("GET", path+"/history", nil, &hist)
	if len(hist) != 2 {
		t.Fatalf("history %d", len(hist))
	}
	// mihomo is disabled in this profile → legacy generator, still valid YAML
	if _, b = get("", "clash.meta"); !strings.Contains(string(b), "proxy-groups") {
		t.Fatal("disabled client must fall back to the legacy generator")
	}

	_, cfg := get("", "sing-box 1.14.4")
	proxy := runProfileClient(t, cfg)
	target := blob(t)
	const size = 8 << 20
	var err error
	for i := 0; i < 3; i++ {
		if _, err = fetch(proxy, target, size, "", 60*time.Second); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("traffic through profile config: %v", err)
	}
	waitFor(t, "usage counted", 30*time.Second, func() bool { return e.user(uid).User.LifeDown >= size })

	// restore the first version into the draft
	e.api.expect(200, "POST", fmt.Sprintf("%s/history/%d/restore", path, hist[len(hist)-1].ID), nil)
	var detail struct {
		Draft json.RawMessage `json:"draft"`
	}
	e.api.do("GET", path, nil, &detail)
	if string(detail.Draft) == "null" {
		t.Fatal("restore did not create a draft")
	}
	e.api.expect(400, "DELETE", fmt.Sprintf("/api/routing/profiles/%d", ids["standard"]), nil)
}

func singboxParses(t *testing.T, b []byte) option.Options {
	t.Helper()
	ctx := include.Context(context.Background())
	var opts option.Options
	if err := sjson.UnmarshalContext(ctx, b, &opts); err != nil {
		t.Fatalf("sing-box rejects: %v\n%s", err, b)
	}
	return opts
}

// runProfileClient starts the full generated config with tun swapped for a mixed inbound.
func runProfileClient(t *testing.T, cfg []byte) string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(cfg, &raw); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	raw["inbounds"] = []any{map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": port}}
	delete(raw, "experimental")
	raw["log"] = map[string]any{"level": "error"}
	b, _ := json.Marshal(raw)
	ctx := include.Context(context.Background())
	var opts option.Options
	if err := sjson.UnmarshalContext(ctx, b, &opts); err != nil {
		t.Fatalf("client config: %v\n%s", err, b)
	}
	inst, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inst.Close() })
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}
