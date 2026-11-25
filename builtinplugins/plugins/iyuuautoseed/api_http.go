package iyuuautoseed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"light-media/builtinplugins/internal/requestsproxy"
)

// TrustedAPIBinding carries the current instance's private configuration. The
// token stays in worker memory and is sent only to the fixed IYUU origin.
type TrustedAPIBinding struct{ Token string }
type APITransportOptions struct {
	Guard   func(context.Context) error
	RootCAs *x509.CertPool
	// Timeout is the Requests socket-idle timeout, not a total request limit.
	Timeout time.Duration
	// Environment is a trusted SDK proxy-environment snapshot. nil snapshots
	// os.Environ for standalone CLI compatibility; nonnil empty means direct.
	Environment []string
	// TransientRetry is a trusted host-only reliability policy, not a user
	// setting. The standalone/source transport keeps its single-attempt default.
	TransientRetry bool
	// LoopbackOrigin is a synthetic test seam, not a plugin setting.
	// It accepts literal loopback IPs exclusively, and disables environment proxy.
	LoopbackOrigin string
}
type APITransport struct {
	origin         string
	token          string
	guard          func(context.Context) error
	timeout        time.Duration
	environment    []string
	rootCAs        *x509.CertPool
	transientRetry bool
	retryDelay     time.Duration
	// Trusted in-package observer; never receives URLs, tokens or raw errors.
	onRetry func(context.Context, apiRetryEvent)
}

type apiRetryEvent struct {
	Operation Operation
	Attempt   int // The next attempt, 2; there is at most one transport retry.
	Kind      string
	Delay     time.Duration
}

// APITransientRetryError reports exhausted host reliability attempts without
// retaining a raw error, URL, proxy credentials, response body or token.
// Standalone/source-compatible transports never return this error.
type APITransientRetryError struct {
	Operation Operation
	Attempts  int
	Kind      string
}

func (*APITransientRetryError) Error() string { return "IYUU transient transport retry exhausted" }

const maxAPIResponse = 16 << 20

var errAPIRedirectDenied = errors.New("IYUU redirect denied")

func NewAPITransport(binding TrustedAPIBinding, options APITransportOptions) (*APITransport, error) {
	if strings.ContainsAny(binding.Token, "\r\n") {
		return nil, errors.New("invalid IYUU token header")
	}
	origin := "https://2025.iyuu.cn"
	if options.LoopbackOrigin != "" {
		u, e := url.Parse(options.LoopbackOrigin)
		if e != nil || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || (u.EscapedPath() != "" && u.EscapedPath() != "/") || (u.Scheme != "http" && u.Scheme != "https") {
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
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}
	// Never retain the SDK's mutable slice, or consult the worker environment
	// later. The private loopback fixture must remain direct even with ALL_PROXY.
	environment = append([]string{}, environment...)
	if options.LoopbackOrigin != "" {
		environment = []string{}
	}
	var roots *x509.CertPool
	if options.RootCAs != nil {
		roots = options.RootCAs.Clone()
	}
	return &APITransport{origin: origin, token: binding.Token, guard: options.Guard, timeout: timeout, environment: environment, rootCAs: roots, transientRetry: options.TransientRetry, retryDelay: 2 * time.Second}, nil
}

// Each Call owns and closes its transport; there is no shared idle pool.
func (t *APITransport) Close() {}
func (t *APITransport) Call(ctx context.Context, r APIRequest) (*APIResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	// Serialize once, including the timestamp/signature. A retry is the same
	// validated operation and payload, not another Helper/business invocation.
	for attempt := 1; ; attempt++ {
		response, sourceErr, kind := t.callOnce(ctx, path, method, body)
		if response != nil || sourceErr != nil || !t.transientRetry || kind == "" {
			return response, sourceErr
		}
		if attempt == 2 {
			return nil, &APITransientRetryError{Operation: r.Operation, Attempts: attempt, Kind: kind}
		}
		if t.onRetry != nil {
			t.onRetry(ctx, apiRetryEvent{Operation: r.Operation, Attempt: 2, Kind: kind, Delay: t.retryDelay})
		}
		if err := t.waitRetry(ctx); err != nil {
			return nil, err
		}
	}
}

// callOnce owns the entire attempt. All body/idle connection cleanup completes
// before returning, including when a read stalls or a partial body is discarded.
// The final string is a whitelist class of an actual wire failure, never a
// decoder/JSON error or private original cause.
func (t *APITransport) callOnce(ctx context.Context, path, method string, body []byte) (*APIResponse, error, string) {
	if err := ctx.Err(); err != nil {
		return nil, err, ""
	}
	if err := t.guard(ctx); err != nil {
		return nil, errors.New("IYUU owner no longer authorized"), ""
	}
	req, err := http.NewRequestWithContext(ctx, method, t.origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid IYUU request"), ""
	}
	req.Header.Set("token", t.token)
	// RequestUtils receives an explicit header map, so its accept_type argument
	// is ignored. requests supplies */* and its own User-Agent; preserve these.
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "python-requests/2.34.2")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	base := requestsproxy.NewRequestsTimeoutTransport(t.timeout)
	base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: t.rootCAs}
	base.DisableCompression = true
	transport, err := requestsproxy.NewRequestsProxyTransport(base, t.environment, nil, req.URL)
	if err != nil {
		return nil, t.transportFailure(ctx, err), apiTransientFailureKind(err)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errAPIRedirectDenied }}
	res, err := client.Do(req)
	if err != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		return nil, t.transportFailure(ctx, err), apiTransientFailureKind(err)
	}
	defer res.Body.Close()
	reader := &apiBodyReadTracker{reader: res.Body}
	raw, err := readAPIContent(reader, strings.Join(res.Header.Values("Content-Encoding"), ","))
	if err != nil {
		kind := ""
		// A decoder's unexpected EOF is not a socket failure. Nor do HTTP
		// error statuses become retryable because their body is truncated.
		if res.StatusCode >= 200 && res.StatusCode < 300 && !errors.Is(err, errAPIResponseLimit) {
			kind = apiTransientFailureKind(reader.failure)
		}
		return nil, t.transportFailure(ctx, err), kind
	}
	if err := ctx.Err(); err != nil {
		return nil, err, ""
	}
	if err := t.guard(ctx); err != nil {
		return nil, errors.New("IYUU owner no longer authorized"), ""
	}
	decoded, err := apiResponseJSONText(raw, res.Header.Get("Content-Type"))
	if err != nil {
		return nil, err, ""
	}
	reason := res.Status
	if i := strings.IndexByte(reason, ' '); i >= 0 {
		reason = reason[i+1:]
	}
	return &APIResponse{Status: res.StatusCode, Reason: reason, Body: []byte(decoded)}, nil, ""
}

