package iyuuautoseed

import (
	"encoding/json"
	"errors"
	"maps"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"light-media/builtinplugins/plugins/autosignin/pyvalue"
)

var configKeys = []string{"enabled", "skipverify", "onlyonce", "clearcache", "cron", "token", "downloaders", "auto_downloader", "sites", "notify", "nolabels", "nopaths", "labelsafterseed", "categoryafterseed", "addhosttotag", "auto_category", "auto_start", "reuse_save_path", "size", "success_caches", "error_caches", "permanent_error_caches"}

// Config retains JSON presence/value semantics until the source consumes a
// particular field. A configuration is host-owned secret material; it must not
// be returned through an unredacted public API or written to ordinary logs.
type Config struct {
	values  map[string]any
	skipRaw json.RawMessage
}

func NewConfig() Config { return classConfig() }

// RestoreConfig restores a prior native checkpoint without rerunning source
// init_plugin normalization or replaying imported one-shot flags.
func RestoreConfig(snapshot map[string]any) (Config, error) {
	m, e := cloneMap(snapshot)
	if e != nil {
		return Config{}, e
	}
	for _, k := range []string{"success_caches", "error_caches", "permanent_error_caches", "sites"} {
		if _, ok := m[k].([]any); !ok {
			return Config{}, errors.New("invalid IYUU config checkpoint")
		}
	}
	return Config{values: m}, nil
}

func classConfig() Config {
	m := make(map[string]any)
	for _, k := range configKeys {
		m[k] = nil
	}
	for _, k := range []string{"enabled", "skipverify", "onlyonce", "clearcache", "notify", "addhosttotag", "auto_category", "auto_start"} {
		m[k] = false
	}
	m["downloaders"] = []any{}
	m["sites"] = []any{}
	for _, k := range []string{"success_caches", "error_caches", "permanent_error_caches"} {
		m[k] = []any{}
	}
	return Config{values: m}
}
func (c Config) State() bool {
	return truth(c.values["enabled"]) && truth(c.values["cron"]) && truth(c.values["token"]) && truth(c.values["downloaders"])
}
func (c Config) Snapshot() (map[string]any, error) {
	v, e := cloneJSON(c.values)
	if e != nil {
		return nil, e
	}
	return v.(map[string]any), nil
}
func withRawSkip(values map[string]any, raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return values
	}
	out := maps.Clone(values)
	out["skipverify"] = raw
	return out
}

func (c Config) String(key string) (string, error) {
	v := c.values[key]
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", sourceFault("AttributeError", key+".split")
	}
	return s, nil
}

type Job struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Cron string `json:"cron,omitempty"`
}

// PrepareInit applies the part of init_plugin before the old scheduler stops.
// Inventory is read only after the source's size and cache conversions. An
// empty input resets the caches and produces no initial configuration write.
func (c *Config) PrepareInit(input map[string]any, siteIDs func() ([]any, error)) (map[string]any, error) {
	if c.values == nil {
		*c = classConfig()
	}
	cacheKeys := []string{"permanent_error_caches", "error_caches", "success_caches"}
	for _, k := range cacheKeys {
		c.values[k] = []any{}
	}
	if len(input) == 0 {
		return nil, nil
	}
	for _, k := range configKeys {
		switch k {
		case "size", "clearcache", "permanent_error_caches", "error_caches", "success_caches":
		default:
			c.values[k] = input[k]
		}
	}
	c.values["sites"] = or(input["sites"], []any{})
	c.values["labelsafterseed"] = or(input["labelsafterseed"], "已整理,辅种")
	if _, ok := input["reuse_save_path"]; !ok {
		c.values["reuse_save_path"] = true
	}
	size, err := pythonFloat(input["size"])
	if err != nil {
		return nil, err
	}
	c.values["size"], c.values["clearcache"] = size, input["clearcache"]
	if !truth(c.values["clearcache"]) {
		for _, k := range cacheKeys {
			values, err := sourceList(or(input[k], []any{}))
			if err != nil {
				return nil, err
			}
			c.values[k] = values
		}
	}
	for _, k := range cacheKeys {
		values, err := TrimCache(c.values[k].([]any), 10000)
		if err != nil {
			return nil, err
		}
		c.values[k] = values
	}
	allSiteIDs, err := siteIDs()
	if err != nil {
		return nil, err
	}
	filtered := []any{}
	for _, id := range allSiteIDs {
		member, err := sourceContains(c.values["sites"], id)
		if err != nil {
			return nil, err
		}
		if member {
			filtered = append(filtered, id)
		}
	}
	c.values["sites"] = filtered
	return c.Snapshot()
}

func (c Config) OnlyOnce() bool       { return truth(c.values["onlyonce"]) }
func (c Config) ShouldSchedule() bool { return c.State() || c.OnlyOnce() }
func (c *Config) ConsumeOnlyOnce() bool {
	if !c.OnlyOnce() {
		return false
	}
	c.values["onlyonce"] = false
	return true
}
func (c *Config) ClearCacheFlag() {
	if truth(c.values["clearcache"]) {
		c.values["clearcache"] = false
	}
}

