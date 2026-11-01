package downloadercommands

// The consumed SimpleCookie string-load grammar/state machine is translated
// from the pinned CPython3.12 http/cookies.py. Preserve its full O'Malley notice
// plus PSF notices (see candidate licenses/), not the unrelated RequestUtils
// split/percent-decode parser. No network or Python runtime is involved.
import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var qbCookiePattern = regexp.MustCompile("^[ \\t\\n\\r\\f\\x0b]*(?P<key>[\\w\\d!#%&'~_`><@,:/\\$\\*\\+\\-\\.\\^\\|\\)\\(\\?\\}\\{\\=]+?)(?:[ \\t\\n\\r\\f\\x0b]*=[ \\t\\n\\r\\f\\x0b]*(?P<val>\"(?:[^\\\\\"]|\\\\.)*\"|(?:\\w{3,6}day|\\w{3}),[ \\t\\n\\r\\f\\x0b][\\w\\d \\t\\n\\r\\f\\x0b-]{9,11}[ \\t\\n\\r\\f\\x0b][\\d:]{8}[ \\t\\n\\r\\f\\x0b]GMT|[\\w\\d!#%&'~_`><@,:/\\$\\*\\+\\-\\.\\^\\|\\)\\(\\?\\}\\{\\=\\[\\]]*))?[ \\t\\n\\r\\f\\x0b]*(?:[ \\t\\n\\r\\f\\x0b]+|;|$)")
var qbCookieLegalKey = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+\\-.^_`|~:]+$")
var qbCookieReserved = map[string]bool{"expires": true, "path": true, "comment": true, "domain": true, "max-age": true, "secure": true, "httponly": true, "version": true, "samesite": true}

func qbCookieControl(s string) bool {
	for _, c := range s {
		if c < 32 || c == 127 {
			return true
		}
	}
	return false
}
func qbCookieError() error {
	return &SourceException{Class: "CookieError", Message: "Invalid cookie header"}
} // Never log credential-bearing parser details.
// Cookie grammar keys are ASCII and cannot contain double quotes/backslashes.
// Match Python repr's quote selection without exposing any Cookie value.
func qbCookieKeyRepr(key string) string {
	if strings.Contains(key, "'") {
		return `"` + key + `"`
	}
	return "'" + key + "'"
}
func qbCookieUnquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	s = s[1 : len(s)-1]
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			out.WriteByte(s[i])
			continue
		}
		if i+3 < len(s) && s[i+1] >= '0' && s[i+1] <= '3' && s[i+2] >= '0' && s[i+2] <= '7' && s[i+3] >= '0' && s[i+3] <= '7' {
			n, _ := strconv.ParseInt(s[i+1:i+4], 8, 32)
			out.WriteRune(rune(n))
			i += 3
		} else if s[i+1] != '\n' {
			i++
			out.WriteByte(s[i])
		} else {
			out.WriteByte(s[i])
		}
	}
	return out.String()
}

type qbCookiePair struct{ name, value string }

func qbSimpleCookies(header string) ([]qbCookiePair, error) {
	type part struct {
		key, raw, value string
		attribute       bool
	}
	parts := []part{}
	seen := false
	keyGroup, valueGroup := qbCookiePattern.SubexpIndex("key"), qbCookiePattern.SubexpIndex("val")
	for offset := 0; offset < len(header); {
		text := header[offset:]
		m := qbCookiePattern.FindStringSubmatchIndex(text)
		if m == nil {
			break
		}
		offset += m[1]
		key := text[m[2*keyGroup]:m[2*keyGroup+1]]
		hasValue := m[2*valueGroup] >= 0
		raw := ""
		if hasValue {
			raw = text[m[2*valueGroup]:m[2*valueGroup+1]]
		}
		lower := strings.ToLower(key)
		switch {
		case strings.HasPrefix(key, "$"):
			if !seen {
				continue
			}
			parts = append(parts, part{key: key[1:], raw: raw, value: raw, attribute: true})
		case qbCookieReserved[lower]:
			if !seen {
				return []qbCookiePair{}, nil
			}
			if !hasValue && lower != "secure" && lower != "httponly" {
				return []qbCookiePair{}, nil
			}
			parts = append(parts, part{key: key, raw: raw, value: qbCookieUnquote(raw), attribute: true})
		case hasValue:
			parts = append(parts, part{key: key, raw: raw, value: qbCookieUnquote(raw)})
			seen = true
		default:
			return []qbCookiePair{}, nil
		}
	}
	result := []qbCookiePair{}
	positions := map[string]int{}
	for _, p := range parts {
		if p.attribute {
			lower := strings.ToLower(p.key)
			if !qbCookieReserved[lower] {
				return nil, &SourceException{Class: "CookieError", Message: "Invalid attribute " + qbCookieKeyRepr(lower)}
			}
			if qbCookieControl(p.key) || qbCookieControl(p.value) {
				return nil, qbCookieError()
			}
			continue
		}
		if qbCookieReserved[strings.ToLower(p.key)] {
			return nil, &SourceException{Class: "CookieError", Message: "Attempt to set a reserved key " + qbCookieKeyRepr(p.key)}
		}
		if !qbCookieLegalKey.MatchString(p.key) {
			return nil, &SourceException{Class: "CookieError", Message: "Illegal key " + qbCookieKeyRepr(p.key)}
		}
		if qbCookieControl(p.key) || qbCookieControl(p.raw) || qbCookieControl(p.value) {
			return nil, qbCookieError()
		}
		item := qbCookiePair{p.key, p.value}
		if i, ok := positions[p.key]; ok {
			result[i] = item
		} else {
			positions[p.key] = len(result)
			result = append(result, item)
		}
	}
	return result, nil
}
func qbBuildSiteCookies(address string, header any) ([]map[string]any, error) {
	text, ok := header.(string)
	if !ok {
		return nil, &BoundaryError{"non-string Cookie input is outside the fixed header domain"}
	}
	// urlparse strips leading WHATWG C0/space and removes ASCII tab/CR/LF.
	address = strings.TrimLeftFunc(address, func(r rune) bool { return r <= 32 })
	address = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(address)
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, &BoundaryError{"URL hostname shape is outside the fixed source-compatible parser domain"}
	}
	domain := parsed.Hostname()
	if domain == "" {
		return []map[string]any{}, nil
	}
	for _, c := range domain {
		if c > 127 {
			return nil, &BoundaryError{"non-ASCII URL hostname normalization is pending"}
		}
	}
	// Go URL parsing decodes percent zones, unlike source urlparse.hostname.
	// Keep this unbound shape explicit; userinfo/path/query are not rejected.
	if strings.Contains(domain, "%") {
		return nil, &BoundaryError{"URL hostname percent/zone normalization is pending"}
	}
	domain = strings.ToLower(domain)
	pairs, err := qbSimpleCookies(text)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(pairs))
	for _, p := range pairs {
		result = append(result, map[string]any{"domain": domain, "path": "/", "name": p.name, "value": p.value})
	}
	return result, nil
}
