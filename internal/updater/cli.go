package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CLI is a shell-driven upgrade ("sbmaster upgrade", "sbnode upgrade"): the same verified
// download, helper, health check and rollback as the panel, waiting for the outcome.
type CLI struct {
	Binary   string // sbmaster | sbnode (name inside the release archive)
	Service  string // service name; default Binary
	Dir      string // the service's data directory; the upgrade directory goes inside it
	Version  string // target tag; "" = latest
	Rollback bool
	Mirror   string
	Timeout  int // seconds for the new process to become healthy
}

// Run performs the upgrade and returns the helper's result.
func (c CLI) Run(ctx context.Context, say func(string, ...any)) (*Result, error) {
	service := or(c.Service, c.Binary)
	initSys := ""
	if InContainer() {
		return nil, ErrContainer
	}
	if st, err := os.Stat("/run/systemd/system"); err == nil && st.IsDir() {
		initSys = "systemd"
	} else if _, err := exec.LookPath("rc-service"); err == nil {
		initSys = "openrc"
	} else {
		return nil, ErrUnsupported
	}
	// the binary the service runs (this command may be started from another path, e.g. <bin>.prev)
	target, err := serviceBinary(initSys, service)
	if err != nil {
		return nil, err
	}
	from, err := binVersion(target)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", target, err)
	}
	dir := filepath.Join(c.Dir, "upgrade")
	var src, to string
	if c.Rollback {
		src = target + ".prev"
		if to, err = binVersion(src); err != nil {
			return nil, fmt.Errorf("no usable previous binary %s: %w", src, err)
		}
	} else {
		s := Source{Mirror: c.Mirror, Client: nil}
		tag := c.Version
		if tag == "" {
			r, err := s.Latest(ctx)
			if err != nil {
				return nil, err
			}
			tag = r.Tag
		}
		if tag == from {
			return nil, fmt.Errorf("already running %s", tag)
		}
		say("downloading and verifying %s (%s)", tag, runtime.GOARCH)
		bins, err := s.Fetch(ctx, tag, runtime.GOARCH, filepath.Join(dir, "cli"), c.Binary)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(filepath.Join(dir, "cli"))
		if n := bins[c.Binary].Note; n != "" {
			say("warning: %s", n)
		}
		src, to = bins[c.Binary].Path, tag
	}
	p := Plan{Service: service, Init: initSys, Target: target, Dir: dir, From: from, To: to,
		Health: HealthFor(to), Timeout: c.Timeout, Rollback: c.Rollback}
	say("%s %s -> %s: restarting %s (health: %s, rollback on failure)", map[bool]string{true: "rollback", false: "upgrade"}[c.Rollback], from, to, service, p.Health)
	if err := Start(p, src); err != nil {
		return nil, err
	}
	var plan Plan
	readJSON(filepath.Join(dir, fPlan), &plan)
	wait := time.Duration(2*max(c.Timeout, 90)+60) * time.Second
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		var r Result
		if readJSON(filepath.Join(dir, fResult), &r) == nil && r.Started == plan.Started {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("no result after %s; see %s", wait, filepath.Join(dir, fLog))
}

// serviceBinary is the executable of the running service (its main process), else this one.
func serviceBinary(initSys, service string) (string, error) {
	if pid := (osEnv{init: initSys}).MainPID(service); pid > 0 {
		if p, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
			return strings.TrimSuffix(p, " (deleted)"), nil
		}
	}
	return Executable()
}
