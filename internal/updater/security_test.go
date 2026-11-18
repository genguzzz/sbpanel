package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "up")
	if err := privateDir(dir, true); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o777)
	if err := privateDir(dir, false); err == nil || !strings.Contains(err.Error(), "other users") {
		t.Fatalf("world-writable dir accepted by the helper: %v", err)
	}
	if err := privateDir(dir, true); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode not tightened: %v", fi.Mode())
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(dir, link)
	if err := privateDir(link, true); err == nil {
		t.Fatal("symlinked upgrade dir accepted")
	}
}

func TestHelperRefusesSharedDir(t *testing.T) {
	p, env := setup(t, "v0.4.0", "v0.5.0")
	os.Chmod(p.Dir, 0o777)
	if err := run(p, env); err == nil || !strings.Contains(err.Error(), "other users") {
		t.Fatalf("helper ran from a shared dir: %v", err)
	}
	if got := installed(t, p); got != "v0.4.0" {
		t.Fatalf("target replaced: %s", got)
	}
}

func TestMirrorMustBeHTTPS(t *testing.T) {
	s := Source{Mirror: "http://ghproxy.example/"}
	if _, err := s.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain-http mirror accepted: %v", err)
	}
}

func TestNoRedirectToPlainHTTP(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) }))
	defer plain.Close()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	}))
	defer tlsSrv.Close()
	s := Source{Client: tlsSrv.Client()}
	if _, err := s.get(context.Background(), tlsSrv.URL+"/checksums.txt"); err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("https -> http redirect followed: %v", err)
	}
}
