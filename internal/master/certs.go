package master

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"sbpanel/internal/pb"
)

// Certificate sources.
const (
	certACME   = "acme"   // issued on the master via ACME DNS-01 (Cloudflare)
	certUpload = "upload" // PEM pasted or uploaded by the admin
	certNode   = "node"   // files on a node (certbot etc.); the panel only references and monitors them
)

// certRow is one certificate record.
type certRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	SANs        []string `json:"sans"`
	Issuer      string   `json:"issuer"`
	NotBefore   int64    `json:"not_before"`
	NotAfter    int64    `json:"not_after"`
	Fingerprint string   `json:"fingerprint"`
	AutoRenew   bool     `json:"auto_renew"`
	ACME        acmeOpts `json:"acme"`
	NodeID      string   `json:"node_id"`
	CertPath    string   `json:"cert_path"`
	KeyPath     string   `json:"key_path"`
	LastError   string   `json:"last_error"`
	Attempts    int      `json:"attempts"`
	NextAttempt int64    `json:"next_attempt"`
	WarnedAt    int64    `json:"-"`
	RenewedAt   int64    `json:"renewed_at"`
	ReportedAt  int64    `json:"reported_at"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
	HasMaterial bool     `json:"has_material"`
}

// acmeOpts is how an ACME certificate is (re)issued.
type acmeOpts struct {
	ProviderID int64    `json:"provider_id,omitempty"`
	CA         string   `json:"ca,omitempty"` // letsencrypt | zerossl
	Staging    bool     `json:"staging,omitempty"`
	Email      string   `json:"email,omitempty"`
	Domains    []string `json:"domains,omitempty"`
}

const certCols = `id, name, source, sans_json, issuer, not_before, not_after, fingerprint, auto_renew, acme_json,
	node_id, cert_path, key_path, last_error, attempts, next_attempt, warned_at, renewed_at, reported_at, created_at, updated_at, cert_pem<>''`

func scanCert(s func(...any) error) (certRow, error) {
	var c certRow
	var sans, acme string
	err := s(&c.ID, &c.Name, &c.Source, &sans, &c.Issuer, &c.NotBefore, &c.NotAfter, &c.Fingerprint, &c.AutoRenew, &acme,
		&c.NodeID, &c.CertPath, &c.KeyPath, &c.LastError, &c.Attempts, &c.NextAttempt, &c.WarnedAt, &c.RenewedAt, &c.ReportedAt,
		&c.CreatedAt, &c.UpdatedAt, &c.HasMaterial)
	json.Unmarshal([]byte(sans), &c.SANs)
	json.Unmarshal([]byte(acme), &c.ACME)
	if c.SANs == nil {
		c.SANs = []string{}
	}
	return c, err
}

func (m *Master) getCert(id string) (certRow, error) {
	return scanCert(m.db.QueryRow(`SELECT `+certCols+` FROM certs WHERE id=?`, id).Scan)
}

func (m *Master) listCerts() ([]certRow, error) {
	return scanRows(m, `SELECT `+certCols+` FROM certs ORDER BY created_at`, nil, scanCert)
}

// certMaterial returns the PEM chain and decrypted key of a panel certificate.
func (m *Master) certMaterial(id string) (chain, key []byte, err error) {
	var c, k string
	if err := m.db.QueryRow(`SELECT cert_pem, key_enc FROM certs WHERE id=?`, id).Scan(&c, &k); err != nil {
		return nil, nil, err
	}
	if c == "" {
		return nil, nil, errors.New("certificate not issued yet")
	}
	kb, err := m.box.open(k, "cert_key:"+id)
	if err != nil {
		return nil, nil, err
	}
	return []byte(c), kb, nil
}

// ---------- PEM validation ----------

type certInfo struct {
	SANs        []string
	Issuer      string
	NotBefore   time.Time
	NotAfter    time.Time
	Fingerprint string
	SelfSigned  bool
}

func leafInfo(leaf *x509.Certificate) certInfo {
	in := certInfo{NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter}
	in.SANs = append(in.SANs, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		in.SANs = append(in.SANs, ip.String())
	}
	if len(in.SANs) == 0 && leaf.Subject.CommonName != "" {
		in.SANs = []string{leaf.Subject.CommonName}
	}
	in.Issuer = leaf.Issuer.CommonName
	if in.Issuer == "" && len(leaf.Issuer.Organization) > 0 {
		in.Issuer = leaf.Issuer.Organization[0]
	}
	in.SelfSigned = bytesEqual(leaf.RawIssuer, leaf.RawSubject) && leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil
	if in.SelfSigned {
		in.Issuer = "Self-signed"
	}
	sum := sha256.Sum256(leaf.Raw)
	in.Fingerprint = hex.EncodeToString(sum[:])
	return in
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

// parsePEMPair validates a certificate chain and private key: both must parse, the
// key must belong to the leaf, and the leaf must not be expired at now. It returns
// the normalised chain (certificates only, leaf first) and key PEM.
func parsePEMPair(certPEM, keyPEM string, now time.Time) (certInfo, []byte, []byte, error) {
	var chain []byte
	var leaf *x509.Certificate
	rest := []byte(strings.TrimSpace(certPEM))
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return certInfo{}, nil, nil, fmt.Errorf("Can't parse the certificate: %v", err)
		}
		if leaf == nil {
			leaf = c
		}
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: blk.Bytes})...)
	}
	if leaf == nil {
		return certInfo{}, nil, nil, errors.New("No PEM certificate found (it should start with -----BEGIN CERTIFICATE-----)")
	}
	kb := []byte(strings.TrimSpace(keyPEM) + "\n")
	if blk, _ := pem.Decode(kb); blk == nil || !strings.Contains(blk.Type, "PRIVATE KEY") {
		return certInfo{}, nil, nil, errors.New("No PEM private key found (it should start with -----BEGIN ... PRIVATE KEY-----)")
	}
	if _, err := tls.X509KeyPair(chain, kb); err != nil {
		if strings.Contains(err.Error(), "does not match") {
			return certInfo{}, nil, nil, errors.New("The private key does not match the certificate")
		}
		return certInfo{}, nil, nil, fmt.Errorf("Invalid private key: %v", err)
	}
	in := leafInfo(leaf)
	if now.After(leaf.NotAfter) {
		return in, nil, nil, fmt.Errorf("The certificate expired on %s", leaf.NotAfter.Format(time.DateOnly))
	}
	return in, chain, kb, nil
}

// certCovers reports whether a SAN list covers a host name (one-label wildcards).
func certCovers(sans []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, s := range sans {
		s = strings.ToLower(s)
		if s == host {
			return true
		}
		if strings.HasPrefix(s, "*.") {
			if i := strings.IndexByte(host, '.'); i > 0 && host[i+1:] == s[2:] {
				return true
			}
		}
	}
	return false
}

// primaryName is the first non-wildcard SAN, used as the default SNI.
func primaryName(sans []string) string {
	for _, s := range sans {
		if !strings.HasPrefix(s, "*.") {
			return s
		}
	}
	return ""
}

func newCertID() string { return "c" + randHex(5) }

// storeMaterial saves a validated chain + key on a certificate record.
func (m *Master) storeMaterial(id string, in certInfo, chain, key []byte) error {
	sans, _ := json.Marshal(in.SANs)
	_, err := m.db.Exec(`UPDATE certs SET cert_pem=?, key_enc=?, sans_json=?, issuer=?, not_before=?, not_after=?, fingerprint=?,
		last_error='', attempts=0, next_attempt=0, warned_at=0, updated_at=? WHERE id=?`,
		string(chain), m.box.seal(key, "cert_key:"+id), string(sans), in.Issuer, in.NotBefore.Unix(), in.NotAfter.Unix(), in.Fingerprint, now(), id)
	return err
}

// certNodes lists nodes with an enabled inbound that references the certificate.
func (m *Master) certNodes(id string) []string {
	ns, _ := scanRows(m, `SELECT DISTINCT node_id FROM inbounds WHERE json_extract(options_json,'$._cert')=?`, []any{id},
		func(s func(...any) error) (string, error) {
			var n string
			return n, s(&n)
		})
	return ns
}

type certUse struct {
	ID     int64  `json:"id"`
	Tag    string `json:"tag"`
	NodeID string `json:"node_id"`
	Node   string `json:"node"`
	SNI    string `json:"sni"`
}

func (m *Master) certUses() map[string][]certUse {
	out := map[string][]certUse{}
	rows, err := m.db.Query(`SELECT i.id, i.tag, i.node_id, COALESCE(n.name,''), json_extract(i.options_json,'$._cert'),
		COALESCE(json_extract(i.options_json,'$.tls.server_name'),'')
		FROM inbounds i LEFT JOIN nodes n ON n.id=i.node_id WHERE json_extract(i.options_json,'$._cert') IS NOT NULL`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u certUse
		var cid string
		if rows.Scan(&u.ID, &u.Tag, &u.NodeID, &u.Node, &cid, &u.SNI) == nil {
			out[cid] = append(out[cid], u)
		}
	}
	return out
}

// ---------- spec integration ----------

// applyCertHint resolves an inbound's "_cert" panel hint. Panel certificates
// (ACME / upload) travel as CertBundle and the node fills in the file paths;
// node-local certificates are plain paths in the options.
func applyCertHint(db *sql.DB, opts map[string]any, spec *pb.NodeSpec, in *pb.InboundSpec) {
	id, _ := opts["_cert"].(string)
	if id == "" {
		return
	}
	t, _ := opts["tls"].(map[string]any)
	if t == nil {
		t = map[string]any{}
		opts["tls"] = t
	}
	t["enabled"] = true
	delete(t, "acme")
	delete(t, "certificate")
	delete(t, "key")
	delete(t, "certificate_path")
	delete(t, "key_path")
	var source, certPath, keyPath, pemC, keyEnc string
	if db.QueryRow(`SELECT source, cert_path, key_path, cert_pem, key_enc FROM certs WHERE id=?`, id).
		Scan(&source, &certPath, &keyPath, &pemC, &keyEnc) != nil {
		in.CertId = id // unknown: the node reports "not on disk", which is the honest state
		return
	}
	if source == certNode {
		t["certificate_path"], t["key_path"] = certPath, keyPath
		return
	}
	in.CertId = id
	for _, b := range spec.Certs {
		if b.Id == id {
			return
		}
	}
	if pemC != "" {
		spec.Certs = append(spec.Certs, &pb.CertBundle{Id: id, Hash: shortHash([]byte(pemC), []byte(keyEnc))})
	}
}

// addCertWatches lists the node-local certificates of a node for reporting.
func addCertWatches(db *sql.DB, spec *pb.NodeSpec, nodeID string) {
	rows, err := db.Query(`SELECT id, cert_path, key_path FROM certs WHERE source='node' AND node_id=? ORDER BY id`, nodeID)
	if err == nil {
		for rows.Next() {
			w := &pb.CertWatch{}
			if rows.Scan(&w.Id, &w.CertPath, &w.KeyPath) == nil {
				spec.WatchCerts = append(spec.WatchCerts, w)
			}
		}
		rows.Close()
	}
	sort.Slice(spec.Certs, func(i, j int) bool { return spec.Certs[i].Id < spec.Certs[j].Id })
}

// fillCertMaterial adds PEM material to a spec just before it is served to the
// node; the stored spec (and the admin spec view) carries only ids and hashes.
func (m *Master) fillCertMaterial(spec *pb.NodeSpec) error {
	for _, b := range spec.Certs {
		c, k, err := m.certMaterial(b.Id)
		if err != nil {
			return fmt.Errorf("cert %s: %w", b.Id, err)
		}
		b.CertPem, b.KeyPem = c, k
	}
	return nil
}

// onCertReport stores what a node found in its local certificate files.
func (m *Master) onCertReport(nodeID string, r *pb.CertReport) {
	for _, st := range r.Certs {
		if st.Error != "" && st.NotAfter == 0 {
			m.db.Exec(`UPDATE certs SET last_error=?, reported_at=? WHERE id=? AND node_id=? AND source='node'`, st.Error, now(), st.Id, nodeID)
			continue
		}
		sans, _ := json.Marshal(st.Sans)
		m.db.Exec(`UPDATE certs SET sans_json=?, issuer=?, not_before=?, not_after=?, fingerprint=?, last_error=?, reported_at=?,
			warned_at=CASE WHEN fingerprint<>? THEN 0 ELSE warned_at END
			WHERE id=? AND node_id=? AND source='node'`,
			string(sans), st.Issuer, st.NotBefore, st.NotAfter, st.Fingerprint, st.Error, now(), st.Fingerprint, st.Id, nodeID)
	}
}

// ---------- API ----------

type certOut struct {
	certRow
	Node     string    `json:"node,omitempty"`
	Uses     []certUse `json:"uses"`
	DaysLeft int       `json:"days_left"`
	Status   string    `json:"status"` // ok | expiring | expired | pending | error | renewing
	Renewing bool      `json:"renewing"`
}

func (m *Master) certOut(c certRow, uses map[string][]certUse, nodes map[string]string) certOut {
	o := certOut{certRow: c, Uses: uses[c.ID], Node: nodes[c.NodeID], Renewing: m.certs.isRenewing(c.ID)}
	if o.Uses == nil {
		o.Uses = []certUse{}
	}
	t := time.Now()
	if c.NotAfter > 0 {
		o.DaysLeft = int(time.Unix(c.NotAfter, 0).Sub(t).Hours() / 24)
	}
	switch {
	case o.Renewing:
		o.Status = "renewing"
	case c.NotAfter == 0 && c.LastError != "":
		o.Status = "error"
	case c.NotAfter == 0:
		o.Status = "pending"
	case t.Unix() > c.NotAfter:
		o.Status = "expired"
	case o.DaysLeft <= 14:
		o.Status = "expiring"
	case c.LastError != "":
		o.Status = "error"
	default:
		o.Status = "ok"
	}
	return o
}

func (m *Master) nodeNames() map[string]string {
	out := map[string]string{}
	rows, err := m.db.Query(`SELECT id, name FROM nodes`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, n string
		rows.Scan(&id, &n)
		out[id] = n
	}
	return out
}

func (m *Master) apiCerts(w http.ResponseWriter, r *http.Request) {
	cs, err := m.listCerts()
	if errf(w, err) {
		return
	}
	uses, nodes := m.certUses(), m.nodeNames()
	out := make([]certOut, 0, len(cs))
	for _, c := range cs {
		out = append(out, m.certOut(c, uses, nodes).localize(langOf(w)))
	}
	writeJSON(w, 200, out)
}

func (m *Master) apiCert(w http.ResponseWriter, r *http.Request) {
	c, err := m.getCert(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "Certificate not found")
		return
	}
	writeJSON(w, 200, m.certOut(c, m.certUses(), m.nodeNames()).localize(langOf(w)))
}

// localize translates the stored English issuer placeholder and last error.
func (o certOut) localize(lang string) certOut {
	o.Issuer, o.LastError = L(lang, o.Issuer), L(lang, o.LastError)
	return o
}

func certName(name string, sans []string) string {
	name = strings.TrimSpace(name)
	if name == "" && len(sans) > 0 {
		name = sans[0]
	}
	if len([]rune(name)) > 64 {
		name = string([]rune(name)[:64])
	}
	return name
}

// apiUploadCert validates (and with dry_run only previews) a PEM certificate + key.
func (m *Master) apiUploadCert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	in, chain, key, err := parsePEMPair(req.CertPEM, req.KeyPEM, time.Now())
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": L(langOf(w), err.Error()), "sans": in.SANs, "not_after": unixOr0(in.NotAfter)})
		return
	}
	preview := map[string]any{"sans": in.SANs, "issuer": in.Issuer, "not_before": in.NotBefore.Unix(), "not_after": in.NotAfter.Unix(),
		"fingerprint": in.Fingerprint, "self_signed": in.SelfSigned}
	if req.DryRun {
		writeJSON(w, 200, preview)
		return
	}
	id := newCertID()
	if _, err := m.db.Exec(`INSERT INTO certs(id, name, source, auto_renew, created_at, updated_at) VALUES(?,?,?,0,?,?)`,
		id, certName(req.Name, in.SANs), certUpload, now(), now()); errf(w, err) {
		return
	}
	if err := m.storeMaterial(id, in, chain, key); errf(w, err) {
		return
	}
	m.audit(r, "cert.upload", id, strings.Join(in.SANs, ","))
	preview["id"] = id
	writeJSON(w, 200, preview)
}

func unixOr0(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

var fqdnRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func normDomains(ds []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, d := range ds {
		d = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
		if d == "" || seen[d] {
			continue
		}
		if !fqdnRe.MatchString(d) {
			return nil, fmt.Errorf("Invalid domain: %s", d)
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("At least one domain is required")
	}
	if len(out) > 50 {
		return nil, errors.New("A certificate can have at most 50 domains")
	}
	return out, nil
}

// apiIssueCert creates an ACME certificate record and starts issuing it.
func (m *Master) apiIssueCert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		acmeOpts
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ds, err := normDomains(req.Domains)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	req.Domains = ds
	if req.CA == "" {
		req.CA = "letsencrypt"
	}
	if req.CA != "letsencrypt" && req.CA != "zerossl" {
		writeErr(w, 400, "ca must be letsencrypt or zerossl")
		return
	}
	if req.CA == "zerossl" && req.Email == "" {
		writeErr(w, 400, "ZeroSSL needs an email address")
		return
	}
	var tokenSet int
	m.db.QueryRow(`SELECT COUNT(*) FROM dns_providers WHERE id=?`, req.ProviderID).Scan(&tokenSet)
	if tokenSet == 0 {
		writeErr(w, 400, "Connect a DNS provider (Cloudflare) first")
		return
	}
	if err := m.providerCovers(req.ProviderID, ds); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id := newCertID()
	aj, _ := json.Marshal(req.acmeOpts)
	sans, _ := json.Marshal(ds)
	if _, err := m.db.Exec(`INSERT INTO certs(id, name, source, sans_json, auto_renew, acme_json, created_at, updated_at) VALUES(?,?,?,?,1,?,?,?)`,
		id, certName(req.Name, ds), certACME, string(sans), string(aj), now(), now()); errf(w, err) {
		return
	}
	m.audit(r, "cert.issue", id, strings.Join(ds, ","))
	m.certs.renewAsync(id)
	writeJSON(w, 200, map[string]string{"id": id})
}

var certbotNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// apiNodeCert references certificate files that already exist on a node.
func (m *Master) apiNodeCert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		NodeID   string `json:"node_id"`
		CertPath string `json:"cert_path"`
		KeyPath  string `json:"key_path"`
		Certbot  string `json:"certbot"` // lineage name: fills both paths
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Certbot != "" {
		if !certbotNameRe.MatchString(req.Certbot) {
			writeErr(w, 400, "Invalid certbot name")
			return
		}
		req.CertPath = "/etc/letsencrypt/live/" + req.Certbot + "/fullchain.pem"
		req.KeyPath = "/etc/letsencrypt/live/" + req.Certbot + "/privkey.pem"
		if req.Name == "" {
			req.Name = req.Certbot
		}
	}
	for _, p := range []string{req.CertPath, req.KeyPath} {
		if !validNodeFilePath(p) {
			writeErr(w, 400, "Certificate and key need absolute paths on the node")
			return
		}
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE id=?`, req.NodeID).Scan(&n)
	if n == 0 {
		writeErr(w, 400, "Node not found")
		return
	}
	id := newCertID()
	name := req.Name
	if name == "" {
		name = path.Base(path.Dir(req.CertPath))
	}
	if _, err := m.db.Exec(`INSERT INTO certs(id, name, source, auto_renew, node_id, cert_path, key_path, created_at, updated_at) VALUES(?,?,?,0,?,?,?,?,?)`,
		id, certName(name, nil), certNode, req.NodeID, req.CertPath, req.KeyPath, now(), now()); errf(w, err) {
		return
	}
	m.audit(r, "cert.node", id, req.NodeID+":"+req.CertPath)
	m.reconcile(req.NodeID) // the node starts watching the files and reports back
	writeJSON(w, 200, map[string]string{"id": id})
}

