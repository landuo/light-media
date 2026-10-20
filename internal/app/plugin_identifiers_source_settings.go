package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// These private AEAD records are written only by the trusted source migration.
// A missing source snapshot never falls back to the new process environment.
type identifiersNetworkSettings struct {
	Version       int
	Known         bool
	GitHubHeaders map[string]string
	Proxy         map[string]string
	Environment   []string
}

func identifiersDependency() error {
	return bad("IDENTIFIERS_SOURCE_DEPENDENCY", "原版识别词运行设置尚未迁移或无法原样使用")
}
func (a *App) identifiersSourceRecord(ctx context.Context, kind string, target any) error {
	var sealed []byte
	if a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='main'`, kind).Scan(&sealed) != nil {
		return identifiersDependency()
	}
	raw, err := a.pluginStore.unseal(kind, "system", "main", sealed)
	if err != nil {
		return identifiersDependency()
	}
	// Go's decoder replaces isolated JSON surrogates. Reject before replacement;
	// no request may silently use a different credential/header/endpoint.
	if identifiersSurrogate(raw) {
		return identifiersDependency()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, target) != nil {
		return identifiersDependency()
	}
	expected := []string{"Version", "Known", "GitHubHeaders", "Proxy", "Environment"}
	if kind == "source_runtime_settings" {
		expected = []string{"Version", "Known", "Timezone", "LocalTimezone"}
	}
	if len(fields) != len(expected) {
		return identifiersDependency()
	}
	for _, field := range expected {
		if _, ok := fields[field]; !ok {
			return identifiersDependency()
		}
	}
	return nil
}
func (a *App) identifiersSourceNetwork(ctx context.Context) (identifiersNetworkSettings, error) {
	var s identifiersNetworkSettings
	if err := a.identifiersSourceRecord(ctx, "source_network_settings", &s); err != nil {
		return s, err
	}
	if s.Version != 1 || !s.Known || s.GitHubHeaders == nil || s.Environment == nil {
		return s, identifiersDependency()
	}
	return s, nil
}
func (a *App) identifiersSourceTimezone(ctx context.Context, local bool) (string, error) {
	var s struct {
		Version       int
		Known         bool
		Timezone      string
		LocalTimezone string
	}
	if err := a.identifiersSourceRecord(ctx, "source_runtime_settings", &s); err != nil {
		return "", err
	}
	if s.Version != 1 || !s.Known {
		return "", identifiersDependency()
	}
	if local {
		return s.LocalTimezone, nil
	}
	return s.Timezone, nil
}
func identifiersProxyEnvironment(s identifiersNetworkSettings) ([]string, error) {
	env := append([]string{}, s.Environment...)
	if s.Proxy == nil {
		return env, nil
	}
	// settings.PROXY's actual selected derivation is None or this exact pair.
	if len(s.Proxy) != 2 {
		return nil, identifiersDependency()
	}
	if _, ok := s.Proxy["http"]; !ok {
		return nil, identifiersDependency()
	}
	if _, ok := s.Proxy["https"]; !ok {
		return nil, identifiersDependency()
	}
	out := []string{}
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			continue
		}
		out = append(out, item)
	}
	return append(out, "http_proxy="+s.Proxy["http"], "https_proxy="+s.Proxy["https"], "no_proxy="), nil
}
func identifiersHeaders(raw string, s identifiersNetworkSettings) (http.Header, []string) {
	out := http.Header{}
	if strings.Contains(raw, "github") && len(s.GitHubHeaders) > 0 {
		out.Set("User-Agent", "python-requests/2.34.2")
		for k, v := range s.GitHubHeaders {
			out.Set(k, v)
		}
		return out, nil
	}
	out.Set("User-Agent", "python-urllib3/2.7.0")
	out.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	return out, []string{"Accept"}
}

// Check JSON string escapes without decoding them through replacement runes.
func identifiersSurrogate(raw []byte) bool {
	in := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '"' {
			in = !in
			continue
		}
		if !in || c != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			break
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			continue
		}
		n := uint16(0)
		ok := true
		for _, h := range raw[i+1 : i+5] {
			n <<= 4
			switch {
			case h >= '0' && h <= '9':
				n += uint16(h - '0')
			case h >= 'a' && h <= 'f':
				n += uint16(h - 'a' + 10)
			case h >= 'A' && h <= 'F':
				n += uint16(h - 'A' + 10)
			default:
				ok = false
			}
		}
		i += 4
		if !ok {
			continue
		}
		if n >= 0xdc00 && n <= 0xdfff {
			return true
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return true
		}
		low := uint16(0)
		for _, h := range raw[i+3 : i+7] {
			low <<= 4
			switch {
			case h >= '0' && h <= '9':
				low += uint16(h - '0')
			case h >= 'a' && h <= 'f':
				low += uint16(h - 'a' + 10)
			case h >= 'A' && h <= 'F':
				low += uint16(h - 'A' + 10)
			default:
				return true
			}
		}
		if low < 0xdc00 || low > 0xdfff {
			return true
		}
		i += 6
	}
	return false
}
