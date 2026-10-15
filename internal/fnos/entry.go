package fnos

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"light-media/internal/logging"
)

const versionAPIPath = "/v/api/v1/sys/version"
const maxEntryRedirects = 3

// fnOS appliances commonly serve a self-signed certificate at their local HTTPS
// port. Clone the transport so compatibility never changes the process-wide TLS
// policy, and restrict it to the configured literal LAN/loopback target.
func newFNOSHTTPClient(host string, transport http.RoundTripper) *http.Client {
	entry, _ := url.Parse(host) // host has already passed NormalizeHost.
	private := privateFNOSHostname(entry.Hostname())
	if transport == nil {
		transport = http.DefaultTransport
	}
	if base, ok := transport.(*http.Transport); ok {
		cloned := base.Clone()
		if cloned.TLSClientConfig == nil {
			cloned.TLSClientConfig = &tls.Config{}
		} else {
			cloned.TLSClientConfig = cloned.TLSClientConfig.Clone()
		}
		cloned.TLSClientConfig.InsecureSkipVerify = private // Only the configured LAN appliance is allowed below.
		if private {
			cloned.Proxy = nil
		}
		transport = cloned
	}
	return &http.Client{
		Timeout:   12 * time.Second,
		Transport: logging.Transport(fnosTargetTransport{entry: entry, base: transport}),
		// Credential-bearing requests never follow redirects. Only signed,
		// unauthenticated version GETs resolve an allowed entry upgrade below.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type fnosTargetTransport struct {
	entry *url.URL
	base  http.RoundTripper
}

func (t fnosTargetTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !allowedFNOSTarget(t.entry, request.URL) {
		return nil, &ServiceError{Code: "redirect_blocked"}
	}
	return t.base.RoundTrip(request)
}

func fnosPort(target *url.URL) string {
	if target.Port() != "" {
		return target.Port()
	}
	if target.Scheme == "https" {
		return "443"
	}
	return "80"
}

func allowedFNOSTarget(configured, target *url.URL) bool {
	if target == nil || target.User != nil || !strings.EqualFold(target.Hostname(), configured.Hostname()) ||
		(target.Scheme != "http" && target.Scheme != "https") {
		return false
	}
	if target.Scheme == configured.Scheme && fnosPort(target) == fnosPort(configured) {
		return true
	}
	return configured.Scheme == "http" && privateFNOSHostname(configured.Hostname()) && target.Scheme == "https" && fnosPort(target) == "5667"
}

// Only resolve the very same signed version endpoint on the configured LAN
// host. Only port 5667 is accepted for the appliance's HTTP -> HTTPS upgrade,
// while host changes, HTTP targets, URL credentials, and alternate API paths
// are rejected before making the next request.
func redirectedVersionEntry(configured string, requestURL *url.URL, location string) (string, bool) {
	if location == "" || len(location) > 4096 || strings.Contains(location, "#") {
		return "", false
	}
	entry, err := url.Parse(configured)
	if err != nil || !privateFNOSHostname(entry.Hostname()) {
		return "", false
	}
	target, err := requestURL.Parse(location)
	if err != nil || target.Scheme != "https" || !allowedFNOSTarget(entry, target) || target.User != nil ||
		target.Path != versionAPIPath || target.RawPath != "" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || target.Opaque != "" {
		return "", false
	}
	base := *target
	base.Path = "/v"
	host, err := NormalizeHost(base.String())
	return host, err == nil
}

func (s *Service) resolveSessionEntry(ctx context.Context) (string, int, error) {
	entry, _ := url.Parse(s.host)
	if !privateFNOSHostname(entry.Hostname()) {
		return s.host, 0, nil
	}
	probe := client{host: s.host, http: s.http}
	data, err := probe.request(ctx, "/sys/version", nil)
	if err != nil {
		code := strings.ToLower(err.Error())
		switch {
		case strings.HasPrefix(code, "http_3"), code == "redirect_limit":
			code = "redirect_blocked"
		case code == "transport_failed", code == "body_failed", strings.HasPrefix(code, "business_"), strings.HasPrefix(code, "http_"):
			code = "unavailable"
		case code == "invalid_data", code == "invalid_envelope", code == "body_limit":
			code = "invalid_response"
		}
		return "", probe.requests, &ServiceError{Code: code}
	}
	var version struct {
		Frontend string `json:"version"`
		Backend  string `json:"mediasrvVersion"`
	}
	if json.Unmarshal(data, &version) != nil || !versionLike.MatchString(version.Frontend) || !versionLike.MatchString(version.Backend) {
		return "", probe.requests, &ServiceError{Code: "invalid_response"}
	}
	return probe.host, probe.requests, nil
}
