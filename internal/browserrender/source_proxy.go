package browserrender

import (
	"net/url"
	"runtime"
	"strings"
)

// ProxySettings is the ordinary source helper's proxy dictionary. It is an
// internal host-to-renderer value, never a plugin SDK credential surface.
type ProxySettings struct{ Server, Username, Password string }

// The pinned source wrapper supplies SOCKS as a command-line proxy. Its free
// Linux amd64 HTTP-auth build also does so, while the pinned arm64 build uses
// Playwright's proxy options. Only the latter force loopback through a proxy.
func (p ProxySettings) inlineOnNativeRuntime() bool {
	return strings.HasPrefix(p.Server, "socks") || runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && p.Username != ""
}

func (r Request) proxySettings() (*ProxySettings, error) {
	if r.SourceProxy != nil {
		return r.SourceProxy, nil
	}
	if r.Proxy == "" {
		return nil, nil
	}
	u, err := url.Parse(r.Proxy)
	if err != nil {
		return nil, &Error{Code: "browser_proxy_invalid"}
	}
	p := &ProxySettings{}
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	u.User = nil
	p.Server = u.String()
	return p, nil
}

func (p ProxySettings) nativeURL() (string, error) {
	u, err := url.Parse(p.Server)
	// The source IPv6 spelling reaches FS unchanged. Playwright rewrites its
	// malformed URI to an unrelated host (http://http); the host credential
	// boundary requires a safe None/error instead of dialing that new host.
	if err != nil || u.Hostname() == "" {
		return "", &Error{Code: "browser_proxy_invalid"}
	}
	// The paired wrapper passes socks5h literally; the pinned Chromium rejects
	// it with ERR_NO_SUPPORTED_PROXIES. Do not silently turn it into working
	// SOCKS5 via the Go relay. Ordinary requests' socks5h support is separate.
	if u.Scheme == "socks5h" {
		return "", &Error{Code: "browser_proxy_invalid"}
	}
	if p.Username != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u.String(), nil
}
