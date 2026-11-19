package master

import (
	"testing"
	"time"

	"sbpanel/internal/pb"
)

// Upgrade results become one event each, however often a node re-reports them on reconnect.
func TestUpgradeEvents(t *testing.T) {
	m := newTestMaster(t)
	m.upd = newUpdates()
	m.recordUpgrade("", "upgraded", "v0.4.0", "v0.5.0", "", false, 100)
	m.recordUpgrade("", "upgraded", "v0.4.0", "v0.5.0", "", false, 100) // panel restarted again
	if n := m.eventCount("panel_upgraded"); n != 1 {
		t.Fatalf("panel_upgraded events: %d", n)
	}
	st := &pb.UpgradeStatus{State: "rolled_back", From: "v0.4.0", To: "v0.5.0", Error: "did not report healthy\n--- service log ---\nboom", Started: 190, Finished: 200}
	for i := 0; i < 3; i++ { // every reconnect sends the last result
		m.onNodeUpgrade("n1", st)
	}
	if n := m.eventCount("node_upgrade_failed"); n != 1 {
		t.Fatalf("node_upgrade_failed events: %d", n)
	}
	var detail string
	m.db.QueryRow(`SELECT detail FROM events WHERE kind='node_upgrade_failed'`).Scan(&detail)
	if detail != "v0.4.0 → v0.5.0 (rolled_back): did not report healthy" {
		t.Fatalf("detail %q", detail)
	}
	// progress updates are not results
	m.onNodeUpgrade("n2", &pb.UpgradeStatus{State: "applying", Started: 300})
	if n := m.eventCount("node_upgraded") + m.eventCount("node_upgrade_failed"); n != 1 {
		t.Fatalf("events after progress: %d", n)
	}
	if s := m.updateState(); len(s.Nodes) != 0 || s.Current != Version {
		t.Fatalf("state %+v", s)
	}
}

// Older nodes report "applying" once and never the result: settle it once the helper's window
// has passed, by the version the node came back on.
func TestSettledStatus(t *testing.T) {
	now := time.Unix(10_000, 0)
	st := &pb.UpgradeStatus{State: "applying", From: "v0.4.4", To: "v0.4.5", Started: now.Add(-5 * time.Minute).Unix()}
	if got := settledStatus(st, true, "v0.4.5", now); got.State != "upgraded" || st.State != "applying" {
		t.Fatalf("on the target version: %q (original %q)", got.State, st.State)
	}
	if got := settledStatus(st, true, "v0.4.4", now); got.State != "rolled_back" || got.Error == "" {
		t.Fatalf("back on the old version: %+v", got)
	}
	if got := settledStatus(st, false, "v0.4.5", now); got != st {
		t.Fatal("offline node settled")
	}
	fresh := &pb.UpgradeStatus{State: "applying", From: "v0.4.4", To: "v0.4.5", Started: now.Add(-30 * time.Second).Unix()}
	if got := settledStatus(fresh, true, "v0.4.5", now); got != fresh {
		t.Fatal("helper still within its window")
	}
}
