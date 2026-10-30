package app

import (
	"strings"
	"unicode/utf8"
)

// Starlette QueryParams decodes ASGI query bytes as Latin-1, then calls Python
// parse_qsl(keep_blank_values=True): '&' is the only separator, malformed '%'
// escapes are literal, UTF-8 errors use replacement, and the last scalar wins.
// This applies only to versioned rating plugin_ids, never token authority/global URLs.
func originalPluginServerQuery(raw, key string) (value string, present bool) {
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		name, item, _ := strings.Cut(pair, "=")
		if originalPluginServerUnquote(name) == key {
			value, present = originalPluginServerUnquote(item), true
		}
	}
	return
}

func originalPluginServerUnquote(raw string) string {
	var out strings.Builder
	// Python unquote processes ASCII runs separately from literal Latin-1
	// characters, so escaped UTF-8 bytes cannot merge across an unescaped byte.
	for len(raw) > 0 {
		if raw[0] >= utf8.RuneSelf {
			out.WriteRune(rune(raw[0]))
			raw = raw[1:]
			continue
		}
		end := 0
		for end < len(raw) && raw[end] < utf8.RuneSelf {
			end++
		}
		run := raw[:end]
		raw = raw[end:]
		decoded := make([]byte, 0, len(run))
		for i := 0; i < len(run); i++ {
			if run[i] == '+' {
				decoded = append(decoded, ' ')
			} else if run[i] == '%' && i+2 < len(run) && originalPluginServerQueryHex(run[i+1]) >= 0 && originalPluginServerQueryHex(run[i+2]) >= 0 {
				decoded = append(decoded, byte(originalPluginServerQueryHex(run[i+1])*16+originalPluginServerQueryHex(run[i+2])))
				i += 2
			} else {
				decoded = append(decoded, run[i])
			}
		}
		for len(decoded) > 0 {
			r, size := utf8.DecodeRune(decoded)
			if r == utf8.RuneError && size == 1 {
				// Python replaces the maximal valid prefix of an incomplete /
				// malformed sequence once, not each byte or all adjacent errors.
				lead, want := decoded[0], 1
				switch {
				case lead >= 0xc2 && lead <= 0xdf:
					want = 2
				case lead >= 0xe0 && lead <= 0xef:
					want = 3
				case lead >= 0xf0 && lead <= 0xf4:
					want = 4
				}
				for size < want && size < len(decoded) {
					b := decoded[size]
					if b < 0x80 || b > 0xbf || size == 1 && (lead == 0xe0 && b < 0xa0 || lead == 0xed && b >= 0xa0 || lead == 0xf0 && b < 0x90 || lead == 0xf4 && b >= 0x90) {
						break
					}
					size++
				}
			}
			out.WriteRune(r)
			decoded = decoded[size:]
		}
	}
	return out.String()
}

func originalPluginServerQueryHex(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}
