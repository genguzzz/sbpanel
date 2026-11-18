package updater

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// osEnv drives the real service manager.
type osEnv struct {
	init  string
	since time.Time // log lines from before the upgrade aren't interesting
}

func (e osEnv) cmd(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (e osEnv) Restart(s string) error {
	var out []byte
	var err error
	if e.init == "openrc" {
		out, err = e.cmd(2*time.Minute, "rc-service", s, "restart")
	} else {
		out, err = e.cmd(2*time.Minute, "systemctl", "restart", s)
	}
	if err != nil {
		return &cmdErr{err, strings.TrimSpace(string(out))}
	}
	return nil
}

func (e osEnv) MainPID(s string) int {
	if e.init == "openrc" {
		if _, err := e.cmd(30*time.Second, "rc-service", s, "status"); err != nil {
			return 0
		}
		b, err := os.ReadFile("/run/" + s + ".pid")
		if err != nil {
			return 0
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return pid
	}
	out, err := e.cmd(30*time.Second, "systemctl", "show", "-p", "MainPID", "-p", "ActiveState", s)
	if err != nil {
		return 0
	}
	pid, active := 0, false
	for _, ln := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(ln), "=")
		switch k {
		case "MainPID":
			pid, _ = strconv.Atoi(v)
		case "ActiveState":
			active = v == "active"
		}
	}
	if !active {
		return 0
	}
	return pid
}

func (e osEnv) Logs(s string, n int) string {
	var out []byte
	if e.init == "openrc" {
		out, _ = e.cmd(10*time.Second, "tail", "-n", strconv.Itoa(n), "/var/log/"+s+".log")
	} else {
		args := []string{"-u", s, "-n", strconv.Itoa(n), "--no-pager", "-o", "cat"}
		if !e.since.IsZero() {
			args = append(args, "--since", e.since.Format("2006-01-02 15:04:05"))
		}
		out, _ = e.cmd(10*time.Second, "journalctl", args...)
	}
	return strings.TrimSpace(string(out))
}

func (osEnv) Sleep(d time.Duration) { time.Sleep(d) }
func (osEnv) Now() time.Time        { return time.Now() }

type cmdErr struct {
	err error
	out string
}

func (c *cmdErr) Error() string {
	if c.out == "" {
		return c.err.Error()
	}
	return c.err.Error() + ": " + c.out
}

func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
