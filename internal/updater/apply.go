package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Health modes: how the helper decides the new binary works.
const (
	// HealthMarker waits for the new process to write boot.json with the expected version
	// (MarkHealthy), then checks it is still the running process a few seconds later.
	HealthMarker = "marker"
	// HealthStable is for binaries that predate MarkHealthy (older releases): the service must be
	// active with an unchanged main PID for stableFor.
	HealthStable = "stable"
)

// Result states.
const (
	StateUpgraded   = "upgraded"
	StateRolledBack = "rolled_back"
	StateFailed     = "failed"
)

// Files in the upgrade directory.
const (
	fPlan   = "plan.json"
	fResult = "result.json"
	fBoot   = "boot.json"
	fLock   = "lock"
	fNew    = "new.bin"
	fHelper = "helper.bin"
	fLog    = "helper.log"
)

var (
	ErrBusy        = errors.New("an upgrade is already running")
	ErrUnsupported = errors.New("not running as a systemd or OpenRC service; upgrade with install.sh")
	ErrContainer   = errors.New("running in the sbpanel Docker image; upgrade by pulling a newer image tag and recreating the container")
)

// InContainer reports the sbpanel image (it sets SBP_CONTAINER=1). Its entrypoint fetches the
// release on every start, so a restart is the upgrade and the panel's binary swap doesn't apply.
// Being in some other container (/.dockerenv, LXC) is not enough: an install.sh setup with
// systemd inside one upgrades like any other.
func InContainer() bool { return os.Getenv("SBP_CONTAINER") == "1" }

// Plan describes one upgrade. Start writes it; the helper executes it.
type Plan struct {
	Service  string `json:"service"`  // systemd unit (without .service) or OpenRC service
	Init     string `json:"init"`     // systemd | openrc
	Target   string `json:"target"`   // installed binary, e.g. /usr/local/bin/sbnode
	Dir      string `json:"dir"`      // upgrade directory (writable by the service)
	From     string `json:"from"`     // version running now
	To       string `json:"to"`       // version the new binary must report ("" = don't check)
	Health   string `json:"health"`   // HealthMarker | HealthStable
	Timeout  int    `json:"timeout"`  // seconds to wait for the new process to become healthy
	Rollback bool   `json:"rollback"` // manual rollback: the "new" binary is the previous one
	Started  int64  `json:"started"`  // unix seconds; identifies the plan
}

// Result is what the helper leaves behind.
type Result struct {
	State    string `json:"state"` // upgraded | rolled_back | failed
	From     string `json:"from"`
	To       string `json:"to"`
	Rollback bool   `json:"rollback,omitempty"`
	Error    string `json:"error,omitempty"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished"`
}

// Status is the upgrade directory as seen by the service.
type Status struct {
	Running     bool    `json:"running"` // a plan is being executed now
	Plan        *Plan   `json:"plan,omitempty"`
	Last        *Result `json:"last,omitempty"`
	CanRollback bool    `json:"can_rollback"`
}

// Env is the service manager and clock; replaced in tests.
type Env interface {
	Restart(service string) error
	// MainPID returns the service's main process, 0 when it isn't running.
	MainPID(service string) int
	Logs(service string, lines int) string
	Sleep(d time.Duration)
	Now() time.Time
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(p string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// lockFresh: a lock older than this belongs to a helper that died.
const lockFresh = 15 * time.Minute

func locked(dir string, now time.Time) bool {
	st, err := os.Stat(filepath.Join(dir, fLock))
	return err == nil && now.Sub(st.ModTime()) < lockFresh
}

// ReadStatus reports the last result and whether an upgrade is in flight.
func ReadStatus(dir, target string) Status {
	var s Status
	var p Plan
	if readJSON(filepath.Join(dir, fPlan), &p) == nil {
		s.Plan = &p
	}
	var r Result
	if readJSON(filepath.Join(dir, fResult), &r) == nil {
		s.Last = &r
	}
	if s.Plan != nil && (s.Last == nil || s.Last.Started != s.Plan.Started) {
		// planned but no result yet: running unless the helper never got going
		s.Running = locked(dir, time.Now()) || time.Since(time.Unix(s.Plan.Started, 0)) < 2*time.Minute
	}
	if target != "" {
		_, err := os.Stat(target + ".prev")
		s.CanRollback = err == nil
	}
	return s
}

// MarkHealthy is called by a service once it is fully up. The helper waits for it.
func MarkHealthy(dir, version string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, fBoot), map[string]any{"version": version, "pid": os.Getpid(), "at": time.Now().Unix()})
}

