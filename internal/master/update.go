package master

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"sbpanel/internal/pb"
	"sbpanel/internal/updater"
)

// Panel and node upgrades: the master checks GitHub releases, upgrades itself through the
// updater helper (swap, restart, health check, automatic rollback), and upgrades nodes by
// streaming them the verified sbnode binary over their existing mTLS connection.

const (
	updateEvery       = 6 * time.Hour
	updateFirstCheck  = 30 * time.Second
	masterHealthAfter = 2 * time.Second // the listeners are up; give Serve a moment to fail
	masterTimeout     = 90              // seconds the new sbmaster has to report healthy
	upgradeChunk      = 1 << 20
	keepReleases      = 2   // cached release directories
	staleUpgrade      = 600 // seconds after which a node's unfinished upgrade no longer blocks a new one
)

// job is upgrade work the master is doing before the helper (or the node) takes over.
type job struct {
	State string `json:"state"` // preparing | downloading | applying | failed
	To    string `json:"to"`
	Error string `json:"error,omitempty"`
	At    int64  `json:"at"`
}

type updates struct {
	mu        sync.Mutex
	latest    *updater.Release
	checkedAt int64
	checkErr  string
	master    *job
	nodeJobs  map[string]*job
	nodeStat  map[string]*pb.UpgradeStatus
	fetchMu   sync.Mutex // one release download at a time
}

// DetectService finds the service manager this panel runs under (a test hook).
var DetectService = updater.Detect

func newUpdates() *updates {
	return &updates{nodeJobs: map[string]*job{}, nodeStat: map[string]*pb.UpgradeStatus{}}
}

func (m *Master) upgradeDir() string { return filepath.Join(m.cfg.DataDir, "upgrade") }

// updateSource honours the mirror setting; SBP_UPDATE_* override the endpoints (tests, forks).
func (m *Master) updateSource() updater.Source {
	return updater.Source{
		Repo: os.Getenv("SBP_UPDATE_REPO"), API: os.Getenv("SBP_UPDATE_API"), Web: os.Getenv("SBP_UPDATE_WEB"),
		Mirror: settingOr(m.db, "update_mirror", ""),
		Client: &http.Client{Timeout: 15 * time.Minute},
	}
}

func (m *Master) updateCheckEnabled() bool { return settingOr(m.db, "update_check", "1") != "0" }

// checkUpdate asks for the latest release and records an "update_available" event once per version.
func (m *Master) checkUpdate(ctx context.Context) {
	r, err := m.updateSource().Latest(ctx)
	u := m.upd
	u.mu.Lock()
	u.checkedAt = now()
	if err != nil {
		u.checkErr = err.Error()
		u.mu.Unlock()
		m.log.Printf("update check: %v", err)
		return
	}
	u.latest, u.checkErr = r, ""
	u.mu.Unlock()
	if c, ok := updater.Compare(r.Tag, Version); ok && c > 0 && settingOr(m.db, "update_notified", "") != r.Tag {
		setSetting(m.db, "update_notified", r.Tag)
		addEvent(m.db, "update_available", 0, "", r.Tag)
	}
}

func (m *Master) updateLoop(stop <-chan struct{}) {
	t := time.NewTimer(updateFirstCheck)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if m.updateCheckEnabled() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			m.checkUpdate(ctx)
			cancel()
		}
		t.Reset(updateEvery)
	}
}

