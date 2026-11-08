package app

import (
	"bytes"
	"encoding/json"
	"html"
	"io"
	"sort"
	"strings"
	"sync"

	"light-media/internal/plugins"
)

const originalSourceLogRedacted = "[REDACTED]"
const originalSourceLogClosed = "[REDACTED: source credential log limit]"
const originalSourceLogSecretCount = 2048
const originalSourceLogSecretBytes = 256 << 10

// Secrets live only with the exact loaded object. Keep old values across init
// so an in-flight old context cannot disclose credentials after a config save.
// Collection never reads Settings, BaseConfig, or a database in a log callback.
type originalSourceLogSecrets struct {
	mu       sync.RWMutex
	values   map[string]struct{}
	bytes    int
	closed   bool
	replacer *strings.Replacer
}

func (s *originalSourceLogSecrets) failClosed() {
	s.mu.Lock()
	s.closed = true
	s.values, s.replacer, s.bytes = nil, nil, 0
	s.mu.Unlock()
}

func (s *originalSourceLogSecrets) remember(values []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.values == nil {
		s.values = map[string]struct{}{}
	}
	changed := false
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := s.values[value]; ok {
			continue
		}
		// A whole SDK log is shorter than a longer credential. Suppress this
		// object's logs rather than let a split credential escape value matching.
		if len(value) > plugins.LogMessageLimit || len(s.values) >= originalSourceLogSecretCount || s.bytes+len(value) > originalSourceLogSecretBytes {
			s.closed = true
			s.values, s.replacer, s.bytes = nil, nil, 0
			return
		}
		s.values[value] = struct{}{}
		s.bytes += len(value)
		changed = true
	}
	if !changed {
		return
	}
	ordered := make([]string, 0, len(s.values))
	for value := range s.values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) == len(ordered[j]) {
			return ordered[i] < ordered[j]
		}
		return len(ordered[i]) > len(ordered[j])
	})
	pairs := make([]string, 0, len(ordered)*2)
	for _, value := range ordered {
		pairs = append(pairs, value, originalSourceLogRedacted)
	}
	s.replacer = strings.NewReplacer(pairs...)
}

func (s *originalSourceLogSecrets) redact(message string) string {
	s.mu.RLock()
	closed, replacer := s.closed, s.replacer
	s.mu.RUnlock()
	if closed {
		return originalSourceLogClosed
	}
	if replacer != nil {
		return replacer.Replace(message)
	}
	return message
}

// Check nesting before using the source Python repr helper. Resource results
// retain their original shape even when logging must fail closed.
func originalSourceLogJSONBounded(raw json.RawMessage) bool {
	if len(raw) > 1<<20 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	depth, nodes := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return depth == 0
		}
		if err != nil {
			return false
		}
		nodes++
		if nodes > 16384 {
			return false
		}
		if delim, ok := token.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				depth++
			} else {
				depth--
			}
			if depth > 24 || depth < 0 {
				return false
			}
		}
	}
}

func originalSourceLogVariants(value string) []string {
	if value == "" {
		return nil
	}
	values := []string{value, sourceSiteTrim(value), html.EscapeString(value), strings.ToValidUTF8(value, "\ufffd"), string([]rune(value))}
	encoded, _ := json.Marshal(value)
	if len(encoded) >= 2 {
		values = append(values, string(encoded[1:len(encoded)-1]))
	}
	return values
}

func (s *originalSourceLogSecrets) registerCustomConfig(raw json.RawMessage) {
	if !originalSourceLogJSONBounded(raw) {
		s.failClosed()
		return
	}
	var config struct {
		Sites []json.RawMessage `json:"sites"`
	}
	if json.Unmarshal(raw, &config) != nil {
		s.failClosed()
		return
	}
	for _, site := range config.Sites {
		s.registerFields(site)
	}
}

func (s *originalSourceLogSecrets) registerFields(raw json.RawMessage) {
	if !originalSourceLogJSONBounded(raw) {
		s.failClosed()
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		s.failClosed()
		return
	}
	values := []string{}
	for _, key := range []string{"cookie", "ua", "apikey", "api_key", "token", "password", "passkey", "authorization", "rss"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		token, err := sourceSiteParseToken(raw)
		if err != nil {
			s.failClosed()
			return
		}
		effective, err := originalSourceSiteMTeamToken(raw) // source str(value or '').strip()
		if err != nil {
			s.failClosed()
			return
		}
		values = append(values, originalSourceLogVariants(effective)...)
		var visit func(sourceSiteTokenValue)
		visit = func(v sourceSiteTokenValue) {
			if v.kind == '"' {
				values = append(values, originalSourceLogVariants(sourceSiteCodepointString(v.text))...)
			}
			for _, item := range v.items {
				visit(item)
			}
			for _, pair := range v.pairs {
				visit(pair.value)
			}
		}
		visit(token)
		if effective != "" {
			values = append(values, string(raw))
			if repr, err := token.repr(); err == nil {
				values = append(values, repr)
			}
		}
		if key == "cookie" && token.kind == '"' {
			cookie := sourceSiteCodepointString(token.text)
			for _, part := range strings.Split(cookie, ";") {
				if _, value, ok := strings.Cut(part, "="); ok {
					values = append(values, originalSourceLogVariants(value)...)
				}
			}
			header := originalOCRCookieHeader(cookie)
			values = append(values, originalSourceLogVariants(header)...)
			for _, part := range strings.Split(header, ";") {
				if _, value, ok := strings.Cut(part, "="); ok {
					values = append(values, originalSourceLogVariants(value)...)
				}
			}
		}
	}
	s.remember(values)
}
