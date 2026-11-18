package master

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"sbpanel/internal/pb"
)

// DefaultSkeleton is the sing-box config (minus inbounds) given to new nodes.
// The sniff rule must stay first: it fills metadata.Domain for traffic analysis.
const DefaultSkeleton = `{
  "log": {"level": "warn", "timestamp": true},
  "dns": {"servers": [{"type": "local", "tag": "local"}]},
  "route": {
    "rules": [{"action": "sniff"}],
    "final": "direct",
    "default_domain_resolver": "local"
  },
  "outbounds": [{"type": "direct", "tag": "direct"}]
}`

func shortHash(b ...[]byte) string {
	h := sha256.New()
	for _, x := range b {
		h.Write(x)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

type specUser struct {
	Name     string // sing-box user name; empty = the numeric ID
	ID       int64
	UUID     string
	Password string
	Flow     string
}

// isStatic reports whether an inbound is a standalone entry ("_static": true): it
// uses the credentials in its own options (a single Shadowsocks password, or its
// own "users"), is not bound to panel users and never appears in subscriptions.
func isStatic(opts map[string]any) bool {
	b, _ := opts["_static"].(bool)
	return b
}

// ssKey derives a per-user Shadowsocks 2022 key from the user's password, so the
// same key can be rebuilt for subscriptions without storing another secret.
func ssKey(password string, n int) string {
	h := sha256.Sum256([]byte("sbpanel-ss2022:" + password))
	return base64.StdEncoding.EncodeToString(h[:n])
}

func ssKeyLen(method string) int {
	switch method {
	case "2022-blake3-aes-128-gcm":
		return 16
	default:
		return 32
	}
}

// usersJSON renders the sing-box "users" array for an inbound type.
func usersJSON(typ string, opts map[string]any, us []specUser) ([]byte, error) {
	out := make([]map[string]any, 0, len(us))
	for _, u := range us {
		name := u.Name
		if name == "" {
			name = strconv.FormatInt(u.ID, 10)
		}
		switch typ {
		case "vless":
			m := map[string]any{"name": name, "uuid": u.UUID}
			flow := u.Flow
			if f, ok := opts["_flow"].(string); ok {
				flow = f
			}
			if flow != "" {
				m["flow"] = flow
			}
			out = append(out, m)
		case "vmess":
			out = append(out, map[string]any{"name": name, "uuid": u.UUID, "alterId": 0})
		case "tuic":
			out = append(out, map[string]any{"name": name, "uuid": u.UUID, "password": u.Password})
		case "shadowsocks":
			method, _ := opts["method"].(string)
			out = append(out, map[string]any{"name": name, "password": ssKey(u.Password, ssKeyLen(method))})
		case "trojan", "hysteria2", "anytls", "naive", "shadowtls":
			out = append(out, map[string]any{"name": name, "password": u.Password})
		default:
			return nil, fmt.Errorf("inbound type %q has no user model", typ)
		}
	}
	return json.Marshal(out)
}

// nodeSkeleton returns the published skeleton JSON of the node's template.
func nodeSkeleton(db *sql.DB, nodeID string) (string, error) {
	var tid int64
	var legacy string
	if err := db.QueryRow(`SELECT template_id, skeleton_json FROM nodes WHERE id=?`, nodeID).Scan(&tid, &legacy); err != nil {
		return "", err
	}
	var js string
	if tid > 0 && db.QueryRow(`SELECT json FROM templates WHERE id=?`, tid).Scan(&js) == nil && js != "" {
		return js, nil
	}
	if db.QueryRow(`SELECT json FROM templates ORDER BY id LIMIT 1`).Scan(&js) == nil && js != "" {
		return js, nil
	}
	if legacy != "" {
		return legacy, nil
	}
	return DefaultSkeleton, nil
}

// buildSpec computes the desired state of one node from the database.
func buildSpec(db *sql.DB, nodeID string) (*pb.NodeSpec, error) {
	skeleton, err := nodeSkeleton(db, nodeID)
	if err != nil {
		return nil, err
	}
	if nr, _ := loadNodeRoute(db, nodeID); len(nr.Rules) > 0 || nr.Final != "" {
		if skeleton, _, _, err = mergeNodeRoute(skeleton, nr, false); err != nil {
			return nil, err
		}
	}
	var sk map[string]any
	if err := json.Unmarshal([]byte(skeleton), &sk); err != nil {
		return nil, fmt.Errorf("skeleton: %w", err)
	}
	delete(sk, "inbounds")
	core := loadNodeCore(db)
	core.applySkeleton(sk)
	skb, _ := json.Marshal(sk) // canonical, compact
	spec := &pb.NodeSpec{SkeletonJson: skb, SkeletonHash: shortHash(skb)}

	type inb struct {
		id            int64
		tag, typ, opt string
	}
	var ins []inb
	rows, err := db.Query(`SELECT id, tag, type, options_json FROM inbounds WHERE node_id=? AND enabled=1 ORDER BY tag`, nodeID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x inb
		rows.Scan(&x.id, &x.tag, &x.typ, &x.opt)
		ins = append(ins, x)
	}
	rows.Close()

	policies := map[int64]*pb.UserPolicy{}
	kickOldest := settingOr(db, "device_policy", "reject") == "kick_oldest"
	for _, in := range ins {
		var users []specUser
		urows, err := db.Query(`SELECT DISTINCT u.id, u.uuid, u.password, u.flow, u.speed_up, u.speed_down, u.device_limit
			FROM users u JOIN user_groups ug ON ug.user_id=u.id JOIN group_inbounds gi ON gi.group_id=ug.group_id
			WHERE gi.inbound_id=? AND u.enabled=1 AND u.status='active' ORDER BY u.id`, in.id)
		if err != nil {
			return nil, err
		}
		for urows.Next() {
			var u specUser
			var su, sd, dl int64
			urows.Scan(&u.ID, &u.UUID, &u.Password, &u.Flow, &su, &sd, &dl)
			users = append(users, u)
			if su > 0 || sd > 0 || dl > 0 {
				policies[u.ID] = &pb.UserPolicy{User: strconv.FormatInt(u.ID, 10), SpeedUp: su, SpeedDown: sd, DeviceLimit: int32(dl), KickOldest: kickOldest && dl > 0}
			}
		}
		urows.Close()
		var opts map[string]any
		if err := json.Unmarshal([]byte(in.opt), &opts); err != nil {
			return nil, fmt.Errorf("inbound %s: %w", in.tag, err)
		}
		var uj []byte
		if isStatic(opts) {
			// a standalone entry (e.g. a relay hop between nodes) keeps its own
			// credentials from the options: no panel users, no probe user
			users = nil
			uj, _ = json.Marshal(opts["users"])
		} else {
			// the hidden connectivity-probe user (probe.go); never in subscriptions or stats
			pu, pp := probeCreds(db, nodeID)
			users = append(users, specUser{Name: probeUser, UUID: pu, Password: pp})
			if uj, err = usersJSON(in.typ, opts, users); err != nil {
				return nil, fmt.Errorf("inbound %s: %w", in.tag, err)
			}
		}
		ppr, _ := opts["_proxy_protocol"].(bool)
		ins := &pb.InboundSpec{Tag: in.tag, Type: in.typ, ProxyProtocol: ppr}
		applyCertHint(db, opts, spec, ins)
		// keys starting with "_" are panel-only hints
		for k := range opts {
			if len(k) > 0 && k[0] == '_' {
				delete(opts, k)
			}
		}
		delete(opts, "users")
		delete(opts, "type")
		delete(opts, "tag")
		core.applyInbound(opts)
		ob, _ := json.Marshal(opts)
		names := make([]string, 0, len(users))
		for _, u := range users {
			if u.Name == "" { // the probe user is not tracked, so it is left out
				names = append(names, strconv.FormatInt(u.ID, 10))
			}
		}
		hp := [][]byte{[]byte(in.typ), ob, []byte(fmt.Sprint(ppr))}
		if ins.CertId != "" {
			hp = append(hp, []byte("cert:"+ins.CertId)) // the id only: renewals keep the hash
		}
		ins.OptionsJson, ins.OptionsHash = ob, shortHash(hp...)
		ins.UsersJson, ins.UsersHash, ins.UserNames = uj, shortHash(uj), names
		spec.Inbounds = append(spec.Inbounds, ins)
	}
	// traffic limits: users over one keep their place in its inbounds (and subscriptions);
	// the node refuses their connections there
	if rows, err := db.Query(`SELECT DISTINCT ru.user_id, i.tag FROM rule_usage ru
		JOIN traffic_rules r ON r.id=ru.rule_id AND ru.period=r.cur_period AND r.enabled=1
		JOIN user_rule_limits ul ON ul.rule_id=r.id AND ul.user_id=ru.user_id
		JOIN traffic_rule_inbounds ri ON ri.rule_id=r.id JOIN inbounds i ON i.id=ri.inbound_id
		WHERE i.node_id=? AND i.enabled=1 AND ru.up+ru.down>=`+effLimit+` ORDER BY ru.user_id, i.tag`, nodeID); err == nil {
		for rows.Next() {
			var uid int64
			var tag string
			rows.Scan(&uid, &tag)
			p := policies[uid]
			if p == nil {
				p = &pb.UserPolicy{User: strconv.FormatInt(uid, 10)}
				policies[uid] = p
			}
			p.BlockedInbounds = append(p.BlockedInbounds, tag)
		}
		rows.Close()
	}
	addCertWatches(db, spec, nodeID)
	spec.Runtime = core.runtime()
	sort.Slice(spec.Inbounds, func(i, j int) bool { return spec.Inbounds[i].Tag < spec.Inbounds[j].Tag })
	ids := make([]int64, 0, len(policies))
	for id := range policies {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		spec.Policies = append(spec.Policies, policies[id])
	}
	return spec, nil
}

func specHash(s *pb.NodeSpec) string {
	parts := [][]byte{[]byte(s.SkeletonHash)}
	for _, in := range s.Inbounds {
		parts = append(parts, []byte(in.Tag), []byte(in.OptionsHash), []byte(in.UsersHash))
		if in.ProxyProtocol {
			parts = append(parts, []byte("pp"))
		}
	}
	for _, p := range s.Policies {
		parts = append(parts, []byte(fmt.Sprintf("%s/%d/%d/%d/%s", p.User, p.SpeedUp, p.SpeedDown, p.DeviceLimit, strings.Join(p.BlockedInbounds, ","))))
		if p.KickOldest { // appended only when set, so existing specs keep their hash
			parts = append(parts, []byte("kick_oldest"))
		}
	}
	for _, c := range s.Certs {
		parts = append(parts, []byte("cert:"+c.Id+"/"+c.Hash))
	}
	if rt := s.Runtime; rt != nil {
		parts = append(parts, []byte(fmt.Sprintf("rt:%d/%d", rt.Gogc, rt.MemoryLimit)))
	}
	for _, w := range s.WatchCerts {
		parts = append(parts, []byte("watch:"+w.Id+"\x00"+w.CertPath+"\x00"+w.KeyPath))
	}
	return shortHash(parts...)
}

// reconcile recomputes desired state for the given nodes (all when empty) and bumps
// the revision of every node whose spec changed. It returns the nodes that changed.
func (m *Master) reconcile(nodeIDs ...string) []string {
	m.specMu.Lock()
	defer m.specMu.Unlock()
	if len(nodeIDs) == 0 {
		rows, err := m.db.Query(`SELECT id FROM nodes`)
		if err != nil {
			return nil
		}
		for rows.Next() {
			var id string
			rows.Scan(&id)
			nodeIDs = append(nodeIDs, id)
		}
		rows.Close()
	}
	var changed []string
	for _, id := range nodeIDs {
		spec, err := buildSpec(m.db, id)
		if err != nil {
			m.log.Printf("reconcile %s: %v", id, err)
			continue
		}
		h := specHash(spec)
		var old string
		var rev uint64
		m.db.QueryRow(`SELECT spec_hash, revision FROM nodes WHERE id=?`, id).Scan(&old, &rev)
		if h == old {
			continue
		}
		rev++
		spec.Revision = rev
		blob, _ := proto.Marshal(spec)
		if _, err := m.db.Exec(`UPDATE nodes SET revision=?, spec_hash=?, spec_pb=? WHERE id=?`, rev, h, blob, id); err != nil {
			m.log.Printf("reconcile %s: %v", id, err)
			continue
		}
		changed = append(changed, id)
		m.hub.notifySpec(id, rev)
	}
	return changed
}

func (m *Master) loadSpec(nodeID string) (*pb.NodeSpec, error) {
	var blob []byte
	if err := m.db.QueryRow(`SELECT spec_pb FROM nodes WHERE id=?`, nodeID).Scan(&blob); err != nil {
		return nil, err
	}
	if len(blob) == 0 {
		m.reconcile(nodeID)
		if err := m.db.QueryRow(`SELECT spec_pb FROM nodes WHERE id=?`, nodeID).Scan(&blob); err != nil {
			return nil, err
		}
	}
	s := &pb.NodeSpec{}
	return s, proto.Unmarshal(blob, s)
}