// Executable is the running binary with symlinks resolved.
func Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// Detect finds the service manager and the unit the current process runs as.
func Detect(def string) (service, init string, err error) {
	if InContainer() {
		return "", "", ErrContainer
	}
	if st, e := os.Stat("/run/systemd/system"); e == nil && st.IsDir() {
		if _, e := exec.LookPath("systemd-run"); e != nil {
			return "", "", fmt.Errorf("%w (systemd-run not found)", ErrUnsupported)
		}
		b, e := os.ReadFile("/proc/self/cgroup")
		if e != nil {
			return "", "", ErrUnsupported
		}
		if u := unitFromCgroup(string(b)); u != "" {
			return u, "systemd", nil
		}
		return "", "", fmt.Errorf("%w (this process isn't a systemd service)", ErrUnsupported)
	}
	if _, e := exec.LookPath("rc-service"); e == nil {
		if _, e := os.Stat("/etc/init.d/" + def); e == nil {
			return def, "openrc", nil
		}
	}
	return "", "", ErrUnsupported
}

// unitFromCgroup picks "sbnode" out of lines like "0::/system.slice/sbnode.service".
func unitFromCgroup(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		i := strings.LastIndex(ln, ".service")
		if i < 0 {
			continue
		}
		u := ln[:i]
		u = u[strings.LastIndex(u, "/")+1:]
		if u != "" {
			return u
		}
	}
	return ""
}

// Start stages src as the new binary, writes the plan and launches the helper outside the
// service's sandbox. It returns once the helper is running; the service is restarted by it.
func Start(p Plan, src string) error {
	if p.Timeout <= 0 {
		p.Timeout = 90
	}
	if p.Health == "" {
		p.Health = HealthMarker
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	if locked(p.Dir, time.Now()) {
		return ErrBusy
	}
	if err := copyFile(src, filepath.Join(p.Dir, fNew), 0o755); err != nil {
		return fmt.Errorf("stage binary: %w", err)
	}
	// The helper is a copy of the running image (not whatever is installed at Target now, which
	// someone may have replaced without restarting): it knows this plan format and doesn't change
	// underneath.
	helper := filepath.Join(p.Dir, fHelper)
	if err := copyFile(runningImage(), helper, 0o755); err != nil {
		return fmt.Errorf("stage helper: %w", err)
	}
	if _, err := binVersion(helper); err != nil {
		return fmt.Errorf("staged helper doesn't run: %w", err)
	}
	p.Started = time.Now().Unix()
	if err := writeJSON(filepath.Join(p.Dir, fPlan), p); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(p.Dir, fLog), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	switch p.Init {
	case "systemd":
		// A transient unit runs as root outside our sandbox and outlives our restart.
		unit := fmt.Sprintf("%s-upgrade-%d", p.Service, p.Started)
		cmd := exec.Command("systemd-run", "--unit="+unit, "--description=sbpanel upgrade of "+p.Service,
			helper, "upgrade-helper", "--dir", p.Dir)
		cmd.Stdout, cmd.Stderr = logf, logf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("systemd-run: %w", err)
		}
	case "openrc":
		// No sandbox under OpenRC; a new session keeps the helper alive when the service is restarted.
		cmd := exec.Command(helper, "upgrade-helper", "--dir", p.Dir)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.SysProcAttr = detached()
		if err := cmd.Start(); err != nil {
			return err
		}
		cmd.Process.Release()
	default:
		return ErrUnsupported
	}
	return nil
}

// Helper executes the plan in dir (the "upgrade-helper" subcommand of both binaries).
func Helper(dir string) error {
	var p Plan
	if err := readJSON(filepath.Join(dir, fPlan), &p); err != nil {
		return err
	}
	err := run(p, osEnv{init: p.Init, since: time.Unix(p.Started, 0)})
	var r Result
	if readJSON(filepath.Join(dir, fResult), &r) == nil && r.Started == p.Started {
		return nil // the outcome (including a rollback) is recorded; the helper did its job
	}
	return err
}

// runningImage is the file of the running process: /proc/self/exe on Linux, which stays valid
// even when the path was replaced since we started.
func runningImage() string {
	if _, err := os.Stat("/proc/self/exe"); err == nil {
		return "/proc/self/exe"
	}
	p, _ := Executable()
	return p
}

// stableFor: how long a process must survive (same PID, active) to count as up.
const (
	stableFor  = 20 * time.Second
	settleFor  = 5 * time.Second
	pollEvery  = time.Second
	maxLogTail = 20
)

