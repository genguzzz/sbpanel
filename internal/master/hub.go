package master

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/time/rate"
	"google.golang.org/grpc/tap"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"sbpanel/internal/pb"
	"sbpanel/internal/pki"
)

const nodeCertValidity = 90 * 24 * time.Hour

type session struct {
	nodeID      string
	send        chan *pb.MasterMsg
	connectedAt time.Time
	remote      string

	mu         sync.Mutex
	hello      *pb.Hello
	hb         *pb.Heartbeat
	lastHB     time.Time
	online     []*pb.OnlineUser
	conns      *pb.ConnSnapshot
	watchUntil time.Time
	watching   bool
	certbot    []string // certbot lineages found on the node
	// probeSeen: inbound tag -> the probe's latest routed connection on the node
	probeSeen map[string]probeSight
	lastConns time.Time // when the node last sent a live snapshot
}

type probeSight struct {
	outbound string
	at       time.Time // node clock
}

// watchFor keeps the node streaming live connections for at least d.
func (h *Hub) watchFor(nodeID string, d time.Duration) {
	s := h.session(nodeID)
	if s == nil {
		return
	}
	s.mu.Lock()
	was := time.Now().Before(s.watchUntil)
	if u := time.Now().Add(d); u.After(s.watchUntil) {
		s.watchUntil = u
	}
	s.mu.Unlock()
	if !was {
		h.trySend(s, &pb.MasterMsg{M: &pb.MasterMsg_Watch{Watch: &pb.Watch{On: true}}})
	}
}

// probeSighting reports whether the node saw the probe routed on inbound tag at
// or after since, waiting up to wait for a fresh snapshot. known is false when
// the node sent no snapshot in time (offline or too old to report probes).
func (h *Hub) probeSighting(nodeID, tag string, since time.Time, wait time.Duration) (ps probeSight, seen, known bool) {
	deadline := time.Now().Add(wait)
	for {
		var fresh bool
		if s := h.session(nodeID); s != nil {
			s.mu.Lock()
			ps, seen = s.probeSeen[tag]
			fresh = s.lastConns.After(since)
			s.mu.Unlock()
			// a sighting older than this probe is a previous run's (allow clock skew)
			if seen && !ps.at.Before(since.Add(-3*time.Second)) {
				return ps, true, true
			}
		}
		if time.Now().After(deadline) {
			return ps, false, fresh
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *session) snapshot() (hello *pb.Hello, hb *pb.Heartbeat, last time.Time, online []*pb.OnlineUser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hello, s.hb, s.lastHB, s.online
}

type Hub struct {
	pb.UnimplementedNodeHubServer
	pb.UnimplementedEnrollServer
	enrollRate *rate.Limiter // anonymous Enroll calls
	m          *Master
	mu         sync.Mutex
	sessions   map[string]*session
	srv        *grpc.Server
}

func newHub(m *Master) *Hub {
	return &Hub{m: m, sessions: map[string]*session{}, enrollRate: rate.NewLimiter(1, 10)}
}

type ctxKey struct{}

func nodeIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// authenticate maps the verified client certificate to an enrolled node.
func (h *Hub) authenticate(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no peer")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.VerifiedChains[0]) == 0 {
		return "", status.Error(codes.Unauthenticated, "client certificate required")
	}
	leaf := ti.State.VerifiedChains[0][0]
	id, ok := pki.NodeIDFromCert(leaf)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "not a node certificate")
	}
	var serial string
	if err := h.m.db.QueryRow(`SELECT cert_serial FROM nodes WHERE id=?`, id).Scan(&serial); err != nil {
		return "", status.Error(codes.PermissionDenied, "unknown node")
	}
	// Only the most recently issued certificate is valid (revocation = re-enroll/delete).
	if serial != leaf.SerialNumber.String() {
		return "", status.Error(codes.PermissionDenied, "certificate revoked")
	}
	return id, nil
}

func (h *Hub) unaryAuth(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if info.FullMethod == pb.Enroll_Enroll_FullMethodName {
		return handler(ctx, req)
	}
	id, err := h.authenticate(ctx)
	if err != nil {
		return nil, err
	}
	return handler(context.WithValue(ctx, ctxKey{}, id), req)
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w wrappedStream) Context() context.Context { return w.ctx }

