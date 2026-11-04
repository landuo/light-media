package mediaservervendors

import (
	"context"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"light-media/internal/releasemeta/legacyanime"
)

// NewUgreenSessionCookies creates actual private state. It does not read a
// user cookie file, return recorded cookies or share a jar across selected APIs.
func NewUgreenSessionCookies() UgreenSessionCookieStore { return newUgreenCookieState() }

var _ UgreenSessionCookieStore = (*ugreenCookieState)(nil)

func ugreenCookieUnquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
func ugreenCookieInteger(s string) (*big.Int, bool) {
	n, err := legacyanime.Integer(s)
	return n, err == nil
}

// The selected ordinary response path is Netscape Set-Cookie. Set-Cookie2's
// obsolete RFC2965 extraction and unusual date grammar remain explicitly
// unverified; no claim that this bounded parser is the entire stdlib CookieJar.
func (c *ugreenCookieState) Extract(ctx context.Context, target *url.URL, headers http.Header) error {
	if err := ugreenCookieContext(ctx); err != nil {
		return err
	}
	if target == nil || target.Host == "" {
		return &EmbyJellyfinBoundaryError{Kind: "Ugreen cookie response target absent"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, line := range headers.Values("Set-Cookie") {
		// http.client exposes response header octets as ISO-8859-1 text. Go's
		// Header retains wire bytes; do not misread non-ASCII octets as UTF-8.
		var decoded strings.Builder
		for i := 0; i < len(line); i++ {
			decoded.WriteRune(rune(line[i]))
		}
		cookie, accepted := c.parse(decoded.String(), target, now)
		if accepted {
			c.set(cookie)
		}
	}
	return ctx.Err()
}
func (c *ugreenCookieState) parse(line string, target *url.URL, now int64) (ugreenStoredCookie, bool) {
	empty := ugreenStoredCookie{}
	parts := strings.Split(line, ";")
	if len(parts) == 0 {
		return empty, false
	}
	key, val, hasValue := strings.Cut(legacyanime.Strip(parts[0]), "=")
	key = legacyanime.Strip(key)
	if key == "" {
		return empty, false
	}
	cookie := ugreenStoredCookie{name: key, discard: true}
	if hasValue {
		value := legacyanime.Strip(val)
		cookie.value = &value
	}
	attrs := map[string]*string{}
	maxAge := false
	for _, part := range parts[1:] {
		k, v, has := strings.Cut(legacyanime.Strip(part), "=")
		k = legacyanime.Strip(k)
		if k == "" {
			continue
		}
		lower := strings.ToLower(k)
		switch lower {
		case "expires", "domain", "path", "secure", "version", "port", "max-age":
			k = lower
		}
		if _, exists := attrs[k]; exists {
			continue
		}
		var value *string
		if has {
			text := legacyanime.Strip(v)
			value = &text
		}
		switch k {
		case "domain":
			if value == nil {
				return empty, false
			}
			text := strings.ToLower(*value)
			value = &text
		case "version":
			if value != nil {
				text := ugreenCookieUnquote(*value)
				value = &text
			}
		case "expires":
			if maxAge {
				continue
			}
			if value == nil {
				continue
			}
			seconds, ok := ugreenCookieHTTPTime(ugreenCookieUnquote(*value), now)
			if !ok {
				continue
			}
			cookie.expires = big.NewInt(seconds)
			attrs[k] = value
			continue
		case "max-age":
			maxAge = true
			if value == nil {
				return empty, false
			}
			age, ok := ugreenCookieInteger(*value)
			if !ok {
				return empty, false
			}
			cookie.expires = new(big.Int).Add(big.NewInt(now), age)
			// Original normalizer stores this under expires, not max-age.
			attrs["expires"] = value
			continue
		}
		attrs[k] = value
	}
	if value, present := attrs["version"]; present {
		if value == nil {
			return empty, false
		}
		version, ok := ugreenCookieInteger(*value)
		if !ok {
			return empty, false
		}
		// Version 1 is subsequently treated as Netscape by the source default
		// rfc2109_as_netscape policy. Other positive versions are rejected by set_ok.
		if version.Sign() > 0 && version.Cmp(big.NewInt(1)) != 0 {
			return empty, false
		}
		if version.Sign() < 0 {
			cookie.version = -1
		} else {
			cookie.version = int(version.Int64())
		}
	}
	if value, present := attrs["path"]; present && value == nil {
		return empty, false
	}
	if value := attrs["path"]; value != nil && *value != "" {
		cookie.pathSpecified = true
		cookie.path = ugreenCookiePathEscape(*value)
	} else {
		cookie.path = ugreenCookieRequestPath(target)
		if i := strings.LastIndex(cookie.path, "/"); i >= 0 {
			if cookie.version == 0 {
				cookie.path = cookie.path[:i]
			} else {
				cookie.path = cookie.path[:i+1]
			}
		}
		if cookie.path == "" {
			cookie.path = "/"
		}
	}
	cookie.domain = ugreenCookieHost(target)
	if value, present := attrs["domain"]; present {
		if value == nil {
			return empty, false
		}
		cookie.domainSpecified = true
		cookie.domainInitialDot = strings.HasPrefix(*value, ".")
		cookie.domain = *value
		if !strings.HasPrefix(cookie.domain, ".") {
			cookie.domain = "." + cookie.domain
		}
	}
	if value, present := attrs["secure"]; present {
		cookie.secure = value == nil || *value != ""
	}
	if value, present := attrs["discard"]; present {
		cookie.discard = value == nil || *value != ""
	} else {
		cookie.discard = cookie.expires == nil
	}
	if value, present := attrs["port"]; present {
		if value == nil {
			port := target.Port()
			if port == "" {
				port = "80"
			}
			cookie.port = &port
		} else {
			port := strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) {
					return -1
				}
				return r
			}, *value)
			cookie.port = &port
			cookie.portSpecified = true
		}
	}
	// CookieJar removes an explicitly expired key before policy acceptance.
	if cookie.expires != nil && cookie.expires.Cmp(big.NewInt(now)) <= 0 {
		c.remove(cookie.domain, cookie.path, cookie.name)
		return empty, false
	}
	if cookie.version == 1 {
		cookie.version = 0
	}
	if cookie.domainSpecified {
		host := ugreenCookieHost(target)
		undotted := strings.TrimPrefix(cookie.domain, ".")
		if !strings.Contains(undotted, ".") && !strings.HasSuffix(host, ".local") {
			return empty, false
		}
		if cookie.version == 0 && !(strings.HasSuffix(host, cookie.domain) || strings.HasSuffix(host, undotted+".local") || (!strings.HasPrefix(host, ".") && strings.HasSuffix("."+host, cookie.domain))) {
			return empty, false
		}
	}
	if cookie.portSpecified {
		port := target.Port()
		if port == "" {
			port = "80"
		}
		matched := false
		for _, v := range strings.Split(*cookie.port, ",") {
			if _, ok := ugreenCookieInteger(v); !ok {
				return empty, false
			}
			if v == port {
				matched = true
			}
		}
		if !matched {
			return empty, false
		}
	}
	return cookie, true
}

