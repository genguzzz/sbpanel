package master

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"
	"gopkg.in/yaml.v3"

	"sbpanel/internal/pb"
	"sbpanel/internal/pki"
)

func newTestMaster(t *testing.T) *Master {
	t.Helper()
	m, err := New(Config{DataDir: t.TempDir(), AdminPassword: "pw-12345678"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stopBackground(); m.db.Close() })
	return m
}

func TestTOTPVector(t *testing.T) {
	// RFC 6238 SHA-1 vector: secret "12345678901234567890", T=59 -> 94287082 (6-digit tail 287082)
	sec := strings.TrimRight(base32Encode([]byte("12345678901234567890")), "=")
	if got := totpCode(sec, time.Unix(59, 0)); got != "287082" {
		t.Fatalf("totp = %s, want 287082", got)
	}
	if got := totpCode(sec, time.Unix(1111111109, 0)); got != "081804" {
		t.Fatalf("totp = %s, want 081804", got)
	}
	if !totpValid(sec, "287082", time.Unix(59+25, 0)) {
		t.Fatal("adjacent window should be accepted")
	}
	if totpValid(sec, "287082", time.Unix(59+120, 0)) {
		t.Fatal("old code accepted")
	}
	if totpValid(sec, "12345", time.Unix(59, 0)) {
		t.Fatal("short code accepted")
	}
}