// afterStart runs once the listeners are serving: confirm a pending self-upgrade and record
// the result of the last one.
func (m *Master) afterStart(stop <-chan struct{}) {
	select {
	case <-stop:
		return
	case <-time.After(masterHealthAfter):
	}
	dir := m.upgradeDir()
	if _, err := os.Stat(filepath.Join(dir, "plan.json")); err == nil {
		if err := updater.MarkHealthy(dir, Version); err != nil {
			m.log.Printf("upgrade: mark healthy: %v", err)
		}
	}
	// the helper writes its result after confirming us; wait for it a little
	for i := 0; i < 30; i++ {
		st := updater.ReadStatus(dir, "")
		if !st.Running {
			if st.Last != nil {
				m.recordUpgrade("", st.Last.State, st.Last.From, st.Last.To, st.Last.Error, st.Last.Rollback, st.Last.Finished)
			}
			return
		}
		select {
		case <-stop:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// recordUpgrade adds an event for a finished upgrade, once (nodeID "" is the panel itself).
func (m *Master) recordUpgrade(nodeID, state, from, to, errMsg string, rollback bool, finished int64) {
	if finished == 0 || (state != updater.StateUpgraded && state != updater.StateRolledBack && state != updater.StateFailed) {
		return
	}
	key := "upgrade_seen:" + nodeID
	if seen, _ := strconv.ParseInt(settingOr(m.db, key, "0"), 10, 64); finished <= seen {
		return
	}
	setSetting(m.db, key, strconv.FormatInt(finished, 10))
	kind := "panel_"
	if nodeID != "" {
		kind = "node_"
	}
	detail := from + " → " + to
	if rollback {
		detail = "rollback " + detail
	}
	if state == updater.StateUpgraded {
		kind += "upgraded"
	} else {
		kind += "upgrade_failed"
		detail += " (" + state + "): " + firstLine(errMsg)
	}
	addEvent(m.db, kind, 0, nodeID, detail)
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

// release returns the verified binaries of tag for arch, downloading them once.
func (m *Master) release(ctx context.Context, tag, arch string) (map[string]updater.Binary, error) {
	if !updater.ValidTag(tag) {
		return nil, fmt.Errorf("bad version %q", tag)
	}
	u := m.upd
	u.fetchMu.Lock()
	defer u.fetchMu.Unlock()
	cache := filepath.Join(m.upgradeDir(), "cache")
	dir := filepath.Join(cache, tag, arch)
	meta := filepath.Join(dir, "binaries.json")
	var bins map[string]updater.Binary
	if b, err := os.ReadFile(meta); err == nil && json.Unmarshal(b, &bins) == nil && len(bins) == 2 {
		ok := true
		for _, x := range bins {
			if sum, _, err := updater.FileSHA256(x.Path); err != nil || sum != x.SHA256 {
				ok = false
			}
		}
		if ok {
			return bins, nil
		}
	}
	bins, err := m.updateSource().Fetch(ctx, tag, arch, dir, "sbmaster", "sbnode")
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(bins)
	if err := os.WriteFile(meta, b, 0o600); err != nil {
		return nil, err
	}
	pruneReleases(cache, tag)
	return bins, nil
}

// pruneReleases keeps the newest keepReleases cached tags (and always keep).
func pruneReleases(cache, keep string) {
	ents, _ := os.ReadDir(cache)
	var tags []string
	for _, e := range ents {
		if e.IsDir() && updater.ValidTag(e.Name()) && e.Name() != keep {
			tags = append(tags, e.Name())
		}
	}
	sort.Slice(tags, func(i, j int) bool { c, _ := updater.Compare(tags[i], tags[j]); return c > 0 })
	for i, t := range tags {
		if i >= keepReleases-1 {
			os.RemoveAll(filepath.Join(cache, t))
		}
	}
}

// ---- API ----

type nodeUpdate struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Online    bool              `json:"online"`
	Version   string            `json:"version"`
	Arch      string            `json:"arch"`
	Release   bool              `json:"release"`   // runs a published release (not a dev build)
	Supported bool              `json:"supported"` // can be upgraded from the panel
	Container bool              `json:"container"` // runs in a container: restart it to upgrade
	HasUpdate bool              `json:"has_update"`
	Status    *pb.UpgradeStatus `json:"status,omitempty"` // last result / progress reported by the node
	Job       *job              `json:"job,omitempty"`    // what the master is doing for it
}

type updateOut struct {
	Current      string           `json:"current"`
	Release      bool             `json:"release"` // the panel runs a published release (not a dev build)
	Latest       *updater.Release `json:"latest"`
	HasUpdate    bool             `json:"has_update"`
	CheckedAt    int64            `json:"checked_at"`
	CheckError   string           `json:"check_error,omitempty"`
	CheckEnabled bool             `json:"check_enabled"`
	Mirror       string           `json:"mirror"`
	// the panel itself
	Supported   bool            `json:"supported"`
	Unsupported string          `json:"unsupported,omitempty"`
	Container   bool            `json:"container"` // upgrade by restarting the container
	Running     bool            `json:"running"`
	Job         *job            `json:"job,omitempty"`
	Last        *updater.Result `json:"last,omitempty"`
	CanRollback bool            `json:"can_rollback"`
	Nodes       []nodeUpdate    `json:"nodes"`
}

func newer(latest *updater.Release, v string) bool {
	if latest == nil {
		return false
	}
	c, ok := updater.Compare(latest.Tag, v)
	return ok && c > 0
}

func (m *Master) updateState() updateOut {
	u := m.upd
	u.mu.Lock()
	out := updateOut{Current: Version, Latest: u.latest, CheckedAt: u.checkedAt, CheckError: u.checkErr, Job: u.master}
	jobs := map[string]*job{}
	for k, v := range u.nodeJobs {
		jobs[k] = v
	}
	stats := map[string]*pb.UpgradeStatus{}
	for k, v := range u.nodeStat {
		stats[k] = v
	}
	u.mu.Unlock()
	out.HasUpdate = newer(out.Latest, Version)
	out.Release = updater.ValidTag(Version)
	out.CheckEnabled = m.updateCheckEnabled()
	out.Mirror = settingOr(m.db, "update_mirror", "")
	if _, _, err := DetectService("sbmaster"); err != nil {
		out.Unsupported = err.Error()
		out.Container = errors.Is(err, updater.ErrContainer)
	} else {
		out.Supported = true
	}
	target, _ := updater.Executable()
	st := updater.ReadStatus(m.upgradeDir(), target)
	out.Running, out.Last, out.CanRollback = st.Running, st.Last, st.CanRollback && out.Supported
	nodes, _ := m.listNodes()
	for _, n := range nodes {
		x := nodeUpdate{ID: n.ID, Name: n.Name, Online: n.Online, Job: jobs[n.ID], Status: stats[n.ID]}
		var info struct {
			Version string `json:"version"`
			Arch    string `json:"arch"`
		}
		json.Unmarshal(n.Info, &info)
		x.Version, x.Arch = info.Version, info.Arch
		if s := m.hub.session(n.ID); s != nil {
			if h, _, _, _ := s.snapshot(); h != nil {
				x.Version, x.Arch = h.Version, h.Arch
				x.Supported = slices.Contains(h.Features, "upgrade")
				x.Container = slices.Contains(h.Features, "container")
			}
		}
		x.Status = settledStatus(x.Status, x.Online, x.Version, time.Now())
		x.HasUpdate = newer(out.Latest, x.Version)
		x.Release = updater.ValidTag(x.Version)
		out.Nodes = append(out.Nodes, x)
	}
	return out
}

// nodeHelperWindow: longer than a node's helper can take after the swap (health timeout plus
// settling), so an "applying" report older than this is no longer in flight.
const nodeHelperWindow = 4 * time.Minute

// settledStatus: sbnode up to v0.4.5 reports its upgrade only once, right after the new binary
// connects and before the helper records the result, so the panel would show "applying" forever.
// Once the node is online on the target version and the helper's window has passed, that upgrade
// is done.
func settledStatus(st *pb.UpgradeStatus, online bool, version string, now time.Time) *pb.UpgradeStatus {
	if st == nil || st.State != "applying" || !online || now.Sub(time.Unix(st.Started, 0)) < nodeHelperWindow {
		return st
	}
	done := proto.Clone(st).(*pb.UpgradeStatus)
	switch version {
	case st.To:
		done.State = updater.StateUpgraded
	case st.From:
		done.State, done.Error = updater.StateRolledBack, st.To+" did not come up; the helper put "+st.From+" back"
	default:
		return st
	}
	return done
}

func (m *Master) apiUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, m.updateState())
}

func (m *Master) apiUpdateCheck(w http.ResponseWriter, r *http.Request) {
	m.checkUpdate(r.Context())
	writeJSON(w, 200, m.updateState())
}

// targetTag is the requested version, or the latest release.
func (m *Master) targetTag(ctx context.Context, v string) (string, error) {
	if v != "" {
		if !updater.ValidTag(v) {
			return "", fmt.Errorf("bad version %q", v)
		}
		return v, nil
	}
	m.upd.mu.Lock()
	l := m.upd.latest
	m.upd.mu.Unlock()
	if l == nil {
		m.checkUpdate(ctx)
		m.upd.mu.Lock()
		l, err := m.upd.latest, m.upd.checkErr
		m.upd.mu.Unlock()
		if l == nil {
			return "", fmt.Errorf("can't find the latest release: %s", err)
		}
		return l.Tag, nil
	}
	return l.Tag, nil
}

type upgradeReq struct {
	Version  string   `json:"version"`
	Rollback bool     `json:"rollback"`
	IDs      []string `json:"ids"`      // nodes; empty with outdated = every online node behind the latest release
	Outdated bool     `json:"outdated"` // nodes only
}

// apiUpdateMaster upgrades (or rolls back) the panel. The response returns before sbmaster
// restarts; the page polls GET /api/update.
func (m *Master) apiUpdateMaster(w http.ResponseWriter, r *http.Request) {
	var req upgradeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if req.Version != "" && !updater.ValidTag(req.Version) {
		writeErr(w, 400, fmt.Sprintf("bad version %q", req.Version))
		return
	}
	service, initSys, err := DetectService("sbmaster")
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	target, err := updater.Executable()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if updater.ReadStatus(m.upgradeDir(), target).Running {
		writeErr(w, 409, updater.ErrBusy.Error())
		return
	}
	tag := ""
	if !req.Rollback {
		if tag, err = m.targetTag(r.Context(), req.Version); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
	}
	u := m.upd
	u.mu.Lock()
	if u.master != nil && u.master.State != "failed" && time.Since(time.Unix(u.master.At, 0)) < 15*time.Minute {
		u.mu.Unlock()
		writeErr(w, 409, updater.ErrBusy.Error())
		return
	}
	j := &job{State: "downloading", To: tag, At: now()}
	u.master = j
	u.mu.Unlock()
	to := tag
	set := func(state, errMsg string) {
		u.mu.Lock()
		j.State, j.Error, j.At = state, errMsg, now()
		if to != "" {
			j.To = to
		}
		u.mu.Unlock()
		if errMsg != "" {
			m.log.Printf("panel upgrade: %s", errMsg)
		}
	}
	m.audit(r, "upgrade_panel", tag, map[bool]string{true: "rollback"}[req.Rollback])
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		src := target + ".prev"
		if req.Rollback {
			v, err := updater.BinaryVersion(src)
			if err != nil {
				set("failed", "no usable previous binary: "+err.Error())
				return
			}
			to = v
		} else {
			bins, err := m.release(ctx, tag, runtime.GOARCH)
			if err != nil {
				set("failed", err.Error())
				return
			}
			src = bins["sbmaster"].Path
		}
		set("applying", "")
		err := updater.Start(updater.Plan{Service: service, Init: initSys, Target: target, Dir: m.upgradeDir(),
			From: Version, To: to, Health: updater.HealthFor(to), Timeout: masterTimeout, Rollback: req.Rollback}, src)
		if err != nil {
			set("failed", err.Error())
		}
	}()
	writeJSON(w, 202, map[string]any{"to": tag, "rollback": req.Rollback})
}

