package node

import (
	"context"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"sbpanel/internal/pb"

	"github.com/sagernet/sing-box/adapter"
	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"
)

// connRec is one routed connection. Byte counters are updated lock-free by the
// copy loop; the aggregator diffs them against last* every window.
type connRec struct {
	id                         uint64
	user, inbound, outbound    string
	network, host, srcIP, rule string
	port                       uint16
	start                      time.Time
	up, down                   atomic.Int64
	closer                     interface{ Close() error }
	evicted                    bool // its IP lost its device slot (guarded by Tracker.mu)

	// aggregator-owned
	lastUp, lastDown int64
	counted          bool
}

// Tracker implements adapter.ConnectionTracker. It is the single place where the
// node counts traffic, sees who is online and can drop connections.
type Tracker struct {
	mu     sync.Mutex
	active map[uint64]*connRec
	ips    map[string]map[string]*ipUse // user -> source IP -> its connections (device limits)
	ipSeq  uint64                       // orders IPs by when they took a device slot
	closed []*connRec
	nextID atomic.Uint64

	totalUp, totalDown atomic.Int64

	polMu    sync.RWMutex
	policies map[string]*userPolicy

	rejected atomic.Int64 // connections refused by a traffic or device limit

	// probes records the panel probe's routed connections per inbound: they are
	// not counted, but their presence proves the inbound authenticated the probe
	// (so a failed probe is the outbound's / upstream's fault, not the inbound's).
	probeMu sync.Mutex
	probes  map[string]probeSight
}

// ipUse is one source IP a user is connected from.
type ipUse struct {
	conns int
	since uint64 // Tracker.ipSeq when the IP took its slot: lower = connected longer
}

type probeSight struct {
	outbound string
	network  string
	at       time.Time
}

// probeKeep is how long a probe sighting stays in the live snapshot.
const probeKeep = 30 * time.Second

func (t *Tracker) sawProbe(m adapter.InboundContext, out adapter.Outbound) {
	ps := probeSight{network: m.Network, at: time.Now()}
	if n := len(m.OutboundChain); n > 0 {
		ps.outbound = m.OutboundChain[n-1].Tag()
	} else if out != nil {
		ps.outbound = out.Tag()
	}
	t.probeMu.Lock()
	if t.probes == nil {
		t.probes = map[string]probeSight{}
	}
	t.probes[m.Inbound] = ps
	t.probeMu.Unlock()
}

// userPolicy holds per-user limits; limiters are shared by all of the user's
// connections on this node so the cap applies to the user, not per connection.
type userPolicy struct {
	deviceLimit int
	kickOldest  bool            // at the device limit a new IP replaces the oldest one
	blocked     map[string]bool // inbound tags closed by a traffic limit
	up, down    *rate.Limiter
	upBps       int64
	downBps     int64
}

func NewTracker() *Tracker {
	return &Tracker{active: map[uint64]*connRec{}, ips: map[string]map[string]*ipUse{}, policies: map[string]*userPolicy{}}
}

func newLimiter(bps int64) *rate.Limiter {
	if bps <= 0 {
		return nil
	}
	burst := int(bps)
	if burst < 64<<10 {
		burst = 64 << 10 // at least one large read
	}
	return rate.NewLimiter(rate.Limit(bps), burst)
}

// SetPolicies replaces user policies, keeping existing limiters whose rate is unchanged.
// Connections of users on inbounds newly closed to them are closed.
func (t *Tracker) SetPolicies(ps []*pb.UserPolicy) {
	t.polMu.Lock()
	next := make(map[string]*userPolicy, len(ps))
	for _, p := range ps {
		old := t.policies[p.User]
		np := &userPolicy{deviceLimit: int(p.DeviceLimit), kickOldest: p.KickOldest, upBps: p.SpeedUp, downBps: p.SpeedDown}
		if len(p.BlockedInbounds) > 0 {
			np.blocked = make(map[string]bool, len(p.BlockedInbounds))
			for _, tag := range p.BlockedInbounds {
				np.blocked[tag] = true
			}
		}
		if old != nil && old.upBps == p.SpeedUp {
			np.up = old.up
		} else {
			np.up = newLimiter(p.SpeedUp)
		}
		if old != nil && old.downBps == p.SpeedDown {
			np.down = old.down
		} else {
			np.down = newLimiter(p.SpeedDown)
		}
		next[p.User] = np
	}
	t.policies = next
	t.polMu.Unlock()
	t.Kick(func(r *connRec) bool { p := next[r.user]; return p != nil && p.blocked[r.inbound] })
}

// blockedOn reports whether the user's traffic limit has closed this inbound.
func (p *userPolicy) blockedOn(inbound string) bool { return p != nil && p.blocked[inbound] }