// http2time's common complete grammar, including source nearest-century choice,
// UTC-only named zones, optional clock and numeric timezone. This is a literal
// source-derived parser rather than Go http.ParseTime's different date policy.
var ugreenCookieLooseDate = regexpCookieDate()

func regexpCookieDate() *regexp.Regexp {
	return regexp.MustCompile(`^([0-9]{1,2})(?:[\t\n\v\f\r ]+|[-/])([A-Za-z0-9_]+)(?:[\t\n\v\f\r ]+|[-/])([0-9]+)(?:(?:[\t\n\v\f\r ]+|:)([0-9]{1,2}):([0-9]{2})(?::([0-9]{2}))?)?[\t\n\v\f\r ]*(?:([-+]?[0-9]{2,4}|[A-Za-z]+)[\t\n\v\f\r ]*)?(?:\([A-Za-z0-9_]+\)[\t\n\v\f\r ]*)?$`)
}

var ugreenCookieWeekday = regexp.MustCompile(`(?i)^(?:Sun|Mon|Tue|Wed|Thu|Fri|Sat)[a-z]*,?[\t\n\v\f\r ]*`)
var ugreenCookieTimezone = regexp.MustCompile(`^([-+])?([0-9]{1,2}):?([0-9]{2})?$`)

func ugreenCookieHTTPTime(text string, now int64) (int64, bool) {
	text = ugreenCookieWeekday.ReplaceAllString(strings.TrimLeftFunc(text, unicode.IsSpace), "")
	m := ugreenCookieLooseDate.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	number := func(s string) int { n, _ := strconv.Atoi(s); return n }
	year, err := strconv.Atoi(m[3])
	if err != nil || year > 9999 {
		return 0, false
	}
	month := 0
	for i, name := range []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"} {
		if strings.ToLower(m[2]) == name {
			month = i + 1
		}
	}
	if month == 0 {
		month = number(m[2])
	}
	if month < 1 || month > 12 {
		return 0, false
	}
	if year < 1000 {
		current := time.Unix(now, 0).Local().Year()
		delta := current%100 - year
		year += current - current%100
		if delta > 50 {
			year += 100
		} else if delta < -50 {
			year -= 100
		}
	}
	day, hour, minute, second := number(m[1]), number(m[4]), number(m[5]), number(m[6])
	if year < 1970 || year > 9999 || day < 1 || day > 31 || hour < 0 || hour > 24 || minute < 0 || minute > 59 || second < 0 || second > 61 {
		return 0, false
	}
	zone := strings.ToUpper(m[7])
	offset := 0
	switch zone {
	case "", "UTC", "GMT", "UT", "Z":
	default:
		tz := ugreenCookieTimezone.FindStringSubmatch(zone)
		if tz == nil {
			return 0, false
		}
		offset = 3600*number(tz[2]) + 60*number(tz[3])
		if tz[1] == "-" {
			offset = -offset
		}
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC).Unix() - int64(offset), true
}
