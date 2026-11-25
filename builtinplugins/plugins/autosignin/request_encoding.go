package autosignin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf16"
)

func encodeSourceForm(form map[string]string, order []string) string {
	keys := []string{}
	seen := map[string]bool{}
	for _, key := range order {
		if _, ok := form[key]; ok && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	extra := []string{}
	for key := range form {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	keys = append(keys, extra...)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(form[key]))
	}
	return strings.Join(parts, "&")
}

// Compiled adapter JSON bodies contain objects of strings/integers. Match the
// actual requests json.dumps default spaces and ensure_ascii representation;
// do not turn this into an arbitrary worker-supplied serialization endpoint.
func encodeSourceJSON(value any) ([]byte, error) {
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(raw.String(), "\n")
	var b strings.Builder
	quoted, escaped := false, false
	for _, r := range text {
		if r > 127 {
			if r <= 0xffff {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				a, c := utf16.EncodeRune(r)
				fmt.Fprintf(&b, `\u%04x\u%04x`, a, c)
			}
			escaped = false
			continue
		}
		b.WriteRune(r)
		if quoted {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				quoted = false
			}
		} else {
			if r == '"' {
				quoted = true
			}
			if r == ',' || r == ':' {
				b.WriteByte(' ')
			}
		}
	}
	return []byte(b.String()), nil
}
