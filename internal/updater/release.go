// Package updater checks GitHub releases and replaces the running sbmaster / sbnode binary
// with automatic rollback.
//
// The services run sandboxed (ProtectSystem=strict/full), so they cannot write /usr/local/bin.
// The upgrade is therefore split in two:
//
//  1. Inside the service: download, verify (sha256 from the release's checksums.txt), stage the
//     new binary in the service's own data directory, and launch a helper outside the sandbox
//     (a systemd-run transient unit, or a detached process under OpenRC). See Start.
//  2. The helper (a copy of the *current* binary, so it keeps working whatever gets installed)
//     keeps the old binary as <target>.prev, swaps the new one in atomically, restarts the
//     service and waits for it to report healthy. If it doesn't within the timeout, or it
//     crashes right after, the helper puts the previous binary back and restarts again. See Helper.
package updater

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are published to.
const Repo = "genguzzz/sbpanel"

// maxArchive caps a release download (the archives are ~35 MB).
const maxArchive = 300 << 20

// Release is one published version.
type Release struct {
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	Notes     string `json:"notes"`
	URL       string `json:"url"`
	Published string `json:"published,omitempty"` // RFC 3339; empty when only the tag is known
}

// Source is where releases come from.
type Source struct {
	Repo   string       // owner/name; default Repo
	Mirror string       // prefix for github.com downloads, e.g. https://ghproxy.example/ (as install.sh --mirror)
	API    string       // default https://api.github.com
	Web    string       // default https://github.com
	Client *http.Client // default: 60 s timeout for metadata, none for downloads (ctx bounds them)
}

func (s Source) repo() string { return or(s.Repo, Repo) }
func (s Source) api() string  { return strings.TrimRight(or(s.API, "https://api.github.com"), "/") }
func (s Source) web() string  { return s.Mirror + s.direct() }

// direct is the release host without the mirror prefix.
func (s Source) direct() string { return strings.TrimRight(or(s.Web, "https://github.com"), "/") }

// checksumDirectTimeout bounds the direct checksums.txt attempt when a mirror is set, so
// mirror users where GitHub is blocked don't wait long before the fallback.
var checksumDirectTimeout = 15 * time.Second

// MirrorChecksumNote is set on Binary.Note when GitHub was unreachable and checksums.txt had
// to come from the mirror.
const MirrorChecksumNote = "checksums.txt came from the mirror: integrity depends on the mirror"

// statusError is a non-200 answer: the host was reachable.
type statusError struct {
	url    string
	status string
}

func (e *statusError) Error() string { return "GET " + e.url + ": " + e.status }

// client is the configured client with a redirect policy that never leaves HTTPS: the
// archive is only as trustworthy as the checksums.txt fetched over the same path.
func (s Source) client() *http.Client {
	c := *http.DefaultClient
	if s.Client != nil {
		c = *s.Client
	}
	next := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return fmt.Errorf("refusing redirect from https to %s", req.URL.Redacted())
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &c
}

