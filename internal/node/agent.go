// Package node is the data plane agent: it embeds sing-box, counts traffic and
// keeps a durable outbox of reports for the master.
package node

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/proto"

	"sbpanel/internal/pb"
	"sbpanel/internal/pki"
	"sbpanel/internal/procstat"
)

var Version = "dev"

type Config struct {
	Dir             string        // state directory
	Window          time.Duration // report window, default 10s
	MaxHostsPerUser int           // dimension cap per window, default 200
	MaxSpool        int           // reports kept while master is unreachable, default 7 days worth
}

const (
	fKey    = "node.key"
	fCert   = "node.crt"
	fCA     = "ca.crt"
	fMaster = "master"
	fID     = "node_id"
	fSpec   = "spec.pb"
	fSpool  = "spool.db"
)

// Enroll exchanges a one-time token for a client certificate, pinning the master CA.
func Enroll(dir, master, token, caSHA256 string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	keyPEM, csr, err := pki.NewKeyAndCSR("sbnode")
	if err != nil {
		return err
	}
	tc := &tls.Config{
		InsecureSkipVerify:    true, // replaced by the CA pin below
		VerifyPeerCertificate: pki.PinVerifier(caSHA256),
		ServerName:            pki.ServerName,
		MinVersion:            tls.VersionTLS13,
	}
	cc, err := grpc.NewClient(master, grpc.WithTransportCredentials(credentials.NewTLS(tc)))
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := pb.NewEnrollClient(cc).Enroll(ctx, &pb.EnrollRequest{Token: token, CsrDer: csr, Hostname: hostname})
	if err != nil {
		return fmt.Errorf("enroll: %w", err)
	}
	if pki.Fingerprint(resp.CaDer) != strings.ToLower(strings.ReplaceAll(caSHA256, ":", "")) {
		return errors.New("enroll: master returned a different CA")
	}
	files := map[string][]byte{
		fKey: keyPEM, fCert: pki.CertPEM(resp.CertDer), fCA: pki.CertPEM(resp.CaDer),
		fMaster: []byte(master), fID: []byte(resp.NodeId),
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

type Agent struct {
	cfg     Config
	log     *log.Logger
	nodeID  string
	master  string
	caPool  *x509.CertPool
	cert    atomic.Pointer[tls.Certificate]
	bootID  string
	tracker *Tracker
	core    *Core
	spool   *Spool

	applyMu    sync.Mutex
	applied    atomic.Uint64
	applyErr   atomic.Value // string
	upgrading  atomic.Bool  // an upgrade is being prepared (upgrade.go)
	pushWake   chan struct{}
	client     pb.NodeHubClient
	lastTotals [2]int64
	lastRateAt time.Time
	proc       *procstat.Sampler
	watchCerts atomic.Pointer[[]*pb.CertWatch]
	certSend   atomic.Pointer[func(*pb.NodeMsg) error]
}

func (a *Agent) setWatch(spec *pb.NodeSpec) {
	ws := spec.WatchCerts
	a.watchCerts.Store(&ws)
}

// sendCertReport inspects node-local certificates and reports them to the master.
func (a *Agent) sendCertReport() {
	sp := a.certSend.Load()
	if sp == nil {
		return
	}
	var ws []*pb.CertWatch
	if p := a.watchCerts.Load(); p != nil {
		ws = *p
	}
	(*sp)(&pb.NodeMsg{M: &pb.NodeMsg_Certs{Certs: certReport(ws)}})
}

func readFile(dir, name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) }

func NewAgent(cfg Config) (*Agent, error) {
	if cfg.Window == 0 {
		cfg.Window = 10 * time.Second
	}
	if cfg.MaxHostsPerUser == 0 {
		cfg.MaxHostsPerUser = defaultMaxHosts
	}
	if cfg.MaxSpool == 0 {
		cfg.MaxSpool = int(7 * 24 * time.Hour / cfg.Window)
	}
	a := &Agent{cfg: cfg, log: log.New(os.Stderr, "[node] ", log.LstdFlags), pushWake: make(chan struct{}, 1), proc: procstat.New()}
	a.applyErr.Store("")
	id, err := readFile(cfg.Dir, fID)
	if err != nil {
		return nil, fmt.Errorf("not enrolled (run `sbnode enroll`): %w", err)
	}
	a.nodeID = strings.TrimSpace(string(id))
	m, err := readFile(cfg.Dir, fMaster)
	if err != nil {
		return nil, err
	}
	a.master = strings.TrimSpace(string(m))
	caPEM, err := readFile(cfg.Dir, fCA)
	if err != nil {
		return nil, err
	}
	a.caPool = x509.NewCertPool()
	if !a.caPool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("bad ca.crt")
	}
	if err := a.loadCert(); err != nil {
		return nil, err
	}
	b := make([]byte, 8)
	rand.Read(b)
	a.bootID = hex.EncodeToString(b)
	a.tracker = NewTracker()
	a.core = NewCore(a.tracker, a.log)
	a.core.SetCertDir(filepath.Join(cfg.Dir, "certs"))
	a.spool, err = OpenSpool(filepath.Join(cfg.Dir, fSpool), cfg.MaxSpool)
	if err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	return a, nil
}