func TestPKI(t *testing.T) {
	ca, err := pki.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	c, k, _ := ca.PEM()
	ca2, err := pki.LoadCA(c, k)
	if err != nil || !ca2.Cert.Equal(ca.Cert) {
		t.Fatalf("reload CA: %v", err)
	}
	_, csr, _ := pki.NewKeyAndCSR("x")
	cert, err := ca.SignNodeCSR(csr, "abc123", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := pki.NodeIDFromCert(cert); !ok || id != "abc123" {
		t.Fatalf("node id %q %v", id, ok)
	}
	if _, err := ca.SignNodeCSR([]byte("garbage"), "x", time.Hour); err == nil {
		t.Fatal("garbage CSR signed")
	}
	srv, _ := ca.ServerCert()
	if err := pki.PinVerifier(pki.Fingerprint(ca.Cert.Raw))(srv.Certificate, nil); err != nil {
		t.Fatalf("pin should pass: %v", err)
	}
	other, _ := pki.NewCA()
	if err := pki.PinVerifier(pki.Fingerprint(other.Cert.Raw))(srv.Certificate, nil); err == nil {
		t.Fatal("pin of another CA accepted")
	}
}

func TestPeriodStart(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	at := func(s string) time.Time { x, _ := time.ParseInLocation("2006-01-02 15:04", s, loc); return x }
	cases := []struct {
		strategy, created, now, want string
	}{
		{"day", "2026-01-01 00:00", "2026-11-14 10:30", "2026-11-14 00:00"},
		{"week", "2026-01-01 00:00", "2026-11-14 10:30", "2026-11-09 00:00"}, // Saturday -> Monday
		{"week", "2026-01-01 00:00", "2026-11-09 00:00", "2026-11-09 00:00"},
		{"month", "2026-01-01 00:00", "2026-11-14 10:30", "2026-11-01 00:00"},
		{"month_rolling", "2026-01-20 08:00", "2026-11-14 10:30", "2026-10-20 00:00"},
		{"month_rolling", "2026-01-20 08:00", "2026-11-25 10:30", "2026-11-20 00:00"},
		{"month_rolling", "2026-01-31 08:00", "2026-02-28 10:00", "2026-02-28 00:00"}, // clamp to month end
		{"month_rolling", "2026-01-31 08:00", "2026-03-15 10:00", "2026-02-28 00:00"},
		{"month:15", "2026-01-31 08:00", "2026-11-14 10:30", "2026-10-15 00:00"},
		{"month:15", "2026-01-31 08:00", "2026-11-15 00:00", "2026-11-15 00:00"},
		{"month:31", "2026-01-01 00:00", "2026-03-10 10:00", "2026-02-28 00:00"}, // clamp to month end
	}
	for _, c := range cases {
		got, ok := periodStart(c.strategy, at(c.created), at(c.now), loc)
		if !ok || !got.Equal(at(c.want)) {
			t.Errorf("%s created %s now %s: got %s want %s", c.strategy, c.created, c.now, got, c.want)
		}
	}
	if _, ok := periodStart("none", time.Now(), time.Now(), loc); ok {
		t.Fatal("none must not have a period")
	}
}

func TestResetStrategies(t *testing.T) {
	for s, want := range map[string]bool{"": true, "none": true, "month_rolling": true, "month:1": true, "month:31": true, "node:9d9a32f41702": true,
		"month:0": false, "month:32": false, "month:05": false, "month:": false, "node:": false, "node:a b": false, "yearly": false} {
		if validReset(s) != want {
			t.Errorf("validReset(%q) = %v", s, !want)
		}
	}
	days := map[string]int{"n1": 18}
	for in, want := range map[string]string{"node:n1": "month:18", "node:gone": "month:1", "week": "week", "month:3": "month:3"} {
		if got := resolveReset(in, days); got != want {
			t.Errorf("resolveReset(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRealityPublicKey(t *testing.T) {
	k, _ := ecdh.X25519().GenerateKey(rand.Reader)
	priv := base64.RawURLEncoding.EncodeToString(k.Bytes())
	want := base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
	if got := realityPublicKey(priv); got != want {
		t.Fatalf("pbk %s want %s", got, want)
	}
	if realityPublicKey("not-base64!!") != "" {
		t.Fatal("garbage key")
	}
}

func TestUsersJSON(t *testing.T) {
	us := []specUser{{ID: 7, UUID: "bf000d23-0752-40b4-affe-68f7707a9661", Password: "pw"}}
	cases := map[string]string{
		"vless":       `[{"flow":"xtls-rprx-vision","name":"7","uuid":"bf000d23-0752-40b4-affe-68f7707a9661"}]`,
		"vmess":       `[{"alterId":0,"name":"7","uuid":"bf000d23-0752-40b4-affe-68f7707a9661"}]`,
		"trojan":      `[{"name":"7","password":"pw"}]`,
		"tuic":        `[{"name":"7","password":"pw","uuid":"bf000d23-0752-40b4-affe-68f7707a9661"}]`,
		"hysteria2":   `[{"name":"7","password":"pw"}]`,
		"anytls":      `[{"name":"7","password":"pw"}]`,
		"shadowsocks": `[{"name":"7","password":"` + ssKey("pw", 16) + `"}]`,
	}
	for typ, want := range cases {
		opts := map[string]any{"_flow": "xtls-rprx-vision", "method": "2022-blake3-aes-128-gcm"}
		b, err := usersJSON(typ, opts, us)
		if err != nil || string(b) != want {
			t.Errorf("%s: %s (%v), want %s", typ, b, err, want)
		}
	}
	if _, err := usersJSON("socks", nil, us); err == nil {
		t.Fatal("unsupported type accepted")
	}
	key, _ := base64.StdEncoding.DecodeString(ssKey("pw", 32))
	if len(key) != 32 {
		t.Fatalf("ss key len %d", len(key))
	}
}

func testEntries() []subEntry {
	k, _ := ecdh.X25519().GenerateKey(rand.Reader)
	priv := base64.RawURLEncoding.EncodeToString(k.Bytes())
	uuid := "bf000d23-0752-40b4-affe-68f7707a9661"
	mk := func(typ string, opts string, remark string) subEntry {
		var o map[string]any
		json.Unmarshal([]byte(opts), &o)
		e := entryFromInbound(typ, o, nil)
		e.UUID, e.Password, e.Address, e.Remark = uuid, "pa ss/wd", "example.com", remark
		if typ == "shadowsocks" {
			e.Password = ssClientPassword(e.SSMethod, str(o, "password"), "pw")
		}
		return e
	}
	return []subEntry{
		mk("vless", `{"listen_port":443,"_flow":"xtls-rprx-vision","tls":{"enabled":true,"server_name":"www.microsoft.com","reality":{"enabled":true,"private_key":"`+priv+`","short_id":["abcd"]}}}`, "reality"),
		mk("vless", `{"listen_port":8443,"tls":{"enabled":true,"server_name":"cdn.example.com","alpn":["h2"]},"transport":{"type":"grpc","service_name":"svc"}}`, "vless-grpc"),
		mk("vmess", `{"listen_port":8443,"tls":{"enabled":true,"server_name":"a.example.com"},"transport":{"type":"ws","path":"/ws","headers":{"Host":"h.example.com","X-Test":"preserved"},"max_early_data":2048,"early_data_header_name":"Sec-WebSocket-Protocol"}}`, "vmess-ws"),
		mk("trojan", `{"listen_port":4433,"tls":{"enabled":true,"server_name":"t.example.com"}}`, "trojan"),
		mk("shadowsocks", `{"listen_port":8388,"method":"2022-blake3-aes-128-gcm","password":"`+ssKey("server", 16)+`"}`, "ss"),
		mk("hysteria2", `{"listen_port":8444,"tls":{"enabled":true,"server_name":"h.example.com"},"obfs":{"type":"salamander","password":"obfs-example"}}`, "hy2"),
		mk("tuic", `{"listen_port":8445,"congestion_control":"bbr","tls":{"enabled":true,"server_name":"tu.example.com"}}`, "tuic"),
		mk("anytls", `{"listen_port":8446,"tls":{"enabled":true,"server_name":"any.example.com"}}`, "anytls"),
		mk("vless", `{"listen_port":80,"transport":{"type":"httpupgrade","path":"/up","host":"up.example.com"}}`, "vless-plain"),
	}
}

func TestShareURIs(t *testing.T) {
	es := testEntries()
	for _, e := range es {
		u := e.uri()
		if u == "" {
			t.Fatalf("%s: empty uri", e.Remark)
		}
		if e.Type == "vmess" {
			b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(u, "vmess://"))
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			json.Unmarshal(b, &m)
			if m["net"] != "ws" || m["path"] != "/ws" || m["host"] != "h.example.com" || m["tls"] != "tls" || m["port"] != "8443" {
				t.Fatalf("vmess json %v", m)
			}
			continue
		}
		p, err := url.Parse(u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		if p.Hostname() != "example.com" || p.Fragment != e.Remark {
			t.Fatalf("%s: host %s frag %s", u, p.Hostname(), p.Fragment)
		}
		q := p.Query()
		switch e.Remark {
		case "reality":
			if q.Get("security") != "reality" || q.Get("pbk") == "" || q.Get("sid") != "abcd" || q.Get("flow") != "xtls-rprx-vision" || q.Get("sni") != "www.microsoft.com" {
				t.Fatalf("reality uri %s", u)
			}
		case "vless-grpc":
			if q.Get("type") != "grpc" || q.Get("serviceName") != "svc" || q.Get("alpn") != "h2" {
				t.Fatalf("grpc uri %s", u)
			}
		case "trojan":
			if p.User.Username() != "pa ss/wd" {
				t.Fatalf("trojan password not round-tripped: %s", u)
			}
		case "ss":
			dec, err := base64.RawURLEncoding.DecodeString(p.User.Username())
			if err != nil || !strings.HasPrefix(string(dec), "2022-blake3-aes-128-gcm:"+ssKey("server", 16)+":") {
				t.Fatalf("ss userinfo %q %v", dec, err)
			}
		case "tuic":
			if q.Get("congestion_control") != "bbr" || q.Get("alpn") != "h3" {
				t.Fatalf("tuic %s", u)
			}
		case "vless-plain":
			if q.Get("security") != "none" || q.Get("type") != "httpupgrade" || q.Get("host") != "up.example.com" {
				t.Fatalf("plain %s", u)
			}
		}
	}
}

func TestSingboxSubscriptionParses(t *testing.T) {
	b := singboxConfig(testEntries())
	ctx := include.Context(context.Background())
	var opts option.Options
	if err := sjson.UnmarshalContext(ctx, b, &opts); err != nil {
		t.Fatalf("sing-box rejects subscription: %v\n%s", err, b)
	}
	if len(opts.Outbounds) != len(testEntries())+3 {
		t.Fatalf("outbounds = %d", len(opts.Outbounds))
	}
	// building the instance validates cross references (selector members, detours, resolvers)
	opts.Inbounds = nil // tun needs privileges
	inst, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatalf("box.New: %v", err)
	}
	inst.Close()
	// empty subscription is still a valid config
	ctx2 := include.Context(context.Background())
	var empty option.Options
	if err := sjson.UnmarshalContext(ctx2, singboxConfig(nil), &empty); err != nil {
		t.Fatal(err)
	}
}

func TestMihomoSubscriptionParses(t *testing.T) {
	var cfg struct {
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(mihomoConfig(testEntries()), &cfg); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if len(cfg.Proxies) != len(testEntries()) || len(cfg.ProxyGroups) != 2 || cfg.Rules[len(cfg.Rules)-1] != "MATCH,节点选择" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	for _, p := range cfg.Proxies {
		if p["name"] == "reality" {
			ro, _ := p["reality-opts"].(map[string]any)
			if p["type"] != "vless" || ro["short-id"] != "abcd" || p["flow"] != "xtls-rprx-vision" {
				t.Fatalf("reality proxy %v", p)
			}
		}
		if p["name"] == "ss" && (p["type"] != "ss" || !strings.Contains(p["password"].(string), ":")) {
			t.Fatalf("ss proxy %v", p)
		}
	}
	if err := yaml.Unmarshal(mihomoConfig(nil), &cfg); err != nil {
		t.Fatalf("empty yaml: %v", err)
	}
}

func TestDetectFormat(t *testing.T) {
	cases := map[string]string{
		"ClashMetaForAndroid/2.11": "mihomo", "clash-verge/v2": "mihomo", "Stash/2.4": "mihomo", "mihomo/1.19": "mihomo",
		"SFA/1.11.0 (sing-box 1.14)": "singbox", "sing-box 1.14.4": "singbox", "SFI/1.0": "singbox",
		"v2rayNG/1.9": "base64", "Shadowrocket/2000": "shadowrocket", "": "base64",
	}
	for ua, want := range cases {
		r := httptest.NewRequest("GET", "/s/x", nil)
		r.Header.Set("User-Agent", ua)
		if got := detectFormat(r); got != want {
			t.Errorf("%q -> %s want %s", ua, got, want)
		}
	}
	r := httptest.NewRequest("GET", "/s/x", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 Chrome")
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	if detectFormat(r) != "html" {
		t.Fatal("browser should get html")
	}
	r = httptest.NewRequest("GET", "/s/x?format=clash", nil)
	if detectFormat(r) != "mihomo" {
		t.Fatal("format override")
	}
}

func TestValidateSkeleton(t *testing.T) {
	ok := `{"route":{"rules":[{"action":"sniff"},{"domain":["x"],"outbound":"direct"}],"final":"direct"},"outbounds":[{"type":"direct","tag":"direct"}]}`
	if _, err := validateSkeleton(ok); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"inbounds":      `{"inbounds":[],"route":{"rules":[{"action":"sniff"}]}}`,
		"no sniff":      `{"route":{"rules":[]},"outbounds":[{"type":"direct","tag":"direct"}]}`,
		"dangling rule": `{"route":{"rules":[{"action":"sniff"},{"outbound":"nope"}]},"outbounds":[{"type":"direct","tag":"direct"}]}`,
		"dangling fin":  `{"route":{"rules":[{"action":"sniff"}],"final":"nope"},"outbounds":[{"type":"direct","tag":"direct"}]}`,
		"dup tag":       `{"outbounds":[{"type":"direct","tag":"a"},{"type":"direct","tag":"a"}]}`,
		"no tag":        `{"outbounds":[{"type":"direct"}]}`,
		"not json":      `{`,
	}
	for name, js := range bad {
		if _, err := validateSkeleton(js); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := validateSkeleton(DefaultSkeleton); err != nil {
		t.Fatalf("default skeleton rejected: %v", err)
	}
}

func TestDefaultSkeletonIsValidSingbox(t *testing.T) {
	ctx := include.Context(context.Background())
	var opts option.Options
	if err := sjson.UnmarshalContext(ctx, []byte(DefaultSkeleton), &opts); err != nil {
		t.Fatal(err)
	}
	inst, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	inst.Close()
}

func seedNode(t *testing.T, m *Master, id string) {
	t.Helper()
	var tid int64
	m.db.QueryRow(`SELECT id FROM templates ORDER BY id LIMIT 1`).Scan(&tid)
	if _, err := m.db.Exec(`INSERT INTO nodes(id, name, template_id, created_at) VALUES(?,?,?,?)`, id, "n-"+id, tid, now()); err != nil {
		t.Fatal(err)
	}
}

func TestIngestExactlyOnce(t *testing.T) {
	m := newTestMaster(t)
	seedNode(t, m, "n1")
	m.db.Exec(`INSERT INTO users(id, username, sub_token, uuid, password, created_at) VALUES(5,'u','tok','x','y',?)`, now())
	rep := &pb.TrafficReport{Seq: 1, WindowStart: 1_700_000_000, Users: []*pb.UserStat{{User: "5", Inbound: "in", Outbound: "direct", Up: 10, Down: 100, Conns: 1}},
		Dims: []*pb.DimStat{{User: "5", Inbound: "in", Outbound: "direct", Host: "Example.COM", Port: 443, Up: 10, Down: 100, Conns: 1}}}
	var wg sync.WaitGroup
	dups := 0
	var mu sync.Mutex
	for i := 0; i < 8; i++ { // concurrent retries of the same report
		wg.Add(1)
		go func() {
			defer wg.Done()
			dup, err := m.ingest("n1", rep)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			if dup {
				dups++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if dups != 7 {
		t.Fatalf("dups = %d, want 7", dups)
	}
	var up, down, lu int64
	m.db.QueryRow(`SELECT used_up, used_down, life_up FROM users WHERE id=5`).Scan(&up, &down, &lu)
	if up != 10 || down != 100 || lu != 10 {
		t.Fatalf("usage %d/%d/%d", up, down, lu)
	}
	var host string
	var hd int64
	m.db.QueryRow(`SELECT host, down FROM dim_hour`).Scan(&host, &hd)
	if host != "example.com" || hd != 100 {
		t.Fatalf("dim %s %d", host, hd)
	}
	// a different node may reuse the same seq
	if dup, _ := m.ingest("n2", rep); dup {
		t.Fatal("seq is per node")
	}
	// quota: limit below usage flips status
	m.db.Exec(`UPDATE users SET traffic_limit=50 WHERE id=5`)
	rep2 := &pb.TrafficReport{Seq: 2, WindowStart: 1_700_000_060, Users: []*pb.UserStat{{User: "5", Inbound: "in", Outbound: "direct", Down: 1}}}
	m.ingest("n1", rep2)
	var st string
	m.db.QueryRow(`SELECT status FROM users WHERE id=5`).Scan(&st)
	if st != "limited" {
		t.Fatalf("status %s", st)
	}
}

func TestResetUsage(t *testing.T) {
	m := newTestMaster(t)
	old := time.Now().Add(-40 * 24 * time.Hour).Unix()
	m.db.Exec(`INSERT INTO users(id, username, sub_token, uuid, password, status, traffic_limit, used_up, used_down, reset_strategy, last_reset_at, created_at)
		VALUES(1,'a','t1','x','y','limited',100,60,60,'month',?,?)`, old, old)
	m.db.Exec(`INSERT INTO users(id, username, sub_token, uuid, password, used_up, used_down, reset_strategy, last_reset_at, created_at)
		VALUES(2,'b','t2','x','y',5,5,'none',?,?)`, old, old)
	m.resetUsage(time.Now())
	var u1, u2 int64
	var st string
	m.db.QueryRow(`SELECT used_up+used_down, status FROM users WHERE id=1`).Scan(&u1, &st)
	m.db.QueryRow(`SELECT used_up+used_down FROM users WHERE id=2`).Scan(&u2)
	if u1 != 0 || st != "active" || u2 != 10 {
		t.Fatalf("after reset: %d %s %d", u1, st, u2)
	}
	// running again in the same period is a no-op
	m.db.Exec(`UPDATE users SET used_up=7 WHERE id=1`)
	m.resetUsage(time.Now())
	m.db.QueryRow(`SELECT used_up FROM users WHERE id=1`).Scan(&u1)
	if u1 != 7 {
		t.Fatal("reset twice in one period")
	}
}

func TestSpecGroupsAndPolicies(t *testing.T) {
	m := newTestMaster(t)
	seedNode(t, m, "n1")
	mk := func(tag string, port int) int64 {
		r, _ := m.db.Exec(`INSERT INTO inbounds(node_id, tag, type, port, options_json, created_at) VALUES('n1',?,?,?,?,?)`, tag, "trojan", port, `{"listen_port":`+itoa(port)+`}`, now())
		id, _ := r.LastInsertId()
		return id
	}
	a, b := mk("a", 1001), mk("b", 1002)
	m.db.Exec(`INSERT INTO groups(id, name, created_at) VALUES(10,'ga',0),(11,'gb',0)`)
	m.db.Exec(`INSERT INTO group_inbounds VALUES(10,?),(11,?)`, a, b)
	m.db.Exec(`INSERT INTO users(id, username, sub_token, uuid, password, speed_down, device_limit, created_at) VALUES(1,'u1','t1','x','p1',1000,2,0),(2,'u2','t2','x','p2',0,0,0),(3,'u3','t3','x','p3',0,0,0)`)
	m.db.Exec(`UPDATE users SET status='expired' WHERE id=3`)
	m.db.Exec(`INSERT INTO user_groups VALUES(1,10),(2,11),(3,10),(3,11)`)
	spec, err := buildSpec(m.db, "n1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, in := range spec.Inbounds {
		got[in.Tag] = in.UserNames
	}
	if strings.Join(got["a"], ",") != "1" || strings.Join(got["b"], ",") != "2" {
		t.Fatalf("membership %v", got)
	}
	if len(spec.Policies) != 1 || spec.Policies[0].User != "1" || spec.Policies[0].SpeedDown != 1000 || spec.Policies[0].DeviceLimit != 2 {
		t.Fatalf("policies %v", spec.Policies)
	}
	// reconcile bumps only on change
	ch := m.reconcile("n1")
	if len(ch) != 1 {
		t.Fatal("first reconcile should bump")
	}
	if ch := m.reconcile("n1"); len(ch) != 0 {
		t.Fatal("no-op reconcile bumped revision")
	}
	m.db.Exec(`UPDATE users SET speed_down=2000 WHERE id=1`)
	if ch := m.reconcile("n1"); len(ch) != 1 {
		t.Fatal("policy change must bump revision")
	}
	// the device policy travels with the limit, so the node can apply it on its own
	if spec.Policies[0].KickOldest {
		t.Fatal("kick_oldest set under the default policy")
	}
	setSetting(m.db, "device_policy", "kick_oldest")
	if ch := m.reconcile("n1"); len(ch) != 1 {
		t.Fatal("device policy change must bump revision")
	}
	m.db.Exec(`UPDATE users SET speed_down=500 WHERE id=2`) // a policy without a device limit
	spec, _ = buildSpec(m.db, "n1")
	if len(spec.Policies) != 2 || !spec.Policies[0].KickOldest || spec.Policies[1].KickOldest {
		t.Fatalf("kick_oldest policies %v", spec.Policies)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestMigrationFromV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sbmaster.db")
	// a v1 database: base schema only, with data
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO nodes(id, name, skeleton_json, created_at) VALUES('a','A',?,0),('b','B','{"route":{"rules":[{"action":"sniff"}],"final":"x"},"outbounds":[{"type":"direct","tag":"x"}]}',0)`, DefaultSkeleton)
	db.Exec(`INSERT INTO inbounds(node_id, tag, type, port, options_json, created_at) VALUES('a','in','vless',443,'{}',0)`)
	db.Exec(`INSERT INTO users(username, sub_token, uuid, password, created_at) VALUES('u','t','x','y',0)`)
	db.Close()
	db2, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var n, gi, ug, tpl int
	db2.QueryRow(`SELECT COUNT(*) FROM groups WHERE is_default=1`).Scan(&n)
	db2.QueryRow(`SELECT COUNT(*) FROM group_inbounds`).Scan(&gi)
	db2.QueryRow(`SELECT COUNT(*) FROM user_groups`).Scan(&ug)
	db2.QueryRow(`SELECT COUNT(*) FROM templates`).Scan(&tpl)
	if n != 1 || gi != 1 || ug != 1 || tpl != 2 {
		t.Fatalf("groups=%d gi=%d ug=%d templates=%d", n, gi, ug, tpl)
	}
	var ta, tb int64
	db2.QueryRow(`SELECT template_id FROM nodes WHERE id='a'`).Scan(&ta)
	db2.QueryRow(`SELECT template_id FROM nodes WHERE id='b'`).Scan(&tb)
	if ta == tb || ta == 0 || tb == 0 {
		t.Fatalf("templates a=%d b=%d", ta, tb)
	}
	// reopening is idempotent
	db2.Close()
	db3, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db3.QueryRow(`SELECT COUNT(*) FROM groups`).Scan(&n)
	db3.Close()
	if n != 1 {
		t.Fatalf("migration re-ran: %d groups", n)
	}
}

func base32Encode(b []byte) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	var out strings.Builder
	var buf, bits uint
	for _, c := range b {
		buf = buf<<8 | uint(c)
		bits += 8
		for bits >= 5 {
			out.WriteByte(alpha[(buf>>(bits-5))&31])
			bits -= 5
		}
	}
	if bits > 0 {
		out.WriteByte(alpha[(buf<<(5-bits))&31])
	}
	return out.String()
}

// Deleted users and user-less traffic (static relay inbounds) never show up in user rankings.
func TestTopUsersSkipsDeletedAndUserless(t *testing.T) {
	m := newTestMaster(t)
	seedNode(t, m, "n1")
	for _, id := range []int64{5, 6} {
		m.db.Exec(`INSERT INTO users(id, username, sub_token, uuid, password, created_at) VALUES(?,?,?,?,?,?)`, id, fmt.Sprint("u", id), fmt.Sprint("t", id), "x", "y", now())
	}
	h := now() - now()%3600
	for _, uid := range []int64{0, 5, 6} {
		for _, tb := range []string{"traffic_hour", "traffic_minute"} {
			if _, err := m.db.Exec(`INSERT INTO `+tb+`(ts, node_id, user_id, inbound, outbound, up, down, conns) VALUES(?,?,?,?,?,?,?,1)`, h, "n1", uid, "in", "direct", 1, 1000); err != nil {
				t.Fatal(err)
			}
		}
	}
	m.purgeUserTraffic(6)
	m.db.Exec(`DELETE FROM users WHERE id=6`)
	rows, err := m.top(trafficFilter{from: h - 3600, to: h + 3600}, "user", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Label != "u5" {
		t.Fatalf("top users: %+v", rows)
	}
}

// Subscription names carry where traffic exits: the inbound's "_region" (relays),
// else its node's region.
func TestExitRegion(t *testing.T) {
	m := newTestMaster(t)
	seedNode(t, m, "n1")
	m.db.Exec(`UPDATE nodes SET region='US' WHERE id='n1'`)
	if got := m.exitRegion("n-n1", `{"listen_port":1}`); got != "US" {
		t.Fatalf("node region: %q", got)
	}
	if got := m.exitRegion("n-n1", `{"_region":"jp"}`); got != "JP" {
		t.Fatalf("relay override: %q", got)
	}
	if got := m.exitRegion("n-n1", `{"_region":"Japan"}`); got != "US" {
		t.Fatalf("invalid override must fall back: %q", got)
	}
	for in, want := range map[string]bool{"us": true, "": true, "USA": false, "1A": false} {
		if _, ok := normRegion(in); ok != want {
			t.Fatalf("normRegion(%q) ok=%v", in, ok)
		}
	}
}