func (h *Hub) streamAuth(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	id, err := h.authenticate(ss.Context())
	if err != nil {
		return err
	}
	return handler(srv, wrappedStream{ss, context.WithValue(ss.Context(), ctxKey{}, id)})
}

func (h *Hub) Serve(ln net.Listener, ca *pki.CA) error {
	cert, err := ca.ServerCert()
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	tc := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}
	h.srv = grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tc)),
		grpc.UnaryInterceptor(h.unaryAuth),
		grpc.StreamInterceptor(h.streamAuth),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
		grpc.MaxRecvMsgSize(64<<20), // merged backlog reports after a long outage
		grpc.MaxConcurrentStreams(256),
		// Enroll is reachable without a client certificate: rate-limit it before the
		// (up to 64 MB) message is read, so anonymous callers can't exhaust memory
		grpc.InTapHandle(func(ctx context.Context, info *tap.Info) (context.Context, error) {
			if info.FullMethodName == pb.Enroll_Enroll_FullMethodName && !h.enrollRate.Allow() {
				return nil, status.Error(codes.ResourceExhausted, "too many enrollment attempts")
			}
			return ctx, nil
		}),
	)
	pb.RegisterEnrollServer(h.srv, h)
	pb.RegisterNodeHubServer(h.srv, h)
	return h.srv.Serve(ln)
}

func (h *Hub) Stop() {
	if h.srv != nil {
		h.srv.Stop()
	}
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

func (h *Hub) Enroll(ctx context.Context, req *pb.EnrollRequest) (*pb.EnrollResponse, error) {
	var nodeID string
	var exp int64
	var used int
	err := h.m.db.QueryRow(`SELECT node_id, expires_at, used FROM enroll_tokens WHERE token_hash=?`, hashToken(req.Token)).
		Scan(&nodeID, &exp, &used)
	if err != nil || used != 0 || exp < now() {
		return nil, status.Error(codes.PermissionDenied, "invalid or expired enrollment token")
	}
	cert, err := h.m.ca.SignNodeCSR(req.CsrDer, nodeID, nodeCertValidity)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "csr: %v", err)
	}
	tx, err := h.m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if res, _ := tx.Exec(`UPDATE enroll_tokens SET used=1 WHERE token_hash=? AND used=0`, hashToken(req.Token)); res == nil {
		return nil, status.Error(codes.Internal, "token update failed")
	} else if n, _ := res.RowsAffected(); n != 1 {
		return nil, status.Error(codes.PermissionDenied, "token already used")
	}
	if _, err := tx.Exec(`UPDATE nodes SET cert_serial=?, cert_expire=? WHERE id=?`,
		cert.SerialNumber.String(), cert.NotAfter.Unix(), nodeID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	addEvent(h.m.db, "node_enrolled", 0, nodeID, req.Hostname)
	h.m.log.Printf("node %s enrolled (%s)", nodeID, req.Hostname)
	return &pb.EnrollResponse{NodeId: nodeID, CertDer: cert.Raw, CaDer: h.m.ca.Cert.Raw}, nil
}

func (h *Hub) Renew(ctx context.Context, req *pb.RenewRequest) (*pb.EnrollResponse, error) {
	id := nodeIDFrom(ctx)
	cert, err := h.m.ca.SignNodeCSR(req.CsrDer, id, nodeCertValidity)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "csr: %v", err)
	}
	if _, err := h.m.db.Exec(`UPDATE nodes SET cert_serial=?, cert_expire=? WHERE id=?`,
		cert.SerialNumber.String(), cert.NotAfter.Unix(), id); err != nil {
		return nil, err
	}
	return &pb.EnrollResponse{NodeId: id, CertDer: cert.Raw, CaDer: h.m.ca.Cert.Raw}, nil
}

func (h *Hub) FetchSpec(ctx context.Context, req *pb.FetchSpecRequest) (*pb.NodeSpec, error) {
	spec, err := h.m.loadSpec(nodeIDFrom(ctx))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "spec: %v", err)
	}
	if err := h.m.fillCertMaterial(spec); err != nil {
		return nil, status.Errorf(codes.Internal, "spec: %v", err)
	}
	return spec, nil
}

