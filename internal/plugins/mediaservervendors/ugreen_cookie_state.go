package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// This is the persistent COOKIE state, not an Api or HTTP-success port. It must
// implement the original complete-jar export/update semantics. A plain Go
// cookiejar.Cookies(origin) adapter cannot satisfy Export. There is deliberately
// no default/empty/fixed-result implementation in this first HTTP block.
type UgreenSessionCookieStore interface {
	Header(context.Context, *url.URL) (string, error)
	Extract(context.Context, *url.URL, http.Header) error
	Export(context.Context) (json.RawMessage, error)
	Update(context.Context, []UgreenHTTPField) error
}

// CookieJar's three nested insertion-ordered mappings are observable through
// RequestsCookieJar.get_dict. A single name map or filtered Go Jar is not this
// state. This first state block is deliberately not an HTTP extraction parser.
type ugreenStoredCookie struct {
	name                              string
	value                             *string
	domain                            string
	domainSpecified, domainInitialDot bool
	path                              string
	pathSpecified, secure             bool
	expires                           *big.Int
	discard                           bool
	port                              *string
	portSpecified                     bool
	version                           int
}
type ugreenCookiePath struct {
	name    string
	cookies []ugreenStoredCookie
}
type ugreenCookieDomain struct {
	name  string
	paths []ugreenCookiePath
}
type ugreenCookieState struct {
	mu      sync.Mutex
	domains []ugreenCookieDomain
}

