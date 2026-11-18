package master

// OpenAPI 3.1 description of the /api surface, served at GET /api/openapi.json.
// apiDocs has one entry per route pattern; TestOpenAPICoversRoutes fails when a registered
// /api route is missing here, so the spec can't drift from routes().

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type oaParam struct {
	Name, In, Desc string
	Schema         map[string]any
	Required       bool
}

type opDoc struct {
	Tag     string
	Summary string
	Desc    string
	Query   []oaParam
	Body    any // schema (map) or "$Name" component ref
	Resp    any // schema (map) or "$Name"; nil = {"ok": true}
	RespCT  string
}

// ---------- schema helpers ----------

type js = map[string]any

func ref(name string) js                { return js{"$ref": "#/components/schemas/" + name} }
func arr(items any) js                  { return js{"type": "array", "items": schemaOf(items)} }
func tStr(desc string) js               { return js{"type": "string", "description": desc} }
func tInt(desc string) js               { return js{"type": "integer", "description": desc} }
func tNum(desc string) js               { return js{"type": "number", "description": desc} }
func tBool(desc string) js              { return js{"type": "boolean", "description": desc} }
func tEnum(desc string, v ...string) js { return js{"type": "string", "description": desc, "enum": v} }
func tTS(desc string) js                { return js{"type": "integer", "format": "unix-time", "description": desc} }
func tBytes(desc string) js             { return js{"type": "integer", "format": "bytes", "description": desc} }
func tObj(props js, required ...string) js {
	o := js{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}
func tMap(desc string) js {
	return js{"type": "object", "description": desc, "additionalProperties": true}
}

func schemaOf(v any) any {
	if s, ok := v.(string); ok && strings.HasPrefix(s, "$") {
		return ref(s[1:])
	}
	return v
}

func qp(name, desc string, schema js) oaParam {
	return oaParam{Name: name, In: "query", Desc: desc, Schema: schema}
}

var trafficQuery = []oaParam{
	qp("from", "Start time (unix seconds); default 24 hours ago", js{"type": "integer"}),
	qp("to", "End time (unix seconds); default now", js{"type": "integer"}),
	qp("user", "Only these user ids (comma-separated)", js{"type": "string"}),
	qp("node", "Only these node ids (comma-separated)", js{"type": "string"}),
	qp("inbound", "Only these inbounds, as node_id/tag (comma-separated)", js{"type": "string"}),
	qp("outbound", "Only these outbound tags (comma-separated)", js{"type": "string"}),
	qp("domain", "Only these registrable domains, subdomains included (comma-separated; 5-minute data)", js{"type": "string"}),
	qp("host", "Only these exact hosts (comma-separated; 5-minute data)", js{"type": "string"}),
	qp("src_ip", "Only these source IPs (comma-separated; 5-minute data)", js{"type": "string"}),
	qp("port", "Only these destination ports (comma-separated; 5-minute data)", js{"type": "string"}),
	qp("rule", "Only these routing rules (comma-separated; 5-minute data)", js{"type": "string"}),
}

var okResp = tObj(js{"ok": tBool("")})
var idResp = tObj(js{"id": tInt("Id of the new object")})

// ---------- components ----------

var oaSchemas = js{
	"Error": tObj(js{"error": tStr("Error message (in the request language)")}, "error"),
	"Ok":    okResp,
	"Me": tObj(js{"username": tStr("Admin username, or token:<key name>"), "totp": tBool("Whether two-factor authentication is on"), "token": tBool("Whether the request used an API key"),
		"scopes": arr(tStr(""))}),
	"Scope": tObj(js{"id": tEnum("", AllScopes...), "label": tStr(""), "description": tStr("")}),
	"ApiKey": tObj(js{
		"id": tInt(""), "name": tStr("Name (recorded in the audit log as token:<name> / mcp:<name>)"), "description": tStr(""), "prefix": tStr("First 10 characters of the key, for identification"),
		"scopes": arr(tEnum("", AllScopes...)), "created_at": tTS(""), "created_by": tStr(""), "expires_at": tTS("0 = never expires"),
		"allow_ips": arr(tStr("IP or CIDR")), "rate_limit": tInt("Requests per minute (effective)"), "rate_burst": tInt("Burst limit (effective)"),
		"last_used": tTS(""), "last_ip": tStr(""), "expired": tBool(""),
	}),
	"ApiKeyCreate": tObj(js{
		"name": tStr("Name, unique"), "description": tStr(""), "scopes": arr(tEnum("", AllScopes...)),
		"expires_at": tTS("Expiry time; 0 = never"), "expires_in_days": tInt("Or: expire after this many days"),
		"allow_ips":  arr(tStr("Allowed source IPs or CIDRs; empty = any. Behind a reverse proxy, X-Forwarded-For is trusted only from localhost")),
		"rate_limit": tInt("Requests per minute; 0 = default 120"), "rate_burst": tInt("Token bucket size; 0 = max(10, rate/4)"),
	}, "name", "scopes"),
	"ApiKeyCreated": tObj(js{"token": tStr("The full key sbp_…, returned only this once"), "key": ref("ApiKey")}),
	"User": tObj(js{
		"id": tInt(""), "username": tStr(""), "sub_token": tStr("Subscription token (/s/<sub_token>)"), "uuid": tStr("VLESS/VMess UUID"),
		"status": tEnum("Derived from quota and expiry", "active", "limited", "expired"), "enabled": tBool(""),
		"traffic_limit": tBytes("Quota; 0 = unlimited"), "expire_at": tTS("0 = no expiry"),
		"reset_strategy": tEnum("", resetEnum...), "note": tStr(""),
		"used_up": tBytes("Upload this period"), "used_down": tBytes("Download this period"), "life_up": tBytes("Total upload"), "life_down": tBytes("Total download"),
		"created_at": tTS(""), "online_ips": tInt("Online IPs right now"), "device_limit": tInt("0 = unlimited"),
		"speed_up": tInt("Upload speed limit in bytes/s; 0 = unlimited"), "speed_down": tInt("Download speed limit in bytes/s; 0 = unlimited"),
		"last_reset_at": tTS(""), "sub_last_at": tTS("Last subscription fetch"), "sub_last_ua": tStr(""), "group_ids": arr(tInt("")),
		"last_online": tTS(""), "sub_profile_id": tInt("Routing profile; 0 = follow group / default"),
	}),
	"UserInput": tObj(js{
		"username": tStr("Letters, digits, . _ @ -; up to 64"), "traffic_limit": tBytes("0 = unlimited"), "expire_at": tTS("0 = never"),
		"created_at": tTS("PATCH only: account start; anchors month_rolling resets"), "reset_strategy": tEnum("", resetEnum...), "note": tStr(""), "enabled": tBool(""),
		"reset_usage": tBool("PATCH only: reset this period's usage"), "rotate_secrets": tBool("PATCH only: regenerate UUID / password / subscription token"),
		"device_limit": tInt(""), "speed_up": tInt(""), "speed_down": tInt(""), "group_ids": arr(tInt("")), "sub_profile_id": tInt(""),
		"limits": arr(tObj(js{"id": tInt("Traffic limit preset"), "traffic_limit": tBytes("Custom size; 0 = the preset's size")})),
	}),
	"UserDetail": tObj(js{"user": ref("User"), "ips": arr(tObj(js{"ip": tStr(""), "node_id": tStr(""), "node": tStr(""),
		"first_seen": tTS(""), "last_seen": tTS(""), "online": tBool("")}))}),
	"UserSub": tObj(js{"url": tStr("Subscription URL"), "qr": tStr("QR code data URI"), "active": tBool(""),
		"formats": tObj(js{"shadowrocket": tStr("Structured node list with per-node IP preference"), "base64": tStr(""), "singbox": tStr(""), "mihomo": tStr(""), "surge": tStr(""), "page": tStr("")}),
		"links":   arr(tObj(js{"remark": tStr(""), "type": tStr(""), "uri": tStr("Share links")})),
		"access":  arr(tObj(js{"ts": tTS(""), "ip": tStr(""), "ua": tStr(""), "format": tStr("")}))}),
	"BatchRequest": tObj(js{
		"ids":    arr(tInt("")),
		"action": tEnum("Batch action", "enable", "disable", "reset_usage", "delete", "extend_days", "add_traffic", "set_groups", "set_limit", "restore"),
		"value":  js{"description": "extend_days: days; add_traffic: bytes (may be negative); set_groups: array of group ids; set_limit: {rule_id, attach: bool, traffic_limit: bytes (0 = the preset's size)}; restore: the undo snapshot from a previous response"},
	}, "ids", "action"),
	"BatchResult": tObj(js{"affected": tInt(""), "undo": arr(tMap("Snapshot; POST it back unchanged with action=restore to undo"))}),
	"BulkRequest": tObj(js{"prefix": tStr(""), "count": tInt("1-1000"), "start": tInt("Starting number"), "traffic_limit": tBytes(""),
		"expire_days": tInt(""), "reset_strategy": tEnum("", resetEnum...), "group_ids": arr(tInt(""))}, "prefix", "count"),
	"Node": tObj(js{
		"id": tStr(""), "name": tStr(""), "address": tStr("Last connected from"), "public_address": tStr("Address used in subscriptions"), "template_id": tInt(""),
		"online": tBool(""), "enrolled": tBool(""), "revision": tInt("Desired config revision"), "applied_revision": tInt("Applied revision"),
		"apply_error": tStr(""), "last_seen": tTS(""), "cert_expire": tTS(""), "info": tMap("Version, hostname, OS"),
		"traffic_limit": tBytes("Monthly traffic cap for the node"), "reset_day": tInt(""), "traffic_mode": tEnum("What traffic_limit is compared with", "both", "out", "in", "max"), "client_ip": tEnum("Address family clients try first", "", "ipv6", "ipv4"),
		"server": tObj(js{"tracked": tBool("The node reports uplink counters"), "rx": tBytes("Uplink in this period"), "tx": tBytes("Uplink out"),
			"adj": tBytes("Admin correction"), "used": tBytes("Metered by traffic_mode, with the correction"), "period_start": tTS(""), "period_end": tTS("")}), "inbounds": tInt(""), "sys": tMap("CPU, memory, load, disk"),
		"proc": tMap("The sbnode process: cpu_pct (% of the machine), rss, heap, goroutines, threads, started"), "master": tMap("The sbmaster process, on the node that runs the panel"),
		"rate_up": tInt("bytes/s"), "rate_down": tInt("bytes/s"), "active_conns": tInt(""), "spool_backlog": tInt(""), "online_users": tInt(""),
		"month_up": tBytes(""), "month_down": tBytes(""), "today_up": tBytes(""), "today_down": tBytes(""),
	}),
	"NodeMetric": nodeMetricSchema,
	"NodeInput": tObj(js{"name": tStr(""), "public_address": tStr(""), "template_id": tInt(""), "traffic_limit": tBytes("PATCH only"),
		"reset_day": tInt("PATCH only, 1-28"), "traffic_mode": tEnum("PATCH only: both (in+out), out, in, max (the larger)", "both", "out", "in", "max"),
		"client_ip":    tEnum("PATCH only: address family clients try first for the node's hostname; empty = client default", "", "ipv6", "ipv4"),
		"traffic_used": tBytes("PATCH only: set this period's server traffic to the provider's figure"), "skeleton": tMap("PATCH only: node-specific skeleton")}),
	"Enroll": tObj(js{"node_id": tStr(""), "token": tStr("One-time enrollment token"), "expires_at": tTS(""), "master": tStr(""),
		"ca_sha256": tStr(""), "command": tStr("The sbnode enroll command to run on the node")}),
	"Inbound": tObj(js{"id": tInt(""), "node_id": tStr(""), "node": tStr(""), "tag": tStr(""), "type": tStr("vless, vmess, trojan, shadowsocks, hysteria2, tuic, anytls…"),
		"port": tInt(""), "options": tMap("sing-box inbound options (including secrets)"), "client": tMap("Client-side parameters"), "enabled": tBool(""), "remark": tStr(""),
		"sort": tInt(""), "group_ids": arr(tInt(""))}),
	"InboundInput": tObj(js{"node_id": tStr(""), "tag": tStr(""), "type": tStr(""), "options": tMap("sing-box inbound options; must include listen_port"),
		"client": tMap(""), "enabled": tBool(""), "remark": tStr(""), "group_ids": arr(tInt("")), "sort": tInt("")}, "node_id", "tag", "type", "options"),
	"Group": tObj(js{"id": tInt(""), "name": tStr(""), "remark": tStr(""), "is_default": tBool("New users / inbounds join automatically"),
		"inbound_ids": arr(tInt("")), "users": tInt(""), "sub_profile_id": tInt("")}),
	"Limit": tObj(js{"id": tInt(""), "name": tStr(""), "note": tStr(""), "traffic_limit": tBytes("Per attached user and period (users can get a custom size)"),
		"period": tEnum("user: resets with each user's own cycle; month: monthly on reset_day", "user", "month"), "reset_day": tInt("1-28 (month)"),
		"enabled": tBool(""), "inbound_ids": arr(tInt("Inbounds whose traffic counts; a user over the limit loses only these")),
		"period_start": tTS("month limits"), "next_reset": tTS("month limits"), "users": tInt("Users it is attached to"),
		"limited": tInt("Users over it now"), "used": tBytes("All users, this period"), "overrides": tInt("Attached with a custom size")}),
	"LimitInput": tObj(js{"name": tStr(""), "note": tStr(""), "traffic_limit": tBytes(""), "period": tEnum("", "user", "month"),
		"reset_day": tInt(""), "enabled": tBool(""), "inbound_ids": arr(tInt(""))}),
	"GroupInput": tObj(js{"name": tStr(""), "remark": tStr(""), "is_default": tBool(""), "inbound_ids": arr(tInt("")), "sub_profile_id": tInt("")}),
	"Host": tObj(js{"id": tInt(""), "inbound_id": tInt(""), "remark": tStr(""), "address": tStr("Address substituted in subscriptions (CDN, relay)"), "port": tInt("0 = use the inbound's port"),
		"sni": tStr(""), "host": tStr(""), "path": tStr(""), "alpn": tStr(""), "fingerprint": tStr(""), "allow_insecure": tBool(""), "sort": tInt(""), "enabled": tBool("")}),
	"Template": tObj(js{"id": tInt(""), "name": tStr(""), "json": tStr("Published sing-box skeleton JSON"), "draft": tStr("Draft"), "has_draft": tBool(""),
		"updated_at": tTS(""), "nodes": arr(tStr("Ids of nodes using this template"))}),
	"TemplateInput": tObj(js{"name": tStr(""), "from": tInt("Copy from template id"), "draft": tStr("PATCH only: skeleton JSON draft")}),
	"RoutingProfile": tObj(js{"id": tInt(""), "slug": tStr(""), "name": tStr(""), "description": tStr(""), "is_default": tBool(""),
		"selectable": tBool("Users can pick it on the subscription page"), "published_at": tTS(""), "updated_at": tTS(""), "has_draft": tBool(""),
		"users": tInt(""), "group_ids": arr(tInt(""))}),
	"RoutingProfileInput": tObj(js{"name": tStr(""), "slug": tStr(""), "description": tStr(""), "preset": tStr("Create from a built-in preset"),
		"from": tInt("Copy from profile id"), "selectable": tBool(""), "is_default": tBool("")}),
	"TopRow": tObj(js{"key": tStr("Dimension value (user id, node id, domain…)"), "label": tStr("Display name"), "up": tBytes(""), "down": tBytes(""),
		"conns": tInt(""), "users": tInt("Number of users involved")}),
	"Series": tObj(js{"key": tStr(""), "label": tStr(""), "up": tBytes(""), "down": tBytes(""),
		"points": arr(js{"type": "array", "items": js{"type": "integer"}, "description": "[ts, up, down]"})}),
	"Online": tObj(js{"user_id": tInt(""), "user": tStr(""), "ip": tStr(""), "node_id": tStr(""), "node": tStr("")}),
	"Conn": tObj(js{"id": tInt(""), "user": tStr("User id"), "username": tStr(""), "inbound": tStr(""), "outbound": tStr(""), "network": tStr(""),
		"host": tStr("Destination"), "port": tInt(""), "src_ip": tStr(""), "rule": tStr("Matched routing rule"), "start": tTS(""), "up": tBytes(""), "down": tBytes("")}),
	"Event": tObj(js{"ts": tTS(""), "kind": tStr("node_offline, quota_exceeded, user_expired…"), "user_id": tInt(""), "user": tStr(""),
		"node_id": tStr(""), "node": tStr(""), "detail": tStr("")}),
	"AuditEntry": tObj(js{"ts": tTS(""), "admin": tStr("Admin username, token:<name> or mcp:<name>"), "action": tStr("e.g. user.create"),
		"target": tStr(""), "detail": tStr(""), "ip": tStr("")}),
	"Overview": tMap("today_up/down, month_up/down, rate_up/down, active_conns, users, active_users, online_users, online_ips, nodes, top_users, top_domains, top_outbounds"),
	"Settings": tObj(js{"sub_base_url": tStr(""), "sub_title": tStr(""), "sub_update_hours": tStr(""), "tg_chat": tStr(""), "webhook_url": tStr(""),
		"notify_events": tStr("Comma-separated"), "device_policy": tEnum("", "reject", "kick_oldest"), "sub_announcement": tStr(""), "sub_logo_url": tStr(""),
		"sub_support_links": tStr("JSON"), "sub_clients": tStr(""), "sub_rule_mirror": tStr(""), "ui_appearance": tStr("JSON"),
		"tg_token_set": tBool("Read-only"), "ca_sha256": tStr("Read-only"), "grpc_listen": tStr("Read-only"), "public_grpc": tStr("Read-only"), "tz": tStr("Read-only")}),
	"SettingsInput": js{"type": "object", "description": "Settings to change (string values); unknown keys return 400; tg_token is also writable", "additionalProperties": js{"type": "string"}},
	"SubTheme":      tMap("Subscription-page themes: id, name, version, active, settings schema"),
	"ImportRequest": tObj(js{"node_id": tStr(""), "data": tStr("JSON from 3x-ui /panel/api/inbounds/list"), "dry_run": tBool("Preview only")}, "node_id", "data"),
	"ImportReport":  tMap("dry_run, users, updated, skipped, groups, warnings, inbounds"),
}

// ---------- operations ----------

var apiDocs = map[string]opDoc{
	"POST /api/auth/login": {Tag: "auth", Summary: "Sign in (sets the session cookie)", Desc: "For automation, use an API key instead.",
		Body: tObj(js{"username": tStr(""), "password": tStr(""), "code": tStr("Two-factor code (required when enabled)")}, "username", "password"),
		Resp: tObj(js{"username": tStr("")})},
	"POST /api/auth/logout":     {Tag: "auth", Summary: "Sign out"},
	"GET /api/auth/me":          {Tag: "auth", Summary: "Current identity and scopes", Resp: "$Me"},
	"POST /api/me/password":     {Tag: "auth", Summary: "Change password (session only)", Body: tObj(js{"old": tStr(""), "new": tStr("At least 8 characters")}, "old", "new")},
	"POST /api/me/totp/setup":   {Tag: "auth", Summary: "Generate a two-factor secret (session only)", Resp: tObj(js{"secret": tStr(""), "uri": tStr(""), "qr": tStr("")})},
	"POST /api/me/totp/enable":  {Tag: "auth", Summary: "Turn on two-factor authentication (session only)", Body: tObj(js{"code": tStr("")}, "code")},
	"POST /api/me/totp/disable": {Tag: "auth", Summary: "Turn off two-factor authentication (session only)", Body: tObj(js{"password": tStr("")}, "password")},
	"GET /api/openapi.json":     {Tag: "auth", Summary: "This OpenAPI document", Resp: tMap("OpenAPI 3.1")},
	"GET /api/scopes":           {Tag: "api-keys", Summary: "Scopes that can be granted", Resp: arr("$Scope")},

	"GET /api/tokens":         {Tag: "api-keys", Summary: "List API keys", Desc: "Requires a session or the admin scope. The key itself is never returned.", Resp: arr("$ApiKey")},
	"POST /api/tokens":        {Tag: "api-keys", Summary: "Create an API key", Desc: "The full key appears only once, in this response.", Body: "$ApiKeyCreate", Resp: "$ApiKeyCreated"},
	"DELETE /api/tokens/{id}": {Tag: "api-keys", Summary: "Delete (revoke) an API key"},

	"GET /api/audit":        {Tag: "events", Summary: "Audit log (newest first)", Query: []oaParam{qp("limit", "Default 200", js{"type": "integer"})}, Resp: arr("$AuditEntry")},
	"GET /api/settings":     {Tag: "settings", Summary: "Get panel settings", Resp: "$Settings"},
	"PUT /api/settings":     {Tag: "settings", Summary: "Update panel settings (including ui_appearance)", Body: "$SettingsInput"},
	"GET /api/backup":       {Tag: "settings", Summary: "Download an SQLite backup (admin)", Desc: "Contains the CA private key and every secret.", Resp: js{"type": "string", "format": "binary"}, RespCT: "application/octet-stream"},
	"POST /api/notify/test": {Tag: "settings", Summary: "Send a test notification"},
	"POST /api/import/3xui": {Tag: "settings", Summary: "Import inbounds and users from 3x-ui (admin)", Body: "$ImportRequest", Resp: "$ImportReport"},

	"GET /api/overview": {Tag: "traffic", Summary: "Overview dashboard data", Resp: "$Overview"},
	"GET /api/traffic/series": {Tag: "traffic", Summary: "Traffic time series", Resp: arr("$Series"),
		Query: append([]oaParam{
			qp("step", "Step in seconds", js{"type": "integer", "enum": []int{60, 300, 900, 1800, 3600, 10800, 21600, 86400}, "default": 300}),
			qp("group", "Split by. domain, host, src_ip, port and rule use 5-minute data", js{"type": "string", "enum": []string{"total", "user", "node", "inbound", "outbound", "domain", "host", "src_ip", "port", "rule"}}),
		}, trafficQuery...)},
	"GET /api/traffic/top": {Tag: "traffic", Summary: "Traffic ranking", Desc: "The X-Precision response header gives the data precision (1m/5m/1h).", Resp: arr("$TopRow"),
		Query: append([]oaParam{
			{Name: "dim", In: "query", Required: true, Desc: "Dimension", Schema: js{"type": "string", "enum": []string{"user", "node", "inbound", "outbound", "host", "domain", "src_ip", "port", "rule", "network"}}},
			qp("limit", "1-1000, default 50", js{"type": "integer"}),
		}, trafficQuery...)},
	"GET /api/events": {Tag: "events", Summary: "System events (newest first)", Query: []oaParam{qp("limit", "Default 100", js{"type": "integer"})}, Resp: arr("$Event")},
	"GET /api/online": {Tag: "traffic", Summary: "Online users and source IPs (live)", Resp: arr("$Online")},

	"GET /api/update": {Tag: "settings", Summary: "Panel and node versions, latest release, upgrade progress and last results",
		Resp: tObj(js{"current": tStr("Running sbmaster version"), "latest": tObj(js{"tag": tStr(""), "name": tStr(""), "notes": tStr("Release notes (Markdown)"), "url": tStr(""), "published": tStr("")}),
			"has_update": tBool(""), "checked_at": tTS(""), "check_error": tStr(""), "supported": tBool("The panel can upgrade itself (systemd / OpenRC service)"),
			"running": tBool("A self-upgrade is in flight"), "can_rollback": tBool("A previous sbmaster binary is kept"),
			"nodes": arr(tObj(js{"id": tStr(""), "name": tStr(""), "online": tBool(""), "version": tStr(""), "arch": tStr(""), "supported": tBool("The node can be upgraded from the panel"), "has_update": tBool("")}))})},
	"POST /api/update/check": {Tag: "settings", Summary: "Check GitHub for the latest release now", Resp: js{"type": "object"}},
	"POST /api/update/master": {Tag: "settings", Summary: "Upgrade (or roll back) the panel; it restarts and rolls back automatically if the new version doesn't come up",
		Desc: "Returns 202 before the restart; poll GET /api/update.", Body: tObj(js{"version": tStr("Release tag; default the latest"), "rollback": tBool("Put the previous binary back")}), Resp: js{"type": "object"}},
	"POST /api/update/nodes": {Tag: "settings", Summary: "Upgrade (or roll back) nodes; each rolls back automatically if the new sbnode doesn't reconnect",
		Body: tObj(js{"ids": arr(tStr("")), "outdated": tBool("Every online node behind the release (when ids is empty)"), "version": tStr("Release tag; default the latest"), "rollback": tBool("")}), Resp: js{"type": "object"}},

	"GET /api/nodes":                   {Tag: "nodes", Summary: "Nodes and their health", Resp: arr("$Node")},
	"POST /api/nodes":                  {Tag: "nodes", Summary: "Create a node and return its enrollment command", Body: "$NodeInput", Resp: "$Enroll"},
	"PATCH /api/nodes/{id}":            {Tag: "nodes", Summary: "Update a node", Body: "$NodeInput"},
	"DELETE /api/nodes/{id}":           {Tag: "nodes", Summary: "Delete a node"},
	"POST /api/nodes/{id}/token":       {Tag: "nodes", Summary: "Regenerate the enrollment token", Resp: "$Enroll"},
	"GET /api/nodes/{id}/spec":         {Tag: "nodes", Summary: "Config currently deployed to the node (debugging)", Resp: tMap("")},
	"GET /api/nodes/{id}/conns":        {Tag: "traffic", Summary: "Live connections on a node", Desc: "The first call makes the node start streaming; retry later while ready=false.", Resp: tObj(js{"conns": arr("$Conn"), "total": tInt(""), "ready": tBool("")})},
	"GET /api/nodes/{id}/metrics":      nodeMetricsDoc,
	"POST /api/nodes/{id}/conns/close": {Tag: "traffic", Summary: "Close a connection", Body: tObj(js{"ids": arr(tInt(""))}, "ids")},

	"GET /api/inbounds":               {Tag: "inbounds", Summary: "List inbounds", Query: []oaParam{qp("node", "Node id", js{"type": "string"})}, Resp: arr("$Inbound")},
	"POST /api/inbounds":              {Tag: "inbounds", Summary: "Create an inbound", Body: "$InboundInput", Resp: idResp},
	"PATCH /api/inbounds/{id}":        {Tag: "inbounds", Summary: "Update an inbound", Body: "$InboundInput"},
	"DELETE /api/inbounds/{id}":       {Tag: "inbounds", Summary: "Delete an inbound"},
	"POST /api/tools/reality-keypair": {Tag: "inbounds", Summary: "Generate a REALITY key pair", Resp: tObj(js{"private_key": tStr(""), "public_key": tStr(""), "short_id": tStr("")})},

	"GET /api/groups":         {Tag: "groups", Summary: "List groups", Resp: arr("$Group")},
	"POST /api/groups":        {Tag: "groups", Summary: "Create a group", Body: "$GroupInput", Resp: idResp},
	"PATCH /api/groups/{id}":  {Tag: "groups", Summary: "Update a group", Body: "$GroupInput"},
	"DELETE /api/groups/{id}": {Tag: "groups", Summary: "Delete a group"},

	"GET /api/limits":         {Tag: "limits", Summary: "List traffic limits", Resp: arr("$Limit")},
	"POST /api/limits":        {Tag: "limits", Summary: "Create a traffic limit", Body: "$LimitInput", Resp: idResp},
	"PATCH /api/limits/{id}":  {Tag: "limits", Summary: "Update a traffic limit (only the fields sent)", Body: "$LimitInput"},
	"DELETE /api/limits/{id}": {Tag: "limits", Summary: "Delete a traffic limit"},
	"GET /api/limits/{id}/users": {Tag: "limits", Summary: "Users a preset is attached to, with their size and usage this period",
		Resp: arr(tObj(js{"user_id": tInt(""), "username": tStr(""), "enabled": tBool(""), "traffic_limit": tBytes("In effect"),
			"override": tBool("Custom size"), "up": tBytes(""), "down": tBytes(""), "used": tBytes(""), "exhausted": tBool(""), "reaches": tBool("Can use one of its inbounds")}))},

	"GET /api/hosts":         {Tag: "hosts", Summary: "List hosts (subscription address overrides)", Resp: arr("$Host")},
	"POST /api/hosts":        {Tag: "hosts", Summary: "Create a host", Body: "$Host", Resp: idResp},
	"PUT /api/hosts/{id}":    {Tag: "hosts", Summary: "Update a host", Body: "$Host", Resp: idResp},
	"DELETE /api/hosts/{id}": {Tag: "hosts", Summary: "Delete a host"},

	"GET /api/templates":             {Tag: "templates", Summary: "List route templates (sing-box skeletons)", Resp: arr("$Template")},
	"POST /api/templates":            {Tag: "templates", Summary: "Create a template", Body: "$TemplateInput", Resp: idResp},
	"PATCH /api/templates/{id}":      {Tag: "templates", Summary: "Save a template draft / rename", Body: "$TemplateInput"},
	"POST /api/templates/{id}/apply": {Tag: "templates", Summary: "Publish the draft and deploy it to nodes"},
	"DELETE /api/templates/{id}":     {Tag: "templates", Summary: "Delete a template"},

	"GET /api/routing/catalog":                              {Tag: "routing", Summary: "Rule-set catalog", Resp: arr(tObj(js{"key": tStr(""), "label": tStr(""), "kind": tStr(""), "geosite": tStr(""), "geoip": tStr("")}))},
	"GET /api/routing/nodes":                                {Tag: "routing", Summary: "Nodes / inbounds usable as exits", Resp: arr(tMap(""))},
	"GET /api/routing/profiles":                             {Tag: "routing", Summary: "List routing profiles", Resp: arr("$RoutingProfile")},
	"POST /api/routing/profiles":                            {Tag: "routing", Summary: "Create a routing profile", Body: "$RoutingProfileInput", Resp: idResp},
	"GET /api/routing/profiles/{id}":                        {Tag: "routing", Summary: "Routing profile details (with rules and draft)", Resp: tMap("")},
	"PATCH /api/routing/profiles/{id}":                      {Tag: "routing", Summary: "Update profile properties", Body: "$RoutingProfileInput"},
	"DELETE /api/routing/profiles/{id}":                     {Tag: "routing", Summary: "Delete a profile"},
	"PUT /api/routing/profiles/{id}/draft":                  {Tag: "routing", Summary: "Save the draft", Body: tObj(js{"draft": tMap("Rules draft")}, "draft")},
	"DELETE /api/routing/profiles/{id}/draft":               {Tag: "routing", Summary: "Discard the draft"},
	"POST /api/routing/profiles/{id}/preview":               {Tag: "routing", Summary: "Preview the config generated for a client", Body: tObj(js{"client": tEnum("", "singbox", "mihomo", "surge"), "user_id": tInt(""), "draft": tMap("")}), Resp: tMap("")},
	"POST /api/routing/profiles/{id}/publish":               {Tag: "routing", Summary: "Publish the draft"},
	"GET /api/routing/profiles/{id}/history":                {Tag: "routing", Summary: "Publish history", Resp: arr(tObj(js{"id": tInt(""), "ts": tTS(""), "admin": tStr(""), "json": tMap("")}))},
	"POST /api/routing/profiles/{id}/history/{hid}/restore": {Tag: "routing", Summary: "Restore a past version as the draft"},

	"GET /api/users":            {Tag: "users", Summary: "List users", Resp: arr("$User")},
	"POST /api/users":           {Tag: "users", Summary: "Create a user", Body: "$UserInput", Resp: idResp},
	"POST /api/users/bulk":      {Tag: "users", Summary: "Create users in bulk by prefix", Body: "$BulkRequest", Resp: tMap("created, ids")},
	"POST /api/users/batch":     {Tag: "users", Summary: "Batch actions (enable / disable / extend / add traffic / reset / set groups / delete / undo)", Body: "$BatchRequest", Resp: "$BatchResult"},
	"GET /api/users/{id}":       {Tag: "users", Summary: "User details and IP history", Resp: "$UserDetail"},
	"GET /api/users/{id}/sub":   {Tag: "users", Summary: "A user's subscription URL, links per format and access log", Resp: "$UserSub"},
	"PATCH /api/users/{id}":     {Tag: "users", Summary: "Update a user (only the fields sent)", Body: "$UserInput"},
	"DELETE /api/users/{id}":    {Tag: "users", Summary: "Delete a user"},
	"POST /api/users/{id}/kick": {Tag: "users", Summary: "Kick (close all connections)"},
	"PUT /api/users/{id}/limits/{rule}": {Tag: "limits", Summary: "Attach a traffic limit preset to a user, or take it off",
		Desc: "attach: put the preset on the user or take it off; traffic_limit: custom size (0 = the preset's size); reset_usage: clear this period's usage.",
		Body: tObj(js{"attach": tBool(""), "traffic_limit": tBytes(""), "reset_usage": tBool("")})},

	"GET /api/sub-themes":               {Tag: "settings", Summary: "List subscription-page themes", Resp: arr("$SubTheme")},
	"POST /api/sub-themes":              {Tag: "settings", Summary: "Upload a theme package (zip, multipart)", Body: js{"type": "string", "format": "binary"}},
	"PUT /api/sub-themes/active":        {Tag: "settings", Summary: "Activate a theme", Body: tObj(js{"id": tStr("")}, "id")},
	"PUT /api/sub-themes/{id}/settings": {Tag: "settings", Summary: "Save theme settings", Body: tMap("")},
	"DELETE /api/sub-themes/{id}":       {Tag: "settings", Summary: "Delete a theme"},
	"GET /api/sub-themes/{id}/preview":  {Tag: "settings", Summary: "Theme preview page (HTML)", Resp: js{"type": "string"}, RespCT: "text/html"},

	"POST /mcp": {Tag: "mcp", Summary: "MCP endpoint (JSON-RPC 2.0, Streamable HTTP, stateless)",
		Desc: "Methods: initialize, tools/list, tools/call, resources/list, resources/read, ping. See docs/API.md.",
		Body: tMap("A JSON-RPC 2.0 request or a batch array"), Resp: tMap("JSON-RPC 2.0 response; notifications return 202")},
}

var oaTags = []js{
	{"name": "auth", "description": "Sign-in, identity and account"},
	{"name": "api-keys", "description": "API key management (session or admin scope)"},
	{"name": "users", "description": "Users, quotas, expiry, batch actions"},
	{"name": "nodes", "description": "Node enrollment and config"},
	{"name": "inbounds", "description": "Inbound protocols"},
	{"name": "groups", "description": "Groups (users ↔ inbounds)"},
	{"name": "limits", "description": "Traffic limits on sets of inbounds (e.g. a home line), per user"},
	{"name": "hosts", "description": "Subscription address overrides (CDN / relay)"},
	{"name": "templates", "description": "sing-box route templates"},
	{"name": "routing", "description": "Subscription routing profiles"},
	{"name": "traffic", "description": "Traffic stats, online users and live connections"},
	{"name": "events", "description": "System events and audit log"},
	{"name": "settings", "description": "Panel settings, themes, backup and migration"},
	{"name": "mcp", "description": "Model Context Protocol endpoint for AI agents"},
}

var pathParamRe = regexp.MustCompile(`\{([a-z]+)\}`)

var (
	openAPIOnce sync.Once
	openAPIJSON map[string][]byte // per language
)

// buildOpenAPI renders the spec from apiDocs + routeScopes.
func buildOpenAPI() map[string]any {
	paths := map[string]js{}
	pats := sortedKeys(apiDocs)
	for _, pat := range pats {
		d := apiDocs[pat]
		method, path, _ := strings.Cut(pat, " ")
		method = strings.ToLower(method)
		scope := routeScopes[pat]
		op := js{
			"tags": []string{d.Tag}, "summary": d.Summary,
			"operationId":      operationID(method, path),
			"x-required-scope": scope,
		}
		desc := d.Desc
		switch scope {
		case scopePublic:
			op["security"] = []js{}
		case scopeSession:
			op["security"] = []js{{"cookieAuth": []string{}}}
			desc = strings.TrimSpace("Signed-in session only; API keys can't call this." + " " + desc)
		case scopeKey:
			op["security"] = []js{{"bearerAuth": []string{}}}
			desc = strings.TrimSpace("API keys (Bearer) only; each tool checks its own scope." + " " + desc)
		case scopeAny:
			desc = strings.TrimSpace("Any authenticated caller." + " " + desc)
		default:
			desc = strings.TrimSpace(fmt.Sprintf("Requires scope `%s`.", scope) + " " + desc)
		}
		op["description"] = desc
		params := []js{}
		for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
			t := "integer"
			if m[1] == "id" && (strings.HasPrefix(path, "/api/nodes/") || strings.HasPrefix(path, "/api/sub-themes/")) {
				t = "string"
			}
			params = append(params, js{"name": m[1], "in": "path", "required": true, "schema": js{"type": t}})
		}
		for _, q := range d.Query {
			p := js{"name": q.Name, "in": q.In, "schema": q.Schema}
			if q.Desc != "" {
				p["description"] = q.Desc
			}
			if q.Required {
				p["required"] = true
			}
			params = append(params, p)
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if d.Body != nil {
			ct := "application/json"
			if pat == "POST /api/sub-themes" {
				ct = "application/zip"
			}
			op["requestBody"] = js{"required": true, "content": js{ct: js{"schema": schemaOf(d.Body)}}}
		}
		resp := d.Resp
		if resp == nil {
			resp = "$Ok"
		}
		ct := d.RespCT
		if ct == "" {
			ct = "application/json"
		}
		responses := js{"200": js{"description": "Success", "content": js{ct: js{"schema": schemaOf(resp)}}}}
		if scope != scopePublic {
			responses["401"] = js{"$ref": "#/components/responses/Unauthorized"}
			responses["403"] = js{"$ref": "#/components/responses/Forbidden"}
			responses["429"] = js{"$ref": "#/components/responses/RateLimited"}
		}
		if d.Body != nil || len(d.Query) > 0 || strings.Contains(path, "{") {
			responses["400"] = js{"$ref": "#/components/responses/BadRequest"}
		}
		op["responses"] = responses
		if paths[path] == nil {
			paths[path] = js{}
		}
		paths[path][method] = op
	}
	errResp := func(desc string) js {
		return js{"description": desc, "content": js{"application/json": js{"schema": ref("Error")}}}
	}
	rl := errResp("Rate limit for this key exceeded")
	rl["headers"] = js{"Retry-After": js{"description": "Seconds to wait", "schema": js{"type": "integer"}}}
	return map[string]any{
		"openapi": "3.1.0",
		"info": js{
			"title":       "sbpanel API",
			"version":     "1",
			"description": "HTTP API of the sing-box proxy panel. Call it with `Authorization: Bearer sbp_…` (create one in Settings → API & agents); `x-required-scope` gives the scope each operation needs. AI agents can use the MCP endpoint `POST /mcp` instead.",
		},
		"servers":  []js{{"url": "/", "description": "This panel"}},
		"security": []js{{"bearerAuth": []string{}}, {"cookieAuth": []string{}}},
		"tags":     oaTags,
		"paths":    paths,
		"components": js{
			"securitySchemes": js{
				"bearerAuth": js{"type": "http", "scheme": "bearer", "bearerFormat": "sbp_<48 hex>", "description": "API key"},
				"cookieAuth": js{"type": "apiKey", "in": "cookie", "name": sessionCookie, "description": "Panel sign-in session"},
			},
			"schemas": oaSchemas,
			"responses": js{
				"BadRequest":   errResp("Invalid parameters"),
				"Unauthorized": errResp("Not authenticated, or the key is invalid or expired"),
				"Forbidden":    errResp("Missing scope, or the source IP is not on the allowlist"),
				"RateLimited":  rl,
			},
		},
		"x-scopes": scopeInfo(),
	}
}

func operationID(method, path string) string {
	parts := []string{method}
	for _, seg := range strings.Split(strings.TrimPrefix(path, "/api/"), "/") {
		seg = strings.NewReplacer("{", "by_", "}", "", "-", "_", ".", "_").Replace(seg)
		parts = append(parts, seg)
	}
	return strings.Join(parts, "_")
}

func (m *Master) apiOpenAPI(w http.ResponseWriter, r *http.Request) {
	openAPIOnce.Do(func() {
		en, _ := json.Marshal(buildOpenAPI())
		var generic any
		json.Unmarshal(en, &generic)
		zh, _ := json.Marshal(localizeDoc(langZH, generic))
		openAPIJSON = map[string][]byte{langEN: en, langZH: zh}
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Add("Vary", "X-Lang, Accept-Language")
	w.Write(openAPIJSON[reqLang(r)])
}

// localizeDoc translates the human-readable text of a JSON-decoded spec (summary / description
// values, x-scopes labels) for lang. Operation descriptions may be a scope sentence plus the
// operation's own text, so a description is also tried as "<sentence>. <rest>" pieces.
func localizeDoc(lang string, v any) any {
	if lang == langEN {
		return v
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if s, ok := e.(string); ok && (k == "summary" || k == "description" || k == "label") {
				out[k] = docText(lang, s)
				continue
			}
			out[k] = localizeDoc(lang, e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = localizeDoc(lang, e)
		}
		return out
	}
	return v
}

var scopeSentenceRe = regexp.MustCompile("^Requires scope `([^`]+)`\\.(?: (.*))?$")

func docText(lang, s string) string {
	if zh, ok := zhAPIDocs[s]; ok {
		return zh
	}
	if m := scopeSentenceRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(fmt.Sprintf(zhAPIDocs["Requires scope `%s`."], m[1]) + docText(lang, m[2]))
	}
	for _, pre := range []string{"Signed-in session only; API keys can't call this.", "API keys (Bearer) only; each tool checks its own scope.", "Any authenticated caller."} {
		if rest, ok := strings.CutPrefix(s, pre); ok && rest != "" {
			return zhAPIDocs[pre] + docText(lang, strings.TrimSpace(rest))
		}
	}
	return L(lang, s)
}

// openAPIPaths lists "METHOD /path" for every operation in the spec (tests).
func openAPIPaths(spec map[string]any) []string {
	out := []string{}
	for p, ops := range spec["paths"].(map[string]js) {
		for meth := range ops {
			out = append(out, strings.ToUpper(meth)+" "+p)
		}
	}
	sort.Strings(out)
	return out
}
