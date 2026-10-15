package sites

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var domainTLD = regexp.MustCompile(`^(?:[a-z]{2,63}|xn--[a-z0-9-]{2,59})$`)

// NormalizeNexusPHPOrigin validates an explicitly configured origin without
// making a network request. Adapter choice for known special sites belongs to
// the caller. DNS addresses are checked again by the generic client's dialer.
func NormalizeNexusPHPOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || (u.Port() != "" && u.Port() != "443") {
		return "", errors.New("site_invalid_origin")
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 253 || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return "", errors.New("site_public_https_origin_required")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !domainLabel.MatchString(label) {
			return "", errors.New("site_invalid_origin")
		}
	}
	tld := labels[len(labels)-1]
	if !domainTLD.MatchString(tld) {
		return "", errors.New("site_invalid_origin")
	}
	for _, reserved := range []string{"localhost", "local", "internal", "lan", "home", "test", "invalid", "example", "onion", "arpa"} {
		if tld == reserved {
			return "", errors.New("site_public_https_origin_required")
		}
	}
	return "https://" + host + "/", nil
}

func validUserAgent(s string) bool {
	if len(s) > 1024 {
		return false
	}
	for _, b := range []byte(s) {
		if b < 32 || b == 127 {
			return false
		}
	}
	return true
}

// Resolve once and dial the checked address, keeping TLS verification bound to
// the configured hostname. This prevents a public-looking hostname from sending
// credentials to a local service through private DNS or DNS rebinding.
// A shared pool avoids leaving an idle pool behind for every application call.
var nexusHTTPTransport = newPublicSiteTransport()

func publicSiteTransport() http.RoundTripper { return nexusHTTPTransport }

func newPublicSiteTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: 25 * time.Second, KeepAlive: 30 * time.Second}
		return dialPublicSite(ctx, network, address, net.DefaultResolver.LookupNetIP, dialer.DialContext)
	}
	return t
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func publicSiteIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func dialPublicSite(ctx context.Context, network, address string,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, errors.New("site_invalid_origin")
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("site_transport_error")
	}
	for _, ip := range ips {
		if !publicSiteIP(ip) {
			return nil, errors.New("site_public_https_origin_required")
		}
	}
	for _, ip := range ips {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("site_transport_error")
}
