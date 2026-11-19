package master

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type mcpTool struct {
	Name, Title, Desc string
	Scope             string
	Schema            map[string]any
	ReadOnly          bool
	Destructive       bool
	Idempotent        bool
	Run               func(c *mcpCtx, a args) (any, error)
}

func (t *mcpTool) describe() map[string]any {
	return map[string]any{
		"name": t.Name, "title": t.Title, "description": t.Desc, "inputSchema": t.Schema,
		"annotations": map[string]any{
			"title": t.Title, "readOnlyHint": t.ReadOnly, "destructiveHint": t.Destructive,
			"idempotentHint": t.Idempotent, "openWorldHint": false,
		},
	}
}

func findTool(name string) *mcpTool {
	for i := range mcpTools {
		if mcpTools[i].Name == name {
			return &mcpTools[i]
		}
	}
	return nil
}

// ---------- JSON-Schema helpers ----------

type prop = map[string]any

func sObj(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func pStr(desc string) prop { return prop{"type": "string", "description": desc} }
func pInt(desc string, min, max float64) prop {
	p := prop{"type": "integer", "description": desc, "minimum": min}
	if max > 0 {
		p["maximum"] = max
	}
	return p
}
func pNum(desc string) prop  { return prop{"type": "number", "description": desc} }
func pBool(desc string) prop { return prop{"type": "boolean", "description": desc} }
func pEnum(desc string, vals ...string) prop {
	return prop{"type": "string", "description": desc, "enum": vals}
}
func pTime(desc string) prop {
	return prop{"type": []string{"string", "integer"}, "description": desc + "（unix 秒、RFC 3339 或 YYYY-MM-DD / unix seconds, RFC 3339 or YYYY-MM-DD）"}
}

var userRef = map[string]any{
	"id":       pInt("用户 id / user id（与 username 二选一）", 1, 0),
	"username": pStr("用户名 / username（与 id 二选一）"),
}

func withUser(extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range userRef {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

var rangeProps = map[string]any{
	"range": pEnum("时间范围 / time range（默认 24h）", "1h", "6h", "24h", "today", "7d", "30d", "month"),
	"from":  pTime("开始时间，覆盖 range / start, overrides range"),
	"to":    pTime("结束时间，默认现在 / end, default now"),
}

func merge(ms ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// resetDesc: reset_strategy values (validReset).
const resetDesc = "none | day | week (Mondays) | month (the 1st) | month_rolling (the account's creation day) | month:N (day N, 1-31) | node:<node id> (that node's monthly reset day)"

// ---------- tools ----------

var mcpTools []mcpTool

func init() {
	mcpTools = []mcpTool{
		{
			Name: "list_users", Title: "列出用户 / List users", Scope: ScopeRead, ReadOnly: true, Idempotent: true,
			Desc: "列出用户及用量、配额、到期和在线状态，可按用户名/备注搜索、按状态或分组过滤。List users with usage, quota, expiry and online status; filter by search text, status or group.",
			Schema: sObj(nil, map[string]any{
				"search": pStr("按用户名或备注模糊匹配 / substring of username or note"),
				"status": pEnum("状态过滤 / status filter", "active", "limited", "expired", "disabled", "online"),
				"group":  prop{"type": []string{"string", "integer"}, "description": "分组名或 id / group name or id"},
				"limit":  pInt("最多返回条数，默认 100 / max rows (default 100)", 1, 1000),
				"offset": pInt("跳过前 N 条 / skip N rows", 0, 0),
			}),
			Run: toolListUsers,
		},
		{
			Name: "get_user", Title: "用户详情 / Get user", Scope: ScopeRead, ReadOnly: true, Idempotent: true,
			Desc:   "按 id 或用户名获取单个用户的详情、在线 IP 和订阅链接。Get one user by id or username, including online IPs and the subscription URL.",
			Schema: sObj(nil, userRef),
			Run:    toolGetUser,
		},
		{
			Name: "traffic_summary", Title: "流量汇总 / Traffic summary", Scope: ScopeRead, ReadOnly: true, Idempotent: true,
			Desc: "按用户、节点、入站、出站或域名汇总一段时间内的流量（字节与 GB），按总量降序。Summarize traffic over a time range grouped by user, node, inbound, outbound or domain, sorted by total.",
			Schema: sObj(nil, merge(rangeProps, map[string]any{
				"group_by": pEnum("分组维度，默认 user / grouping (default user)", "user", "node", "inbound", "outbound", "domain"),
				"user":     prop{"type": []string{"string", "integer"}, "description": "只看某个用户（id 或用户名）/ restrict to one user (id or username)"},
				"node":     pStr("只看某个节点（id 或名称）/ restrict to one node (id or name)"),
				"limit":    pInt("返回行数，默认 20 / rows (default 20)", 1, 1000),
			})),
			Run: toolTrafficSummary,
		},
		{
			Name: "top_domains", Title: "热门域名 / Top domains", Scope: ScopeRead, ReadOnly: true, Idempotent: true,
			Desc: "一段时间内访问流量最多的域名（按可注册域名聚合），可限定用户或节点。Top destination domains by traffic (registrable domain), optionally for one user or node.",
			Schema: sObj(nil, merge(rangeProps, map[string]any{
				"user":  prop{"type": []string{"string", "integer"}, "description": "用户 id 或用户名 / user id or username"},
				"node":  pStr("节点 id 或名称 / node id or name"),
				"limit": pInt("返回行数，默认 20 / rows (default 20)", 1, 500),
			})),
			Run: func(c *mcpCtx, a args) (any, error) { a["group_by"] = "domain"; return toolTrafficSummary(c, a) },
		},
		{
			Name: "list_nodes", Title: "列出节点 / List nodes", Scope: ScopeRead, ReadOnly: true, Idempotent: true,
			Desc:   "列出节点及健康状态：在线、配置是否已下发、错误、实时速率、连接数、CPU/内存、本月流量。List nodes with health: online, config applied, errors, live rate, connections, CPU/memory and monthly traffic.",
			Schema: sObj(nil, map[string]any{}),
			Run:    toolListNodes,
		},
		{
			Name: "list_inbounds", Title: "列出入站 / List inbounds", Scope: ScopeRead, ReadOnly: true, Idempotent: true,
			Desc:   "列出入站（协议、端口、所属节点、是否启用），不含密钥等敏感配置。List inbounds (protocol, port, node, enabled) without secret options.",
			Schema: sObj(nil, map[string]any{"node": pStr("只看某个节点（id 或名称）/ restrict to one node (id or name)")}),
			Run:    toolListInbounds,
		},
		{
			Name: "list_events", Title: "事件与审计 / Events & audit", Scope: ScopeRead, ReadOnly: true, Idempotent: true,
			Desc: "最近的系统事件（节点上下线、流量用尽、到期等）或管理员审计日志。Recent system events (node offline, quota exceeded, expiry…) or the admin audit log.",
			Schema: sObj(nil, map[string]any{
				"source": pEnum("events=系统事件，audit=审计日志，默认 events / which log (default events)", "events", "audit"),
				"kind":   pStr("按事件类型或审计动作前缀过滤，如 quota_exceeded、user. / filter by kind or action prefix"),
				"user":   prop{"type": []string{"string", "integer"}, "description": "只看某个用户的事件 / events of one user"},
				"limit":  pInt("返回条数，默认 50 / rows (default 50)", 1, 500),
			}),
			Run: toolListEvents,
		},
		{
			Name: "get_live_connections", Title: "实时连接 / Live connections", Scope: ScopeRead, ReadOnly: true,
			Desc: "当前在线用户（来源 IP）和节点上的实时连接（目标、出站、速率）。Currently online users (source IPs) and live connections on nodes (destination, outbound, bytes).",
			Schema: sObj(nil, map[string]any{
				"node":  pStr("节点 id 或名称；不填则查所有在线节点 / node id or name; default all online nodes"),
				"user":  prop{"type": []string{"string", "integer"}, "description": "只看某个用户 / only one user"},
				"limit": pInt("每个节点最多返回的连接数，默认 50 / max connections per node (default 50)", 1, 1000),
			}),
			Run: toolLiveConns,
		},
		// ---- writes (users:write) ----
		{
			Name: "create_user", Title: "创建用户 / Create user", Scope: ScopeUsersWrite,
			Desc: "创建代理用户，可设流量配额（GB）、有效期、分组、重置周期和设备数，返回订阅链接。Create a proxy user with optional quota (GB), expiry, groups, reset cycle and device limit; returns the subscription URL.",
			Schema: sObj([]string{"username"}, map[string]any{
				"username":       pStr("用户名：字母、数字、. _ @ -，最长 64 / letters, digits, . _ @ -, max 64"),
				"traffic_gb":     prop{"type": "number", "description": "流量配额 GB，0 或不填为不限 / quota in GB, 0 = unlimited", "minimum": 0.0},
				"expire_days":    prop{"type": "number", "description": "从现在起多少天后到期 / days from now until expiry", "minimum": 0.0},
				"expire_at":      pTime("到期时间（与 expire_days 二选一）/ absolute expiry"),
				"groups":         prop{"type": "array", "items": prop{"type": []string{"string", "integer"}}, "description": "分组名或 id，不填用默认分组 / group names or ids (default groups if omitted)"},
				"reset_strategy": pStr("用量重置周期，默认 month_rolling（每月开通日）/ usage reset cycle, default month_rolling: " + resetDesc),
				"device_limit":   pInt("同时在线设备（IP）数，0 不限 / max concurrent IPs, 0 = unlimited", 0, 0),
				"note":           pStr("备注 / note"),
			}),
			Run: toolCreateUser,
		},
		{
			Name: "update_user", Title: "修改用户 / Update user", Scope: ScopeUsersWrite, Idempotent: true,
			Desc: "修改用户的配额、到期时间、分组、备注、重置周期、设备数或用户名；只改传入的字段。Update a user's quota, expiry, groups, note, reset cycle, device limit or username; only given fields change.",
			Schema: sObj(nil, withUser(map[string]any{
				"new_username":   pStr("新用户名 / new username"),
				"traffic_gb":     prop{"type": "number", "description": "新流量配额 GB，0 为不限 / new quota in GB, 0 = unlimited", "minimum": 0.0},
				"expire_at":      prop{"type": []string{"string", "integer"}, "description": "新到期时间，0 为永不到期 / new expiry, 0 = never (unix seconds, RFC 3339 or YYYY-MM-DD)"},
				"groups":         prop{"type": "array", "items": prop{"type": []string{"string", "integer"}}, "description": "替换分组（名称或 id）/ replace groups (names or ids)"},
				"reset_strategy": pStr("用量重置周期 / usage reset cycle: " + resetDesc),
				"device_limit":   pInt("同时在线设备数，0 不限 / max concurrent IPs", 0, 0),
				"note":           pStr("备注 / note"),
			})),
			Run: toolUpdateUser,
		},
		{
			Name: "extend_user", Title: "续期 / Extend expiry", Scope: ScopeUsersWrite,
			Desc:   "把用户到期时间延长 N 天（从当前到期时间和现在中较晚者算起；未设到期则从现在起）。Extend a user's expiry by N days (from the later of now and the current expiry).",
			Schema: sObj([]string{"days"}, withUser(map[string]any{"days": prop{"type": "number", "description": "延长天数，可为小数 / days to add", "minimum": 0.0, "maximum": 3650.0}})),
			Run:    toolExtendUser,
		},
		{
			Name: "add_traffic", Title: "加流量 / Add traffic", Scope: ScopeUsersWrite,
			Desc:   "给有配额的用户增加 N GB 流量配额（不限流量的用户不受影响）。Add N GB to a user's quota (users without a quota are unaffected).",
			Schema: sObj([]string{"gb"}, withUser(map[string]any{"gb": prop{"type": "number", "description": "增加的 GB 数 / GB to add", "minimum": 0.0}})),
			Run:    toolAddTraffic,
		},
		{
			Name: "set_user_enabled", Title: "启用/禁用用户 / Enable or disable user", Scope: ScopeUsersWrite, Idempotent: true,
			Desc:   "启用或禁用用户；禁用会立刻断开其连接。Enable or disable a user; disabling drops their connections immediately.",
			Schema: sObj([]string{"enabled"}, withUser(map[string]any{"enabled": pBool("true 启用，false 禁用 / true to enable")})),
			Run:    toolSetEnabled,
		},
		{
			Name: "reset_usage", Title: "重置用量 / Reset usage", Scope: ScopeUsersWrite, Destructive: true, Idempotent: true,
			Desc:   "把用户本周期已用流量清零（因流量用尽被限的用户会恢复）。Reset a user's used traffic for the current cycle (re-activates quota-limited users).",
			Schema: sObj(nil, userRef),
			Run:    toolResetUsage,
		},
		{
			Name: "kick_user", Title: "踢下线 / Kick user", Scope: ScopeUsersWrite, Destructive: true, Idempotent: true,
			Desc:   "立刻断开用户在所有节点上的连接（客户端可重新连接）。Drop all of a user's connections on every node now (clients may reconnect).",
			Schema: sObj(nil, userRef),
			Run:    toolKickUser,
		},
	}
}

// ---------- read tools ----------

type userLite struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	Status      string  `json:"status"`
	Enabled     bool    `json:"enabled"`
	UsedBytes   int64   `json:"used_bytes"`
	UsedGB      float64 `json:"used_gb"`
	LimitBytes  int64   `json:"limit_bytes"`
	LimitGB     any     `json:"limit_gb"` // number or "unlimited"
	ExpireAt    int64   `json:"expire_at"`
	Expire      string  `json:"expire"`
	ResetCycle  string  `json:"reset_strategy"`
	OnlineIPs   int     `json:"online_ips"`
	LastOnline  int64   `json:"last_online"`
	DeviceLimit int64   `json:"device_limit"`
	GroupIDs    []int64 `json:"group_ids"`
	Note        string  `json:"note,omitempty"`
}

func liteUser(u userOut) userLite {
	l := userLite{ID: u.ID, Username: u.Username, Status: u.Status, Enabled: u.Enabled, UsedBytes: u.UsedUp + u.UsedDown,
		LimitBytes: u.TrafficLimit, ExpireAt: u.ExpireAt, ResetCycle: u.ResetStrategy, OnlineIPs: u.OnlineIPs,
		LastOnline: u.LastOnline, DeviceLimit: u.DeviceLimit, GroupIDs: u.GroupIDs, Note: u.Note}
	l.UsedGB = gbOf(l.UsedBytes)
	l.LimitGB = "unlimited"
	if u.TrafficLimit > 0 {
		l.LimitGB = gbOf(u.TrafficLimit)
	}
	l.Expire = "never"
	if u.ExpireAt > 0 {
		l.Expire = isoTime(u.ExpireAt)
	}
	if !u.Enabled {
		l.Status = "disabled"
	}
	return l
}

func toolListUsers(c *mcpCtx, a args) (any, error) {
	var us []userOut
	if err := c.get("/api/users", &us); err != nil {
		return nil, err
	}
	var gid int64 = -1
	if a.has("group") {
		ids, err := c.resolveGroups([]any{a["group"]})
		if err != nil {
			return nil, err
		}
		gid = ids[0]
	}
	q := strings.ToLower(a.str("search"))
	st := a.str("status")
	out := []userLite{}
	for _, u := range us {
		l := liteUser(u)
		if q != "" && !strings.Contains(strings.ToLower(u.Username), q) && !strings.Contains(strings.ToLower(u.Note), q) {
			continue
		}
		if st == "online" && u.OnlineIPs == 0 || st != "" && st != "online" && l.Status != st {
			continue
		}
		if gid >= 0 && !containsID(u.GroupIDs, gid) {
			continue
		}
		out = append(out, l)
	}
	total := len(out)
	off, lim := int(a.int("offset", 0)), int(a.int("limit", 100))
	out = out[min(off, len(out)):]
	out = out[:min(lim, len(out))]
	return map[string]any{"total": total, "returned": len(out), "users": out}, nil
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func toolGetUser(c *mcpCtx, a args) (any, error) {
	id, _, err := c.resolveUser(a)
	if err != nil {
		return nil, err
	}
	return c.userDetail(id)
}

func (c *mcpCtx) userDetail(id int64) (map[string]any, error) {
	var d struct {
		User userOut          `json:"user"`
		IPs  []map[string]any `json:"ips"`
	}
	if err := c.get(fmt.Sprintf("/api/users/%d", id), &d); err != nil {
		return nil, err
	}
	var sub struct {
		URL     string            `json:"url"`
		Formats map[string]string `json:"formats"`
		Active  bool              `json:"active"`
		Links   []any             `json:"links"`
	}
	if err := c.get(fmt.Sprintf("/api/users/%d/sub", id), &sub); err != nil {
		return nil, err
	}
	var groups []groupOut
	c.get("/api/groups", &groups)
	names := []string{}
	for _, g := range groups {
		if containsID(d.User.GroupIDs, g.ID) {
			names = append(names, g.Name)
		}
	}
	l := liteUser(d.User)
	return map[string]any{
		"user": l, "groups": names, "created_at": isoTime(d.User.CreatedAt), "last_reset_at": isoTime(d.User.LastResetAt),
		"subscription": map[string]any{"url": sub.URL, "formats": sub.Formats, "active": sub.Active, "nodes": len(sub.Links),
			"last_fetch": isoTime(d.User.SubLastAt), "last_client": d.User.SubLastUA},
		"ips": d.IPs,
	}, nil
}

func toolTrafficSummary(c *mcpCtx, a args) (any, error) {
	from, to, err := c.timeRange(a)
	if err != nil {
		return nil, err
	}
	dim := a.str("group_by")
	if dim == "" {
		dim = "user"
	}
	q := url.Values{"dim": {dim}, "from": {fmt.Sprint(from)}, "to": {fmt.Sprint(to)}, "limit": {"1000"}}
	if a.has("user") {
		uid, _, err := c.resolveUser(userArg(a["user"]))
		if err != nil {
			return nil, err
		}
		q.Set("user", fmt.Sprint(uid))
	}
	if n := a.str("node"); n != "" {
		id, err := c.resolveNode(n)
		if err != nil {
			return nil, err
		}
		q.Set("node", id)
	}
	code, b, h := c.call("GET", "/api/traffic/top?"+q.Encode(), nil)
	if code != 200 {
		return nil, fmt.Errorf("%s", apiErrMsg(code, b))
	}
	var rows []topRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	var up, down int64
	type row struct {
		Key     string  `json:"key"`
		Label   string  `json:"label"`
		Up      int64   `json:"up"`
		Down    int64   `json:"down"`
		Total   int64   `json:"total"`
		TotalGB float64 `json:"total_gb"`
		Conns   int64   `json:"conns"`
		Users   int64   `json:"users"`
	}
	out := []row{}
	for _, r := range rows {
		up += r.Up
		down += r.Down
		out = append(out, row{r.Key, r.Label, r.Up, r.Down, r.Up + r.Down, gbOf(r.Up + r.Down), r.Conns, r.Users})
	}
	lim := int(a.int("limit", 20))
	out = out[:min(lim, len(out))]
	return map[string]any{
		"from": isoTime(from), "to": isoTime(to), "group_by": dim, "precision": h.Get("X-Precision"),
		"total_up": up, "total_down": down, "total_gb": gbOf(up + down), "groups": len(rows), "rows": out,
	}, nil
}

// userArg turns a "user" argument (id or name) into id/username args.
func userArg(v any) args {
	switch x := v.(type) {
	case float64:
		return args{"id": x}
	case string:
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return args{"id": float64(n)}
		}
		return args{"username": x}
	}
	return args{}
}

func toolListNodes(c *mcpCtx, a args) (any, error) {
	var ns []nodeOut
	if err := c.get("/api/nodes", &ns); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, n := range ns {
		health := "ok"
		switch {
		case !n.Enrolled:
			health = "not_enrolled"
		case !n.Online:
			health = "offline"
		case n.ApplyError != "":
			health = "apply_error"
		case n.AppliedRevision < n.Revision:
			health = "pending_apply"
		}
		x := map[string]any{
			"id": n.ID, "name": n.Name, "health": health, "online": n.Online, "address": n.Address, "public_address": n.PublicAddress,
			"last_seen": isoTime(n.LastSeen), "revision": n.Revision, "applied_revision": n.AppliedRevision, "apply_error": n.ApplyError,
			"rate_up_bps": n.RateUp, "rate_down_bps": n.RateDown, "active_conns": n.ActiveConns, "online_users": n.OnlineUsers,
			"inbounds": n.Inbounds, "month_gb": gbOf(n.MonthUp + n.MonthDown), "today_gb": gbOf(n.TodayUp + n.TodayDown),
			"cert_expire": isoTime(n.CertExpire), "info": n.Info,
		}
		if n.TrafficLimit > 0 {
			x["month_limit_gb"] = gbOf(n.TrafficLimit)
		}
		if n.Sys != nil {
			x["sys"] = map[string]any{"cpu_pct": n.Sys.CpuPct, "mem_used": n.Sys.MemUsed, "mem_total": n.Sys.MemTotal, "load1": n.Sys.Load1, "uptime_s": n.Sys.Uptime}
		}
		out = append(out, x)
	}
	return map[string]any{"nodes": out}, nil
}

func toolListInbounds(c *mcpCtx, a args) (any, error) {
	path := "/api/inbounds"
	if n := a.str("node"); n != "" {
		id, err := c.resolveNode(n)
		if err != nil {
			return nil, err
		}
		path += "?node=" + url.QueryEscape(id)
	}
	var ins []map[string]any
	if err := c.get(path, &ins); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, in := range ins {
		x := map[string]any{}
		for _, k := range []string{"id", "node_id", "node", "tag", "type", "port", "enabled", "remark", "group_ids"} {
			if v, ok := in[k]; ok {
				x[k] = v
			}
		}
		out = append(out, x)
	}
	return map[string]any{"inbounds": out}, nil
}

func toolListEvents(c *mcpCtx, a args) (any, error) {
	lim := a.int("limit", 50)
	kind := a.str("kind")
	if a.str("source") == "audit" {
		var rows []map[string]any
		if err := c.get(fmt.Sprintf("/api/audit?limit=%d", max(lim, 500)), &rows); err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, r := range rows {
			if kind != "" && !strings.HasPrefix(fmt.Sprint(r["action"]), kind) {
				continue
			}
			if ts, ok := r["ts"].(float64); ok {
				r["time"] = isoTime(int64(ts))
			}
			out = append(out, r)
			if int64(len(out)) >= lim {
				break
			}
		}
		return map[string]any{"audit": out}, nil
	}
	var uid int64
	if a.has("user") {
		id, _, err := c.resolveUser(userArg(a["user"]))
		if err != nil {
			return nil, err
		}
		uid = id
	}
	var rows []map[string]any
	if err := c.get("/api/events?limit=1000", &rows); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range rows {
		if kind != "" && !strings.HasPrefix(fmt.Sprint(r["kind"]), kind) {
			continue
		}
		if uid > 0 && int64(r["user_id"].(float64)) != uid {
			continue
		}
		if ts, ok := r["ts"].(float64); ok {
			r["time"] = isoTime(int64(ts))
		}
		out = append(out, r)
		if int64(len(out)) >= lim {
			break
		}
	}
	return map[string]any{"events": out}, nil
}

func toolLiveConns(c *mcpCtx, a args) (any, error) {
	var uid int64
	if a.has("user") {
		id, _, err := c.resolveUser(userArg(a["user"]))
		if err != nil {
			return nil, err
		}
		uid = id
	}
	var online []map[string]any
	if err := c.get("/api/online", &online); err != nil {
		return nil, err
	}
	if uid > 0 {
		f := online[:0]
		for _, o := range online {
			if int64(o["user_id"].(float64)) == uid {
				f = append(f, o)
			}
		}
		online = f
	}
	var nodes []nodeOut
	if err := c.get("/api/nodes", &nodes); err != nil {
		return nil, err
	}
	want := ""
	if n := a.str("node"); n != "" {
		id, err := c.resolveNode(n)
		if err != nil {
			return nil, err
		}
		want = id
	}
	lim := int(a.int("limit", 50))
	type nodeConns struct {
		NodeID string           `json:"node_id"`
		Node   string           `json:"node"`
		Total  int64            `json:"total"`
		Shown  int              `json:"shown"`
		Ready  bool             `json:"ready"`
		Error  string           `json:"error,omitempty"`
		Conns  []map[string]any `json:"conns"`
	}
	res := []nodeConns{}
	for _, n := range nodes {
		if want != "" && n.ID != want || want == "" && !n.Online {
			continue
		}
		nc := nodeConns{NodeID: n.ID, Node: n.Name, Conns: []map[string]any{}}
		var snap struct {
			Conns []map[string]any `json:"conns"`
			Total int64            `json:"total"`
			Ready bool             `json:"ready"`
		}
		// the first request asks the node to start streaming; give it a moment
		for i := 0; i < 15; i++ {
			if err := c.get("/api/nodes/"+url.PathEscape(n.ID)+"/conns", &snap); err != nil {
				nc.Error = err.Error()
				break
			}
			if snap.Ready {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		nc.Ready, nc.Total = snap.Ready, snap.Total
		for _, cn := range snap.Conns {
			if uid > 0 && fmt.Sprint(cn["user"]) != fmt.Sprint(uid) {
				continue
			}
			if len(nc.Conns) < lim {
				nc.Conns = append(nc.Conns, cn)
			}
		}
		nc.Shown = len(nc.Conns)
		res = append(res, nc)
	}
	return map[string]any{"online": online, "nodes": res}, nil
}

// ---------- write tools ----------

func toolCreateUser(c *mcpCtx, a args) (any, error) {
	body := map[string]any{"username": a.str("username")}
	if a.has("traffic_gb") {
		body["traffic_limit"] = int64(a.num("traffic_gb", 0) * gb)
	}
	if a.has("expire_days") {
		body["expire_at"] = now() + int64(a.num("expire_days", 0)*86400)
	} else if a.has("expire_at") {
		t, err := c.m.parseTime(a["expire_at"])
		if err != nil {
			return nil, err
		}
		body["expire_at"] = t
	}
	if a.has("groups") {
		ids, err := c.resolveGroups(a["groups"])
		if err != nil {
			return nil, err
		}
		body["group_ids"] = ids
	}
	if s := a.str("reset_strategy"); s != "" {
		body["reset_strategy"] = s
	}
	if a.has("device_limit") {
		body["device_limit"] = a.int("device_limit", 0)
	}
	if a.has("note") {
		body["note"] = a.str("note")
	}
	var res struct {
		ID int64 `json:"id"`
	}
	if err := c.send("POST", "/api/users", body, &res); err != nil {
		return nil, err
	}
	d, err := c.userDetail(res.ID)
	if err != nil {
		return nil, err
	}
	d["created"] = true
	return d, nil
}

func toolUpdateUser(c *mcpCtx, a args) (any, error) {
	id, _, err := c.resolveUser(a)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if s := a.str("new_username"); s != "" {
		body["username"] = s
	}
	if a.has("traffic_gb") {
		body["traffic_limit"] = int64(a.num("traffic_gb", 0) * gb)
	}
	if a.has("expire_at") {
		var t int64
		if a.str("expire_at") != "0" {
			if t, err = c.m.parseTime(a["expire_at"]); err != nil {
				return nil, err
			}
		}
		body["expire_at"] = t
	}
	if a.has("groups") {
		ids, err := c.resolveGroups(a["groups"])
		if err != nil {
			return nil, err
		}
		body["group_ids"] = ids
	}
	if s := a.str("reset_strategy"); s != "" {
		body["reset_strategy"] = s
	}
	if a.has("device_limit") {
		body["device_limit"] = a.int("device_limit", 0)
	}
	if a.has("note") {
		body["note"] = a.str("note")
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("没有要修改的字段 / nothing to update")
	}
	if err := c.send("PATCH", fmt.Sprintf("/api/users/%d", id), body, nil); err != nil {
		return nil, err
	}
	return c.userDetail(id)
}

func (c *mcpCtx) batch(id int64, action string, value any) (int64, error) {
	var res struct {
		Affected int64 `json:"affected"`
	}
	body := map[string]any{"ids": []int64{id}, "action": action}
	if value != nil {
		body["value"] = value
	}
	err := c.send("POST", "/api/users/batch", body, &res)
	return res.Affected, err
}

func (c *mcpCtx) after(id int64, extra map[string]any) (any, error) {
	d, err := c.userDetail(id)
	if err != nil {
		return nil, err
	}
	for k, v := range extra {
		d[k] = v
	}
	return d, nil
}

func toolExtendUser(c *mcpCtx, a args) (any, error) {
	id, _, err := c.resolveUser(a)
	if err != nil {
		return nil, err
	}
	if _, err := c.batch(id, "extend_days", a.num("days", 0)); err != nil {
		return nil, err
	}
	return c.after(id, map[string]any{"extended_days": a.num("days", 0)})
}

func toolAddTraffic(c *mcpCtx, a args) (any, error) {
	id, name, err := c.resolveUser(a)
	if err != nil {
		return nil, err
	}
	var limit int64
	c.m.db.QueryRow(`SELECT traffic_limit FROM users WHERE id=?`, id).Scan(&limit)
	if limit == 0 {
		return nil, fmt.Errorf("用户 %s 不限流量，无需增加 / user %s has no quota (unlimited)", name, name)
	}
	if _, err := c.batch(id, "add_traffic", int64(a.num("gb", 0)*gb)); err != nil {
		return nil, err
	}
	return c.after(id, map[string]any{"added_gb": a.num("gb", 0)})
}

func toolSetEnabled(c *mcpCtx, a args) (any, error) {
	id, _, err := c.resolveUser(a)
	if err != nil {
		return nil, err
	}
	on, _ := a.boolean("enabled")
	action := "disable"
	if on {
		action = "enable"
	}
	if _, err := c.batch(id, action, nil); err != nil {
		return nil, err
	}
	return c.after(id, nil)
}

func toolResetUsage(c *mcpCtx, a args) (any, error) {
	id, _, err := c.resolveUser(a)
	if err != nil {
		return nil, err
	}
	if _, err := c.batch(id, "reset_usage", nil); err != nil {
		return nil, err
	}
	return c.after(id, map[string]any{"usage_reset": true})
}

func toolKickUser(c *mcpCtx, a args) (any, error) {
	id, name, err := c.resolveUser(a)
	if err != nil {
		return nil, err
	}
	if err := c.send("POST", fmt.Sprintf("/api/users/%d/kick", id), nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{"kicked": true, "id": id, "username": name}, nil
}