func (t *Tracker) policy(user string) *userPolicy {
	t.polMu.RLock()
	defer t.polMu.RUnlock()
	return t.policies[user]
}

var _ adapter.ConnectionTracker = (*Tracker)(nil)

func (t *Tracker) newRec(m adapter.InboundContext, rule adapter.Rule, out adapter.Outbound) *connRec {
	r := &connRec{
		id:      t.nextID.Add(1),
		user:    m.User,
		inbound: m.Inbound,
		network: m.Network,
		port:    m.Destination.Port,
		start:   time.Now(),
		rule:    "final",
	}
	if n := len(m.OutboundChain); n > 0 {
		r.outbound = m.OutboundChain[n-1].Tag()
	} else if out != nil {
		r.outbound = out.Tag()
	}
	if rule != nil {
		r.rule = rule.String()
	}
	switch {
	case m.Domain != "":
		r.host = m.Domain
	case m.Destination.IsFqdn():
		r.host = m.Destination.Fqdn
	case m.Destination.Addr.IsValid():
		r.host = m.Destination.Addr.Unmap().String()
	}
	if m.Source.Addr.IsValid() {
		r.srcIP = m.Source.Addr.Unmap().String()
	}
	return r
}

// admit registers a new connection, unless it comes from a new IP while the user's device
// limit is full. With kickOldest the new IP gets in and the IPs connected the longest lose
// their connections instead. The check and the registration share one critical section, so
// two new IPs arriving together cannot both take the last slot.
func (t *Tracker) admit(r *connRec, p *userPolicy) bool {
	t.mu.Lock()
	ips := t.ips[r.user]
	var drop []*connRec
	if p != nil && p.deviceLimit > 0 && r.user != "" && ips[r.srcIP] == nil && len(ips) >= p.deviceLimit {
		if !p.kickOldest {
			t.mu.Unlock()
			return false
		}
		drop = t.evictLocked(r.user, len(ips)-p.deviceLimit+1)
	}
	t.active[r.id] = r
	if ips == nil {
		ips = map[string]*ipUse{}
		t.ips[r.user] = ips
	}
	u := ips[r.srcIP]
	if u == nil {
		// numbered here, under the lock: connection ids are handed out before it, so
		// two connections racing in could be admitted in the opposite order
		t.ipSeq++
		u = &ipUse{since: t.ipSeq}
		ips[r.srcIP] = u
	}
	u.conns++
	t.mu.Unlock()
	for _, d := range drop {
		if d.closer != nil {
			d.closer.Close()
		}
	}
	return true
}

// evictLocked frees n device slots of user: the IPs connected the longest leave the count
// at once, and their connections are returned for the caller to close.
func (t *Tracker) evictLocked(user string, n int) []*connRec {
	ips := t.ips[user]
	gone := map[string]bool{}
	for ; n > 0 && len(ips) > 0; n-- {
		var ip string
		var old *ipUse
		for k, u := range ips {
			if old == nil || u.since < old.since {
				ip, old = k, u
			}
		}
		delete(ips, ip)
		gone[ip] = true
	}
	var drop []*connRec
	for _, r := range t.active {
		if r.user == user && gone[r.srcIP] {
			r.evicted = true // leave must not count it against the IP's next visit
			drop = append(drop, r)
		}
	}
	return drop
}

func (t *Tracker) leave(r *connRec) {
	t.mu.Lock()
	if _, ok := t.active[r.id]; ok {
		delete(t.active, r.id)
		t.closed = append(t.closed, r)
		if m := t.ips[r.user]; m != nil && !r.evicted {
			if u := m[r.srcIP]; u != nil {
				if u.conns--; u.conns <= 0 {
					delete(m, r.srcIP)
				}
			}
			if len(m) == 0 {
				delete(t.ips, r.user)
			}
		}
	}
	t.mu.Unlock()
}

func (t *Tracker) RoutedConnection(ctx context.Context, conn net.Conn, m adapter.InboundContext, rule adapter.Rule, out adapter.Outbound) net.Conn {
	if m.User == ProbeUser {
		t.sawProbe(m, out)
		return conn // the panel's connectivity probe: not counted, not online
	}
	r := t.newRec(m, rule, out)
	p := t.policy(r.user)
	if p.blockedOn(r.inbound) {
		t.rejected.Add(1)
		conn.Close()
		return conn
	}
	var inner net.Conn = conn
	if p != nil && (p.up != nil || p.down != nil) {
		// upload = bytes read from the client side; download = bytes written to it
		inner = &limitedConn{Conn: conn, ctx: ctx, read: p.up, write: p.down}
	}
	c := &trackedConn{
		ExtendedConn: bufio.NewInt64CounterConn(inner, []*atomic.Int64{&r.up, &t.totalUp}, []*atomic.Int64{&r.down, &t.totalDown}),
		t:            t, r: r, limited: inner != conn,
	}
	r.closer = c
	if !t.admit(r, p) {
		t.rejected.Add(1)
		conn.Close()
		return conn
	}
	return c
}