func (a *Agent) loadCert() error {
	c, err := tls.LoadX509KeyPair(filepath.Join(a.cfg.Dir, fCert), filepath.Join(a.cfg.Dir, fKey))
	if err != nil {
		return err
	}
	a.cert.Store(&c)
	return nil
}

func (a *Agent) certExpiry() time.Time {
	c := a.cert.Load()
	if c == nil || len(c.Certificate) == 0 {
		return time.Time{}
	}
	x, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return time.Time{}
	}
	return x.NotAfter
}

func (a *Agent) tlsConfig() *tls.Config {
	return &tls.Config{
		RootCAs:    a.caPool,
		ServerName: pki.ServerName,
		MinVersion: tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return a.cert.Load(), nil
		},
	}
}

// Run blocks until ctx is cancelled. The core keeps serving the last applied spec
// whenever the master is unreachable.
func (a *Agent) Run(ctx context.Context) error {
	defer a.spool.Close()
	defer a.core.Close()

	if b, err := readFile(a.cfg.Dir, fSpec); err == nil {
		spec := &pb.NodeSpec{}
		if proto.Unmarshal(b, spec) == nil {
			if err := a.core.Apply(spec); err != nil {
				a.log.Printf("apply cached spec r%d: %v", spec.Revision, err)
				a.applyErr.Store(err.Error())
			} else {
				a.log.Printf("serving cached spec r%d", spec.Revision)
			}
			a.applied.Store(spec.Revision)
			a.setWatch(spec)
		}
	}

	cc, err := grpc.NewClient(a.master,
		grpc.WithTransportCredentials(credentials.NewTLS(a.tlsConfig())),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(64<<20)),
	)
	if err != nil {
		return err
	}
	defer cc.Close()
	a.client = pb.NewNodeHubClient(cc)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); a.collectLoop(ctx) }()
	go func() { defer wg.Done(); a.pushLoop(ctx) }()
	go func() { defer wg.Done(); a.controlLoop(ctx) }()
	<-ctx.Done()
	wg.Wait()
	// final window so nothing counted since the last tick is lost
	a.collect(time.Now())
	return nil
}

func (a *Agent) wakePush() {
	select {
	case a.pushWake <- struct{}{}:
	default:
	}
}

// collectLoop closes a report window on wall-clock boundaries (e.g. :00, :10, ...).
func (a *Agent) collectLoop(ctx context.Context) {
	w := a.cfg.Window
	for {
		next := time.Now().Truncate(w).Add(w)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		a.collect(next)
		if did, err := a.spool.Compact(60); err != nil {
			a.log.Printf("spool compact: %v", err)
		} else if did {
			a.log.Printf("spool over limit, compacted oldest reports")
		}
	}
}

func (a *Agent) collect(end time.Time) {
	start := end.Truncate(a.cfg.Window)
	if start.Equal(end) {
		start = end.Add(-a.cfg.Window)
	}
	r := a.tracker.Collect(start.Unix(), end.Unix(), a.cfg.MaxHostsPerUser)
	if r == nil {
		return
	}
	r.BootId = a.bootID
	if err := a.spool.Append(r); err != nil {
		a.log.Printf("spool append: %v (report lost)", err)
		return
	}
	a.wakePush()
}

