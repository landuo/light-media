// Package urlprep ports requests 2.34.2 PreparedRequest.prepare_url for str URLs
// without request params. Its Unicode tables come from the exact source runtime.
package urlprep

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Error reports the source exception class. Messages are diagnostic; callers
// must branch on ErrorClass rather than matching message text.
type Error struct{ Class, Message string }

func (e *Error) Error() string { return e.Class + ": " + e.Message }
func ErrorClass(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Class
	}
	if err == nil {
		return ""
	}
	return "Error"
}
func fail(class, message string) error { return &Error{class, message} }

var schemeRE = regexp.MustCompile(`^(?:[a-zA-Z][a-zA-Z0-9+\-]*:|/)`)
var uriRE = regexp.MustCompile(`(?s)^(?:([a-zA-Z][a-zA-Z0-9+.\-]*):)?(?://([^\\/?#]*))?([^?#]*)(?:\?([^#]*))?(?:#(.*))?$`)
var ipv4RE = regexp.MustCompile(`^(?:[0-9]{1,3}\.){3}[0-9]{1,3}$`)
var ipv6Pattern = makeIPv6Pattern()
var zonePattern = `(?:%25|%)(?:[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._\-~]|%[a-fA-F0-9]{2})+`
var ipv6RE = regexp.MustCompile(`^\[` + ipv6Pattern + `(?:` + zonePattern + `)?\]$`)
var hostPortRE = regexp.MustCompile(`(?s)^((?:[^\[\]%:/?#]|%[a-fA-F0-9]{2})*|(?:[0-9]{1,3}\.){3}[0-9]{1,3}|\[` + ipv6Pattern + `(?:` + zonePattern + `)?\])(?::0*?(|0|[1-9][0-9]{0,4}))?$`)
var zoneRE = regexp.MustCompile(`(` + zonePattern + `)\]$`)

func makeIPv6Pattern() string {
	h := `[0-9A-Fa-f]{1,4}`
	l := `(?:` + h + `:` + h + `|(?:[0-9]{1,3}\.){3}[0-9]{1,3})`
	return `(?:` + strings.Join([]string{`(?:` + h + `:){6}` + l, `::(?:` + h + `:){5}` + l, `(?:` + h + `)?::(?:` + h + `:){4}` + l, `(?:(?:` + h + `:)?` + h + `)?::(?:` + h + `:){3}` + l, `(?:(?:` + h + `:){0,2}` + h + `)?::(?:` + h + `:){2}` + l, `(?:(?:` + h + `:){0,3}` + h + `)?::` + h + `:` + l, `(?:(?:` + h + `:){0,4}` + h + `)?::` + l, `(?:(?:` + h + `:){0,5}` + h + `)?::` + h, `(?:(?:` + h + `:){0,6}` + h + `)?::`}, `|`) + `)`
}

const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-~"
const userInfo = unreserved + "!$&'()*+,;=:"
const pathChars = userInfo + "@/"
const queryChars = pathChars + "?"

func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 128 {
			return false
		}
	}
	return true
}
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f' }

const hex = "0123456789ABCDEF"

