// Package sourceindexer implements the reviewed MoviePilot indexer registry.
// It operates on admitted JSON projections; templates and rows can contain
// credentials and are host-only, never public API or log values.
package sourceindexer

import (
	"encoding/json"
	"strings"
	"sync"
)

type Registry struct {
	mu       sync.Mutex
	entries  map[string]map[string]any
	order    []string
	globalUA string
}

// Values originate in reviewed JSON. Copy maps/lists recursively without a
// JSON round trip so integer and nullable source values keep their types.
func cloneValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		return clone(v)
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = cloneValue(x)
		}
		return out
	case []string:
		return append([]string{}, v...)
	default:
		return v
	}
}
func clone(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}
func netloc(value string) (string, string) {
	if value == "" {
		return "", ""
	}
	if !strings.HasPrefix(value, "http") {
		return "http", value
	}
	// Python urlparse exposes the literal netloc without validating its port
	// or decoding/re-encoding userinfo. Go net/url would reject/normalize some
	// source inputs (percent host escapes, nonnumeric ports, lowercase escapes).
	clean := strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(value)
	scheme, rest, found := strings.Cut(clean, ":")
	if !found || scheme == "" {
		return "", ""
	}
	for i, ch := range scheme {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || i > 0 && (ch >= '0' && ch <= '9' || ch == '+' || ch == '-' || ch == '.')) {
			return "", ""
		}
	}
	scheme = strings.ToLower(scheme)
	if !strings.HasPrefix(rest, "//") {
		return scheme, ""
	}
	host := rest[2:]
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	return scheme, host
}
func Domain(value string) string {
	for _, x := range []string{"u2.dmhy.org", "pt.ecust.pp.ua", "pt.gtkpw.xyz", "pt.gtk.pw"} {
		if strings.Contains(value, x) {
			return x
		}
	}
	_, v := netloc(value)
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return v
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, ".")
}
func New(templates []map[string]any, ua string) *Registry {
	r := &Registry{entries: map[string]map[string]any{}, globalUA: ua}
	for _, template := range templates {
		m := clone(template)
		domain, _ := m["domain"].(string)
		r.put(Domain(domain), m)
		switch exts := m["ext_domains"].(type) {
		case []any:
			for _, e := range exts {
				if s, ok := e.(string); ok {
					r.put(Domain(s), m)
				}
			}
		case []string:
			for _, s := range exts {
				r.put(Domain(s), m)
			}
		}
	}
	return r
}
func (r *Registry) put(key string, m map[string]any) {
	if _, ok := r.entries[key]; !ok {
		r.order = append(r.order, key)
	}
	r.entries[key] = m
}

// Add takes ownership of template. Reusing that same map in later Add calls
// intentionally shares its subsequent registry state. The caller must not read
// or mutate an admitted map directly; use Get/List snapshots instead.
func (r *Registry) Add(key string, template map[string]any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == "" || len(template) == 0 {
		return false
	}
	r.put(key, template)
	return true
}
func truth(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		mantissa := strings.Split(strings.ToLower(string(v)), "e")[0]
		return strings.Trim(mantissa, "+-.0") != ""
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case []any:
		return len(v) != 0
	case map[string]any:
		return len(v) != 0
	}
	return true
}
func (r *Registry) merge(m, db map[string]any) map[string]any {
	// Account projection: source schema/public and other template metadata remain
	// on the template. Account credentials and options come from the DB row.
	// Custom _scheme/api_url merge variants are outside the reviewed projection.
	for _, key := range []string{"id", "name", "url", "is_active", "pri", "cookie", "apikey", "token", "rss", "proxy", "render", "filter", "note", "timeout", "downloader", "limit_interval", "limit_count", "limit_seconds"} {
		m[key] = db[key]
	}
	u, _ := db["url"].(string)
	scheme, host := netloc(u)
	m["domain"] = scheme + "://" + host + "/"
	m["ua"] = db["ua"]
	if !truth(m["ua"]) {
		m["ua"] = r.globalUA
	}
	return m
}

// Get's key is intentionally raw, including the DB lookup key supplied by host.
func (r *Registry) Get(key string, db map[string]any) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.entries[key]
	if m == nil {
		return nil
	}
	if db == nil {
		return clone(m)
	}
	return r.merge(clone(m), db)
}

// List mutates shared templates as observed. Clone only after the entire pass,
// preserving repeated outputs and resulting registry state rather than exposing
// mutable maps to concurrent host callers.
func (r *Registry) List(active []map[string]any) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := []map[string]any{}
	for _, db := range active {
		key, _ := db["domain"].(string)
		if m := r.entries[key]; m != nil {
			rows = append(rows, r.merge(m, db))
		}
	}
	out := []map[string]any{}
	for _, m := range rows {
		out = append(out, clone(m))
	}
	return out
}
func (r *Registry) Sites() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []map[string]any{}
	for _, key := range r.order {
		m := r.entries[key]
		out = append(out, map[string]any{"key": key, "value": map[string]any{"name": m["name"], "url": m["domain"], "domain": nil, "ext_domains": nil, "schema": nil, "url_bak": nil}})
	}
	return out
}
