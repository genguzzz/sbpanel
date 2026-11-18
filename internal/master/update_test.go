package master

import (
	"testing"

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
