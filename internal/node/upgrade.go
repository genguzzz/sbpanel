package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"sbpanel/internal/pb"
	"sbpanel/internal/updater"
)

// Test hooks: the service manager lookup and the hand-off to the helper.
var (
	DetectService = updater.Detect
	StartUpgrade  = updater.Start
)

// healthTimeout: how long the new sbnode has to reconnect to the master before it is rolled back.
const healthTimeout = 120

func (a *Agent) upgradeDir() string { return filepath.Join(a.cfg.Dir, "upgrade") }

// markHealthy tells a running upgrade helper that this binary works: it is connected to the
// master and serving its configuration. Called after every successful Hello; cheap.
func (a *Agent) markHealthy() {
	if a.applyErr.Load().(string) != "" {
		return // the cached configuration doesn't apply: not healthy
	}
	if _, err := os.Stat(filepath.Join(a.upgradeDir(), "plan.json")); err != nil {
		return // never upgraded from the panel
	}
	if err := updater.MarkHealthy(a.upgradeDir(), Version); err != nil {
		a.log.Printf("upgrade: mark healthy: %v", err)
	}
}

// upgradeStatus is the last upgrade result (or the one in flight), for the master.
func (a *Agent) upgradeStatus() *pb.UpgradeStatus {
	target, _ := updater.Executable()
	st := updater.ReadStatus(a.upgradeDir(), target)
	out := &pb.UpgradeStatus{CanRollback: st.CanRollback}
	if st.Last != nil {
		out.State, out.From, out.To, out.Error = st.Last.State, st.Last.From, st.Last.To, st.Last.Error
		out.Started, out.Finished, out.Rollback = st.Last.Started, st.Last.Finished, st.Last.Rollback
	}
	if st.Running && st.Plan != nil {
		out.State, out.From, out.To, out.Error = "applying", st.Plan.From, st.Plan.To, ""
		out.Started, out.Finished, out.Rollback = st.Plan.Started, 0, st.Plan.Rollback
	}
	return out
}

// upgrade handles MasterMsg.Upgrade: fetch the binary from the master, verify it, and hand
// over to the helper, which swaps it in, restarts sbnode and rolls back if it doesn't come up.
func (a *Agent) upgrade(ctx context.Context, m *pb.Upgrade, send func(*pb.NodeMsg) error) {
	if !a.upgrading.CompareAndSwap(false, true) {
		return
	}
	defer a.upgrading.Store(false)
	started := time.Now().Unix()
	report := func(state, to, errMsg string) {
		st := &pb.UpgradeStatus{State: state, From: Version, To: to, Error: errMsg, Started: started, Rollback: m.Rollback}
		if state == updater.StateFailed {
			st.Finished = time.Now().Unix()
		}
		send(&pb.NodeMsg{M: &pb.NodeMsg_Upgrade{Upgrade: st}})
	}
	fail := func(to string, err error) {
		a.log.Printf("upgrade to %s: %v", to, err)
		report(updater.StateFailed, to, err.Error())
	}
	service, initSys, err := DetectService("sbnode")
	if err != nil {
		fail(m.Version, err)
		return
	}
	target, err := updater.Executable()
	if err != nil {
		fail(m.Version, err)
		return
	}
	dir := a.upgradeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail(m.Version, err)
		return
	}
	var src, to string
	if m.Rollback {
		src = target + ".prev"
		if to, err = updater.BinaryVersion(src); err != nil {
			fail("", fmt.Errorf("no usable previous binary: %w", err))
			return
		}
	} else {
		to = m.Version
		report("downloading", to, "")
		if src, err = a.fetchUpgrade(ctx, m, filepath.Join(dir, "download.bin")); err != nil {
			fail(to, err)
			return
		}
		defer os.Remove(src)
	}
	report("applying", to, "")
	plan := updater.Plan{Service: service, Init: initSys, Target: target, Dir: dir, From: Version, To: to,
		Health: or(m.Health, updater.HealthFor(to)), Timeout: healthTimeout, Rollback: m.Rollback}
	if err := StartUpgrade(plan, src); err != nil {
		fail(to, err)
		return
	}
	a.log.Printf("upgrade %s -> %s handed to the helper; sbnode restarts shortly", Version, to)
}

func (a *Agent) fetchUpgrade(ctx context.Context, m *pb.Upgrade, dst string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	stream, err := a.client.FetchUpgrade(ctx, &pb.FetchUpgradeRequest{Version: m.Version, Arch: runtime.GOARCH})
	if err != nil {
		return "", err
	}
	pr, pw := io.Pipe()
	go func() {
		for {
			c, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				pw.Close()
				return
			}
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := pw.Write(c.Data); err != nil {
				return
			}
		}
	}()
	b, err := updater.WriteBinary(dst, pr)
	pr.Close()
	if err != nil {
		os.Remove(dst)
		return "", fmt.Errorf("download from master: %w", err)
	}
	if b.SHA256 != m.Sha256 || (m.Size > 0 && b.Size != m.Size) {
		os.Remove(dst)
		return "", fmt.Errorf("binary from master doesn't match: sha256 %s (%d bytes), want %s (%d bytes)", b.SHA256, b.Size, m.Sha256, m.Size)
	}
	return dst, nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
