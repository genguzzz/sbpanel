package master

import (
	"net/http"
	"strings"
)

// routeScopes maps every /api (and /mcp) route pattern registered in routes() to the scope it
// needs. scopedMux wraps each route with guard(scope); a route missing here is treated as
// admin-only (fail closed) and TestRouteScopesComplete fails, so a new route can't silently
// bypass the check. Sessions (logged-in admins) pass every scope except scopeKey.
var routeScopes = map[string]string{
	// auth & account
	"POST /api/auth/login":      scopePublic,
	"POST /api/auth/logout":     scopePublic,
	"GET /api/auth/me":          scopeAny,
	"POST /api/me/password":     scopeSession,
	"POST /api/me/totp/setup":   scopeSession,
	"POST /api/me/totp/enable":  scopeSession,
	"POST /api/me/totp/disable": scopeSession,
	"GET /api/openapi.json":     scopeAny,
	"GET /api/scopes":           scopeAny,
	"/api/":                     scopePublic, // 404 fallback
	"POST /mcp":                 scopeKey,
	"GET /mcp":                  scopeKey,
	"DELETE /mcp":               scopeKey,

	// API key management
	"GET /api/tokens":         ScopeAdmin,
	"POST /api/tokens":        ScopeAdmin,
	"DELETE /api/tokens/{id}": ScopeAdmin,

	// settings / system
	"GET /api/audit":          ScopeRead,
	"GET /api/settings":       ScopeRead,
	"PUT /api/settings":       ScopeSettingsWrite,
	"GET /api/backup":         ScopeAdmin, // the database holds the CA key and every secret
	"POST /api/notify/test":   ScopeSettingsWrite,
	"POST /api/import/3xui":   ScopeAdmin,
	"GET /api/update":         ScopeRead,
	"POST /api/update/check":  ScopeSettingsWrite,
	"POST /api/update/master": ScopeAdmin, // replaces the panel binary
	"POST /api/update/nodes":  ScopeAdmin, // replaces node binaries

	// observability
	"GET /api/overview":       ScopeRead,
	"GET /api/traffic/series": ScopeRead,
	"GET /api/traffic/top":    ScopeRead,
	"GET /api/events":         ScopeRead,
	"GET /api/online":         ScopeRead,

	// nodes
	"GET /api/nodes":                   ScopeRead,
	"POST /api/nodes":                  ScopeAdmin, // enrolling a machine gives it the certificate keys its inbounds use
	"PATCH /api/nodes/{id}":            ScopeNodesWrite,
	"DELETE /api/nodes/{id}":           ScopeNodesWrite,
	"POST /api/nodes/{id}/token":       ScopeAdmin,
	"GET /api/nodes/{id}/spec":         ScopeRead,
	"GET /api/nodes/{id}/conns":        ScopeRead,
	"GET /api/nodes/{id}/metrics":      ScopeRead,
	"GET /api/nodes/{id}/route":        ScopeRead,
	"PUT /api/nodes/{id}/route":        ScopeNodesWrite,
	"DELETE /api/nodes/{id}/route":     ScopeNodesWrite,
	"POST /api/nodes/{id}/conns/close": ScopeUsersWrite, // like kick: drops live user connections

	// inbounds
	"GET /api/inbounds":               ScopeRead,
	"POST /api/inbounds":              ScopeNodesWrite,
	"PATCH /api/inbounds/{id}":        ScopeNodesWrite,
	"DELETE /api/inbounds/{id}":       ScopeNodesWrite,
	"POST /api/tools/reality-keypair": ScopeNodesWrite,
	"GET /api/tools/dns-check":        ScopeRead,

	// groups / hosts / templates
	"GET /api/groups":                ScopeRead,
	"POST /api/groups":               ScopeNodesWrite,
	"PATCH /api/groups/{id}":         ScopeNodesWrite,
	"DELETE /api/groups/{id}":        ScopeNodesWrite,
	"GET /api/hosts":                 ScopeRead,
	"POST /api/hosts":                ScopeNodesWrite,
	"PUT /api/hosts/{id}":            ScopeNodesWrite,
	"DELETE /api/hosts/{id}":         ScopeNodesWrite,
	"GET /api/templates":             ScopeRead,
	"POST /api/templates":            ScopeNodesWrite,
	"PATCH /api/templates/{id}":      ScopeNodesWrite,
	"POST /api/templates/{id}/apply": ScopeNodesWrite,
	"DELETE /api/templates/{id}":     ScopeNodesWrite,

	// routing profiles
	"GET /api/routing/catalog":                              ScopeRead,
	"GET /api/routing/nodes":                                ScopeRead,
	"GET /api/routing/profiles":                             ScopeRead,
	"POST /api/routing/profiles":                            ScopeNodesWrite,
	"GET /api/routing/profiles/{id}":                        ScopeRead,
	"PATCH /api/routing/profiles/{id}":                      ScopeNodesWrite,
	"DELETE /api/routing/profiles/{id}":                     ScopeNodesWrite,
	"PUT /api/routing/profiles/{id}/draft":                  ScopeNodesWrite,
	"DELETE /api/routing/profiles/{id}/draft":               ScopeNodesWrite,
	"POST /api/routing/profiles/{id}/preview":               ScopeRead, // renders a draft, writes nothing
	"POST /api/routing/profiles/{id}/publish":               ScopeNodesWrite,
	"GET /api/routing/profiles/{id}/history":                ScopeRead,
	"POST /api/routing/profiles/{id}/history/{hid}/restore": ScopeNodesWrite,

	// users
	"GET /api/users":                    ScopeRead,
	"POST /api/users":                   ScopeUsersWrite,
	"POST /api/users/bulk":              ScopeUsersWrite,
	"POST /api/users/batch":             ScopeUsersWrite,
	"GET /api/users/{id}":               ScopeRead,
	"GET /api/users/{id}/sub":           ScopeRead,
	"PATCH /api/users/{id}":             ScopeUsersWrite,
	"DELETE /api/users/{id}":            ScopeUsersWrite,
	"POST /api/users/{id}/kick":         ScopeUsersWrite,
	"PUT /api/users/{id}/limits/{rule}": ScopeUsersWrite,
	"GET /api/limits":                   ScopeRead,
	"POST /api/limits":                  ScopeUsersWrite,
	"PATCH /api/limits/{id}":            ScopeUsersWrite,
	"GET /api/limits/{id}/users":        ScopeRead,
	"DELETE /api/limits/{id}":           ScopeUsersWrite,

	// subscription-page themes
	"GET /api/sub-themes":               ScopeRead,
	"POST /api/sub-themes":              ScopeSettingsWrite,
	"PUT /api/sub-themes/active":        ScopeSettingsWrite,
	"PUT /api/sub-themes/{id}/settings": ScopeSettingsWrite,
	"DELETE /api/sub-themes/{id}":       ScopeSettingsWrite,
	"GET /api/sub-themes/{id}/preview":  ScopeRead,
}