func quoteBytes(s, allowed string, keepPercent bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 128 && strings.IndexByte(allowed, c) >= 0 || keepPercent && c == '%' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
func encodeInvalid(s, allowed string) string {
	b := []byte(s)
	pct, n := 0, 0
	for i := 0; i < len(b); i++ {
		if b[i] != '%' {
			continue
		}
		pct++
		if i+2 < len(b) && isHex(b[i+1]) && isHex(b[i+2]) {
			n++
			if b[i+1] >= 'a' && b[i+1] <= 'f' {
				b[i+1] -= 32
			}
			if b[i+2] >= 'a' && b[i+2] <= 'f' {
				b[i+2] -= 32
			}
			i += 2
		}
	}
	return quoteBytes(string(b), allowed, pct == n)
}
func removeDotSegments(path string) string {
	out := []string{}
	for _, s := range strings.Split(path, "/") {
		if s == "." {
			continue
		}
		if s != ".." {
			out = append(out, s)
		} else if len(out) > 0 {
			out = out[:len(out)-1]
		}
	}
	if strings.HasPrefix(path, "/") && (len(out) == 0 || out[0] != "") {
		out = append([]string{""}, out...)
	}
	if strings.HasSuffix(path, "/.") || strings.HasSuffix(path, "/..") {
		out = append(out, "")
	}
	return strings.Join(out, "/")
}

type parsedURL struct {
	scheme, auth, host, path, query, fragment string
	port                                      int
	hasPort                                   bool
}

func parseURL(input string) (p parsedURL, err error) {
	s := input
	if s == "" {
		return
	}
	if !schemeRE.MatchString(s) {
		s = "//" + s
	}
	m := uriRE.FindStringSubmatch(s)
	if m == nil {
		return p, fail("InvalidURL", "Failed to parse: "+input)
	}
	p.scheme = lowerASCII(m[1])
	p.path = m[3]
	p.query = m[4]
	p.fragment = m[5]
	normal := p.scheme == "" || p.scheme == "http" || p.scheme == "https"
	if m[2] != "" {
		hp := m[2]
		if i := strings.LastIndexByte(hp, '@'); i >= 0 {
			p.auth = hp[:i]
			hp = hp[i+1:]
		}
		hm := hostPortRE.FindStringSubmatch(hp)
		if hm == nil && strings.HasSuffix(hp, "\n") {
			hm = hostPortRE.FindStringSubmatch(hp[:len(hp)-1])
		}
		if hm == nil {
			return p, fail("InvalidURL", "Failed to parse: "+input)
		}
		p.host = hm[1]
		if hm[2] != "" {
			p.port, _ = strconv.Atoi(hm[2])
			p.hasPort = true
			if p.port > 65535 {
				return p, fail("InvalidURL", "Failed to parse: "+input)
			}
		}
		if p.auth != "" && normal {
			p.auth = encodeInvalid(p.auth, userInfo)
		}
	}
	if p.host != "" && normal {
		if ipv6RE.MatchString(p.host) {
			if z := zoneRE.FindStringSubmatchIndex(p.host); z != nil {
				zone := p.host[z[2]:z[3]]
				if strings.HasPrefix(zone, "%25") && zone != "%25" {
					zone = zone[3:]
				} else {
					zone = zone[1:]
				}
				p.host = lowerASCII(p.host[:z[2]]) + "%" + encodeInvalid(zone, unreserved) + p.host[z[3]:]
			} else {
				p.host = lowerASCII(p.host)
			}
		} else if !ipv4RE.MatchString(p.host) {
			labels := strings.Split(p.host, ".")
			for i, l := range labels {
				if ascii(l) {
					labels[i] = lowerASCII(l)
				} else {
					lo, e := pythonLower(l)
					if e != nil {
						return p, e
					}
					a, e := idnaEncode(lo, true, false, true)
					if e != nil {
						return p, fail("InvalidURL", "Name is not a valid IDNA label")
					}
					labels[i] = a
				}
			}
			p.host = strings.Join(labels, ".")
		}
	}
	if normal {
		if p.path != "" {
			p.path = encodeInvalid(removeDotSegments(p.path), pathChars)
		}
		if p.query != "" {
			p.query = encodeInvalid(p.query, queryChars)
		}
		if p.fragment != "" {
			p.fragment = encodeInvalid(p.fragment, queryChars)
		}
	}
	if p.path != "" && !strings.HasPrefix(p.path, "/") {
		p.path = "/" + p.path
	}
	return
}

// Prepare reproduces the source str URL path with params=None. Strings may
// carry Python surrogate code points as WTF-8; arbitrary malformed UTF-8 is
// rejected because it cannot represent a Python str.
func Prepare(input string) (string, error) {
	rs, e := pythonRunes(input)
	if e != nil {
		return "", e
	}
	i := 0
	for i < len(rs) && property(rs[i]).Space {
		i++
	}
	input = pythonString(rs[i:])
	if strings.Contains(input, ":") && !strings.HasPrefix(lowerASCII(input), "http") {
		return input, nil
	}
	p, e := parseURL(input)
	if e != nil {
		return "", e
	}
	if p.scheme == "" {
		return "", fail("MissingSchema", fmt.Sprintf("Invalid URL %q: No scheme supplied.", input))
	}
	if p.host == "" {
		return "", fail("InvalidURL", "No host supplied")
	}
	if !ascii(p.host) {
		p.host, e = idnaEncode(p.host, false, true, false)
		if e != nil {
			return "", fail("InvalidURL", "URL has an invalid label.")
		}
	} else if strings.HasPrefix(p.host, "*") || strings.HasPrefix(p.host, ".") {
		return "", fail("InvalidURL", "URL has an invalid label.")
	}
	netloc := p.host
	if p.auth != "" {
		netloc = p.auth + "@" + netloc
	}
	if p.hasPort && p.port != 0 {
		netloc += ":" + strconv.Itoa(p.port)
	}
	if p.path == "" {
		p.path = "/"
	}
	s := p.scheme + "://" + netloc + p.path
	if p.query != "" {
		s += "?" + p.query
	}
	if p.fragment != "" {
		s += "#" + p.fragment
	}
	for _, c := range mustRunes(s) {
		if c >= 0xD800 && c <= 0xDFFF {
			return "", fail("UnicodeEncodeError", "surrogates not allowed by quote")
		}
	}
	return requote(s), nil
}
func requote(s string) string {
	parts := strings.Split(s, "%")
	bad := false
	for i := 1; i < len(parts); i++ {
		r, _ := pythonRunes(parts[i])
		if len(r) >= 2 && property(r[0]).Alnum && property(r[1]).Alnum {
			h := pythonString(r[:2])
			v, e := pythonHexInt(h)
			if e != nil {
				bad = true
				break
			}
			if v < 128 && strings.IndexByte(unreserved, byte(v)) >= 0 {
				parts[i] = string(byte(v)) + pythonString(r[2:])
			} else {
				parts[i] = "%" + parts[i]
			}
		} else {
			parts[i] = "%" + parts[i]
		}
	}
	if bad {
		return quoteBytes(s, unreserved+"!#$&'()*+,/:;=?@[]~", false)
	}
	return quoteBytes(strings.Join(parts, ""), unreserved+"!#$%&'()*+,/:;=?@[]~", false)
}

func pythonHexInt(s string) (uint64, error) {
	rs, e := pythonRunes(s)
	if e != nil {
		return 0, e
	}
	v := uint64(0)
	for _, r := range rs {
		d := -1
		if r >= '0' && r <= '9' {
			d = int(r - '0')
		} else if r >= 'A' && r <= 'F' {
			d = int(r-'A') + 10
		} else if r >= 'a' && r <= 'f' {
			d = int(r-'a') + 10
		} else {
			loadTables()
			if n, ok := tables.decimal[r]; ok {
				d = n
			}
		}
		if d < 0 {
			return 0, fail("ValueError", "invalid integer literal")
		}
		v = v*16 + uint64(d)
	}
	return v, nil
}

func mustRunes(s string) []rune {
	rs, e := pythonRunes(s)
	if e != nil {
		panic(e)
	}
	return rs
}
