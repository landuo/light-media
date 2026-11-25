package apprise

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// sourceURL follows Apprise's parser rather than net/url: a leading query +
// denotes a header, values keep literal +, and custom values unquote twice.
type sourceURL struct {
	scheme, host, port, path string
	rawHost                  string
	user, password           *string
	rawUser, rawPassword     *string
	query                    map[string]string
	headers, params, extras  [][2]string
	queryPairs               [][2]string
}

var sourcePortPattern = regexp.MustCompile(`^([\[][0-9a-f:]+[\]]|[^:]+):([^:]*)$`)
var sourceIPv4 = regexp.MustCompile(sourceIPv4Pattern)
var sourceIPv6 = regexp.MustCompile("(?i)" + sourceIPv6Pattern)
var sourceColons = regexp.MustCompile(`:+`)
var sourceSlashes = regexp.MustCompile(`/+`)

func stringPointer(s string) *string { return &s }
func (o object) set(key string, value any) object {
	for i, p := range o {
		if p.key == key {
			o[i].value = value
			return o
		}
	}
	return append(o, field{key, value})
}
func (o object) del(key string) object {
	for i, p := range o {
		if p.key == key {
			return append(o[:i], o[i+1:]...)
		}
	}
	return o
}
func orderedPairSet(o [][2]string, key, value string) [][2]string {
	for i, p := range o {
		if p[0] == key {
			o[i][1] = value
			return o
		}
	}
	return append(o, [2]string{key, value})
}
func parseSourceURL(raw string) (sourceURL, bool) {
	return parseSourceURLWithHostVerification(raw, true)
}

func parseSourceURLWithHostVerification(raw string, verifyHost bool) (sourceURL, bool) {
	u := sourceURL{query: map[string]string{}}
	raw = strings.TrimFunc(raw, sourceSpace)
	sep := strings.Index(raw, "://")
	if sep <= 0 {
		return u, false
	}
	u.scheme = strings.ToLower(raw[:sep])
	rest := raw[sep+3:]
	hostpath, qs, hasQS := strings.Cut(rest, "?")
	hostpath = strings.TrimFunc(hostpath, sourceSpace)
	authority := hostpath
	path := ""
	if slash := strings.IndexByte(authority, '/'); slash >= 0 {
		path = authority[slash:]
		authority = authority[:slash]
	}
	if hash := strings.IndexByte(authority, '#'); hash >= 0 {
		authority = authority[:hash]
	}
	if hash := strings.IndexByte(path, '#'); hash >= 0 {
		path = path[:hash]
	}
	// urllib.parse.urlparse's params component is excluded by original parsed[2].
	if semi := strings.LastIndexByte(path, ';'); semi >= 0 && !strings.Contains(path[semi:], "/") {
		path = path[:semi]
	}
	path = sourceSlashes.ReplaceAllString(strings.TrimFunc(path, sourceSpace), "/")
	path = strings.TrimRight(path, "\\")
	u.path = sourceQuote(sourceUnquote(path), "/")
	if u.path != "" && !strings.HasSuffix(u.path, "/") && strings.HasSuffix(raw, "/") {
		u.path += "/"
	}
	if at := strings.IndexByte(authority, '@'); at >= 0 {
		login := authority[:at]
		authority = strings.TrimLeft(authority[at:], "@")
		if next := strings.IndexByte(authority, '@'); next >= 0 {
			authority = authority[:next]
		}
		parts := sourceColons.Split(login, -1)
		u.user = stringPointer(parts[0])
		if len(parts) > 1 {
			u.password = stringPointer(parts[1])
		}
	}
	if match := sourcePortPattern.FindStringSubmatch(authority); match != nil {
		if p, err := strconv.ParseInt(strings.TrimFunc(match[2], sourceSpace), 10, 64); err == nil {
			authority = match[1]
			u.port = ":" + strconv.FormatInt(p, 10)
		} else if verifyHost {
			return u, false
		}
	}
	if verifyHost {
		host, ok := sourceHostname(authority)
		if !ok {
			return u, false
		}
		authority = host
	}
	u.rawHost = authority
	u.host = sourceUnquote(authority)
	if hasQS {
		for _, part := range strings.FieldsFunc(strings.TrimFunc(qs, sourceSpace), func(r rune) bool { return r == '&' || r == ';' }) {
			key, value, _ := strings.Cut(part, "=")
			if len(key) > 1 {
				key = key[:1] + strings.ReplaceAll(key[1:], "+", " ")
			}
			key = sourceUnquote(key)
			value = strings.TrimFunc(sourceUnquote(value), sourceSpace)
			u.query[strings.ToLower(strings.TrimFunc(key, sourceSpace))] = value
			u.queryPairs = orderedPairSet(u.queryPairs, key, value)
			if len(key) > 0 {
				k := sourceUnquote(key[1:])
				v := sourceUnquote(value)
				switch key[0] {
				case '+', ' ':
					u.headers = orderedPairSet(u.headers, k, v)
				case '-':
					u.params = orderedPairSet(u.params, k, v)
				case ':':
					u.extras = orderedPairSet(u.extras, k, v)
				}
			}
		}
	}
	if user, ok := u.query["user"]; ok {
		original := u.user
		u.user = stringPointer(user)
		if u.password == nil {
			u.password = original
		}
	}
	for _, key := range []string{"password", "pass"} {
		if pass, ok := u.query[key]; ok {
			u.password = stringPointer(pass)
		}
	}
	u.rawUser, u.rawPassword = u.user, u.password
	if u.user != nil {
		u.user = stringPointer(sourceUnquote(*u.user))
	}
	if u.password != nil {
		u.password = stringPointer(sourceUnquote(*u.password))
	}
	if port, ok := u.query["port"]; ok {
		u.port = ""
		if n, e := strconv.ParseInt(port, 10, 64); e == nil {
			u.port = ":" + strconv.FormatInt(n, 10)
		}
	}
	return u, true
}
func sourceHostname(host string) (string, bool) {
	if utf8.RuneCountInString(host) == 0 || utf8.RuneCountInString(host) > 253 {
		return "", false
	}
	host = strings.TrimSuffix(host, ".")
	labels := strings.Split(host, ".")
	digits := true
	for _, r := range host {
		if r != '.' && (r < '0' || r > '9') {
			digits = false
		}
	}
	if len(labels) == 4 && digits {
		m := sourceIPv4.FindStringSubmatch(host)
		if m != nil {
			return m[1], true
		}
		return "", false
	}
	valid := true
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 {
			valid = false
			break
		}
		low := strings.ToLower(label)
		if len(low) == 1 {
			if low[0] < 'a' || low[0] > 'z' {
				valid = false
				break
			}
		} else {
			for i, c := range []byte(low) {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_')) {
					valid = false
					break
				}
			}
			last := low[len(low)-1]
			if last == '_' || last == '-' {
				valid = false
			}
		}
	}
	if valid {
		return host, true
	}
	if m := sourceIPv4.FindStringSubmatch(host); m != nil {
		return m[1], true
	}
	if m := sourceIPv6.FindStringSubmatch(host); m != nil {
		return "[" + m[sourceIPv6.SubexpIndex("ip")] + "]", true
	}
	return "", false
}
func sourceUnquote(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if n, e := strconv.ParseUint(s[i+1:i+3], 16, 8); e == nil {
				b = append(b, byte(n))
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return strings.ToValidUTF8(string(b), "\ufffd")
}
func sourceQuote(s, safe string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(s) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("_.-~"+safe, rune(b)) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}
