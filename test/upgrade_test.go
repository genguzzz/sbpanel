package test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"sbpanel/internal/node"
	"sbpanel/internal/updater"
)

// fakeReleases serves a GitHub-like release v9.9.0 with stand-in binaries.
func fakeReleases(t *testing.T, arch string, sbnode []byte) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	dir := "sbpanel_v9.9.0_linux_" + arch
	for n, c := range map[string][]byte{dir + "/sbmaster": []byte("#!/bin/sh\necho sbmaster v9.9.0\n"), dir + "/sbnode": sbnode} {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write(c)
	}
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/repos/o/r/releases/latest":
			fmt.Fprint(w, `{"tag_name":"v9.9.0","name":"sbpanel v9.9.0","body":"- faster","html_url":"https://example/v9.9.0","published_at":"2026-11-18T00:00:00Z"}`)
		case "/o/r/releases/download/v9.9.0/checksums.txt":
			fmt.Fprintf(w, "%s  %s.tar.gz\n", hex.EncodeToString(sum[:]), dir)
		case "/o/r/releases/download/v9.9.0/" + dir + ".tar.gz":
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SBP_UPDATE_REPO", "o/r")
	t.Setenv("SBP_UPDATE_API", srv.URL+"/api")
	t.Setenv("SBP_UPDATE_WEB", srv.URL)
}

type updateState struct {
	Current   string `json:"current"`
	HasUpdate bool   `json:"has_update"`
	Supported bool   `json:"supported"`
	Latest    *struct {
		Tag   string `json:"tag"`
		Notes string `json:"notes"`
	} `json:"latest"`
	Nodes []struct {
		ID        string `json:"id"`
		Supported bool   `json:"supported"`
		HasUpdate bool   `json:"has_update"`
		Arch      string `json:"arch"`
		Status    *struct {
			State string `json:"state"`
			To    string `json:"to"`
			Error string `json:"error"`
		} `json:"status"`
	} `json:"nodes"`
}

// The master finds the release, verifies and caches it, streams sbnode to the node over its
// mTLS connection, and the node checks the hash and hands a plan to the helper.
func TestNodeUpgradeFromPanel(t *testing.T) {
	sbnode := []byte("#!/bin/sh\necho 'sbnode v9.9.0 (sing-box test)'\n" + strings.Repeat("#padding\n", 300000)) // > 1 chunk
	fakeReleases(t, runtime.GOARCH, sbnode)
	var mu sync.Mutex
	var plan *updater.Plan
	var staged []byte
	node.DetectService = func(string) (string, string, error) { return "sbnode", "systemd", nil }
	node.StartUpgrade = func(p updater.Plan, src string) error {
		b, err := os.ReadFile(src)
		mu.Lock()
		defer mu.Unlock()
		plan, staged = &p, b
		return err
	}
	t.Cleanup(func() { node.DetectService, node.StartUpgrade = updater.Detect, updater.Start })

	e := newEnv(t)
	var st updateState
	e.api.do("POST", "/api/update/check", nil, &st)
	// a dev build isn't "behind" a release (it may be newer code), but the release is offered
	if st.Latest == nil || st.Latest.Tag != "v9.9.0" || st.Latest.Notes != "- faster" || st.HasUpdate || st.Current != "dev" {
		t.Fatalf("check: %+v", st)
	}
	if len(st.Nodes) != 1 || !st.Nodes[0].Supported {
		t.Fatalf("node: %+v", st.Nodes)
	}
	// not a systemd service here: the panel can't upgrade itself, and says so
	if st.Supported {
		t.Fatal("panel claims it can self-upgrade outside a service manager")
	}
	e.api.expect(409, "POST", "/api/update/master", map[string]any{})

	// rollback without a previous binary is refused up front
	body := e.api.expect(202, "POST", "/api/update/nodes", map[string]any{"ids": []string{e.nodeID}, "rollback": true})
	if !strings.Contains(body, "no previous binary") {
		t.Fatalf("rollback: %s", body)
	}
	e.api.expect(400, "POST", "/api/update/nodes", map[string]any{"ids": []string{"nope"}})
	e.api.expect(400, "POST", "/api/update/nodes", map[string]any{"ids": []string{e.nodeID}, "version": "../x"})

	e.api.expect(202, "POST", "/api/update/nodes", map[string]any{"outdated": true})
	waitFor(t, "plan handed to the helper", 30*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return plan != nil })
	mu.Lock()
	if !bytes.Equal(staged, sbnode) || plan.To != "v9.9.0" || plan.Health != updater.HealthMarker || plan.Service != "sbnode" || plan.Rollback {
		t.Fatalf("plan %+v, staged %d bytes", plan, len(staged))
	}
	mu.Unlock()
	waitFor(t, "node reports applying", 10*time.Second, func() bool {
		e.api.do("GET", "/api/update", nil, &st)
		return st.Nodes[0].Status != nil && st.Nodes[0].Status.State == "applying" && st.Nodes[0].Status.To == "v9.9.0"
	})
	// a second request while the node is applying is refused
	body = e.api.expect(202, "POST", "/api/update/nodes", map[string]any{"ids": []string{e.nodeID}})
	if !strings.Contains(body, "already running") {
		t.Fatalf("busy: %s", body)
	}

}