func (h *Hub) PushTraffic(ctx context.Context, r *pb.TrafficReport) (*pb.Ack, error) {
	dup, err := h.m.ingest(nodeIDFrom(ctx), r)
	if err != nil {
		h.m.log.Printf("ingest %s seq %d: %v", nodeIDFrom(ctx), r.Seq, err)
		return nil, status.Errorf(codes.Unavailable, "ingest: %v", err)
	}
	return &pb.Ack{Seq: r.Seq, Duplicate: dup}, nil
}

func (h *Hub) Connect(stream pb.NodeHub_ConnectServer) error {
	id := nodeIDFrom(stream.Context())
	s := &session{nodeID: id, send: make(chan *pb.MasterMsg, 32), connectedAt: time.Now()}
	if p, ok := peer.FromContext(stream.Context()); ok {
		s.remote = p.Addr.String()
	}
	h.mu.Lock()
	if old := h.sessions[id]; old != nil {
		close(old.send) // a node has one live stream; the newest wins
	}
	h.sessions[id] = s
	h.mu.Unlock()
	h.m.log.Printf("node %s connected from %s", id, s.remote)
	h.m.db.Exec(`UPDATE nodes SET last_seen=?, address=? WHERE id=?`, now(), hostOnly(s.remote), id)
	addEvent(h.m.db, "node_online", 0, id, s.remote)

	defer func() {
		h.mu.Lock()
		if h.sessions[id] == s {
			delete(h.sessions, id)
			close(s.send)
		}
		h.mu.Unlock()
		addEvent(h.m.db, "node_offline", 0, id, "")
		h.m.log.Printf("node %s disconnected", id)
	}()

	errc := make(chan error, 1)
	go func() {
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		watchTick := time.NewTicker(5 * time.Second)
		defer watchTick.Stop()
		for {
			select {
			case msg, ok := <-s.send:
				if !ok {
					errc <- errors.New("replaced")
					return
				}
				if w, isWatch := msg.M.(*pb.MasterMsg_Watch); isWatch && w.Watch.On {
					s.mu.Lock()
					s.watching = true
					s.mu.Unlock()
				}
				if err := stream.Send(msg); err != nil {
					errc <- err
					return
				}
			case <-ping.C:
				if err := stream.Send(&pb.MasterMsg{M: &pb.MasterMsg_Ping{Ping: &pb.Ping{Ts: now()}}}); err != nil {
					errc <- err
					return
				}
			case <-watchTick.C:
				s.mu.Lock()
				stop := s.watching && time.Now().After(s.watchUntil)
				if stop {
					s.watching, s.conns = false, nil
				}
				s.mu.Unlock()
				if stop {
					if err := stream.Send(&pb.MasterMsg{M: &pb.MasterMsg_Watch{Watch: &pb.Watch{On: false}}}); err != nil {
						errc <- err
						return
					}
				}
			case <-stream.Context().Done():
				errc <- stream.Context().Err()
				return
			}
		}
	}()
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errc <- err
				return
			}
			h.handle(s, msg)
		}
	}()
	err := <-errc
	return err
}

func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func (h *Hub) trySend(s *session, msg *pb.MasterMsg) {
	defer func() { recover() }() // send on a channel closed by a replacing stream
	select {
	case s.send <- msg:
	default:
		h.m.log.Printf("node %s: send queue full, dropping message", s.nodeID)
	}
}

