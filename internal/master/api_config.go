package master

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ---------- groups ----------

type groupOut struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Remark     string  `json:"remark"`
	IsDefault  bool    `json:"is_default"`
	InboundIDs []int64 `json:"inbound_ids"`
	Users      int64   `json:"users"`
	ProfileID  int64   `json:"sub_profile_id"`
}

func (m *Master) listGroups() ([]groupOut, error) {
	gs, err := scanRows(m, `SELECT id, name, remark, is_default, (SELECT COUNT(*) FROM user_groups ug WHERE ug.group_id=groups.id), sub_profile_id
		FROM groups ORDER BY id`, nil, func(s func(...any) error) (groupOut, error) {
		var g groupOut
		return g, s(&g.ID, &g.Name, &g.Remark, &g.IsDefault, &g.Users, &g.ProfileID)
	})
	if err != nil {
		return nil, err
	}
	for i := range gs {
		gs[i].InboundIDs, _ = scanRows(m, `SELECT inbound_id FROM group_inbounds WHERE group_id=? ORDER BY inbound_id`, []any{gs[i].ID},
			func(s func(...any) error) (int64, error) {
				var id int64
				return id, s(&id)
			})
	}
	return gs, nil
}

func (m *Master) apiGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := m.listGroups()
	if errf(w, err) {
		return
	}
	writeJSON(w, 200, gs)
}

type groupReq struct {
	Name       *string  `json:"name"`
	Remark     *string  `json:"remark"`
	IsDefault  *bool    `json:"is_default"`
	InboundIDs *[]int64 `json:"inbound_ids"`
	ProfileID  *int64   `json:"sub_profile_id"`
}