// checkMirror: a mirror serves the archive and its checksums.txt alike, so the connection
// to it must at least be authenticated.
func (s Source) checkMirror() error {
	if s.Mirror != "" && !strings.HasPrefix(strings.ToLower(s.Mirror), "https://") {
		return fmt.Errorf("download mirror %q must be an https:// URL", s.Mirror)
	}
	return nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var tagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

// describeRe matches `git describe` builds (v0.3.0-4-g0461cfa[-dirty]): commits after a tag, not a prerelease.
var describeRe = regexp.MustCompile(`-\d+-g[0-9a-f]{4,}(?:-dirty)?$|-dirty$`)

// ValidTag reports whether t is a release tag (vX.Y.Z or vX.Y.Z-pre). Tags end up in URLs and paths.
// Local builds named by git describe are not releases.
func ValidTag(t string) bool { return tagRe.MatchString(t) && !describeRe.MatchString(t) }

func (s Source) get(ctx context.Context, url string) (*http.Response, error) {
	if err := s.checkMirror(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sbpanel-updater")
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &statusError{url, resp.Status}
	}
	return resp, nil
}

// Latest returns the newest non-prerelease release. It asks the GitHub API first (for notes);
// when that is unreachable or rate-limited it falls back to the releases/latest redirect on the
// (possibly mirrored) web host, which only yields the tag.
func (s Source) Latest(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, apiErr := s.get(ctx, s.api()+"/repos/"+s.repo()+"/releases/latest")
	if apiErr == nil {
		defer resp.Body.Close()
		var r struct {
			Tag       string    `json:"tag_name"`
			Name      string    `json:"name"`
			Body      string    `json:"body"`
			URL       string    `json:"html_url"`
			Published time.Time `json:"published_at"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
			return nil, fmt.Errorf("release metadata: %w", err)
		}
		if !ValidTag(r.Tag) {
			return nil, fmt.Errorf("unexpected release tag %q", r.Tag)
		}
		rel := &Release{Tag: r.Tag, Name: r.Name, Notes: r.Body, URL: r.URL}
		if !r.Published.IsZero() {
			rel.Published = r.Published.UTC().Format(time.RFC3339)
		}
		return rel, nil
	}
	resp, err := s.get(ctx, s.web()+"/"+s.repo()+"/releases/latest")
	if err != nil {
		return nil, fmt.Errorf("%v; fallback: %w", apiErr, err)
	}
	resp.Body.Close()
	tag := path.Base(resp.Request.URL.Path)
	if !ValidTag(tag) {
		return nil, fmt.Errorf("%v; fallback redirected to %s", apiErr, resp.Request.URL)
	}
	return &Release{Tag: tag, URL: "https://github.com/" + s.repo() + "/releases/tag/" + tag}, nil
}

// Compare compares two versions "vX.Y.Z[-pre]". ok is false when either isn't one
// (dev builds such as "dev-1118.0054-abc").
func Compare(a, b string) (c int, ok bool) {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			if pa.n[i] < pb.n[i] {
				return -1, true
			}
			return 1, true
		}
	}
	// a release sorts after any of its prereleases
	switch {
	case pa.pre == pb.pre:
		return 0, true
	case pa.pre == "":
		return 1, true
	case pb.pre == "":
		return -1, true
	case pa.pre < pb.pre:
		return -1, true
	default:
		return 1, true
	}
}

type ver struct {
	n   [3]int
	pre string
}

func parse(v string) (ver, bool) {
	if !ValidTag(v) {
		return ver{}, false
	}
	core, pre, _ := strings.Cut(v[1:], "-")
	var x ver
	for i, p := range strings.Split(core, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return ver{}, false
		}
		x.n[i] = n
	}
	x.pre = pre
	return x, true
}

// Binary is a verified binary extracted from a release archive.
type Binary struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Note   string `json:"note,omitempty"` // e.g. MirrorChecksumNote
}

// Fetch downloads sbpanel_<tag>_linux_<arch>.tar.gz and checksums.txt, verifies the archive
// and extracts the named binaries (sbmaster, sbnode) into dir as <name>.
func (s Source) Fetch(ctx context.Context, tag, arch, dir string, names ...string) (map[string]Binary, error) {
	if !ValidTag(tag) {
		return nil, fmt.Errorf("bad version %q", tag)
	}
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("unsupported architecture %q", arch)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("sbpanel_%s_linux_%s", tag, arch)
	rel := "/" + s.repo() + "/releases/download/" + tag + "/"
	base := s.web() + rel
	want, note, err := s.releaseChecksum(ctx, rel, name+".tar.gz")
	if err != nil {
		return nil, err
	}
	archive := filepath.Join(dir, name+".tar.gz.part")
	defer os.Remove(archive)
	sum, err := s.download(ctx, base+name+".tar.gz", archive)
	if err != nil {
		return nil, err
	}
	if sum != want {
		return nil, fmt.Errorf("checksum mismatch for %s.tar.gz: got %s, want %s", name, sum, want)
	}
	bins, err := extract(archive, name, dir, names)
	if err == nil && note != "" {
		logf("%s %s: %s", name, tag, note)
		for n, b := range bins {
			b.Note = note
			bins[n] = b
		}
	}
	return bins, err
}

// releaseChecksum reads the archive's sha256. With a mirror, checksums.txt comes from the
// release host directly, so the mirror can't serve a tampered archive with a matching sum;
// only when that host is unreachable (a network error or timeout, not an HTTP error) is the
// mirror's copy used, and the returned note says so.
func (s Source) releaseChecksum(ctx context.Context, rel, file string) (sum, note string, err error) {
	if s.Mirror == "" {
		sum, err = s.checksum(ctx, s.direct()+rel+"checksums.txt", file)
		return sum, "", err
	}
	dctx, cancel := context.WithTimeout(ctx, checksumDirectTimeout)
	sum, err = s.checksum(dctx, s.direct()+rel+"checksums.txt", file)
	cancel()
	var se *statusError
	if err == nil || ctx.Err() != nil || errors.As(err, &se) || !errors.Is(err, errFetch) {
		return sum, "", err
	}
	sum, err = s.checksum(ctx, s.web()+rel+"checksums.txt", file)
	return sum, MirrorChecksumNote, err
}

// errFetch marks a checksums.txt that couldn't be fetched (as opposed to one without the file).
var errFetch = errors.New("checksums")

func (s Source) checksum(ctx context.Context, url, file string) (string, error) {
	resp, err := s.get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errFetch, err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == file && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in checksums.txt", file)
}

func (s Source) download(ctx context.Context, url, dst string) (string, error) {
	resp, err := s.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if n > maxArchive {
		return "", fmt.Errorf("download %s: larger than %d MB", url, maxArchive>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), f.Close()
}

func extract(archive, prefix, dir string, names []string) (map[string]Binary, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[prefix+"/"+n] = true
	}
	out := map[string]Binary{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archive: %w", err)
		}
		if !want[h.Name] || h.Typeflag != tar.TypeReg {
			continue
		}
		n := path.Base(h.Name)
		b, err := writeVerified(filepath.Join(dir, n), io.LimitReader(tr, maxArchive))
		if err != nil {
			return nil, err
		}
		out[n] = b
	}
	for _, n := range names {
		if _, ok := out[n]; !ok {
			return nil, fmt.Errorf("archive has no %s/%s", prefix, n)
		}
	}
	return out, nil
}

// writeVerified writes r to dst (via a temp file, mode 0755) and returns its hash and size.
func writeVerified(dst string, r io.Reader) (Binary, error) {
	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return Binary{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return Binary{}, err
	}
	return Binary{Path: dst, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

// WriteBinary stores a binary received from elsewhere (a node receiving it from the master).
func WriteBinary(dst string, r io.Reader) (Binary, error) {
	return writeVerified(dst, io.LimitReader(r, maxArchive))
}

// FileSHA256 hashes a file.
func FileSHA256(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}