func (m *Master) apiUpdateCert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := m.getCert(id)
	if err != nil {
		writeErr(w, 404, "Certificate not found")
		return
	}
	var req struct {
		Name      *string `json:"name"`
		AutoRenew *bool   `json:"auto_renew"`
		CertPEM   string  `json:"cert_pem"`
		KeyPEM    string  `json:"key_pem"`
		CertPath  string  `json:"cert_path"`
		KeyPath   string  `json:"key_path"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Name != nil {
		m.db.Exec(`UPDATE certs SET name=?, updated_at=? WHERE id=?`, certName(*req.Name, c.SANs), now(), id)
	}
	if req.AutoRenew != nil {
		if *req.AutoRenew && c.Source != certACME {
			writeErr(w, 400, "Only ACME certificates can auto-renew (certbot renews its own certificates)")
			return
		}
		m.db.Exec(`UPDATE certs SET auto_renew=?, updated_at=? WHERE id=?`, *req.AutoRenew, now(), id)
	}
	if req.CertPEM != "" {
		if c.Source != certUpload {
			writeErr(w, 400, "Only uploaded certificates can have their PEM replaced")
			return
		}
		in, chain, key, err := parsePEMPair(req.CertPEM, req.KeyPEM, time.Now())
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if err := m.storeMaterial(id, in, chain, key); errf(w, err) {
			return
		}
		m.audit(r, "cert.replace", id, strings.Join(in.SANs, ","))
		m.reconcile(m.certNodes(id)...)
		addEvent(m.db, "cert_renewed", 0, "", fmt.Sprintf("%s · replaced with a new certificate, expires %s", c.Name, in.NotAfter.In(m.loc).Format(time.DateOnly)))
	}
	if req.CertPath != "" || req.KeyPath != "" {
		if c.Source != certNode {
			writeErr(w, 400, "Only node-local certificates have paths to change")
			return
		}
		cp, kp := nz(req.CertPath, c.CertPath), nz(req.KeyPath, c.KeyPath)
		for _, p := range []string{cp, kp} {
			if !validNodeFilePath(p) {
				writeErr(w, 400, "Certificate and key need absolute paths on the node")
				return
			}
		}
		m.db.Exec(`UPDATE certs SET cert_path=?, key_path=?, updated_at=? WHERE id=?`, cp, kp, now(), id)
		m.reconcile(append(m.certNodes(id), c.NodeID)...)
	}
	m.audit(r, "cert.update", id, "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (m *Master) apiRenewCert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := m.getCert(id)
	if err != nil {
		writeErr(w, 404, "Certificate not found")
		return
	}
	switch c.Source {
	case certACME:
		if !m.certs.renewAsync(id) {
			writeErr(w, 409, "Renewal already in progress")
			return
		}
		m.audit(r, "cert.renew", id, "")
		writeJSON(w, 202, map[string]bool{"started": true})
	case certNode:
		// certbot renews the files itself; ask the node to look at them again
		m.db.Exec(`UPDATE certs SET reported_at=0 WHERE id=?`, id)
		m.reconcile(c.NodeID)
		if s := m.hub.session(c.NodeID); s != nil {
			m.hub.trySend(s, &pb.MasterMsg{M: &pb.MasterMsg_Spec{Spec: &pb.SpecAvailable{Revision: 0}}})
		}
		writeJSON(w, 200, map[string]any{"started": false, "hint": T(langOf(w), "Node-local certificates are renewed by certbot (certbot renew); asked the node to reload the files")})
	default:
		writeErr(w, 400, "Uploaded certificates can't auto-renew: upload a new PEM to replace it")
	}
}

func (m *Master) apiDownloadCert(w http.ResponseWriter, r *http.Request) { m.downloadCert(w, r, false) }
func (m *Master) apiDownloadKey(w http.ResponseWriter, r *http.Request)  { m.downloadCert(w, r, true) }

func (m *Master) downloadCert(w http.ResponseWriter, r *http.Request, wantKey bool) {
	id := r.PathValue("id")
	c, err := m.getCert(id)
	if err != nil || c.Source == certNode {
		writeErr(w, 404, "Certificate not found, or it is stored on the node")
		return
	}
	chain, key, err := m.certMaterial(id)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	name := strings.NewReplacer("*", "_", "/", "_", " ", "_").Replace(nz(primaryName(c.SANs), c.ID))
	body := chain
	name += ".fullchain.pem"
	if wantKey {
		body, name = key, strings.TrimSuffix(name, ".fullchain.pem")+".key"
		m.audit(r, "cert.download_key", id, "")
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}

func (m *Master) apiDeleteCert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := m.getCert(id)
	if err != nil {
		writeErr(w, 404, "Certificate not found")
		return
	}
	if uses := m.certUses()[id]; len(uses) > 0 {
		tags := make([]string, len(uses))
		for i, u := range uses {
			tags[i] = u.Node + "/" + u.Tag
		}
		writeJSON(w, 409, map[string]any{"error": T(langOf(w), "The certificate is still used by inbounds: %s", strings.Join(tags, ", ")), "uses": uses})
		return
	}
	m.db.Exec(`DELETE FROM certs WHERE id=?`, id)
	m.audit(r, "cert.delete", id, c.Name)
	if c.NodeID != "" {
		m.reconcile(c.NodeID)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// apiCertbotNames lists certbot lineages a node reported (path suggestions).
func (m *Master) apiCertbotNames(w http.ResponseWriter, r *http.Request) {
	out := []string{}
	if s := m.hub.session(r.URL.Query().Get("node")); s != nil {
		s.mu.Lock()
		out = append(out, s.certbot...)
		s.mu.Unlock()
	}
	writeJSON(w, 200, map[string]any{"names": out})
}

// certForSNI picks a panel certificate covering host (inbound editor default).
func (m *Master) apiCertMatch(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	node := r.URL.Query().Get("node")
	cs, _ := m.listCerts()
	best := ""
	var bestAfter int64
	for _, c := range cs {
		if c.Source == certNode && c.NodeID != node {
			continue
		}
		if c.NotAfter > now() && certCovers(c.SANs, host) && c.NotAfter > bestAfter {
			best, bestAfter = c.ID, c.NotAfter
		}
	}
	writeJSON(w, 200, map[string]string{"id": best})
}