func (t *APITransport) waitRetry(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.guard(ctx); err != nil {
		return errors.New("IYUU owner no longer authorized")
	}
	if t.retryDelay > 0 {
		timer := time.NewTimer(t.retryDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.guard(ctx); err != nil {
		return errors.New("IYUU owner no longer authorized")
	}
	return nil
}

type apiBodyReadTracker struct {
	reader  io.Reader
	failure error
}

func (r *apiBodyReadTracker) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.failure = err
	}
	return n, err
}

func apiTransientFailureKind(err error) string {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errAPIRedirectDenied) || errors.Is(err, errAPIResponseLimit) {
		return ""
	}
	var certificate *tls.CertificateVerificationError
	var hostname x509.HostnameError
	var unknownAuthority x509.UnknownAuthorityError
	var invalidCertificate x509.CertificateInvalidError
	var roots x509.SystemRootsError
	if errors.As(err, &certificate) || errors.As(err, &hostname) || errors.As(err, &unknownAuthority) || errors.As(err, &invalidCertificate) || errors.As(err, &roots) {
		return ""
	}
	// url.Error itself satisfies net.Error, but its immediate private proxy
	// wrapper does not. A single errors.As would stop at that outer URL error
	// with Timeout()==false and miss the actual socket timeout further inside.
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if timeout, ok := cause.(net.Error); ok && timeout.Timeout() {
			return "timeout"
		}
	}
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED):
		return "reset"
	case errors.Is(err, syscall.EPIPE):
		return "broken_pipe"
	}
	return ""
}

func (t *APITransport) transportFailure(ctx context.Context, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.guard(ctx); err != nil {
		return errors.New("IYUU owner no longer authorized")
	}
	var verification *tls.CertificateVerificationError
	switch {
	case errors.Is(cause, errAPIRedirectDenied):
		return errAPIRedirectDenied
	case errors.Is(cause, errAPIResponseLimit):
		return errAPIResponseLimit
	case errors.As(cause, &verification):
		return errors.New("IYUU TLS verification failed")
	}
	// RequestUtils defaults to swallowing Requests connection/body failures.
	// The helper maps a missing response to the source's ordinary failure.
	return nil
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
