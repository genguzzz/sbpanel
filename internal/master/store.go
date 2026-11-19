package master

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"sbpanel/internal/subgen"
)

const schema = `
CREATE TABLE IF NOT EXISTS settings(k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS admins(
  id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL, pass_hash TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS nodes(
  id TEXT PRIMARY KEY, name TEXT NOT NULL, address TEXT NOT NULL DEFAULT '',
  cert_serial TEXT NOT NULL DEFAULT '', cert_expire INTEGER NOT NULL DEFAULT 0,
  skeleton_json TEXT NOT NULL DEFAULT '',
  revision INTEGER NOT NULL DEFAULT 0, spec_hash TEXT NOT NULL DEFAULT '', spec_pb BLOB,
  applied_revision INTEGER NOT NULL DEFAULT 0, apply_error TEXT NOT NULL DEFAULT '',
  last_seen INTEGER NOT NULL DEFAULT 0, info_json TEXT NOT NULL DEFAULT '{}',
  traffic_limit INTEGER NOT NULL DEFAULT 0, reset_day INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS enroll_tokens(
  token_hash TEXT PRIMARY KEY, node_id TEXT NOT NULL, expires_at INTEGER NOT NULL, used INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS inbounds(
  id INTEGER PRIMARY KEY, node_id TEXT NOT NULL, tag TEXT NOT NULL, type TEXT NOT NULL,
  port INTEGER NOT NULL, options_json TEXT NOT NULL, client_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1, remark TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
  UNIQUE(node_id, tag));
CREATE TABLE IF NOT EXISTS users(
  id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL, sub_token TEXT UNIQUE NOT NULL,
  uuid TEXT NOT NULL, password TEXT NOT NULL, flow TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active', enabled INTEGER NOT NULL DEFAULT 1,
  traffic_limit INTEGER NOT NULL DEFAULT 0, expire_at INTEGER NOT NULL DEFAULT 0,
  reset_strategy TEXT NOT NULL DEFAULT 'none', note TEXT NOT NULL DEFAULT '',
  used_up INTEGER NOT NULL DEFAULT 0, used_down INTEGER NOT NULL DEFAULT 0,
  life_up INTEGER NOT NULL DEFAULT 0, life_down INTEGER NOT NULL DEFAULT 0,
  last_reset_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS report_seen(
  node_id TEXT NOT NULL, seq INTEGER NOT NULL, ts INTEGER NOT NULL, PRIMARY KEY(node_id, seq)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS traffic_minute(
  ts INTEGER NOT NULL, node_id TEXT NOT NULL, user_id INTEGER NOT NULL, inbound TEXT NOT NULL, outbound TEXT NOT NULL,
  up INTEGER NOT NULL, down INTEGER NOT NULL, conns INTEGER NOT NULL,
  PRIMARY KEY(ts, node_id, user_id, inbound, outbound)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS traffic_hour(
  ts INTEGER NOT NULL, node_id TEXT NOT NULL, user_id INTEGER NOT NULL, inbound TEXT NOT NULL, outbound TEXT NOT NULL,
  up INTEGER NOT NULL, down INTEGER NOT NULL, conns INTEGER NOT NULL,
  PRIMARY KEY(ts, node_id, user_id, inbound, outbound)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS dim_hour(
  ts INTEGER NOT NULL, node_id TEXT NOT NULL, user_id INTEGER NOT NULL, inbound TEXT NOT NULL, outbound TEXT NOT NULL,
  host TEXT NOT NULL, port INTEGER NOT NULL, src_ip TEXT NOT NULL, network TEXT NOT NULL, rule TEXT NOT NULL,
  up INTEGER NOT NULL, down INTEGER NOT NULL, conns INTEGER NOT NULL,
  PRIMARY KEY(ts, node_id, user_id, inbound, outbound, host, port, src_ip, network, rule)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS dim_hour_user ON dim_hour(user_id, ts);
CREATE TABLE IF NOT EXISTS online_ip(
  user_id INTEGER NOT NULL, node_id TEXT NOT NULL, ip TEXT NOT NULL,
  first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, PRIMARY KEY(user_id, node_id, ip)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS events(
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, kind TEXT NOT NULL, user_id INTEGER NOT NULL DEFAULT 0,
  node_id TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS events_ts ON events(ts);
`

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One writer connection avoids SQLITE_BUSY storms; reads go through the same pool
	// but WAL lets them run alongside.
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// migrations[i] upgrades schema version i+1 -> i+2 (version 1 is the base schema).
var migrations = []string{
	// v2: groups, hosts, subscriptions, templates, policies, admin security, audit
	`ALTER TABLE users ADD COLUMN device_limit INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE users ADD COLUMN speed_up INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE users ADD COLUMN speed_down INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE users ADD COLUMN sub_last_at INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE users ADD COLUMN sub_last_ua TEXT NOT NULL DEFAULT '';
	ALTER TABLE nodes ADD COLUMN public_address TEXT NOT NULL DEFAULT '';
	ALTER TABLE nodes ADD COLUMN template_id INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE nodes ADD COLUMN notified_quota INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE inbounds ADD COLUMN sort INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE admins ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
	CREATE TABLE groups(id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL, remark TEXT NOT NULL DEFAULT '',
	  is_default INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL);
	CREATE TABLE group_inbounds(group_id INTEGER NOT NULL, inbound_id INTEGER NOT NULL, PRIMARY KEY(group_id, inbound_id)) WITHOUT ROWID;
	CREATE TABLE user_groups(user_id INTEGER NOT NULL, group_id INTEGER NOT NULL, PRIMARY KEY(user_id, group_id)) WITHOUT ROWID;
	CREATE TABLE hosts(id INTEGER PRIMARY KEY, inbound_id INTEGER NOT NULL, remark TEXT NOT NULL DEFAULT '',
	  address TEXT NOT NULL DEFAULT '', port INTEGER NOT NULL DEFAULT 0, sni TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '',
	  path TEXT NOT NULL DEFAULT '', alpn TEXT NOT NULL DEFAULT '', fingerprint TEXT NOT NULL DEFAULT '',
	  allow_insecure INTEGER NOT NULL DEFAULT 0, sort INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL);
	CREATE TABLE sub_access(id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, user_id INTEGER NOT NULL, ip TEXT NOT NULL, ua TEXT NOT NULL, format TEXT NOT NULL);
	CREATE INDEX sub_access_user ON sub_access(user_id, ts);
	CREATE TABLE templates(id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL, json TEXT NOT NULL, draft_json TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);
	CREATE TABLE audit_log(id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, admin TEXT NOT NULL, action TEXT NOT NULL,
	  target TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '');
	CREATE INDEX audit_ts ON audit_log(ts);
	CREATE TABLE api_tokens(id INTEGER PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT UNIQUE NOT NULL, prefix TEXT NOT NULL,
	  created_at INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0);`,
	// v3: 5-minute dimension table for minute-range analysis over the last 30 days
	`CREATE TABLE dim_5m(
	  ts INTEGER NOT NULL, node_id TEXT NOT NULL, user_id INTEGER NOT NULL, inbound TEXT NOT NULL, outbound TEXT NOT NULL,
	  host TEXT NOT NULL, port INTEGER NOT NULL, src_ip TEXT NOT NULL, network TEXT NOT NULL, rule TEXT NOT NULL,
	  up INTEGER NOT NULL, down INTEGER NOT NULL, conns INTEGER NOT NULL,
	  PRIMARY KEY(ts, node_id, user_id, inbound, outbound, host, port, src_ip, network, rule)) WITHOUT ROWID;
	CREATE INDEX dim_5m_user ON dim_5m(user_id, ts);
	CREATE INDEX traffic_minute_user ON traffic_minute(user_id, ts)`,
	// v4: subscription routing profiles (generic rules → per-client configs), see internal/subgen
	`CREATE TABLE sub_profiles(id INTEGER PRIMARY KEY, slug TEXT UNIQUE NOT NULL, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
	  json TEXT NOT NULL, draft_json TEXT NOT NULL DEFAULT '', is_default INTEGER NOT NULL DEFAULT 0, selectable INTEGER NOT NULL DEFAULT 1,
	  sort INTEGER NOT NULL DEFAULT 0, published_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
	CREATE TABLE sub_profile_history(id INTEGER PRIMARY KEY, profile_id INTEGER NOT NULL, ts INTEGER NOT NULL, admin TEXT NOT NULL, json TEXT NOT NULL);
	CREATE INDEX sub_profile_history_p ON sub_profile_history(profile_id, id);
	ALTER TABLE groups ADD COLUMN sub_profile_id INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE users ADD COLUMN sub_profile_id INTEGER NOT NULL DEFAULT 0`,
	// v5: scoped API keys (apikeys.go); existing tokens keep full access via the 'admin' default
	`ALTER TABLE api_tokens ADD COLUMN scopes TEXT NOT NULL DEFAULT 'admin';
	ALTER TABLE api_tokens ADD COLUMN description TEXT NOT NULL DEFAULT '';
	ALTER TABLE api_tokens ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
	ALTER TABLE api_tokens ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE api_tokens ADD COLUMN allow_ips TEXT NOT NULL DEFAULT '';
	ALTER TABLE api_tokens ADD COLUMN rate_limit INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE api_tokens ADD COLUMN rate_burst INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE api_tokens ADD COLUMN last_ip TEXT NOT NULL DEFAULT ''`,
	// v6: DNS providers, domains, certificates, connectivity probe results (certs.go, domains.go, probe.go)
	`CREATE TABLE dns_providers(id INTEGER PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, token_enc TEXT NOT NULL,
	  zones_json TEXT NOT NULL DEFAULT '[]', verified_at INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
	CREATE TABLE domains(id INTEGER PRIMARY KEY, fqdn TEXT UNIQUE NOT NULL, provider_id INTEGER NOT NULL DEFAULT 0,
	  zone_id TEXT NOT NULL DEFAULT '', zone TEXT NOT NULL DEFAULT '', node_id TEXT NOT NULL DEFAULT '',
	  record_type TEXT NOT NULL DEFAULT '', record_id TEXT NOT NULL DEFAULT '', record_content TEXT NOT NULL DEFAULT '',
	  proxied INTEGER NOT NULL DEFAULT 0, synced_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL);
	CREATE TABLE certs(id TEXT PRIMARY KEY, name TEXT NOT NULL, source TEXT NOT NULL,
	  sans_json TEXT NOT NULL DEFAULT '[]', issuer TEXT NOT NULL DEFAULT '', not_before INTEGER NOT NULL DEFAULT 0,
	  not_after INTEGER NOT NULL DEFAULT 0, fingerprint TEXT NOT NULL DEFAULT '', cert_pem TEXT NOT NULL DEFAULT '',
	  key_enc TEXT NOT NULL DEFAULT '', auto_renew INTEGER NOT NULL DEFAULT 1, acme_json TEXT NOT NULL DEFAULT '{}',
	  node_id TEXT NOT NULL DEFAULT '', cert_path TEXT NOT NULL DEFAULT '', key_path TEXT NOT NULL DEFAULT '',
	  last_error TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL DEFAULT 0,
	  warned_at INTEGER NOT NULL DEFAULT 0, renewed_at INTEGER NOT NULL DEFAULT 0, reported_at INTEGER NOT NULL DEFAULT 0,
	  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
	CREATE TABLE probe_results(inbound_id INTEGER NOT NULL, endpoint TEXT NOT NULL, ts INTEGER NOT NULL, ok INTEGER NOT NULL,
	  result_json TEXT NOT NULL, PRIMARY KEY(inbound_id, endpoint)) WITHOUT ROWID`,
	// v7: the 3x-ui usage already folded into used_*/life_* (-1: never synced), so
	// re-importing during a parallel run adds only what 3x-ui counted since.
	`ALTER TABLE users ADD COLUMN xui_up INTEGER NOT NULL DEFAULT -1;
	ALTER TABLE users ADD COLUMN xui_down INTEGER NOT NULL DEFAULT -1`,
	// v8: drop traffic of users deleted before deletion purged it, and the user-less
	// rows recorded while relay hops were being set up
	`DELETE FROM traffic_minute WHERE user_id NOT IN (SELECT id FROM users);
	DELETE FROM traffic_hour WHERE user_id NOT IN (SELECT id FROM users);
	DELETE FROM dim_hour WHERE user_id NOT IN (SELECT id FROM users);
	DELETE FROM dim_5m WHERE user_id NOT IN (SELECT id FROM users)`,
	// v9: where a node's traffic exits (ISO 3166 code); inbounds may override it with "_region"
	`ALTER TABLE nodes ADD COLUMN region TEXT NOT NULL DEFAULT ''`,
	// v10: traffic limits on sets of inbounds (limits.go) and per-user, per-inbound usage
	`CREATE TABLE traffic_rules(id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT NOT NULL DEFAULT '',
	  traffic_limit INTEGER NOT NULL, period TEXT NOT NULL DEFAULT 'user', reset_day INTEGER NOT NULL DEFAULT 1,
	  cur_period INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL);
	CREATE TABLE traffic_rule_inbounds(rule_id INTEGER NOT NULL, inbound_id INTEGER NOT NULL, PRIMARY KEY(rule_id, inbound_id)) WITHOUT ROWID;
	CREATE INDEX traffic_rule_inbounds_in ON traffic_rule_inbounds(inbound_id);
	CREATE TABLE user_rule_limits(user_id INTEGER NOT NULL, rule_id INTEGER NOT NULL, traffic_limit INTEGER NOT NULL,
	  PRIMARY KEY(user_id, rule_id)) WITHOUT ROWID;
	CREATE TABLE rule_usage(rule_id INTEGER NOT NULL, user_id INTEGER NOT NULL, period INTEGER NOT NULL,
	  up INTEGER NOT NULL DEFAULT 0, down INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(rule_id, user_id, period)) WITHOUT ROWID;
	CREATE TABLE inbound_usage(user_id INTEGER NOT NULL, inbound_id INTEGER NOT NULL,
	  up INTEGER NOT NULL DEFAULT 0, down INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(user_id, inbound_id)) WITHOUT ROWID;
	INSERT INTO inbound_usage(user_id, inbound_id, up, down)
	  SELECT t.user_id, i.id, SUM(t.up), SUM(t.down) FROM traffic_hour t
	  JOIN inbounds i ON i.node_id=t.node_id AND i.tag=t.inbound JOIN users u ON u.id=t.user_id
	  WHERE t.ts >= u.last_reset_at - u.last_reset_at % 3600 GROUP BY t.user_id, i.id`,
	// v11: per-minute node health (machine + sbnode process) and the master's own process (metrics.go)
	`CREATE TABLE node_metrics(node_id TEXT NOT NULL, ts INTEGER NOT NULL, n INTEGER NOT NULL,
	  cpu REAL NOT NULL, mem_used INTEGER NOT NULL, mem_total INTEGER NOT NULL,
	  proc INTEGER NOT NULL, proc_cpu REAL NOT NULL, proc_rss INTEGER NOT NULL,
	  rate_up INTEGER NOT NULL, rate_down INTEGER NOT NULL, net_rx INTEGER NOT NULL, net_tx INTEGER NOT NULL,
	  conns INTEGER NOT NULL, PRIMARY KEY(node_id, ts)) WITHOUT ROWID`,
	// v12: a node's own route rules, merged into its template's route (noderoute.go)
	`CREATE TABLE node_routes(node_id TEXT PRIMARY KEY, json TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
	// v13: the machine's own metered traffic per billing period (nodeusage.go)
	`ALTER TABLE nodes ADD COLUMN traffic_mode TEXT NOT NULL DEFAULT 'both';
	ALTER TABLE nodes ADD COLUMN wan_last_rx INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE nodes ADD COLUMN wan_last_tx INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE nodes ADD COLUMN wan_boot INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE node_usage(node_id TEXT NOT NULL, period INTEGER NOT NULL, rx INTEGER NOT NULL DEFAULT 0, tx INTEGER NOT NULL DEFAULT 0,
	  adj INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(node_id, period)) WITHOUT ROWID`,
	// v14: which address family clients try first for the node's hostname (subscriptions)
	`ALTER TABLE nodes ADD COLUMN client_ip TEXT NOT NULL DEFAULT ''`,
	// v15: admin passkeys (WebAuthn credentials, passkey.go); id is the base64url credential id
	`CREATE TABLE passkeys(id TEXT PRIMARY KEY, admin TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
	  credential_json TEXT NOT NULL, created_at INTEGER NOT NULL, last_used_at INTEGER NOT NULL DEFAULT 0);
	CREATE INDEX passkeys_admin ON passkeys(admin)`,
}

func migrate(db *sql.DB) error {
	v := 1
	if s, ok := getSetting(db, "schema_version"); ok {
		fmt.Sscan(s, &v)
	}
	for v-1 < len(migrations) {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range strings.Split(migrations[v-1], ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("v%d: %w (%s)", v+1, err, strings.TrimSpace(stmt))
			}
		}
		if err := postMigrate(tx, v+1); err != nil {
			tx.Rollback()
			return fmt.Errorf("v%d data: %w", v+1, err)
		}
		v++
		if _, err := tx.Exec(`INSERT INTO settings(k,v) VALUES('schema_version',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, fmt.Sprint(v)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// postMigrate moves existing data into new structures.
func postMigrate(tx *sql.Tx, version int) error {
	switch version {
	case 4:
		// built-in presets; 标准分流 becomes the default every subscription uses
		for i, p := range subgen.Presets {
			b, _ := json.Marshal(p.Build())
			res, err := tx.Exec(`INSERT INTO sub_profiles(slug, name, description, json, is_default, selectable, sort, published_at, updated_at)
				VALUES(?,?,?,?,?,1,?,?,?)`, p.Slug, p.Name, p.Description, string(b), i == 0, i, now(), now())
			if err != nil {
				return err
			}
			id, _ := res.LastInsertId()
			if _, err := tx.Exec(`INSERT INTO sub_profile_history(profile_id, ts, admin, json) VALUES(?,?,'system',?)`, id, now(), string(b)); err != nil {
				return err
			}
		}
	case 2:
		t := now()
		// everyone keeps access to everything through a default group
		res, err := tx.Exec(`INSERT INTO groups(name, remark, is_default, created_at) VALUES('默认', '新用户和新入站自动加入', 1, ?)`, t)
		if err != nil {
			return err
		}
		gid, _ := res.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO group_inbounds(group_id, inbound_id) SELECT ?, id FROM inbounds`, gid); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO user_groups(user_id, group_id) SELECT id, ? FROM users`, gid); err != nil {
			return err
		}
		res, err = tx.Exec(`INSERT INTO templates(name, json, updated_at) VALUES('默认', ?, ?)`, canonicalJSON(DefaultSkeleton), t)
		if err != nil {
			return err
		}
		tid, _ := res.LastInsertId()
		rows, err := tx.Query(`SELECT id, name, skeleton_json FROM nodes`)
		if err != nil {
			return err
		}
		type nd struct{ id, name, sk string }
		var ns []nd
		for rows.Next() {
			var n nd
			rows.Scan(&n.id, &n.name, &n.sk)
			ns = append(ns, n)
		}
		rows.Close()
		for _, n := range ns {
			use := tid
			if n.sk != "" && canonicalJSON(n.sk) != canonicalJSON(DefaultSkeleton) {
				r, err := tx.Exec(`INSERT INTO templates(name, json, updated_at) VALUES(?, ?, ?)`, n.name+" 自定义", canonicalJSON(n.sk), t)
				if err != nil {
					return err
				}
				use, _ = r.LastInsertId()
			}
			if _, err := tx.Exec(`UPDATE nodes SET template_id=? WHERE id=?`, use, n.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func canonicalJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func getSetting(db *sql.DB, k string) (string, bool) {
	var v string
	if err := db.QueryRow(`SELECT v FROM settings WHERE k=?`, k).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func setSetting(db *sql.DB, k, v string) error {
	_, err := db.Exec(`INSERT INTO settings(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

func now() int64 { return time.Now().Unix() }

func addEvent(db *sql.DB, kind string, userID int64, nodeID, detail string) {
	db.Exec(`INSERT INTO events(ts,kind,user_id,node_id,detail) VALUES(?,?,?,?,?)`, now(), kind, userID, nodeID, detail)
	if eventHook != nil {
		eventHook(kind, userID, nodeID, detail)
	}
}