// apiUpdateNodes upgrades (or rolls back) nodes.
func (m *Master) apiUpdateNodes(w http.ResponseWriter, r *http.Request) {
	var req upgradeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if req.Version != "" && !updater.ValidTag(req.Version) {
		writeErr(w, 400, fmt.Sprintf("bad version %q", req.Version))
		return
	}
	tag := ""
	var err error
	if !req.Rollback {
		if tag, err = m.targetTag(r.Context(), req.Version); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
	}
	state := m.updateState()
	want := map[string]bool{}
	for _, id := range req.IDs {
		want[id] = true
	}
	type outcome struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Ok     bool   `json:"ok"`
		Reason string `json:"reason,omitempty"`
	}
	var res []outcome
	byArch := map[string][]string{}
	for _, n := range state.Nodes {
		if req.Outdated && len(req.IDs) == 0 {
			if !n.Online || !n.Supported || !newerTag(tag, n.Version) {
				continue
			}
		} else if !want[n.ID] {
			continue
		}
		o := outcome{ID: n.ID, Name: n.Name}
		switch {
		case !n.Online:
			o.Reason = "offline"
		case !n.Supported:
			o.Reason = "this node's sbnode predates panel upgrades; run install.sh upgrade on it once"
		case n.Status != nil && (n.Status.State == "downloading" || n.Status.State == "applying") && now()-n.Status.Started < staleUpgrade:
			o.Reason = updater.ErrBusy.Error()
		case req.Rollback && (n.Status == nil || !n.Status.CanRollback):
			o.Reason = "no previous binary on the node"
		default:
			o.Ok = true
			byArch[n.Arch] = append(byArch[n.Arch], n.ID)
		}
		res = append(res, o)
	}
	if len(res) == 0 {
		writeErr(w, 400, "no matching nodes")
		return
	}
	m.audit(r, "upgrade_nodes", tag, fmt.Sprintf("%v rollback=%v", req.IDs, req.Rollback))
	u := m.upd
	for arch, ids := range byArch {
		u.mu.Lock()
		for _, id := range ids {
			u.nodeJobs[id] = &job{State: "preparing", To: tag, At: now()}
			delete(u.nodeStat, id)
		}
		u.mu.Unlock()
		go m.sendNodeUpgrades(tag, arch, ids, req.Rollback)
	}
	writeJSON(w, 202, map[string]any{"to": tag, "nodes": res})
}