func (m *Master) saveGroup(id int64, req groupReq) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return fmt.Errorf("Name is required")
		}
		if _, err := tx.Exec(`UPDATE groups SET name=? WHERE id=?`, strings.TrimSpace(*req.Name), id); err != nil {
			return err
		}
	}
	if req.Remark != nil {
		tx.Exec(`UPDATE groups SET remark=? WHERE id=?`, *req.Remark, id)
	}
	if req.IsDefault != nil {
		tx.Exec(`UPDATE groups SET is_default=? WHERE id=?`, *req.IsDefault, id)
	}
	if req.ProfileID != nil {
		if err := checkProfileID(tx, *req.ProfileID); err != nil {
			return err
		}
		tx.Exec(`UPDATE groups SET sub_profile_id=? WHERE id=?`, *req.ProfileID, id)
	}
	if req.InboundIDs != nil {
		tx.Exec(`DELETE FROM group_inbounds WHERE group_id=?`, id)
		for _, iid := range *req.InboundIDs {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO group_inbounds(group_id, inbound_id) VALUES(?,?)`, id, iid); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (m *Master) apiCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupReq
	if err := readJSON(r, &req); err != nil || req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		writeErr(w, 400, "Name is required")
		return
	}
	res, err := m.db.Exec(`INSERT INTO groups(name, created_at) VALUES(?,?)`, strings.TrimSpace(*req.Name), now())
	if err != nil {
		writeErr(w, 400, "A group with this name already exists")
		return
	}
	id, _ := res.LastInsertId()
	req.Name = nil
	if err := m.saveGroup(id, req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	m.audit(r, "group.create", fmtID(id), "")
	m.reconcile()
	writeJSON(w, 200, map[string]int64{"id": id})
}

func (m *Master) apiUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var req groupReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := m.saveGroup(id, req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	m.audit(r, "group.update", fmtID(id), "")
	m.reconcile()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (m *Master) apiDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	m.db.Exec(`DELETE FROM group_inbounds WHERE group_id=?`, id)
	m.db.Exec(`DELETE FROM user_groups WHERE group_id=?`, id)
	m.db.Exec(`DELETE FROM groups WHERE id=?`, id)
	m.audit(r, "group.delete", fmtID(id), "")
	m.reconcile()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func defaultGroupIDs(db interface {
	Query(string, ...any) (*sql.Rows, error)
}) []int64 {
	rows, err := db.Query(`SELECT id FROM groups WHERE is_default=1`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

func (m *Master) setUserGroups(userID int64, groupIDs []int64) {
	m.db.Exec(`DELETE FROM user_groups WHERE user_id=?`, userID)
	for _, g := range groupIDs {
		m.db.Exec(`INSERT OR IGNORE INTO user_groups(user_id, group_id) VALUES(?,?)`, userID, g)
	}
}

func (m *Master) userGroupIDs(userID int64) []int64 {
	ids, _ := scanRows(m, `SELECT group_id FROM user_groups WHERE user_id=? ORDER BY group_id`, []any{userID}, func(s func(...any) error) (int64, error) {
		var id int64
		return id, s(&id)
	})
	return ids
}

// ---------- hosts ----------

type hostOut struct {
	ID            int64  `json:"id"`
	InboundID     int64  `json:"inbound_id"`
	Remark        string `json:"remark"`
	Address       string `json:"address"`
	Port          int64  `json:"port"`
	SNI           string `json:"sni"`
	Host          string `json:"host"`
	Path          string `json:"path"`
	ALPN          string `json:"alpn"`
	Fingerprint   string `json:"fingerprint"`
	AllowInsecure bool   `json:"allow_insecure"`
	Sort          int64  `json:"sort"`
	Enabled       bool   `json:"enabled"`
}

func (m *Master) apiHosts(w http.ResponseWriter, r *http.Request) {
	out, err := scanRows(m, `SELECT id, inbound_id, remark, address, port, sni, host, path, alpn, fingerprint, allow_insecure, sort, enabled
		FROM hosts ORDER BY inbound_id, sort, id`, nil, func(s func(...any) error) (hostOut, error) {
		var h hostOut
		return h, s(&h.ID, &h.InboundID, &h.Remark, &h.Address, &h.Port, &h.SNI, &h.Host, &h.Path, &h.ALPN, &h.Fingerprint, &h.AllowInsecure, &h.Sort, &h.Enabled)
	})
	if errf(w, err) {
		return
	}
	writeJSON(w, 200, out)
}

func (m *Master) apiSaveHost(w http.ResponseWriter, r *http.Request) {
	var h hostOut
	if err := readJSON(r, &h); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if h.Port < 0 || h.Port > 65535 {
		writeErr(w, 400, "Port must be 0-65535 (0 = the inbound's port)")
		return
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM inbounds WHERE id=?`, h.InboundID).Scan(&n)
	if n == 0 {
		writeErr(w, 400, "Inbound not found")
		return
	}
	if idStr := r.PathValue("id"); idStr != "" {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		_, err := m.db.Exec(`UPDATE hosts SET inbound_id=?, remark=?, address=?, port=?, sni=?, host=?, path=?, alpn=?, fingerprint=?, allow_insecure=?, sort=?, enabled=? WHERE id=?`,
			h.InboundID, h.Remark, strings.TrimSpace(h.Address), h.Port, h.SNI, h.Host, h.Path, h.ALPN, h.Fingerprint, h.AllowInsecure, h.Sort, h.Enabled, id)
		if errf(w, err) {
			return
		}
		m.audit(r, "host.update", idStr, h.Address)
		writeJSON(w, 200, map[string]int64{"id": id})
		return
	}
	res, err := m.db.Exec(`INSERT INTO hosts(inbound_id, remark, address, port, sni, host, path, alpn, fingerprint, allow_insecure, sort, enabled, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, h.InboundID, h.Remark, strings.TrimSpace(h.Address), h.Port, h.SNI, h.Host, h.Path, h.ALPN, h.Fingerprint, h.AllowInsecure, h.Sort, true, now())
	if errf(w, err) {
		return
	}
	id, _ := res.LastInsertId()
	m.audit(r, "host.create", fmtID(id), h.Address)
	writeJSON(w, 200, map[string]int64{"id": id})
}

func (m *Master) apiDeleteHost(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	m.db.Exec(`DELETE FROM hosts WHERE id=?`, id)
	m.audit(r, "host.delete", fmtID(id), "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- templates (staged) ----------

type templateOut struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	JSON      string   `json:"json"`
	Draft     string   `json:"draft"`
	HasDraft  bool     `json:"has_draft"`
	UpdatedAt int64    `json:"updated_at"`
	Nodes     []string `json:"nodes"`
}

func (m *Master) apiTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := scanRows(m, `SELECT id, name, json, draft_json, updated_at FROM templates ORDER BY id`, nil, func(s func(...any) error) (templateOut, error) {
		var t templateOut
		err := s(&t.ID, &t.Name, &t.JSON, &t.Draft, &t.UpdatedAt)
		t.HasDraft = t.Draft != "" && t.Draft != t.JSON
		return t, err
	})
	if errf(w, err) {
		return
	}
	for i := range ts {
		ts[i].Nodes, _ = scanRows(m, `SELECT id FROM nodes WHERE template_id=?`, []any{ts[i].ID}, func(s func(...any) error) (string, error) {
			var id string
			return id, s(&id)
		})
	}
	writeJSON(w, 200, ts)
}

// validateSkeleton catches the mistakes the node would otherwise reject later.
func validateSkeleton(js string) (string, error) {
	var v map[string]any
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		return "", fmt.Errorf("Invalid JSON: %v", err)
	}
	if _, ok := v["inbounds"]; ok {
		return "", fmt.Errorf("Templates cannot contain inbounds; manage them on the Inbounds page")
	}
	if err := checkSkeletonFiles(v); err != nil {
		return "", err
	}
	tags := map[string]bool{}
	if outs, ok := v["outbounds"].([]any); ok {
		for _, o := range outs {
			om, _ := o.(map[string]any)
			t := str(om, "tag")
			if t == "" {
				return "", fmt.Errorf("Every outbound needs a tag")
			}
			if tags[t] {
				return "", fmt.Errorf("Duplicate outbound tag: %s", t)
			}
			tags[t] = true
		}
	}
	if route := obj(v, "route"); route != nil {
		if f := str(route, "final"); f != "" && !tags[f] && f != "direct" {
			return "", fmt.Errorf("route.final points to a missing outbound: %s", f)
		}
		hasSniff := false
		if rules, ok := route["rules"].([]any); ok {
			for _, r := range rules {
				rm, _ := r.(map[string]any)
				if str(rm, "action") == "sniff" {
					hasSniff = true
				}
				if ob := str(rm, "outbound"); ob != "" && !tags[ob] {
					return "", fmt.Errorf("A route rule references a missing outbound: %s", ob)
				}
			}
		}
		if !hasSniff {
			return "", fmt.Errorf("route.rules must include {\"action\":\"sniff\"}, otherwise domains can't be counted")
		}
	}
	return canonicalJSON(js), nil
}

func (m *Master) apiCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		From int64  `json:"from"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, 400, "Name is required")
		return
	}
	js := canonicalJSON(DefaultSkeleton)
	if req.From > 0 {
		m.db.QueryRow(`SELECT json FROM templates WHERE id=?`, req.From).Scan(&js)
	}
	res, err := m.db.Exec(`INSERT INTO templates(name, json, updated_at) VALUES(?,?,?)`, strings.TrimSpace(req.Name), js, now())
	if err != nil {
		writeErr(w, 400, "A template with this name already exists")
		return
	}
	id, _ := res.LastInsertId()
	m.audit(r, "template.create", fmtID(id), req.Name)
	writeJSON(w, 200, map[string]int64{"id": id})
}