// needsScope reports whether a pattern must have a routeScopes entry.
func needsScope(pattern string) bool {
	p := pattern
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = strings.TrimSpace(p[i+1:])
	}
	return strings.HasPrefix(p, "/api/") || p == "/api" || p == "/mcp" || strings.HasPrefix(p, "/mcp/")
}

// scopedMux is a ServeMux that wraps every /api and /mcp handler with guard(scope).
type scopedMux struct {
	*http.ServeMux
	m        *Master
	patterns []string
}

func (m *Master) newScopedMux() *scopedMux {
	s := &scopedMux{ServeMux: http.NewServeMux(), m: m}
	m.keyState().mux = s // tests inspect the registered patterns
	return s
}

func (s *scopedMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	s.Handle(pattern, http.HandlerFunc(h))
}

func (s *scopedMux) Handle(pattern string, h http.Handler) {
	if needsScope(pattern) {
		scope, ok := routeScopes[pattern]
		if !ok {
			// fail closed: an unmapped route is admin-only (TestRouteScopesComplete flags it)
			scope = ScopeAdmin
			s.m.log.Printf("route %q has no entry in routeScopes (api_scopes.go); requiring admin", pattern)
		}
		h = s.m.guard(scope, h.ServeHTTP)
	}
	s.patterns = append(s.patterns, pattern)
	s.ServeMux.Handle(pattern, h)
}