func newerTag(tag, v string) bool {
	c, ok := updater.Compare(tag, v)
	return !ok || c > 0 // dev builds count as behind a release
}

func (m *Master) sendNodeUpgrades(tag, arch string, ids []string, rollback bool) {
	u := m.upd
	fail := func(err error) {
		u.mu.Lock()
		for _, id := range ids {
			u.nodeJobs[id] = &job{State: "failed", To: tag, Error: err.Error(), At: now()}
		}
		u.mu.Unlock()
	}
	msg := &pb.Upgrade{Rollback: rollback}
	if !rollback {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		bins, err := m.release(ctx, tag, arch)
		cancel()
		if err != nil {
			fail(err)
			return
		}
		b := bins["sbnode"]
		msg.Version, msg.Sha256, msg.Size, msg.Health = tag, b.SHA256, b.Size, updater.HealthFor(tag)
	}
	for _, id := range ids {
		s := m.hub.session(id)
		u.mu.Lock()
		if s == nil {
			u.nodeJobs[id] = &job{State: "failed", To: tag, Error: "node went offline", At: now()}
		} else {
			u.nodeJobs[id] = &job{State: "sent", To: tag, At: now()}
		}
		u.mu.Unlock()
		if s != nil {
			m.hub.trySend(s, &pb.MasterMsg{M: &pb.MasterMsg_Upgrade{Upgrade: msg}})
		}
	}
}

