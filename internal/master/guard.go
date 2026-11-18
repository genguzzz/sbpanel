package master

// Guards on the sing-box JSON that admins (or API keys / agents with nodes:write) send to
// nodes. sbnode runs as root and hands templates, node rules and inbound options to
// sing-box, so any option that makes sing-box write files, download and unpack archives,
// or read arbitrary paths would turn "can edit routing" into "root on every node".

import (
	"fmt"
	"path"
	"strings"
)

// noFileKey reports option names that point sing-box at the filesystem: *_path,
// *_directory, ACME (writes its data directory) and the Clash dashboard download.
func noFileKey(k string) bool {
	switch k {
	case "acme", "external_ui", "external_ui_download_url", "external_ui_download_detour":
		return true
	}
	return strings.HasSuffix(k, "_path") || strings.HasSuffix(k, "_directory")
}

// walkJSON calls fn for every object key (with its dotted path) in a decoded JSON value.
func walkJSON(v any, at string, fn func(path, key string, val any) error) error {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			p := k
			if at != "" {
				p = at + "." + k
			}
			if err := fn(p, k, val); err != nil {
				return err
			}
			if err := walkJSON(val, p, fn); err != nil {
				return err
			}
		}
	case []any:
		for i, val := range x {
			if err := walkJSON(val, fmt.Sprintf("%s[%d]", at, i), fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkSkeletonFiles rejects template / node-route options that touch the node's files.
func checkSkeletonFiles(v map[string]any) error {
	if log := obj(v, "log"); log != nil && str(log, "output") != "" {
		return fmt.Errorf("%s is not allowed: it makes the node write files", "log.output")
	}
	if exp := obj(v, "experimental"); exp != nil {
		if cf := obj(exp, "cache_file"); cf != nil && str(cf, "path") != "" {
			return fmt.Errorf("%s is not allowed: it makes the node write files", "experimental.cache_file.path")
		}
	}
	if route := obj(v, "route"); route != nil {
		if rs, ok := route["rule_set"].([]any); ok {
			for _, x := range rs {
				m, _ := x.(map[string]any)
				if str(m, "type") == "local" || str(m, "path") != "" {
					return fmt.Errorf("%s is not allowed: use remote or inline rule sets", "route.rule_set (local file)")
				}
			}
		}
	}
	if dns := obj(v, "dns"); dns != nil {
		if ss, ok := dns["servers"].([]any); ok {
			for _, x := range ss {
				m, _ := x.(map[string]any)
				if _, ok := m["path"]; ok {
					return fmt.Errorf("%s is not allowed: it reads files on the node", "dns.servers[].path")
				}
			}
		}
	}
	return walkJSON(v, "", func(p, k string, _ any) error {
		if noFileKey(k) {
			return fmt.Errorf("%s is not allowed: it makes the node read or write files", p)
		}
		return nil
	})
}

// badNodePath rejects file paths a node must never be pointed at: device and kernel
// files (reading /dev/zero would exhaust the agent's memory) and relative paths.
func badNodePath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\n\r") || strings.Contains(p, "/../") {
		return true
	}
	// "//dev/zero" and "/./proc/self/mem" name the same files as their clean forms
	c := path.Clean(p) + "/"
	for _, pre := range []string{"/dev/", "/proc/", "/sys/", "/run/"} {
		if strings.HasPrefix(c, pre) {
			return true
		}
	}
	return false
}

// validNodeFilePath accepts a clean absolute path to a certificate / key file on a node.
func validNodeFilePath(p string) bool {
	return path.IsAbs(p) && path.Clean(p) == p && !badNodePath(p)
}

// checkInboundFiles rejects inbound options that make sing-box write files (ACME, data
// directories) or read device files; certificate_path / key_path stay allowed for real
// certificate files on the node.
func checkInboundFiles(o map[string]any) error {
	return walkJSON(o, "", func(p, k string, val any) error {
		switch {
		case k == "acme" || strings.HasSuffix(k, "_directory") || strings.HasPrefix(k, "external_ui"):
			return fmt.Errorf("%s is not allowed: it makes the node write files", p)
		case strings.HasSuffix(k, "_path"):
			if s, _ := val.(string); s != "" && badNodePath(s) {
				return fmt.Errorf("%s must be an absolute path to a regular file (not /dev, /proc, /sys, /run)", p)
			}
		}
		return nil
	})
}
