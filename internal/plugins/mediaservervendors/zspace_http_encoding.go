package mediaservervendors

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	jinjaexec "light-media/internal/jinja/exec"
	"light-media/internal/plugins"
)

// Python json.dumps(data) has ensure_ascii=True and default separators. Reuse
// the existing ordered/codepoint object reader and numeric formatter, rather
// than round-tripping credentials through a Go map or lossy Unicode strings.
func zspaceHTTPJSON(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if !json.Valid(raw) {
		return "", &EmbyJellyfinBoundaryError{Kind: "invalid owned request JSON"}
	}
	switch raw[0] {
	case '"':
		points, err := plugins.DecodeSystemConfigJSONString(raw)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteByte('"')
		var appendPoint func(rune)
		appendPoint = func(r rune) {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 32 || r >= 127 {
					if r > 0xffff {
						hi, lo := utf16.EncodeRune(r)
						appendPoint(hi)
						appendPoint(lo)
						return
					}
					hex := strconv.FormatInt(int64(r), 16)
					b.WriteString(`\u` + strings.Repeat("0", 4-len(hex)) + hex)
				} else {
					b.WriteRune(r)
				}
			}
		}
		for _, r := range points {
			appendPoint(r)
		}
		b.WriteByte('"')
		return b.String(), nil
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return "", err
		}
		parts := make([]string, len(values))
		for i, v := range values {
			text, err := zspaceHTTPJSON(v)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case '{':
		object, err := ejObject(raw)
		if err != nil {
			return "", err
		}
		parts := make([]string, 0, len(object.keys))
		for _, key := range object.keys {
			k, err := zspaceHTTPJSON(key)
			if err != nil {
				return "", err
			}
			v, err := zspaceHTTPJSON(object.values[string(key)])
			if err != nil {
				return "", err
			}
			parts = append(parts, k+": "+v)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case 'n', 't', 'f':
		return string(raw), nil
	default:
		return jinjaexec.PythonDumps(json.Number(raw), jinjaexec.JSONOptions{EnsureASCII: true})
	}
}
func zspaceHTTPBody(fields []ZSpaceField) ([]byte, error) {
	if fields == nil {
		return nil, nil
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		key, err := zspaceHTTPJSON(zspaceString(field.Name))
		if err != nil {
			return nil, err
		}
		value, err := zspaceHTTPJSON(field.Value)
		if err != nil {
			return nil, err
		}
		parts = append(parts, key+": "+value)
	}
	return []byte("{" + strings.Join(parts, ", ") + "}"), nil
}
func zspaceHTTPParams(fields []ZSpaceField) (string, error) {
	parts := []string{}
	for _, field := range fields {
		values := []json.RawMessage{field.Value}
		raw := bytes.TrimSpace(field.Value)
		if len(raw) > 0 && (raw[0] == '[' || raw[0] == '{') {
			var err error
			values, err = ejIter(raw)
			if err != nil {
				return "", err
			}
		}
		for _, value := range values {
			if ejNull(value) {
				continue
			}
			text, err := ejPythonText(value, false)
			if err != nil {
				return "", err
			}
			parts = append(parts, url.QueryEscape(field.Name)+"="+url.QueryEscape(text))
		}
	}
	return strings.Join(parts, "&"), nil
}
func zspaceHTTPHeaders(fields []ZSpaceField) (http.Header, error) {
	// Fixed Requests 2.34.2 / urllib3 environment: no importable Brotli or Zstd
	// backend. This is not a guessed browser UA or Go's implicit gzip policy.
	headers := http.Header{"User-Agent": {"python-requests/2.34.2"}, "Accept-Encoding": {"gzip, deflate"}, "Accept": {"*/*"}, "Connection": {"keep-alive"}}
	for _, field := range fields {
		if ejNull(field.Value) {
			headers.Del(field.Name)
			continue
		}
		points, err := plugins.DecodeSystemConfigJSONString(field.Value)
		if err != nil {
			return nil, ejSource("InvalidHeader")
		}
		if len(points) > 0 && unicode.IsSpace(points[0]) {
			return nil, ejSource("InvalidHeader")
		}
		value := make([]byte, 0, len(points))
		for _, point := range points {
			if point == '\r' || point == '\n' {
				return nil, ejSource("InvalidHeader")
			}
			if point > 255 {
				return nil, ejSource("UnicodeEncodeError")
			}
			value = append(value, byte(point))
		}
		headers.Set(field.Name, string(value))
	}
	return headers, nil
}

// Decode only the fixed installed urllib3 default coding set. Unsupported
// encodings are left untouched, as that source does without an optional codec.
// There is no new total response-size quota.
func zspaceHTTPContent(body []byte, encoding string) ([]byte, error) {
	encoding = strings.ToLower(encoding)
	modes := strings.Split(encoding, ",")
	known := func(s string) bool { return s == "gzip" || s == "x-gzip" || s == "deflate" }
	hasKnown := false
	for _, m := range modes {
		if known(strings.TrimSpace(m)) {
			hasKnown = true
		}
	}
	if !hasKnown {
		return body, nil
	}
	for i := len(modes) - 1; i >= 0; i-- {
		mode := strings.TrimSpace(modes[i])
		var reader io.ReadCloser
		var err error
		if mode == "gzip" || mode == "x-gzip" {
			reader, err = gzip.NewReader(bytes.NewReader(body))
		} else {
			reader, err = zlib.NewReader(bytes.NewReader(body))
			if err != nil {
				reader = flate.NewReader(bytes.NewReader(body))
				err = nil
			}
		}
		if err != nil {
			return nil, ejSource("ContentDecodingError")
		}
		next, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			return nil, ejSource("ContentDecodingError")
		}
		body = next
	}
	return body, nil
}
