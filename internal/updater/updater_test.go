package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBin writes a shell script that answers "version" like sbnode does.
func fakeBin(t *testing.T, p, version string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"sbnode "+version+" (sing-box 1.14.4)\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeEnv plays the service manager: Restart "starts" whatever binary is installed.
// behave decides what the started version does: "ok" writes the marker, "silent" runs without
// writing it, "crash" writes it then dies, "down" never starts.
type fakeEnv struct {
	t        *testing.T
	p        Plan
	now      time.Time
	pid      int
	behave   map[string]string
	restarts []string // versions started, in order
	crashAt  time.Time
}

func (e *fakeEnv) Restart(string) error {
	v, err := binVersion(e.p.Target)
	if err != nil {
		return err
	}
	e.restarts = append(e.restarts, v)
	e.pid += 100
	switch e.behave[v] {
	case "down":
		e.pid = 0
	case "crash":
		writeJSON(filepath.Join(e.p.Dir, fBoot), map[string]any{"version": v, "pid": e.pid})
		e.crashAt = e.now.Add(2 * time.Second)
	case "silent":
	default:
		writeJSON(filepath.Join(e.p.Dir, fBoot), map[string]any{"version": v, "pid": e.pid})
	}
	return nil
}
func (e *fakeEnv) MainPID(string) int {
	if !e.crashAt.IsZero() && !e.now.Before(e.crashAt) {
		e.crashAt = time.Time{}
		e.pid += 100 // the service manager restarted it: a new PID
	}
	return e.pid
}
func (e *fakeEnv) Logs(string, int) string { return "boom: config error" }
func (e *fakeEnv) Sleep(d time.Duration)   { e.now = e.now.Add(d) }
func (e *fakeEnv) Now() time.Time          { return e.now }

func setup(t *testing.T, from, to string) (Plan, *fakeEnv) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bin", "sbnode")
	os.MkdirAll(filepath.Dir(target), 0o755)
	fakeBin(t, target, from)
	up := filepath.Join(dir, "up")
	os.MkdirAll(up, 0o700)
	fakeBin(t, filepath.Join(up, fNew), to)
	p := Plan{Service: "sbnode", Init: "systemd", Target: target, Dir: up, From: from, To: to, Health: HealthMarker, Timeout: 30, Started: 1}
	return p, &fakeEnv{t: t, p: p, now: time.Unix(1000, 0), pid: 1, behave: map[string]string{}}
}