// pushLoop sends spooled reports strictly in seq order and deletes them on ack.
func (a *Agent) pushLoop(ctx context.Context) {
	backoff := time.Second
	for {
		r, err := a.spool.Oldest()
		if errors.Is(err, ErrEmpty) {
			select {
			case <-ctx.Done():
				return
			case <-a.pushWake:
			case <-time.After(30 * time.Second):
			}
			continue
		}
		if err != nil {
			a.log.Printf("spool read: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ack, err := a.client.PushTraffic(cctx, r)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		if ack.Seq == r.Seq {
			if err := a.spool.Delete(r.Seq); err != nil {
				a.log.Printf("spool delete %d: %v", r.Seq, err)
			}
		}
	}
}

func (a *Agent) controlLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		a.log.Printf("control stream ended: %v; retry in %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (a *Agent) session(ctx context.Context) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := a.client.Connect(sctx)
	if err != nil {
		return err
	}
	var sendMu sync.Mutex
	send := func(m *pb.NodeMsg) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(m)
	}
	hostname, _ := os.Hostname()
	if err := send(&pb.NodeMsg{M: &pb.NodeMsg_Hello{Hello: &pb.Hello{
		Version: Version, SingboxVersion: SingboxVersion(), AppliedRevision: a.applied.Load(),
		Hostname: hostname, Os: runtime.GOOS, Arch: runtime.GOARCH, BootId: a.bootID, Features: features(),
	}}}); err != nil {
		return err
	}
	a.markHealthy()
	if err := send(&pb.NodeMsg{M: &pb.NodeMsg_Upgrade{Upgrade: a.upgradeStatus()}}); err != nil {
		return err
	}
	a.log.Printf("connected to master %s as %s", a.master, a.nodeID)
	a.maybeRenew(sctx)
	a.certSend.Store(&send)
	defer a.certSend.CompareAndSwap(&send, nil)

	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		a.sendStatus(send)
		a.sendCertReport()
		for i := 1; ; i++ {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				if err := a.sendStatus(send); err != nil {
					cancel()
					return
				}
				if i%60 == 0 { // every 10 minutes: certbot renews behind our back
					a.sendCertReport()
				}
			}
		}
	}()

	var watchStop chan struct{}
	defer func() {
		if watchStop != nil {
			close(watchStop)
		}
	}()
	specCh := make(chan uint64, 1)
	go func() {
		for rev := range specCh {
			a.syncSpec(sctx, rev, send)
		}
	}()
	defer close(specCh)

	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		switch m := msg.M.(type) {
		case *pb.MasterMsg_Spec:
			if m.Spec.Revision != a.applied.Load() || a.applyErr.Load().(string) != "" {
				select {
				case specCh <- m.Spec.Revision:
				default: // a sync is already queued; it fetches the latest anyway
				}
			}
		case *pb.MasterMsg_Kick:
			kick := userSet(m.Kick.Users)
			n := a.tracker.Kick(func(r *connRec) bool { return kick[r.user] })
			a.log.Printf("kick %v: closed %d conns", m.Kick.Users, n)
		case *pb.MasterMsg_KickIps:
			ips := userSet(m.KickIps.Ips)
			n := a.tracker.Kick(func(r *connRec) bool { return r.user == m.KickIps.User && ips[r.srcIP] })
			a.log.Printf("device limit: user %s, closed %d conns from %v", m.KickIps.User, n, m.KickIps.Ips)
		case *pb.MasterMsg_CloseConns:
			ids := map[uint64]bool{}
			for _, id := range m.CloseConns.Ids {
				ids[id] = true
			}
			a.tracker.Kick(func(r *connRec) bool { return ids[r.id] })
		case *pb.MasterMsg_Watch:
			if m.Watch.On && watchStop == nil {
				watchStop = make(chan struct{})
				go a.watch(send, watchStop)
			} else if !m.Watch.On && watchStop != nil {
				close(watchStop)
				watchStop = nil
			}
		case *pb.MasterMsg_Upgrade:
			go a.upgrade(ctx, m.Upgrade, send)
		case *pb.MasterMsg_Ping:
		}
	}
}

