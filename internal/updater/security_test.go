package updater

import (
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

// releaseServer serves one release; sum overrides the archive's listed checksum.
func releaseServer(archive []byte, sum string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/o/r/releases/download/v0.5.0/checksums.txt"):
			fmt.Fprintf(w, "%s  sbpanel_v0.5.0_linux_amd64.tar.gz\n", sum)
		case strings.HasSuffix(p, "/o/r/releases/download/v0.5.0/sbpanel_v0.5.0_linux_amd64.tar.gz"):
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}
}

func shaHex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestChecksumsBypassMirror(t *testing.T) {
	good := tarGz(t, map[string]string{"sbpanel_v0.5.0_linux_amd64/sbnode": "NODE"})
	evil := tarGz(t, map[string]string{"sbpanel_v0.5.0_linux_amd64/sbnode": "EVIL"})
	direct := httptest.NewServer(releaseServer(good, shaHex(good)))
	defer direct.Close()
	// the mirror serves a tampered archive with a matching checksums.txt
	mirror := httptest.NewTLSServer(releaseServer(evil, shaHex(evil)))
	defer mirror.Close()
	s := Source{Repo: "o/r", Web: direct.URL, Mirror: mirror.URL + "/", Client: mirror.Client()}
	_, err := s.Fetch(context.Background(), "v0.5.0", "amd64", t.TempDir(), "sbnode")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered mirror archive accepted: %v", err)
	}
	// a direct 404 is an answer, not an outage: no fallback to the mirror's checksums
	gone := httptest.NewServer(http.NotFoundHandler())
	defer gone.Close()
	s.Web = gone.URL
	if _, err := s.Fetch(context.Background(), "v0.5.0", "amd64", t.TempDir(), "sbnode"); err == nil {
		t.Fatal("fell back to the mirror after a 404")
	}
}

func TestChecksumsMirrorFallback(t *testing.T) {
	good := tarGz(t, map[string]string{"sbpanel_v0.5.0_linux_amd64/sbnode": "NODE"})
	mirror := httptest.NewTLSServer(releaseServer(good, shaHex(good)))
	defer mirror.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close() // GitHub unreachable: connection refused
	s := Source{Repo: "o/r", Web: downURL, Mirror: mirror.URL + "/", Client: mirror.Client()}
	bins, err := s.Fetch(context.Background(), "v0.5.0", "amd64", t.TempDir(), "sbnode")
	if err != nil {
		t.Fatal(err)
	}
	if bins["sbnode"].Note != MirrorChecksumNote {
		t.Fatalf("fallback not reported: %+v", bins["sbnode"])
	}
}