func (h *Hub) handle(s *session, msg *pb.NodeMsg) {
	db := h.m.db
	switch m := msg.M.(type) {
	case *pb.NodeMsg_Hello:
		s.mu.Lock()
		s.hello = m.Hello
		s.mu.Unlock()
		info, _ := json.Marshal(map[string]any{
			"version": m.Hello.Version, "singbox": m.Hello.SingboxVersion, "hostname": m.Hello.Hostname,
			"os": m.Hello.Os, "arch": m.Hello.Arch,
		})
		db.Exec(`UPDATE nodes SET info_json=?, applied_revision=?, last_seen=? WHERE id=?`, string(info), m.Hello.AppliedRevision, now(), s.nodeID)
		var rev uint64
		db.QueryRow(`SELECT revision FROM nodes WHERE id=?`, s.nodeID).Scan(&rev)
		if rev == 0 {
			h.m.reconcile(s.nodeID)
			db.QueryRow(`SELECT revision FROM nodes WHERE id=?`, s.nodeID).Scan(&rev)
		}
		if rev != m.Hello.AppliedRevision {
			h.trySend(s, &pb.MasterMsg{M: &pb.MasterMsg_Spec{Spec: &pb.SpecAvailable{Revision: rev}}})
		}
	case *pb.NodeMsg_Heartbeat:
		s.mu.Lock()
		s.hb = m.Heartbeat
		s.lastHB = time.Now()
		s.mu.Unlock()
		h.m.recordHeartbeat(s.nodeID, m.Heartbeat)
		h.m.recordWan(s.nodeID, m.Heartbeat.GetSys())
		db.Exec(`UPDATE nodes SET last_seen=?, applied_revision=?, apply_error=? WHERE id=?`,
			now(), m.Heartbeat.AppliedRevision, m.Heartbeat.ApplyError, s.nodeID)
	case *pb.NodeMsg_Apply:
		if m.Apply.Ok {
			db.Exec(`UPDATE nodes SET applied_revision=?, apply_error='' WHERE id=?`, m.Apply.Revision, s.nodeID)
		} else {
			db.Exec(`UPDATE nodes SET apply_error=? WHERE id=?`, m.Apply.Error, s.nodeID)
			addEvent(db, "apply_failed", 0, s.nodeID, m.Apply.Error)
		}
	case *pb.NodeMsg_Conns:
		s.mu.Lock()
		if s.watching {
			s.conns = m.Conns
		}
		s.lastConns = time.Now()
		for _, c := range m.Conns.Conns {
			if c.User == probeUser {
				if s.probeSeen == nil {
					s.probeSeen = map[string]probeSight{}
				}
				s.probeSeen[c.Inbound] = probeSight{outbound: c.Outbound, at: time.Unix(c.Start, 0)}
			}
		}
		s.mu.Unlock()
	case *pb.NodeMsg_Upgrade:
		h.m.onNodeUpgrade(s.nodeID, m.Upgrade)
	case *pb.NodeMsg_Certs:
		s.mu.Lock()
		s.certbot = m.Certs.CertbotNames
		s.mu.Unlock()
		h.m.onCertReport(s.nodeID, m.Certs)
	case *pb.NodeMsg_Online:
		for i := 0; i < len(m.Online.Users); i++ {
			if m.Online.Users[i].User == probeUser { // older nodes report the probe as online
				m.Online.Users = append(m.Online.Users[:i], m.Online.Users[i+1:]...)
				i--
			}
		}
		s.mu.Lock()
		s.online = m.Online.Users
		s.mu.Unlock()
		defer h.m.enforceDevices()
		t := now()
		// one transaction (one commit) per snapshot instead of one per user × IP
		if tx, err := db.Begin(); err == nil {
			for _, u := range m.Online.Users {
				uid := parseUserID(u.User)
				for _, ip := range u.Ips {
					tx.Exec(`INSERT INTO online_ip(user_id,node_id,ip,first_seen,last_seen) VALUES(?,?,?,?,?)
						ON CONFLICT(user_id,node_id,ip) DO UPDATE SET last_seen=excluded.last_seen`, uid, s.nodeID, ip, t, t)
				}
			}
			tx.Commit()
		}
	}
}

func (h *Hub) notifySpec(nodeID string, rev uint64) {
	h.mu.Lock()
	s := h.sessions[nodeID]
	h.mu.Unlock()
	if s != nil {
		h.trySend(s, &pb.MasterMsg{M: &pb.MasterMsg_Spec{Spec: &pb.SpecAvailable{Revision: rev}}})
	}
}

func (h *Hub) kick(users []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		h.trySend(s, &pb.MasterMsg{M: &pb.MasterMsg_Kick{Kick: &pb.Kick{Users: users}}})
	}
}

// drop closes a node's control stream (used when the node is deleted).
func (h *Hub) drop(nodeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[nodeID]; s != nil {
		delete(h.sessions, nodeID)
		close(s.send)
	}
}

func (h *Hub) session(nodeID string) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[nodeID]
}