func run(p Plan, env Env) error {
	lock := filepath.Join(p.Dir, fLock)
	if locked(p.Dir, env.Now()) {
		return ErrBusy
	}
	if err := os.WriteFile(lock, []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		return err
	}
	defer os.Remove(lock)
	res := Result{From: p.From, To: p.To, Rollback: p.Rollback, Started: p.Started}
	finish := func(state string, err error) error {
		res.State, res.Finished = state, env.Now().Unix()
		if err != nil {
			res.Error = err.Error()
		}
		os.Remove(filepath.Join(p.Dir, fNew))
		os.Remove(filepath.Join(p.Dir, fHelper)) // this process keeps running from the unlinked file
		logf("%s: %s %s -> %s %s", p.Service, state, p.From, p.To, res.Error)
		if werr := writeJSON(filepath.Join(p.Dir, fResult), res); werr != nil {
			return werr
		}
		return err
	}
	staged := filepath.Join(p.Dir, fNew)
	// 1. the staged binary must run and report the version we expect
	got, err := binVersion(staged)
	if err != nil {
		return finish(StateFailed, fmt.Errorf("new binary doesn't run: %w", err))
	}
	if p.To != "" && got != p.To {
		return finish(StateFailed, fmt.Errorf("new binary reports %s, expected %s", got, p.To))
	}
	res.To = got
	// 2. keep the running binary, swap the new one in atomically
	prev := p.Target + ".prev"
	if err := copyFile(p.Target, prev+".tmp", 0o755); err != nil {
		return finish(StateFailed, fmt.Errorf("back up %s: %w", p.Target, err))
	}
	if err := install(staged, p.Target); err != nil {
		os.Remove(prev + ".tmp")
		return finish(StateFailed, fmt.Errorf("install: %w", err))
	}
	if err := os.Rename(prev+".tmp", prev); err != nil {
		logf("keep %s: %v", prev, err)
	}
	// 3. restart and wait for the new process to prove itself
	os.Remove(filepath.Join(p.Dir, fBoot))
	reason := ""
	if err := env.Restart(p.Service); err != nil {
		reason = "restart: " + err.Error()
	} else if err := waitHealthy(env, p, p.Health, got); err != nil {
		reason = err.Error()
	}
	if reason == "" {
		return finish(StateUpgraded, nil)
	}
	// 4. roll back to the binary that was running (it launched this helper, so it writes the marker)
	logs := env.Logs(p.Service, maxLogTail)
	if err := install(prev, p.Target); err != nil {
		return finish(StateFailed, fmt.Errorf("%s; rollback failed: %v", reason, err))
	}
	os.Remove(filepath.Join(p.Dir, fBoot))
	var rbErr error
	if err := env.Restart(p.Service); err != nil {
		rbErr = err
	} else {
		rbErr = waitHealthy(env, p, HealthMarker, p.From)
	}
	msg := reason
	if logs != "" {
		msg += "\n--- service log ---\n" + logs
	}
	if rbErr != nil {
		return finish(StateFailed, fmt.Errorf("%s\nrolled back, but %s did not come back: %v", msg, p.From, rbErr))
	}
	return finish(StateRolledBack, errors.New(msg))
}

func waitHealthy(env Env, p Plan, mode, version string) error {
	deadline := env.Now().Add(time.Duration(p.Timeout) * time.Second)
	if mode == HealthStable {
		pid, since := 0, time.Time{}
		for env.Now().Before(deadline) {
			cur := env.MainPID(p.Service)
			switch {
			case cur == 0:
				pid = 0
			case cur != pid:
				pid, since = cur, env.Now()
			case env.Now().Sub(since) >= stableFor:
				return nil
			}
			env.Sleep(pollEvery)
		}
		return fmt.Errorf("%s did not stay running for %s within %ds", p.Service, stableFor, p.Timeout)
	}
	for env.Now().Before(deadline) {
		var b struct {
			Version string `json:"version"`
			PID     int    `json:"pid"`
		}
		if readJSON(filepath.Join(p.Dir, fBoot), &b) == nil && b.Version == version && b.PID > 0 {
			// up; make sure it doesn't crash right after
			env.Sleep(settleFor)
			if env.MainPID(p.Service) == b.PID {
				return nil
			}
			return fmt.Errorf("%s %s started but exited right after (pid %d)", p.Service, version, b.PID)
		}
		env.Sleep(pollEvery)
	}
	return fmt.Errorf("%s %s did not report healthy within %ds", p.Service, version, p.Timeout)
}

// binVersion runs "<bin> version" and returns the version word ("sbnode v0.4.0 (sing-box …)").
func binVersion(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return "", fmt.Errorf("unexpected version output %q", out)
	}
	return f[1], nil
}

// BinaryVersion is binVersion for callers outside the helper.
func BinaryVersion(bin string) (string, error) { return binVersion(bin) }

// install copies src next to dst and renames it over dst (atomic on the same filesystem).
func install(src, dst string) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[upgrade] "+time.Now().Format("2006/01/02 15:04:05 ")+format+"\n", a...)
}

// MarkerSince is the first release whose binaries call MarkHealthy.
const MarkerSince = "v0.4.0"

// HealthFor picks how to judge a binary of the given version: releases before MarkerSince never
// write the marker, so they are judged by staying up; dev builds come from this code.
func HealthFor(version string) string {
	if c, ok := Compare(version, MarkerSince); ok && c < 0 {
		return HealthStable
	}
	return HealthMarker
}
