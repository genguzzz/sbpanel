// Package master is the control plane: web/API, node hub, ingest and policy.
package master

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"sbpanel/internal/pki"
)

type Config struct {
	DataDir        string // sqlite db + secrets
	WebListen      string // e.g. 127.0.0.1:8080
	GRPCListen     string // e.g. :9443, nodes dial this
	PublicGRPCAddr string // host:port nodes should use (shown in enroll command)
	AdminUser      string
	AdminPassword  string // used only when no admin exists
	KeepMinuteDays int
	KeepHourDays   int
	KeepDimDays    int
	TZ             string
	Web            fs.FS  // built SPA; nil = API only
	CloudflareAPI  string // Cloudflare API base URL (tests point it at a fake)
}

func (c *Config) defaults() {
	if c.DataDir == "" {
		c.DataDir = "/var/lib/sbmaster"
	}
	if c.WebListen == "" {
		c.WebListen = "127.0.0.1:8080"
	}
	if c.GRPCListen == "" {
		c.GRPCListen = ":9443"
	}
	if c.AdminUser == "" {
		c.AdminUser = "admin"
	}
	if c.KeepMinuteDays == 0 {
		c.KeepMinuteDays = 30
	}
	if c.KeepHourDays == 0 {
		c.KeepHourDays = 400
	}
	if c.KeepDimDays == 0 {
		c.KeepDimDays = 90
	}
	if c.TZ == "" {
		c.TZ = "Asia/Shanghai"
	}
}

type Master struct {
	bg        sync.WaitGroup // background notifications, waited for by stopBackground
	quit      chan struct{}
	quitOnce  sync.Once
	cfg       Config
	logins    loginLimiter
	topsMu    sync.Mutex
	topsCache *topsCache
	db        *sql.DB
	ca        *pki.CA
	hub       *Hub
	log       *log.Logger
	loc       *time.Location
	secret    []byte // session HMAC key
	specMu    sync.Mutex
	ingestMu  sync.Mutex
	box       *secretBox // encrypts DNS tokens and private keys at rest
	certs     *certManager
	probes    *prober
	metrics   *masterMetrics
	upd       *updates // release checks and upgrades (update.go)
}

func New(cfg Config) (*Master, error) {
	cfg.defaults()
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := openDB(filepath.Join(cfg.DataDir, "sbmaster.db"))
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(cfg.TZ)
	if err != nil {
		return nil, fmt.Errorf("tz: %w", err)
	}
	m := &Master{cfg: cfg, db: db, log: log.New(os.Stderr, "[master] ", log.LstdFlags), loc: loc, quit: make(chan struct{})}
	m.hub = newHub(m)
	m.metrics = newMasterMetrics()
	m.upd = newUpdates()
	eventHook = m.onEvent
	if err := m.initSecrets(); err != nil {
		return nil, err
	}
	if m.box, err = loadSecretBox(cfg.DataDir); err != nil {
		return nil, err
	}
	m.certs = newCertManager(m)
	m.probes = newProber(m)
	return m, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Master) initSecrets() error {
	certPEM, ok1 := getSetting(m.db, "ca_cert")
	keyPEM, ok2 := getSetting(m.db, "ca_key")
	if ok1 && ok2 {
		ca, err := pki.LoadCA(certPEM, keyPEM)
		if err != nil {
			return err
		}
		m.ca = ca
	} else {
		ca, err := pki.NewCA()
		if err != nil {
			return err
		}
		c, k, err := ca.PEM()
		if err != nil {
			return err
		}
		setSetting(m.db, "ca_cert", c)
		setSetting(m.db, "ca_key", k)
		m.ca = ca
	}
	sec, ok := getSetting(m.db, "session_secret")
	if !ok {
		sec = randHex(32)
		setSetting(m.db, "session_secret", sec)
	}
	m.secret, _ = hex.DecodeString(sec)

	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	if n == 0 {
		pw := m.cfg.AdminPassword
		generated := pw == ""
		if generated {
			pw = randHex(8)
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if _, err := m.db.Exec(`INSERT INTO admins(username, pass_hash, created_at) VALUES(?,?,?)`, m.cfg.AdminUser, string(h), now()); err != nil {
			return err
		}
		if generated {
			m.log.Printf("created admin %q with password %s (change it after login)", m.cfg.AdminUser, pw)
		} else {
			m.log.Printf("created admin %q", m.cfg.AdminUser)
		}
	}
	return nil
}

// CAFingerprint is what nodes pin at enrollment.
func (m *Master) CAFingerprint() string { return pki.Fingerprint(m.ca.Cert.Raw) }

func (m *Master) Run(ctx context.Context) error {
	gl, err := net.Listen("tcp", m.cfg.GRPCListen)
	if err != nil {
		return fmt.Errorf("grpc listen: %w", err)
	}
	wl, err := net.Listen("tcp", m.cfg.WebListen)
	if err != nil {
		return fmt.Errorf("web listen: %w", err)
	}
	m.log.Printf("grpc on %s, web on http://%s, CA sha256 %s", gl.Addr(), wl.Addr(), m.CAFingerprint())
	stop := make(chan struct{})
	go m.policyLoop(stop)
	go m.metricsLoop(stop)
	go m.certs.loop(stop)
	go m.probes.loop(stop)
	srv := &http.Server{Handler: m.routes(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 2 * time.Minute, WriteTimeout: 10 * time.Minute, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 2)
	go func() { errc <- m.hub.Serve(gl, m.ca) }()
	go func() { errc <- srv.Serve(wl) }()
	go m.updateLoop(stop)
	go m.afterStart(stop)
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	close(stop)
	m.stopBackground()
	m.hub.Stop()
	srv.Close()
	m.db.Close()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// Version is set at build time (-X sbpanel/internal/master.Version=...).
var Version = "dev"

// stopBackground cancels pending notifications and waits for running ones, so
// nothing touches the database after shutdown.
func (m *Master) stopBackground() {
	m.quitOnce.Do(func() { close(m.quit) })
	m.bg.Wait()
}