// onNodeUpgrade records progress and results reported by a node.
func (m *Master) onNodeUpgrade(nodeID string, st *pb.UpgradeStatus) {
	u := m.upd
	u.mu.Lock()
	if st.State == "" && !st.CanRollback {
		delete(u.nodeStat, nodeID)
	} else {
		u.nodeStat[nodeID] = st
	}
	if j := u.nodeJobs[nodeID]; j != nil && st.Started >= j.At-5 {
		delete(u.nodeJobs, nodeID) // the node has taken over
	}
	u.mu.Unlock()
	m.recordUpgrade(nodeID, st.State, st.From, st.To, st.Error, st.Rollback, st.Finished)
}

// FetchUpgrade streams a cached, verified sbnode binary to a node.
func (h *Hub) FetchUpgrade(req *pb.FetchUpgradeRequest, stream pb.NodeHub_FetchUpgradeServer) error {
	if !updater.ValidTag(req.Version) || (req.Arch != "amd64" && req.Arch != "arm64") {
		return errors.New("bad version or arch")
	}
	meta := filepath.Join(h.m.upgradeDir(), "cache", req.Version, req.Arch, "binaries.json")
	var bins map[string]updater.Binary
	b, err := os.ReadFile(meta)
	if err != nil || json.Unmarshal(b, &bins) != nil || bins["sbnode"].Path == "" {
		return fmt.Errorf("release %s/%s is not prepared on the master", req.Version, req.Arch)
	}
	f, err := os.Open(bins["sbnode"].Path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, upgradeChunk)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if serr := stream.Send(&pb.UpgradeChunk{Data: buf[:n]}); serr != nil {
				return serr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