// apiSaveTemplateDraft stores an edit without affecting any node.
func (m *Master) apiSaveTemplateDraft(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var req struct {
		Name  *string `json:"name"`
		Draft *string `json:"draft"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		if _, err := m.db.Exec(`UPDATE templates SET name=? WHERE id=?`, strings.TrimSpace(*req.Name), id); err != nil {
			writeErr(w, 400, "A template with this name already exists")
			return
		}
	}
	if req.Draft != nil {
		d := *req.Draft
		if d != "" {
			c, err := validateSkeleton(d)
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			d = c
		}
		m.db.Exec(`UPDATE templates SET draft_json=? WHERE id=?`, d, id)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// apiApplyTemplate publishes the draft and pushes new specs to the nodes using it.
func (m *Master) apiApplyTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var draft string
	if err := m.db.QueryRow(`SELECT draft_json FROM templates WHERE id=?`, id).Scan(&draft); err != nil {
		writeErr(w, 404, "Template not found")
		return
	}
	if draft == "" {
		writeJSON(w, 200, map[string]any{"ok": true, "nodes": []string{}})
		return
	}
	if _, err := validateSkeleton(draft); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	m.db.Exec(`UPDATE templates SET json=draft_json, draft_json='', updated_at=? WHERE id=?`, now(), id)
	nodes, _ := scanRows(m, `SELECT id FROM nodes WHERE template_id=?`, []any{id}, func(s func(...any) error) (string, error) {
		var x string
		return x, s(&x)
	})
	changed := []string{}
	if len(nodes) > 0 {
		changed = m.reconcile(nodes...)
	}
	m.audit(r, "template.apply", fmtID(id), fmt.Sprintf("%d nodes", len(changed)))
	writeJSON(w, 200, map[string]any{"ok": true, "nodes": changed})
}

func (m *Master) apiDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE template_id=?`, id).Scan(&n)
	if n > 0 {
		writeErr(w, 400, "Some nodes still use this template")
		return
	}
	m.db.QueryRow(`SELECT COUNT(*) FROM templates`).Scan(&n)
	if n <= 1 {
		writeErr(w, 400, "Keep at least one template")
		return
	}
	m.db.Exec(`DELETE FROM templates WHERE id=?`, id)
	m.audit(r, "template.delete", fmtID(id), "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- settings ----------

var publicSettings = map[string]string{
	"sub_base_url":     "",
	"sub_title":        "sbpanel",
	"sub_update_hours": "12",
	"tg_chat":          "",
	"webhook_url":      "",
	"notify_events":    "node_offline,quota_exceeded,limit_exceeded,user_expired,node_quota,apply_failed",
	"device_policy":    "reject",
	// subscription page (public data for themes)
	"sub_announcement":  "",
	"sub_logo_url":      "",
	"sub_support_links": "[]",
	"sub_clients":       "",
	// prefix for GitHub-hosted rule-set URLs in generated configs (profiles may override)
	"sub_rule_mirror": "",
	// panel appearance default (per-browser choices override it)
	"ui_appearance": "",
	// sing-box core tuning pushed to every node (nodecore.go)
	"node_core": "",
}

func (m *Master) apiSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	for k, d := range publicSettings {
		out[k] = settingOr(m.db, k, d)
	}
	_, hasTG := getSetting(m.db, "tg_token")
	out["tg_token_set"] = hasTG && settingOr(m.db, "tg_token", "") != ""
	out["ca_sha256"] = m.CAFingerprint()
	out["grpc_listen"] = m.cfg.GRPCListen
	out["public_grpc"] = m.cfg.PublicGRPCAddr
	out["keep_minute_days"] = m.cfg.KeepMinuteDays
	out["keep_hour_days"] = m.cfg.KeepHourDays
	out["keep_dim_days"] = m.cfg.KeepDimDays
	out["tz"] = m.cfg.TZ
	writeJSON(w, 200, out)
}

func (m *Master) apiSaveSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	for k, v := range req {
		if _, ok := publicSettings[k]; !ok && k != "tg_token" {
			writeErrf(w, 400, "Unknown setting: %s", k)
			return
		}
		v = strings.TrimSpace(v)
		switch k {
		case "sub_base_url":
			if v != "" && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
				writeErr(w, 400, "The subscription URL must start with http:// or https://")
				return
			}
		case "sub_update_hours":
			if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 168 {
				writeErr(w, 400, "Update interval must be 1-168 hours")
				return
			}
		case "device_policy":
			if v != "reject" && v != "kick_oldest" {
				writeErr(w, 400, "device_policy must be reject or kick_oldest")
				return
			}
		case "sub_announcement":
			if utf8.RuneCountInString(v) > 2000 {
				writeErr(w, 400, "The announcement can be at most 2000 characters")
				return
			}
		case "sub_rule_mirror":
			if v != "" && (!(strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://")) || !strings.HasSuffix(v, "/") || len(v) > 300) {
				writeErr(w, 400, "The rule mirror prefix must be an http(s) URL ending in /, e.g. https://ghfast.top/")
				return
			}
		case "sub_logo_url":
			if v != "" && (!strings.HasPrefix(v, "https://") || len(v) > 1000) {
				writeErr(w, 400, "The logo URL must start with https://")
				return
			}
		case "node_core":
			var err error
			if v, err = validateNodeCore(v); err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		case "sub_support_links", "sub_clients", "ui_appearance":
			var err error
			switch k {
			case "sub_support_links":
				v, err = validateSupportLinks(v)
			case "sub_clients":
				v, err = validateClientList(v)
			default:
				v, err = validateAppearance(v)
			}
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
		}
		setSetting(m.db, k, v)
	}
	if _, ok := req["node_core"]; ok {
		m.reconcile() // every node's spec carries it
	}
	m.audit(r, "settings.update", "", strings.Join(keys(req), ","))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---------- users: subscription + bulk ----------

func (m *Master) apiUserSub(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var token string
	if err := m.db.QueryRow(`SELECT sub_token FROM users WHERE id=?`, id).Scan(&token); err != nil {
		writeErr(w, 404, "User not found")
		return
	}
	u, _ := m.loadSubUser(token)
	es, err := m.subEntries(u)
	if errf(w, err) {
		return
	}
	type link struct {
		Remark string `json:"remark"`
		Type   string `json:"type"`
		URI    string `json:"uri"`
	}
	links := []link{}
	for _, e := range es {
		links = append(links, link{e.Remark, e.Type, e.uri()})
	}
	type acc struct {
		TS     int64  `json:"ts"`
		IP     string `json:"ip"`
		UA     string `json:"ua"`
		Format string `json:"format"`
	}
	access, _ := scanRows(m, `SELECT ts, ip, ua, format FROM sub_access WHERE user_id=? ORDER BY id DESC LIMIT 50`, []any{id},
		func(s func(...any) error) (acc, error) {
			var a acc
			return a, s(&a.TS, &a.IP, &a.UA, &a.Format)
		})
	base := m.subURL(r, token)
	writeJSON(w, 200, map[string]any{
		"url": base, "qr": string(qrDataURI(base)), "links": links, "access": access, "active": u.active(),
		"formats": map[string]string{"shadowrocket": base + "?format=shadowrocket", "base64": base + "?format=base64", "singbox": base + "?format=singbox", "mihomo": base + "?format=mihomo", "surge": base + "?format=surge", "page": base + "?format=html"},
	})
}

type bulkReq struct {
	Prefix        string  `json:"prefix"`
	Count         int     `json:"count"`
	Start         int     `json:"start"`
	TrafficLimit  int64   `json:"traffic_limit"`
	ExpireDays    int     `json:"expire_days"`
	ResetStrategy string  `json:"reset_strategy"`
	GroupIDs      []int64 `json:"group_ids"`
}

func (m *Master) apiBulkUsers(w http.ResponseWriter, r *http.Request) {
	var req bulkReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !userRe.MatchString(req.Prefix) || req.Count < 1 || req.Count > 500 {
		writeErr(w, 400, "The prefix may contain letters, digits and . _ @ -; count must be 1-500")
		return
	}
	if !validReset(req.ResetStrategy) {
		writeErr(w, 400, "Invalid reset period")
		return
	}
	if req.Start <= 0 {
		req.Start = 1
	}
	groups := req.GroupIDs
	if len(groups) == 0 {
		groups = defaultGroupIDs(m.db)
	}
	var exp int64
	if req.ExpireDays > 0 {
		exp = time.Now().Add(time.Duration(req.ExpireDays) * 24 * time.Hour).Unix()
	}
	width := len(strconv.Itoa(req.Start + req.Count - 1))
	if width < 2 {
		width = 2
	}
	tx, err := m.db.Begin()
	if errf(w, err) {
		return
	}
	defer tx.Rollback()
	var ids []int64
	for i := 0; i < req.Count; i++ {
		name := fmt.Sprintf("%s%0*d", req.Prefix, width, req.Start+i)
		res, err := tx.Exec(`INSERT INTO users(username, sub_token, uuid, password, traffic_limit, expire_at, reset_strategy, last_reset_at, created_at)
			VALUES(?,?,?,?,?,?,?,?,?)`, name, randHex(12), uuid.NewString(), randHex(12), req.TrafficLimit, exp, nz(req.ResetStrategy, defaultReset), now(), now())
		if err != nil {
			writeErrf(w, 400, "Username conflict: %s", name)
			return
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
		for _, g := range groups {
			tx.Exec(`INSERT OR IGNORE INTO user_groups(user_id, group_id) VALUES(?,?)`, id, g)
		}
	}
	if errf(w, tx.Commit()) {
		return
	}
	m.audit(r, "user.bulk_create", req.Prefix, strconv.Itoa(req.Count))
	m.reconcile()
	writeJSON(w, 200, map[string]any{"ids": ids})
}

func nz(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func validReset(s string) bool {
	switch s {
	case "", "none", "day", "week", "month", "month_rolling":
		return true
	}
	return false
}