func (c Config) Services() ([]Job, error) {
	if !c.State() {
		return []Job{}, nil
	}
	cron, ok := c.values["cron"].(string)
	if !ok {
		return nil, sourceFault("AttributeError", "cron.split")
	}
	return []Job{{ID: "IYUUAutoSeed", Name: "IYUU自动辅种服务", Kind: "cron", Cron: cron}}, nil
}

// SiteDeleted reads the selected sites from the saved config and leaves the
// rest of the live plugin state intact for the source's full config write.
func (c *Config) SiteDeleted(savedConfig map[string]any, siteID any) (bool, error) {
	sites := savedConfig["sites"]
	if !truth(sites) {
		return false, nil
	}
	kept := []any{}
	if truth(siteID) {
		var values []any
		if s, ok := sites.(string); ok {
			values = []any{s}
		} else {
			var err error
			values, err = sourceList(sites)
			if err != nil {
				return false, err
			}
		}
		for _, v := range values {
			n, e := sourceInt(v)
			if e != nil {
				return false, e
			}
			target, e := sourceInt(siteID)
			if e != nil {
				return false, e
			}
			if n != target {
				kept = append(kept, v)
			}
		}
	}
	if len(kept) == 0 {
		c.values["enabled"] = false
	}
	c.values["sites"] = kept
	return true, nil
}
func or(v, fallback any) any {
	if truth(v) {
		return v
	}
	return fallback
}
func sourceList(v any) ([]any, error) {
	switch x := v.(type) {
	case []any:
		return append([]any{}, x...), nil
	case string:
		out := []any{}
		for _, r := range x {
			out = append(out, string(r))
		}
		return out, nil
	default:
		return nil, sourceFault("TypeError", "list")
	}
}
func sourceInt(v any) (string, error) {
	switch x := v.(type) {
	case bool:
		if x {
			return "1", nil
		}
		return "0", nil
	case json.Number:
		r, ok := numberRat(x)
		if !ok {
			return "", sourceFault("ValueError", "int")
		}
		return new(big.Int).Quo(r.Num(), r.Denom()).String(), nil
	case string:
		z, e := pyvalue.Integer(x)
		if e != nil {
			return "", sourceFault("ValueError", "int")
		}
		return z.String(), nil
	default:
		return "", sourceFault("TypeError", "int")
	}
}
func pythonFloat(v any) (json.Number, error) {
	if !truth(v) {
		return json.Number("0"), nil
	}
	var s string
	switch x := v.(type) {
	case string:
		s = strings.Map(func(r rune) rune {
			if r > unicode.MaxASCII && unicode.IsDigit(r) {
				if digit, err := pyvalue.Integer(string(r)); err == nil {
					return rune('0' + digit.Int64())
				}
			}
			return r
		}, x)
		if strings.ContainsAny(s, "xX") {
			return "", sourceFault("ValueError", "float")
		}
	case json.Number:
		s = x.String()
	case bool:
		if x {
			return json.Number("1"), nil
		}
		return json.Number("0"), nil
	default:
		return "", sourceFault("TypeError", "float")
	}
	n, e := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if e != nil {
		return "", sourceFault("ValueError", "float")
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return "", errors.New("unsupported non-finite source size")
	}
	return json.Number(strconv.FormatFloat(n, 'g', -1, 64)), nil
}
func scalarEqual(a, b any) bool {
	ka, ea := scalarKey(a)
	kb, eb := scalarKey(b)
	return ea == nil && eb == nil && ka == kb
}
func containsScalar(values []any, value any) bool {
	for _, v := range values {
		if scalarEqual(v, value) {
			return true
		}
	}
	return false
}
func sourceContains(container, value any) (bool, error) {
	switch x := container.(type) {
	case []any:
		return containsScalar(x, value), nil
	case string:
		s, ok := value.(string)
		if !ok {
			return false, sourceFault("TypeError", "str.__contains__")
		}
		return strings.Contains(x, s), nil
	case map[string]any:
		s, ok := value.(string)
		if !ok {
			return false, nil
		}
		_, ok = x[s]
		return ok, nil
	default:
		return false, sourceFault("TypeError", "container.__contains__")
	}
}
func TrimCache(cache []any, max int) ([]any, error) {
	seen := make(map[string]bool)
	out := []any{}
	for i := len(cache) - 1; i >= 0; i-- {
		if !truth(cache[i]) {
			continue
		}
		key, e := scalarKey(cache[i])
		if e != nil {
			return nil, sourceFault("TypeError", "cache.set")
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cache[i])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if max < 0 {
		return nil, errors.New("invalid cache limit")
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out, nil
}
func AppendCache(cache []any, value any) ([]any, error) {
	if !truth(value) {
		return cache, nil
	}
	if !containsScalar(cache, value) {
		cache = append(cache, value)
	}
	return TrimCache(cache, 10000)
}
