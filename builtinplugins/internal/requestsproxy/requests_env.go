package requestsproxy

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// RequestsEnvironmentProxy follows the Linux requests 2.34.2 trust-env path
// used by the installed source. It is independent of MoviePilot's explicit
// PROXY mapping in Resolve. Supplying environment values avoids global mutation
// and makes per-client source snapshots and exact offline comparison possible.
// No environment/proxy credentials are included in returned error messages.
func RequestsEnvironmentProxy(environ []string, target *url.URL) (*url.URL, error) {
	if target == nil {
		return nil, errors.New("requests_proxy_missing_target")
	}
	environment := environmentValues(environ)
	proxies := requestsGetproxies(environment)
	host := strings.ToLower(target.Hostname())
	if host == "" || requestsBypass(environment, proxies, host, target.Port()) {
		return nil, nil
	}
	return requestsSelectProxy(proxies, target)
}

func requestsSelectProxy(proxies map[string]string, target *url.URL) (*url.URL, error) {
	scheme, host := strings.ToLower(target.Scheme), strings.ToLower(target.Hostname())
	keys := []string{scheme + "://" + host, scheme, "all://" + host, "all"}
	if host == "" {
		keys = []string{scheme, "all"}
	}
	raw := ""
	for _, key := range keys {
		if v, ok := proxies[key]; ok {
			raw = v
			break
		}
	}
	if raw == "" {
		return nil, nil
	}
	// requests.adapters.prepend_scheme_if_needed defaults schemeless proxy
	// values to HTTP. Go's url.Parse otherwise treats hostname:port as a scheme.
	if strings.HasPrefix(raw, "//") {
		raw = "http:" + raw
	} else if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	proxy, err := url.Parse(requestsEscapeUnicodeUserinfo(raw))
	if err != nil || proxy.Hostname() == "" || proxy.Opaque != "" {
		return nil, errors.New("requests_proxy_invalid_url")
	}
	proxy.Scheme = strings.ToLower(proxy.Scheme)
	return proxy, nil
}

// urllib3 accepts raw Unicode userinfo and percent-encodes its UTF-8 bytes;
// net/url otherwise rejects it before HTTPAdapter's Latin-1 auth conversion.
// Preserve existing ASCII escapes and authority delimiters verbatim.
func requestsEscapeUnicodeUserinfo(raw string) string {
	start := strings.Index(raw, "://") + 3
	end := len(raw)
	if at := strings.IndexAny(raw[start:], "/?#"); at >= 0 {
		end = start + at
	}
	at := strings.LastIndexByte(raw[start:end], '@')
	if at < 0 {
		return raw
	}
	at += start
	var escaped strings.Builder
	changed := false
	const hex = "0123456789ABCDEF"
	for i := start; i < at; i++ {
		c := raw[i]
		if c < 128 {
			escaped.WriteByte(c)
			continue
		}
		changed = true
		escaped.WriteByte('%')
		escaped.WriteByte(hex[c>>4])
		escaped.WriteByte(hex[c&15])
	}
	if !changed {
		return raw
	}
	return raw[:start] + escaped.String() + raw[at:]
}

// RequestsProxy can resolve http.Transport's HTTP(S) proxy callback. Callers
// supporting SOCKS must use NewRequestsTransport to preserve local/remote DNS
// semantics. Unlike Go's cached
// ProxyFromEnvironment it observes each request's current environment and does
// not add an implicit localhost bypass. This callback only resolves routing;
// the caller retains TLS, target-origin and redirect/credential policy.
func RequestsProxy(req *http.Request) (*url.URL, error) {
	if req == nil {
		return nil, errors.New("requests_proxy_missing_request")
	}
	return RequestsEnvironmentProxy(os.Environ(), req.URL)
}

type environmentItem struct{ name, value string }

