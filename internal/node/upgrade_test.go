package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sbpanel/internal/pb"
)

// A freshly upgraded sbnode connects while its helper is still checking it, so the first report
// says "applying"; the result the helper writes afterwards must still reach the master.
func TestReportUpgradeSendsResultAfterReconnect(t *testing.T) {
	upgradePoll = 10 * time.Millisecond
	a := &Agent{cfg: Config{Dir: t.TempDir()}}
	dir := a.upgradeDir()
	os.MkdirAll(dir, 0o700)
	started := time.Now().Unix()
	write := func(name string, v any) {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.json", map[string]any{"from": "v0.4.5", "to": "v0.4.6", "started": started})
	sent := make(chan *pb.UpgradeStatus, 4)
	send := func(m *pb.NodeMsg) error { sent <- m.GetUpgrade(); return nil }
	if err := a.reportUpgrade(context.Background(), send); err != nil {
		t.Fatal(err)
	}
	if st := <-sent; st.State != "applying" || st.To != "v0.4.6" {
		t.Fatalf("first report: %+v", st)
	}
	write("result.json", map[string]any{"state": "upgraded", "from": "v0.4.5", "to": "v0.4.6", "started": started, "finished": started + 10})
	select {
	case st := <-sent:
		if st.State != "upgraded" || st.Finished != started+10 {
			t.Fatalf("result report: %+v", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the result was never reported")
	}
}

// Nothing in flight: one report, no watcher.
func TestReportUpgradeIdle(t *testing.T) {
	a := &Agent{cfg: Config{Dir: t.TempDir()}}
	n := 0
	if err := a.reportUpgrade(context.Background(), func(*pb.NodeMsg) error { n++; return nil }); err != nil || n != 1 {
		t.Fatalf("err %v, sends %d", err, n)
	}
}