func result(t *testing.T, p Plan) Result {
	t.Helper()
	var r Result
	if err := readJSON(filepath.Join(p.Dir, fResult), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func installed(t *testing.T, p Plan) string {
	t.Helper()
	v, err := binVersion(p.Target)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestUpgradeSucceeds(t *testing.T) {
	p, env := setup(t, "v0.4.0", "v0.5.0")
	if err := run(p, env); err != nil {
		t.Fatal(err)
	}
	if r := result(t, p); r.State != StateUpgraded || r.To != "v0.5.0" || r.From != "v0.4.0" {
		t.Fatalf("result %+v", r)
	}
	if installed(t, p) != "v0.5.0" {
		t.Fatal("new binary not installed")
	}
	if v, _ := binVersion(p.Target + ".prev"); v != "v0.4.0" {
		t.Fatalf(".prev is %q", v)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, fLock)); err == nil {
		t.Fatal("lock left behind")
	}
	if st := ReadStatus(p.Dir, p.Target); st.Running || !st.CanRollback || st.Last.State != StateUpgraded {
		t.Fatalf("status %+v", st)
	}
}

func TestUpgradeRollsBack(t *testing.T) {
	for _, behave := range []string{"silent", "crash", "down"} {
		t.Run(behave, func(t *testing.T) {
			p, env := setup(t, "v0.4.0", "v0.5.0")
			env.behave["v0.5.0"] = behave
			if err := run(p, env); err == nil {
				t.Fatal("expected an error")
			}
			r := result(t, p)
			if r.State != StateRolledBack || !strings.Contains(r.Error, "boom: config error") {
				t.Fatalf("result %+v", r)
			}
			if installed(t, p) != "v0.4.0" {
				t.Fatal("old binary not restored")
			}
			if strings.Join(env.restarts, ",") != "v0.5.0,v0.4.0" {
				t.Fatalf("restarts %v", env.restarts)
			}
		})
	}
}

func TestRollbackAlsoFails(t *testing.T) {
	p, env := setup(t, "v0.4.0", "v0.5.0")
	env.behave["v0.5.0"], env.behave["v0.4.0"] = "down", "down"
	run(p, env)
	if r := result(t, p); r.State != StateFailed || !strings.Contains(r.Error, "did not come back") {
		t.Fatalf("result %+v", r)
	}
	if installed(t, p) != "v0.4.0" {
		t.Fatal("old binary should still be put back")
	}
}

func TestStableHealth(t *testing.T) {
	p, env := setup(t, "v0.4.0", "v0.3.0") // an older release: no marker
	p.Health = HealthStable
	env.behave["v0.3.0"] = "silent"
	if err := run(p, env); err != nil {
		t.Fatal(err)
	}
	if r := result(t, p); r.State != StateUpgraded {
		t.Fatalf("result %+v", r)
	}
	// a crash loop never stays up
	p, env = setup(t, "v0.4.0", "v0.3.0")
	p.Health = HealthStable
	env.behave["v0.3.0"] = "down"
	run(p, env)
	if r := result(t, p); r.State != StateRolledBack || installed(t, p) != "v0.4.0" {
		t.Fatalf("result %+v", r)
	}
}

func TestBadStagedBinary(t *testing.T) {
	p, env := setup(t, "v0.4.0", "v0.5.0")
	p.To = "v0.6.0" // the staged binary reports v0.5.0
	run(p, env)
	if r := result(t, p); r.State != StateFailed || len(env.restarts) != 0 || installed(t, p) != "v0.4.0" {
		t.Fatalf("result %+v restarts %v", r, env.restarts)
	}
	p, env = setup(t, "v0.4.0", "v0.5.0")
	os.WriteFile(filepath.Join(p.Dir, fNew), []byte("not a program"), 0o755)
	run(p, env)
	if r := result(t, p); r.State != StateFailed || installed(t, p) != "v0.4.0" {
		t.Fatalf("result %+v", r)
	}
}

func TestManualRollbackCanRollForward(t *testing.T) {
	p, env := setup(t, "v0.4.0", "v0.5.0")
	if err := run(p, env); err != nil {
		t.Fatal(err)
	}
	// roll back: the staged binary is a copy of .prev
	copyFile(p.Target+".prev", filepath.Join(p.Dir, fNew), 0o755)
	p.From, p.To, p.Rollback, p.Started = "v0.5.0", "v0.4.0", true, 2
	if err := run(p, env); err != nil {
		t.Fatal(err)
	}
	if installed(t, p) != "v0.4.0" {
		t.Fatal("rollback not installed")
	}
	if v, _ := binVersion(p.Target + ".prev"); v != "v0.5.0" {
		t.Fatalf(".prev after rollback is %q", v)
	}
}

func TestBusy(t *testing.T) {
	p, env := setup(t, "v0.4.0", "v0.5.0")
	os.WriteFile(filepath.Join(p.Dir, fLock), nil, 0o600)
	if err := run(p, env); err != ErrBusy {
		t.Fatalf("err %v", err)
	}
	if installed(t, p) != "v0.4.0" {
		t.Fatal("changed while busy")
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		c    int
		ok   bool
	}{
		{"v0.3.0", "v0.4.0", -1, true}, {"v0.10.0", "v0.9.9", 1, true}, {"v1.0.0", "v1.0.0", 0, true},
		{"v1.0.0-rc.1", "v1.0.0", -1, true}, {"v1.0.0", "v1.0.0-rc.1", 1, true},
		{"dev-1118.0054-abc", "v0.3.0", 0, false}, {"v1.2", "v1.2.0", 0, false},
		{"v0.3.0-1-g0461cfa-dirty", "v0.3.0", 0, false}, {"v0.3.0-4-g0461cfa", "v0.3.0", 0, false},
	}
	for _, c := range cases {
		if got, ok := Compare(c.a, c.b); got != c.c || ok != c.ok {
			t.Errorf("Compare(%s,%s) = %d,%v", c.a, c.b, got, ok)
		}
	}
	if ValidTag("v1.0.0/../x") || ValidTag("1.0.0") {
		t.Error("ValidTag accepts junk")
	}
}

func TestUnitFromCgroup(t *testing.T) {
	if u := unitFromCgroup("12:pids:/system.slice/sbnode.service\n0::/system.slice/sbnode.service\n"); u != "sbnode" {
		t.Fatal(u)
	}
	if u := unitFromCgroup("0::/user.slice/user-0.slice/session-1.scope\n"); u != "" {
		t.Fatal(u)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, c := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write([]byte(c))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestLatestAndFetch(t *testing.T) {
	arch := tarGz(t, map[string]string{"sbpanel_v0.5.0_linux_amd64/sbmaster": "M", "sbpanel_v0.5.0_linux_amd64/sbnode": "NODE"})
	sum := sha256.Sum256(arch)
	good := hex.EncodeToString(sum[:])
	checksum := good
	apiDown := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/repos/o/r/releases/latest":
			if apiDown {
				http.Error(w, "rate limited", 403)
				return
			}
			fmt.Fprint(w, `{"tag_name":"v0.5.0","name":"sbpanel v0.5.0","body":"notes","html_url":"u","published_at":"2026-11-18T00:00:00Z"}`)
		case "/o/r/releases/latest":
			http.Redirect(w, r, "/o/r/releases/tag/v0.5.0", http.StatusFound)
		case "/o/r/releases/tag/v0.5.0":
		case "/o/r/releases/download/v0.5.0/checksums.txt":
			fmt.Fprintf(w, "%s  sbpanel_v0.5.0_linux_amd64.tar.gz\n%s  other.tar.gz\n", checksum, strings.Repeat("0", 64))
		case "/o/r/releases/download/v0.5.0/sbpanel_v0.5.0_linux_amd64.tar.gz":
			w.Write(arch)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := Source{Repo: "o/r", API: srv.URL + "/api", Web: srv.URL}
	ctx := context.Background()
	r, err := s.Latest(ctx)
	if err != nil || r.Tag != "v0.5.0" || r.Notes != "notes" {
		t.Fatalf("latest %+v %v", r, err)
	}
	apiDown = true
	if r, err = s.Latest(ctx); err != nil || r.Tag != "v0.5.0" {
		t.Fatalf("fallback %+v %v", r, err)
	}
	dir := t.TempDir()
	bins, err := s.Fetch(ctx, "v0.5.0", "amd64", dir, "sbnode")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bins["sbnode"].Path); string(b) != "NODE" || bins["sbnode"].Size != 4 {
		t.Fatalf("extracted %q", b)
	}
	checksum = strings.Repeat("a", 64)
	if _, err := s.Fetch(ctx, "v0.5.0", "amd64", dir, "sbnode"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("mismatch not detected: %v", err)
	}
	if _, err := s.Fetch(ctx, "v0.5.0/../x", "amd64", dir, "sbnode"); err == nil {
		t.Fatal("bad tag accepted")
	}
}
