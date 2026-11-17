package subgen

import (
	"encoding/json"
	"fmt"
)

// InlineExpandMax: inline rule sets up to this many values are emitted as
// individual rules; bigger ones become inline providers / served lists.
const InlineExpandMax = 64

// Result of a render.
type Result struct {
	Client      string
	ContentType string
	Ext         string
	Body        []byte
	Warnings    []string
}

var DefaultOptions = map[string]map[string]string{
	"mihomo":  {"mixed_port": "7890", "allow_lan": "false", "log_level": "warning", "ipv6": "true", "controller": "127.0.0.1:9090"},
	"singbox": {"mixed_port": "2080", "log_level": "warn", "tun_stack": "mixed", "controller": "127.0.0.1:9090"},
	"surge":   {"update_interval": "43200", "ipv6": "true", "test_url": "http://www.gstatic.com/generate_204"},
}

// Render is the single entry point sbpanel calls from handleSub.
func Render(client string, p *Profile, eps []Endpoint, meta Meta) (*Result, error) {
	ct, ok := p.Clients[client]
	if !ok || !ct.Enabled {
		return nil, fmt.Errorf("profile %s has no %s template", p.ID, client)
	}
	c, err := Compile(p, client, eps)
	if err != nil {
		return nil, err
	}
	c.Mirror = p.Mirror
	if c.Mirror == "" {
		c.Mirror = meta.Mirror
	}
	opts := mergeOpts(DefaultOptions[client], ct.Options)
	if v, ok := opts["ipv6"]; ok && v != "true" {
		if n := c.ipv6Node(); n != "" { // see Compile: a global off switch overrides the node
			opts["ipv6"] = "true"
			c.warn("IPv6 enabled: node %s prefers IPv6", n)
		}
	}
	var vals map[string]string
	res := &Result{Client: client}
	switch client {
	case "mihomo":
		vals, res.ContentType, res.Ext = mihomoSections(c), "text/yaml; charset=utf-8", ".yaml"
	case "singbox":
		vals, res.ContentType, res.Ext = singboxSections(c), "application/json; charset=utf-8", ".json"
	case "surge":
		vals, res.ContentType, res.Ext = surgeSections(c, opts), "text/plain; charset=utf-8", ".conf"
		vals["sub_url"] = meta.SubURL
	default:
		return nil, fmt.Errorf("unknown client %s", client)
	}
	out, err := render(client, ct.Base, vals, opts)
	if err != nil {
		return nil, err
	}
	res.Body, res.Warnings = []byte(out), c.Warnings
	return res, nil
}

func jstr(v any) string { b, _ := json.Marshal(v); return string(b) }

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func orI(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}
func orS(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}
func orA(a []any) []any {
	if a == nil {
		return []any{}
	}
	return a
}
