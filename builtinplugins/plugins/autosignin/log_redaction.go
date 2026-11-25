package autosignin

import (
	"bytes"
	"encoding/json"
	stdhtml "html"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

// The source logs generated CSRF/sign-in tokens and sometimes full responses.
// The user's secret boundary requires value redaction in the runtime logger.
// Pure source comparisons keep the original message and call positions.
var diagnosticSecretPair = regexp.MustCompile(`(?i)((?:"|')?(?:[a-z0-9_-]*token|x-csrf(?:-token)?|csrf(?:_token)?|api[_-]?key|cookie|set-cookie|authorization|password|passkey)(?:"|')?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;<>}]+)`)

const sourceCredentialLogClosed = "[REDACTED: source credential log limit]"

func sourceSiteLogCredentialValues(site Object) ([]string, bool) {
	secrets := []string{}
	var collect func(any, int) bool
	collect = func(value any, depth int) bool {
		if depth > 24 || len(secrets) > 2048 {
			return false
		}
		switch x := value.(type) {
		case string:
			secrets = append(secrets, x)
		case Object:
			for _, v := range x {
				if !collect(v, depth+1) {
					return false
				}
			}
		case []any:
			for _, v := range x {
				if !collect(v, depth+1) {
					return false
				}
			}
		}
		return true
	}
	for _, key := range []string{"cookie", "ua", "apikey", "api_key", "token", "password", "passkey", "authorization", "rss"} {
		value := site[key]
		if !collect(value, 0) {
			return nil, false
		}
		if Truth(value) {
			secrets = append(secrets, authString(value))
		}
		if key == "cookie" {
			if raw, ok := value.(string); ok {
				for _, part := range strings.Split(raw, ";") {
					if _, v, ok := strings.Cut(part, "="); ok {
						secrets = append(secrets, strings.TrimFunc(v, sourcePythonSpace), sourceDiagnosticCookieValue(strings.TrimFunc(v, sourcePythonSpace)))
					}
				}
			}
		}
	}

	values := []string{}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if len(secret) > 2048 {
			return nil, false
		}
		encoded, _ := json.Marshal(secret)
		values = append(values, secret, stdhtml.EscapeString(secret), string(encoded[1:len(encoded)-1]), strings.ToValidUTF8(secret, "\ufffd"), string([]rune(secret)))
	}
	return values, true
}

func redactSiteDiagnostic(message string, site Object) string {
	values, ok := sourceSiteLogCredentialValues(site)
	if !ok {
		return sourceCredentialLogClosed
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := []string{}
	for _, value := range values {
		if value != "" {
			pairs = append(pairs, value, "[REDACTED]")
		}
	}

	if start := strings.Index(message, "获取到x-csrf "); start >= 0 {
		message = message[:start] + "获取到x-csrf [REDACTED]"
	}
	// Preserve surrounding text rather than serializing or dropping a response.
	if strings.Contains(message, "<") {
		if doc, err := html.Parse(strings.NewReader(message)); err == nil {
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				name, value := "", ""
				for _, a := range n.Attr {
					if a.Key == "name" || a.Key == "id" {
						name = a.Val
					}
					if a.Key == "value" || a.Key == "content" {
						value = a.Val
					}
				}
				lowered := strings.ToLower(name)
				if value != "" && (strings.Contains(lowered, "token") || strings.Contains(lowered, "csrf") || strings.Contains(lowered, "passkey") || strings.Contains(lowered, "password") || strings.Contains(lowered, "apikey") || strings.Contains(lowered, "api-key")) {
					message = strings.ReplaceAll(message, value, "[REDACTED]")
					message = strings.ReplaceAll(message, stdhtml.EscapeString(value), "[REDACTED]")
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)
		}
	}
	message = diagnosticSecretPair.ReplaceAllString(message, "${1}[REDACTED]")
	if len(pairs) > 0 {
		message = strings.NewReplacer(pairs...).Replace(message)
	}
	return message
}

// RequestUtils.cookie_parse decodes only when every resulting byte is ASCII.
// It leaves '+' literal and keeps a non-ASCII original percent value unchanged.
func sourceDiagnosticCookieValue(value string) string {
	decoded := []byte{}
	for pos := 0; pos < len(value); pos++ {
		b := value[pos]
		if b == '%' && pos+2 < len(value) {
			if n, e := strconv.ParseUint(value[pos+1:pos+3], 16, 8); e == nil {
				b = byte(n)
				pos += 2
			}
		}
		if b > 127 {
			return value
		}
		decoded = append(decoded, b)
	}
	return string(decoded)
}

// One in-memory owner per Plugin, shared by its callback frames. Intake occurs
// at successful source getter returns; log never reads configuration or storage.
// Retain old credentials until this Plugin dies, protecting in-flight frames.
type sourceCredentialLogSecrets struct {
	mu       sync.RWMutex
	values   map[string]struct{}
	bytes    int
	closed   bool
	replacer *strings.Replacer
}

func (s *sourceCredentialLogSecrets) failClosed() {
	s.mu.Lock()
	s.closed = true
	s.values, s.replacer, s.bytes = nil, nil, 0
	s.mu.Unlock()
}
func (s *sourceCredentialLogSecrets) remember(values []string) {
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
		if len(value) > 2048 || len(s.values) >= 2048 || s.bytes+len(value) > 256<<10 {
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
	values = make([]string, 0, len(s.values))
	for value := range s.values {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) == len(values[j]) {
			return values[i] < values[j]
		}
		return len(values[i]) > len(values[j])
	})
	pairs := make([]string, 0, len(values)*2)
	for _, value := range values {
		pairs = append(pairs, value, "[REDACTED]")
	}
	s.replacer = strings.NewReplacer(pairs...)
}
func (s *sourceCredentialLogSecrets) redact(message string) string {
	s.mu.RLock()
	closed, replacer := s.closed, s.replacer
	s.mu.RUnlock()
	if closed {
		return sourceCredentialLogClosed
	}
	if replacer != nil {
		return replacer.Replace(message)
	}
	return message
}
func sourceCredentialLogJSONBounded(raw json.RawMessage) bool {
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
func (s *sourceCredentialLogSecrets) registerCustom(raw json.RawMessage) { s.registerRaw(raw, true) }
func (s *sourceCredentialLogSecrets) registerFields(raw json.RawMessage) { s.registerRaw(raw, false) }
func (s *sourceCredentialLogSecrets) registerRaw(raw json.RawMessage, custom bool) {
	// Privacy intake cannot change the source method's result or exception class.
	// A malformed/oversized private value only closes this owner's future logs.
	defer func() {
		if recover() != nil {
			s.failClosed()
		}
	}()
	if !sourceCredentialLogJSONBounded(raw) {
		s.failClosed()
		return
	}
	value, err := sourceRestoreSavedJSON(raw)
	if err != nil {
		s.failClosed()
		return
	}
	if !Truth(value) {
		return
	}
	fields, ok := value.(Object)
	if !ok {
		s.failClosed()
		return
	}
	rows := []any{fields}
	if custom {
		if !Truth(fields["sites"]) {
			return
		}
		rows, ok = fields["sites"].([]any)
		if !ok {
			s.failClosed()
			return
		}
	}
	values := []string{}
	for _, row := range rows {
		fields, ok := row.(Object)
		if !ok {
			s.failClosed()
			return
		}
		private, ok := sourceSiteLogCredentialValues(fields)
		if !ok {
			s.failClosed()
			return
		}
		values = append(values, private...)
	}
	s.remember(values)
}