func environmentValues(values []string) []environmentItem {
	out := []environmentItem{}
	indices := map[string]int{}
	for _, line := range values {
		at := strings.IndexByte(line, '=')
		if at <= 0 {
			continue
		}
		name, value := line[:at], line[at+1:]
		if i, ok := indices[name]; ok {
			out[i].value = value
		} else {
			indices[name] = len(out)
			out = append(out, environmentItem{name, value})
		}
	}
	return out
}
func environmentValue(values []environmentItem, name string) (string, bool) {
	for _, v := range values {
		if v.name == name {
			return v.value, true
		}
	}
	return "", false
}
func requestsGetproxies(environment []environmentItem) map[string]string {
	proxies := map[string]string{}
	for _, item := range environment {
		name := strings.ToLower(item.name)
		if strings.HasSuffix(name, "_proxy") && item.value != "" {
			proxies[strings.TrimSuffix(name, "_proxy")] = item.value
		}
	}
	if _, cgi := environmentValue(environment, "REQUEST_METHOD"); cgi {
		delete(proxies, "http")
	}
	for _, item := range environment {
		if strings.HasSuffix(item.name, "_proxy") {
			name := strings.ToLower(strings.TrimSuffix(item.name, "_proxy"))
			if item.value == "" {
				delete(proxies, name)
			} else {
				proxies[name] = item.value
			}
		}
	}
	return proxies
}
func requestsBypass(environment []environmentItem, proxies map[string]string, host, port string) bool {
	return requestsBypassOverride(environment, proxies, host, port, nil)
}

func requestsBypassOverride(environment []environmentItem, proxies map[string]string, host, port string, override *string) bool {
	noProxy, _ := environmentValue(environment, "no_proxy")
	if noProxy == "" {
		noProxy, _ = environmentValue(environment, "NO_PROXY")
	}
	if override != nil {
		noProxy = *override
	}
	if noProxy != "" {
		if ip, ipv4 := requestsIPv4(host); ipv4 {
			for _, candidate := range strings.Split(strings.ReplaceAll(noProxy, " ", ""), ",") {
				if candidate == "" {
					continue
				}
				if network, bits, ok := requestsCIDR(candidate); ok {
					mask := uint32(0xffffffff) << uint(32-bits)
					if ip&mask == network&mask {
						return true
					}
				} else if candidate == host {
					return true
				}
			}
		} else {
			hostPort := host
			if port != "" && port != "0" {
				hostPort += ":" + port
			}
			for _, candidate := range strings.Split(strings.ReplaceAll(noProxy, " ", ""), ",") {
				if candidate == "" {
					continue
				}
				candidate = strings.TrimLeft(candidate, ".")
				if host == candidate || hostPort == candidate || strings.HasSuffix(host, "."+candidate) || strings.HasSuffix(hostPort, "."+candidate) {
					return true
				}
			}
		}
	}
	// requests then calls urllib.request.proxy_bypass(hostname), whose Linux
	// environment fallback is case-insensitive and also understands '*'. The
	// port is intentionally absent here; requests passed only parsed.hostname.
	noProxy = proxies["no"]
	if override != nil {
		noProxy = *override
	}
	if noProxy == "*" {
		return true
	}
	for _, candidate := range strings.Split(noProxy, ",") {
		candidate = strings.ToLower(strings.TrimLeft(strings.TrimSpace(candidate), "."))
		if candidate != "" && (host == candidate || strings.HasSuffix(host, "."+candidate)) {
			return true
		}
	}
	return false
}
func requestsCIDR(s string) (uint32, int, bool) {
	if strings.Count(s, "/") != 1 {
		return 0, 0, false
	}
	parts := strings.SplitN(s, "/", 2)
	bits, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || bits < 1 || bits > 32 {
		return 0, 0, false
	}
	ip, ok := requestsIPv4(parts[0])
	return ip, bits, ok
}

// socket.inet_aton accepts the historical one-to-four-part IPv4 notation,
// octal and hexadecimal components. net.ParseIP intentionally does not, so it
// cannot replace the requests source's CIDR/address test here.
func requestsIPv4(s string) (uint32, bool) {
	// POSIX inet_aton accepts a trailing whitespace suffix. URL preparation is
	// the caller's separate responsibility; this is only the source address test.
	if i := strings.IndexAny(s, " \t\r\n\v\f"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return 0, false
	}
	values := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" || p[0] == '+' || p[0] == '-' {
			return 0, false
		}
		base := 10
		if len(p) > 1 && p[0] == '0' {
			base = 8
			if len(p) > 2 && (p[1] == 'x' || p[1] == 'X') {
				base = 16
				p = p[2:]
			}
		}
		v, err := strconv.ParseUint(p, base, 32)
		if err != nil {
			return 0, false
		}
		values[i] = v
	}
	var result uint64
	for i, v := range values {
		last := i == len(values)-1
		bits := 8
		if last {
			bits = 8 * (5 - len(values))
		}
		limit := uint64(1) << uint(bits)
		if v >= limit {
			return 0, false
		}
		if last {
			result = (result << uint(bits)) | v
		} else {
			result = (result << 8) | v
		}
	}
	return uint32(result), true
}