func (t *Tracker) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, m adapter.InboundContext, rule adapter.Rule, out adapter.Outbound) N.PacketConn {
	if m.User == ProbeUser {
		t.sawProbe(m, out)
		return conn
	}
	r := t.newRec(m, rule, out)
	p := t.policy(r.user)
	if p.blockedOn(r.inbound) {
		t.rejected.Add(1)
		conn.Close()
		return conn
	}
	var inner N.PacketConn = conn
	if p != nil && (p.up != nil || p.down != nil) {
		inner = &limitedPacketConn{PacketConn: conn, ctx: ctx, read: p.up, write: p.down}
	}
	c := &trackedPacketConn{
		PacketConn: bufio.NewInt64CounterPacketConn(inner, []*atomic.Int64{&r.up, &t.totalUp}, nil, []*atomic.Int64{&r.down, &t.totalDown}, nil),
		t:          t, r: r, limited: inner != conn,
	}
	r.closer = c
	if !t.admit(r, p) {
		t.rejected.Add(1)
		conn.Close()
		return conn
	}
	return c
}

// RoutedFlow covers TUN fast-path flows, which a server node does not use.
func (t *Tracker) RoutedFlow(ctx context.Context, m adapter.InboundContext, rule adapter.Rule, out adapter.Outbound) tun.FlowTracker {
	return nil
}

// Kick closes every active connection matching fn.
func (t *Tracker) Kick(fn func(r *connRec) bool) int {
	t.mu.Lock()
	var hit []*connRec
	for _, r := range t.active {
		if fn(r) {
			hit = append(hit, r)
		}
	}
	t.mu.Unlock()
	for _, r := range hit {
		if r.closer != nil {
			r.closer.Close()
		}
	}
	return len(hit)
}

// Online returns user -> distinct source IPs of active connections.
func (t *Tracker) Online() map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range t.active {
		if r.user == "" {
			continue
		}
		s := out[r.user]
		if s == nil {
			s = map[string]struct{}{}
			out[r.user] = s
		}
		s[r.srcIP] = struct{}{}
	}
	return out
}

func (t *Tracker) ActiveCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.active)
}

type trackedConn struct {
	N.ExtendedConn
	t       *Tracker
	r       *connRec
	once    sync.Once
	limited bool
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.t.leave(c.r) })
	return c.ExtendedConn.Close()
}

func (c *trackedConn) Upstream() any { return c.ExtendedConn }

// A rate-limited connection must not be bypassed by splice/readv fast paths.
func (c *trackedConn) ReaderReplaceable() bool { return !c.limited }
func (c *trackedConn) WriterReplaceable() bool { return !c.limited }

type trackedPacketConn struct {
	N.PacketConn
	t       *Tracker
	r       *connRec
	once    sync.Once
	limited bool
}

func (c *trackedPacketConn) Close() error {
	c.once.Do(func() { c.t.leave(c.r) })
	return c.PacketConn.Close()
}

func (c *trackedPacketConn) Upstream() any           { return c.PacketConn }
func (c *trackedPacketConn) ReaderReplaceable() bool { return !c.limited }
func (c *trackedPacketConn) WriterReplaceable() bool { return !c.limited }

// Snapshot lists active connections, newest first (at most max).
func (t *Tracker) Snapshot(max int) *pb.ConnSnapshot {
	t.mu.Lock()
	out := make([]*pb.ConnInfo, 0, len(t.active))
	for _, r := range t.active {
		out = append(out, &pb.ConnInfo{Id: r.id, User: r.user, Inbound: r.inbound, Outbound: r.outbound, Network: r.network,
			Host: r.host, Port: uint32(r.port), SrcIp: r.srcIP, Rule: r.rule, Start: r.start.Unix(), Up: r.up.Load(), Down: r.down.Load()})
	}
	total := len(t.active)
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].Start > out[j].Start || (out[i].Start == out[j].Start && out[i].Id > out[j].Id)
	})
	if len(out) > max {
		out = out[:max]
	}
	// recent probe sightings ride along (not part of total; the panel hides them)
	t.probeMu.Lock()
	for in, ps := range t.probes {
		if time.Since(ps.at) > probeKeep {
			delete(t.probes, in)
			continue
		}
		out = append(out, &pb.ConnInfo{User: ProbeUser, Inbound: in, Outbound: ps.outbound, Network: ps.network, Start: ps.at.Unix()})
	}
	t.probeMu.Unlock()
	return &pb.ConnSnapshot{Conns: out, Total: int64(total)}
}
