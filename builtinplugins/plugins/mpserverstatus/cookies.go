package mpserverstatus

import (
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// A request-local Netscape policy. Default CookiePolicy is deliberately not a
// public-suffix policy: it rejects nonlocal single-label Domain attributes,
// has the original .local effective-host rule and liberal host-only returns.
// No URL/network/credential source is rewritten or consulted by this jar.
type sourceCookieEntry struct {
	cookie                        http.Cookie
	domain, path                  string
	expires                       time.Time
	domainOrder, pathOrder, order int
}
type sourceCookieJar struct {
	mu                     sync.Mutex
	entries                []*sourceCookieEntry
	domainOrder, pathOrder map[string]int
	next                   int
}

func newSourceCookieJar() *sourceCookieJar {
	return &sourceCookieJar{domainOrder: map[string]int{}, pathOrder: map[string]int{}}
}
func sourceCookieHost(u *url.URL) (string, string) {
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	effective := host
	if !strings.Contains(host, ".") {
		effective += ".local"
	}
	return host, effective
}
func sourceCookiePath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" || p[0] != '/' {
		return "/"
	}
	return p
}
func (j *sourceCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	_, effective := sourceCookieHost(u)
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		c := *cookie
		domain := effective
		if c.Domain != "" {
			domain = strings.ToLower(c.Domain)
			if !strings.HasPrefix(domain, ".") {
				domain = "." + domain
			}
			undotted := strings.TrimPrefix(domain, ".")
			if !strings.Contains(undotted, ".") && !strings.HasSuffix(effective, ".local") {
				continue
			}
			if !(strings.HasSuffix(effective, domain) || strings.HasSuffix(effective, undotted+".local")) && !strings.HasPrefix(effective, ".") && !strings.HasSuffix("."+effective, domain) {
				continue
			}
		}
		path := c.Path
		if path == "" {
			path = sourceCookiePath(u)
			if i := strings.LastIndex(path, "/"); i >= 0 {
				path = path[:i]
			}
			if path == "" {
				path = "/"
			}
		}
		expires := c.Expires
		remove := c.MaxAge < 0
		age, present, valid := sourceCookieMaxAge(c.Raw)
		if present {
			if !valid {
				continue
			}
			remove = age.Sign() <= 0
			if !remove && age.IsInt64() && age.Int64() <= int64((1<<63-1)/time.Second) {
				expires = now.Add(time.Duration(age.Int64()) * time.Second)
			} else if !remove {
				expires = time.Unix(1<<62, 0)
			}
		} else if c.MaxAge > 0 {
			expires = now.Add(time.Duration(c.MaxAge) * time.Second)
		}
		remove = remove || !expires.IsZero() && !expires.After(now)
		found := -1
		for i, entry := range j.entries {
			if entry.domain == domain && entry.path == path && entry.cookie.Name == c.Name {
				found = i
				break
			}
		}
		if remove {
			if found >= 0 {
				j.entries = append(j.entries[:found], j.entries[found+1:]...)
			}
			continue
		}
		if found >= 0 {
			j.entries[found].cookie = c
			j.entries[found].expires = expires
			continue
		}
		if _, ok := j.domainOrder[domain]; !ok {
			j.domainOrder[domain] = len(j.domainOrder)
		}
		pathKey := domain + "\x00" + path
		if _, ok := j.pathOrder[pathKey]; !ok {
			j.pathOrder[pathKey] = len(j.pathOrder)
		}
		j.next++
		j.entries = append(j.entries, &sourceCookieEntry{cookie: c, domain: domain, path: path, expires: expires, domainOrder: j.domainOrder[domain], pathOrder: j.pathOrder[pathKey], order: j.next})
	}
}
func (j *sourceCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	_, effective := sourceCookieHost(u)
	path := sourceCookiePath(u)
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	selected := []*sourceCookieEntry{}
	for _, entry := range j.entries {
		if !entry.expires.IsZero() && !entry.expires.After(now) {
			continue
		}
		domain := entry.domain
		if !strings.HasPrefix(domain, ".") {
			domain = "." + domain
		}
		if !strings.HasSuffix("."+effective, domain) {
			continue
		}
		if entry.cookie.Secure && u.Scheme != "https" {
			continue
		}
		if !strings.HasPrefix(path, entry.path) || len(path) > len(entry.path) && !strings.HasSuffix(entry.path, "/") && path[len(entry.path)] != '/' {
			continue
		}
		selected = append(selected, entry)
	}
	sort.SliceStable(selected, func(i, k int) bool {
		a, b := selected[i], selected[k]
		if len(a.path) != len(b.path) {
			return len(a.path) > len(b.path)
		}
		if a.domainOrder != b.domainOrder {
			return a.domainOrder < b.domainOrder
		}
		if a.pathOrder != b.pathOrder {
			return a.pathOrder < b.pathOrder
		}
		return a.order < b.order
	})
	out := make([]*http.Cookie, 0, len(selected))
	for _, entry := range selected {
		out = append(out, &http.Cookie{Name: entry.cookie.Name, Value: entry.cookie.Value, Quoted: entry.cookie.Quoted})
	}
	return out
}
func (j *sourceCookieJar) domains() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []string{}
	seen := map[string]bool{}
	for _, e := range j.entries {
		if !seen[e.domain] {
			seen[e.domain] = true
			out = append(out, e.domain)
		}
	}
	return out
}

func sourceCookieMaxAge(raw string) (*big.Int, bool, bool) {
	var value string
	present := false
	for _, attribute := range strings.Split(raw, ";")[1:] {
		key, v, has := strings.Cut(attribute, "=")
		if strings.EqualFold(strings.TrimSpace(key), "max-age") {
			present = true
			if !has {
				value = ""
			} else {
				value = strings.Trim(strings.TrimFunc(v, pythonSpace), "\"")
			}
		}
	}
	if !present {
		return nil, false, true
	}
	n, ok := sourceHTTPInteger(value)
	return n, true, ok
}
