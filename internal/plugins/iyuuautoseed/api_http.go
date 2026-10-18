package iyuuautoseed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TrustedAPIBinding is supplied exclusively by the authenticated host. It is
// never serialized into the SDK request/response or accepted from a worker.
type TrustedAPIBinding struct{ Token string }
type APITransportOptions struct {
	Guard   func(context.Context) error
	RootCAs *x509.CertPool
	Timeout time.Duration
	// LoopbackOrigin is a host-only synthetic test seam, not a plugin setting.
	// It accepts literal loopback IPs exclusively, and disables environment proxy.
	LoopbackOrigin string
}
type APITransport struct {
	origin string
	token  string
	guard  func(context.Context) error
	client *http.Client
}

const maxAPIResponse = 16 << 20

func NewAPITransport(binding TrustedAPIBinding, options APITransportOptions) (*APITransport, error) {
	if strings.ContainsAny(binding.Token, "\r\n") {
		return nil, errors.New("invalid IYUU token header")
	}
	origin := "https://2025.iyuu.cn"
	if options.LoopbackOrigin != "" {
		u, e := url.Parse(options.LoopbackOrigin)
		if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, errors.New("invalid test origin")
		}
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("test origin must be literal loopback")
		}
		origin = strings.TrimRight(u.String(), "/")
	}
	if options.Guard == nil {
		return nil, errors.New("IYUU host guard is required")
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	if timeout < 0 {
		return nil, errors.New("invalid timeout")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: options.RootCAs}, DisableCompression: true}
	// The source helper does not pass settings.PROXY; RequestUtils uses its
	// environment route. The private loopback fixture must remain direct.
	if options.LoopbackOrigin == "" {
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &APITransport{origin: origin, token: binding.Token, guard: options.Guard, client: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("IYUU redirect denied") }}}, nil
}

func (t *APITransport) Close() { t.client.CloseIdleConnections() }
func (t *APITransport) Call(ctx context.Context, r APIRequest) (*APIResponse, error) {
	if err := t.guard(ctx); err != nil {
		return nil, errors.New("IYUU owner not authorized")
	}
	var path, method string
	var body []byte
	var err error
	switch r.Operation {
	case SitesIndex:
		path = "/reseed/sites/index"
		method = http.MethodGet
		if len(r.Params) != 0 {
			return nil, errors.New("unexpected site-index parameters")
		}
	case ReportExisting:
		path = "/reseed/sites/reportExisting"
		method = http.MethodPost
		if !exactKeys(r.Params, "sid_list") {
			return nil, errors.New("invalid report parameters")
		}
		body, err = orderedJSON([]jsonField{{"sid_list", r.Params["sid_list"]}}, true)
	case ReseedIndex:
		path = "/reseed/index/index"
		method = http.MethodPost
		if !exactKeys(r.Params, "hash", "sha1", "sid_sha1", "timestamp", "version") {
			return nil, errors.New("invalid reseed parameters")
		}
		body, err = orderedJSON([]jsonField{{"hash", r.Params["hash"]}, {"sha1", r.Params["sha1"]}, {"sid_sha1", r.Params["sid_sha1"]}, {"timestamp", r.Params["timestamp"]}, {"version", r.Params["version"]}}, true)
	default:
		return nil, errors.New("unsupported IYUU operation")
	}
	if err != nil {
		return nil, errors.New("invalid IYUU payload")
	}
	req, err := http.NewRequestWithContext(ctx, method, t.origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid IYUU request")
	}
	req.Header.Set("token", t.token)
	// RequestUtils receives an explicit header map, so its accept_type argument
	// is ignored. requests supplies */* and its own User-Agent; preserve these.
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "python-requests/2.34.2")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("IYUU HTTP transport failed")
	}
	defer res.Body.Close()
	raw, err := readSourceContent(res.Body, res.Header.Get("Content-Encoding"))
	if err != nil {
		return nil, errors.New("IYUU response read failed")
	}
	if len(raw) > maxAPIResponse {
		return nil, errors.New("IYUU response exceeds limit")
	}
	if err := t.guard(ctx); err != nil {
		return nil, errors.New("IYUU owner no longer authorized")
	}
	decoded, err := responseJSONText(raw, res.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	reason := res.Status
	if i := strings.IndexByte(reason, ' '); i >= 0 {
		reason = reason[i+1:]
	}
	return &APIResponse{Status: res.StatusCode, Reason: reason, Body: []byte(decoded)}, nil
}

func exactKeys(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