func newUgreenCookieState() *ugreenCookieState { return &ugreenCookieState{} }
func ugreenCookieContext(ctx context.Context) error {
	if ctx == nil {
		return &EmbyJellyfinBoundaryError{Kind: "Ugreen cookie store requires context"}
	}
	return ctx.Err()
}
func (c *ugreenCookieState) set(cookie ugreenStoredCookie) {
	if cookie.value != nil {
		value := *cookie.value
		if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			value = strings.ReplaceAll(value, `\"`, "")
		}
		cookie.value = &value
	}
	if cookie.expires != nil {
		cookie.expires = new(big.Int).Set(cookie.expires)
	}
	if cookie.port != nil {
		port := *cookie.port
		cookie.port = &port
	}
	di := -1
	for i := range c.domains {
		if c.domains[i].name == cookie.domain {
			di = i
			break
		}
	}
	if di < 0 {
		c.domains = append(c.domains, ugreenCookieDomain{name: cookie.domain})
		di = len(c.domains) - 1
	}
	domain := &c.domains[di]
	pi := -1
	for i := range domain.paths {
		if domain.paths[i].name == cookie.path {
			pi = i
			break
		}
	}
	if pi < 0 {
		domain.paths = append(domain.paths, ugreenCookiePath{name: cookie.path})
		pi = len(domain.paths) - 1
	}
	path := &domain.paths[pi]
	for i := range path.cookies {
		if path.cookies[i].name == cookie.name {
			path.cookies[i] = cookie
			return
		}
	}
	path.cookies = append(path.cookies, cookie)
}
func (c *ugreenCookieState) remove(domain, path, name string) {
	for di := range c.domains {
		if c.domains[di].name != domain {
			continue
		}
		for pi := range c.domains[di].paths {
			p := &c.domains[di].paths[pi]
			if p.name != path {
				continue
			}
			for i := range p.cookies {
				if p.cookies[i].name == name {
					p.cookies = append(p.cookies[:i], p.cookies[i+1:]...)
					return
				}
			}
		}
	}
	// CookieJar.clear(domain,path,name) leaves the enclosing empty mappings in
	// place; their insertion positions matter if the path later gets a new name.
}
func (c *ugreenCookieState) Update(ctx context.Context, fields []UgreenHTTPField) error {
	if err := ugreenCookieContext(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, field := range fields {
		value := field.Value
		// This API carrier already performed the original non-None/string filter.
		// Requests create_cookie defaults: empty domain, path '/', no expiry/secure.
		c.set(ugreenStoredCookie{name: field.Name, value: &value, path: "/", pathSpecified: true, discard: true})
	}
	return ctx.Err()
}
func (c *ugreenCookieState) Export(ctx context.Context) (json.RawMessage, error) {
	if err := ugreenCookieContext(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := []string{}
	values := map[string]*string{}
	for _, domain := range c.domains {
		for _, path := range domain.paths {
			for _, cookie := range path.cookies {
				if _, ok := values[cookie.name]; !ok {
					names = append(names, cookie.name)
				}
				values[cookie.name] = cookie.value
			}
		}
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(ugreenKey(name))
		out.WriteByte(':')
		if values[name] == nil {
			out.WriteString("null")
		} else {
			out.Write(ugreenKey(*values[name]))
		}
	}
	out.WriteByte('}')
	return out.Bytes(), ctx.Err()
}

var ugreenCookiePortSuffix = regexp.MustCompile(`:[0-9]+$`)
var ugreenCookiePercent = regexp.MustCompile(`%[0-9a-fA-F]{2}`)

func ugreenCookieHost(target *url.URL) string {
	host := strings.ToLower(ugreenCookiePortSuffix.ReplaceAllString(target.Host, ""))
	if !strings.Contains(host, ".") {
		host += ".local"
	}
	return host
}
func ugreenCookiePathEscape(path string) string {
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-%/;:@&=+$,!~*'()"
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		v := path[i]
		if strings.IndexByte(safe, v) >= 0 {
			b.WriteByte(v)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[v>>4])
			b.WriteByte(hex[v&15])
		}
	}
	return ugreenCookiePercent.ReplaceAllStringFunc(b.String(), strings.ToUpper)
}
func ugreenCookieRequestPath(target *url.URL) string {
	// RawPath is the original escaped component when available. Escaping already
	// encoded percent signs must not decode the path or collapse slash boundaries.
	path := target.EscapedPath()
	path = ugreenCookiePathEscape(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}
func (c *ugreenCookieState) Header(ctx context.Context, target *url.URL) (string, error) {
	if err := ugreenCookieContext(ctx); err != nil {
		return "", err
	}
	if target == nil || target.Host == "" {
		return "", &EmbyJellyfinBoundaryError{Kind: "Ugreen cookie target absent"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	host := ugreenCookieHost(target)
	path := ugreenCookieRequestPath(target)
	now := big.NewInt(time.Now().Unix())
	selected := []ugreenStoredCookie{}
	for _, domain := range c.domains {
		for _, group := range domain.paths {
			for _, cookie := range group.cookies {
				if cookie.version > 0 {
					continue
				} // Default policy rejects positive non-Netscape versions.
				dot := cookie.domain
				if dot != "" && !strings.HasPrefix(dot, ".") {
					dot = "." + dot
				}
				if !strings.HasSuffix("."+host, dot) {
					continue
				}
				if path != cookie.path && !(strings.HasPrefix(path, cookie.path) && (strings.HasSuffix(cookie.path, "/") || strings.HasPrefix(path[len(cookie.path):], "/"))) {
					continue
				}
				if cookie.secure && target.Scheme != "https" && target.Scheme != "wss" {
					continue
				}
				if cookie.expires != nil && cookie.expires.Cmp(now) <= 0 {
					continue
				}
				if cookie.port != nil {
					port := target.Port()
					if port == "" {
						port = "80"
					}
					matched := false
					for _, p := range strings.Split(*cookie.port, ",") {
						if p == port {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}
				selected = append(selected, cookie)
			}
		}
	}
	sort.SliceStable(selected, func(i, j int) bool { return len(selected[i].path) > len(selected[j].path) })
	parts := make([]string, 0, len(selected))
	for _, cookie := range selected {
		value := cookie.name
		if cookie.value != nil {
			value += "=" + *cookie.value
		}
		// Header serialization must not grant CR/LF/header injection authority.
		// This is a platform boundary, never silently sanitize a credential value.
		if strings.ContainsAny(value, "\r\n") {
			return "", &EmbyJellyfinBoundaryError{Kind: "invalid Ugreen Cookie header"}
		}
		parts = append(parts, value)
	}
	// Requests prepares a COPY for add_cookie_header; expiry cleanup on that
	// copied jar must not remove records from the persistent Session export.
	text := strings.Join(parts, "; ")
	wire := make([]byte, 0, len(text))
	for _, point := range text {
		if point > 255 {
			return "", ejSource("UnicodeEncodeError")
		}
		wire = append(wire, byte(point))
	}
	return string(wire), ctx.Err()
}
