// dist builds release archives: dist/sbpanel_<ver>_linux_<arch>.tar.gz with sbmaster,
// sbnode, install.sh, deploy/, README.md and LICENSE, plus checksums.txt and NOTES.md.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	version := flag.String("version", "dev", "release version (tag)")
	singbox := flag.String("singbox", "", "sing-box version")
	ldflags := flag.String("ldflags", "", "go -ldflags")
	tags := flag.String("tags", "", "go build tags")
	arches := flag.String("arches", "amd64 arm64", "architectures")
	flag.Parse()

	if err := os.MkdirAll("dist", 0o755); err != nil {
		log.Fatal(err)
	}
	// remove only our own previous outputs
	old, _ := filepath.Glob("dist/sbpanel_*.tar.gz")
	for _, f := range append(old, "dist/checksums.txt", "dist/NOTES.md") {
		os.Remove(f)
	}
	var sums []string
	for _, arch := range strings.Fields(*arches) {
		stage, err := os.MkdirTemp("", "sbpanel-dist-")
		if err != nil {
			log.Fatal(err)
		}
		for _, bin := range []string{"sbmaster", "sbnode"} {
			cmd := exec.Command("go", "build", "-trimpath", "-tags", *tags, "-ldflags", *ldflags, "-o", filepath.Join(stage, bin), "./cmd/"+bin)
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				log.Fatalf("build %s/%s: %v", bin, arch, err)
			}
		}
		name := fmt.Sprintf("sbpanel_%s_linux_%s", *version, arch)
		out := filepath.Join("dist", name+".tar.gz")
		files := map[string]string{ // archive path -> source
			name + "/sbmaster": filepath.Join(stage, "sbmaster"),
			name + "/sbnode":   filepath.Join(stage, "sbnode"),
		}
		for _, f := range []string{"install.sh", "README.md", "LICENSE"} {
			files[name+"/"+f] = f
		}
		filepath.WalkDir("deploy", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files[name+"/"+filepath.ToSlash(p)] = p
			}
			return nil
		})
		if err := writeTarGz(out, files); err != nil {
			log.Fatal(err)
		}
		os.RemoveAll(stage) // our own temp dir from MkdirTemp
		sum, err := fileSHA256(out)
		if err != nil {
			log.Fatal(err)
		}
		sums = append(sums, sum+"  "+filepath.Base(out))
		log.Printf("built %s", out)
	}
	os.WriteFile("dist/checksums.txt", []byte(strings.Join(sums, "\n")+"\n"), 0o644)
	notes := fmt.Sprintf(`sbpanel %s (sing-box %s)

Install the panel (master):

    curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- master

Add a node: copy the command from the panel (节点 → 添加节点), or:

    curl -fsSL https://raw.githubusercontent.com/genguzzz/sbpanel/main/install.sh | sh -s -- node --master HOST:9443 --token TOKEN --ca-sha256 FP

Upgrade everything on a machine: `+"`install.sh upgrade`"+`, or from the panel (Settings → Updates).

Docker (amd64 / arm64): `+"`ghcr.io/genguzzz/sbpanel:%s`"+` runs this release (see "Docker" in the README):

    docker run -d --name sbmaster --restart unless-stopped --network host -e SBM_WEB=127.0.0.1:8080 \
      -e SBM_PUBLIC_GRPC=panel.example.com:9443 -v sbmaster:/var/lib/sbmaster ghcr.io/genguzzz/sbpanel:%s
`, *version, *singbox, *version, *version)
	os.WriteFile("dist/NOTES.md", []byte(notes), 0o644)
}

func writeTarGz(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		src := files[n]
		st, err := os.Stat(src)
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if st.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: mode, Size: st.Size(), ModTime: st.ModTime()}); err != nil {
			return err
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, in)
		in.Close()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