// watch streams live connections once a second while the panel is looking.
func (a *Agent) watch(send func(*pb.NodeMsg) error, stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if err := send(&pb.NodeMsg{M: &pb.NodeMsg_Conns{Conns: a.tracker.Snapshot(500)}}); err != nil {
			return
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

func (a *Agent) sendStatus(send func(*pb.NodeMsg) error) error {
	up, down := a.tracker.totalUp.Load(), a.tracker.totalDown.Load()
	var ru, rd int64
	if !a.lastRateAt.IsZero() {
		dt := time.Since(a.lastRateAt).Seconds()
		if dt > 0 {
			ru, rd = int64(float64(up-a.lastTotals[0])/dt), int64(float64(down-a.lastTotals[1])/dt)
		}
	}
	a.lastTotals, a.lastRateAt = [2]int64{up, down}, time.Now()
	hb := &pb.Heartbeat{
		AppliedRevision: a.applied.Load(), Sys: sysInfo(), SpoolBacklog: uint64(a.spool.Len()),
		ActiveConns: int64(a.tracker.ActiveCount()), RateUp: ru, RateDown: rd,
		ApplyError: a.applyErr.Load().(string), Proc: a.proc.Sample(),
	}
	if err := send(&pb.NodeMsg{M: &pb.NodeMsg_Heartbeat{Heartbeat: hb}}); err != nil {
		return err
	}
	on := &pb.OnlineSnapshot{}
	for u, ips := range a.tracker.Online() {
		ou := &pb.OnlineUser{User: u}
		for ip := range ips {
			ou.Ips = append(ou.Ips, ip)
		}
		on.Users = append(on.Users, ou)
	}
	return send(&pb.NodeMsg{M: &pb.NodeMsg_Online{Online: on}})
}

func (a *Agent) syncSpec(ctx context.Context, rev uint64, send func(*pb.NodeMsg) error) {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	spec, err := a.client.FetchSpec(cctx, &pb.FetchSpecRequest{HaveRevision: a.applied.Load()})
	cancel()
	if err != nil {
		a.log.Printf("fetch spec: %v", err)
		return
	}
	res := &pb.ApplyResult{Revision: spec.Revision, Ok: true}
	if err := a.core.Apply(spec); err != nil {
		res.Ok, res.Error = false, err.Error()
		a.applyErr.Store(err.Error())
		a.log.Printf("apply r%d: %v", spec.Revision, err)
	} else {
		a.applyErr.Store("")
		a.applied.Store(spec.Revision)
		a.setWatch(spec)
		go a.sendCertReport()
		if b, err := proto.Marshal(spec); err == nil {
			writeAtomic(filepath.Join(a.cfg.Dir, fSpec), b)
		}
		a.log.Printf("applied r%d (%d inbounds)", spec.Revision, len(spec.Inbounds))
	}
	send(&pb.NodeMsg{M: &pb.NodeMsg_Apply{Apply: res}})
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// maybeRenew rotates the client certificate when it is within 30 days of expiry.
func (a *Agent) maybeRenew(ctx context.Context) {
	if time.Until(a.certExpiry()) > 30*24*time.Hour {
		return
	}
	keyPEM, csr, err := pki.NewKeyAndCSR("sbnode")
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := a.client.Renew(cctx, &pb.RenewRequest{CsrDer: csr})
	if err != nil {
		a.log.Printf("renew cert: %v", err)
		return
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: resp.CertDer})
	if err := writeAtomic(filepath.Join(a.cfg.Dir, fKey), keyPEM); err != nil {
		return
	}
	writeAtomic(filepath.Join(a.cfg.Dir, fCert), certPEM)
	if err := a.loadCert(); err == nil {
		a.log.Printf("client certificate renewed, expires %s", a.certExpiry().Format(time.DateOnly))
	}
}

// Status is printed by `sbnode status`.
func Status(dir string) (string, error) {
	id, err := readFile(dir, fID)
	if err != nil {
		return "", errors.New("not enrolled")
	}
	m, _ := readFile(dir, fMaster)
	s := fmt.Sprintf("node_id: %s\nmaster:  %s\n", strings.TrimSpace(string(id)), strings.TrimSpace(string(m)))
	if b, err := readFile(dir, fSpec); err == nil {
		spec := &pb.NodeSpec{}
		if proto.Unmarshal(b, spec) == nil {
			s += fmt.Sprintf("applied: r%d, %d inbounds\n", spec.Revision, len(spec.Inbounds))
		}
	}
	return s, nil
}

// Totals returns bytes counted by the core since start (up, down).
func (a *Agent) Totals() (int64, int64) { return a.tracker.totalUp.Load(), a.tracker.totalDown.Load() }

// Backlog is the number of reports not yet acknowledged by the master.
func (a *Agent) Backlog() int { return a.spool.Len() }

// CertWrites counts panel certificate files written (tests).
func (a *Agent) CertWrites() int { return a.core.CertWrites() }

// CoreStats returns (in-place user updates, inbound creations) for tests.
func (a *Agent) CoreStats() (int, int) { return a.core.Stats() }
