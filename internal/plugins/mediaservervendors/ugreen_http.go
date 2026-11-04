package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins/mediaserverruntime"
)

type UgreenHTTPSessionDependencies struct {
	BaseTransport *http.Transport
	Current       func(context.Context) error
	Cookies       UgreenSessionCookieStore
}

type ugreenHTTPSession struct {
	origin       string
	pool         *networkproxy.RequestsProxySession
	currentGuard func(context.Context) error
	cookies      UgreenSessionCookieStore
}

var _ UgreenPrivateSession = (*ugreenHTTPSession)(nil)

// NewUgreenHTTPSession owns one private route pool and one mandatory persistent
// cookie store. Origin is host-selected, never a URL supplied by a worker.
func NewUgreenHTTPSession(ctx context.Context, origin string, deps UgreenHTTPSessionDependencies) (UgreenPrivateSession, error) {
	if deps.BaseTransport == nil || deps.Current == nil || ugreenAPINil(deps.Cookies) {
		return nil, &EmbyJellyfinBoundaryError{Kind: "missing Ugreen HTTP session dependency"}
	}
	target, err := url.Parse(origin)
	if err != nil || target == nil || target.Host == "" || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, &EmbyJellyfinBoundaryError{Kind: "invalid selected Ugreen origin"}
	}
	s := &ugreenHTTPSession{origin: ejOrigin(target), currentGuard: deps.Current, cookies: deps.Cookies}
	if err := s.current(ctx); err != nil {
		return nil, err
	}
	// The reusable transport is NOT owned by this constructor's callback. Every
	// real request carries its fresh context through requestsDialBinding. Capturing
	// ctx here would kill later retained-instance calls after this callback ends.
	base, err := ugreenPrivateTransport(context.Background(), deps.BaseTransport, 20*time.Second)
	if err != nil {
		return nil, err
	}
	pool, err := networkproxy.NewRequestsProxySession(base)
	if err != nil {
		return nil, err
	}
	s.pool = pool
	if err := s.current(ctx); err != nil {
		pool.CloseIdleConnections()
		return nil, err
	}
	return s, nil
}

func (s *ugreenHTTPSession) current(ctx context.Context) error {
	if ctx == nil {
		return &EmbyJellyfinBoundaryError{Kind: "Ugreen session requires context"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.currentGuard(ctx); err != nil {
		return &EmbyJellyfinBoundaryError{Kind: "Ugreen session current authority", cause: err}
	}
	return ctx.Err()
}
func (s *ugreenHTTPSession) Cookies(ctx context.Context) (json.RawMessage, error) {
	if err := s.current(ctx); err != nil {
		return nil, err
	}
	result, err := s.cookies.Export(ctx)
	if guard := s.current(ctx); guard != nil {
		return nil, guard
	}
	return ejCopy(result), err
}
func (s *ugreenHTTPSession) UpdateCookies(ctx context.Context, fields []UgreenHTTPField) error {
	if err := s.current(ctx); err != nil {
		return err
	}
	err := s.cookies.Update(ctx, append([]UgreenHTTPField(nil), fields...))
	if guard := s.current(ctx); guard != nil {
		return guard
	}
	return err
}
func (s *ugreenHTTPSession) Close(ctx context.Context) error {
	if err := s.current(ctx); err != nil {
		return err
	}
	s.pool.CloseIdleConnections()
	// Source close() neither logs out, clears the jar, nor forbids later use.
	return s.current(ctx)
}
func (s *ugreenHTTPSession) failure(ctx context.Context, err error) error {
	if guard := s.current(ctx); guard != nil {
		return guard
	}
	leaf := err
	if wrapped, ok := leaf.(*url.Error); ok {
		leaf = wrapped.Err
	}
	if source, ok := leaf.(*mediaserverruntime.SourceError); ok && source != nil && (source.Class == "UnicodeEncodeError" || source.Class == "InvalidHeader") {
		return source
	}
	if zspaceHTTPNetworkError(err) {
		return &mediaserverruntime.SourceError{Class: "RequestException"}
	}
	return &EmbyJellyfinBoundaryError{Kind: "unclassified Ugreen HTTP failure", cause: err}
}

func (s *ugreenHTTPSession) Request(ctx context.Context, input UgreenSessionRequest) (*UgreenSessionResponse, error) {
	if err := s.current(ctx); err != nil {
		return nil, err
	}
	if input.Timeout != 20*time.Second || (input.Method != http.MethodGet && input.Method != http.MethodPost) {
		return nil, &EmbyJellyfinBoundaryError{Kind: "Ugreen HTTP request contract"}
	}
	var body []byte
	if len(input.JSON) > 0 && !ejNull(input.JSON) {
		text, err := zspaceHTTPJSON(input.JSON)
		if err != nil {
			return nil, err
		}
		body = []byte(text)
	}
	req, err := http.NewRequestWithContext(ctx, input.Method, input.URL, bytes.NewReader(body))
	if err != nil {
		return nil, ejSource("InvalidURL")
	}
	if req.URL.User != nil || ejOrigin(req.URL) != s.origin {
		return nil, &EmbyJellyfinBoundaryError{Kind: "Ugreen request origin"}
	}
	fields := make([]ZSpaceField, 0, len(input.Headers))
	for _, field := range input.Headers {
		fields = append(fields, ZSpaceField{Name: field.Name, Value: ugreenKey(field.Value)})
	}
	req.Header, err = zspaceHTTPHeaders(fields)
	if err != nil {
		return nil, err
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	params := []ZSpaceField{}
	if len(input.Params) > 0 && !ejNull(input.Params) {
		object, err := ejObject(input.Params)
		if err != nil {
			return nil, err
		}
		for _, key := range object.keys {
			name, err := ejText(key)
			if err != nil {
				return nil, err
			}
			params = append(params, ZSpaceField{Name: name, Value: object.values[string(key)]})
		}
	}
	query, err := zspaceHTTPParams(params)
	if err != nil {
		return nil, err
	}
	if query != "" {
		if req.URL.RawQuery != "" {
			req.URL.RawQuery += "&"
		}
		req.URL.RawQuery += query
	}
	routed, err := s.pool.NewRequestTransport(os.Environ(), nil, req.URL)
	if err != nil {
		return nil, &EmbyJellyfinBoundaryError{Kind: "Ugreen proxy configuration", cause: err}
	}
	// A new logical request gets a fresh proxy merge; cookies and actual route
	// pools are retained by s. No new fallback, proxy or environment mutation.
	client := &http.Client{Transport: &ugreenCookieRoundTripper{session: s, next: routed}, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if err := s.current(next.Context()); err != nil {
			return err
		}
		if next.URL.User != nil || ejOrigin(next.URL) != s.origin {
			return &EmbyJellyfinBoundaryError{Kind: "cross-origin Ugreen redirect"}
		}
		if len(via) > 30 {
			return &ejRedirectLimitError{}
		}
		return nil
	}}
	req, phaseFailure, cancelRequest := ugreenHTTPPhaseRequest(req)
	defer cancelRequest()
	response, err := client.Do(req)
	if guard := s.current(ctx); guard != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, guard
	}
	if phase := phaseFailure(); phase != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, phase
	}
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, s.failure(ctx, err)
	}
	if response == nil || response.Body == nil {
		return nil, &EmbyJellyfinBoundaryError{Kind: "nil Ugreen HTTP response"}
	}
	content, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if guard := s.current(ctx); guard != nil {
		return nil, guard
	}
	if phase := phaseFailure(); phase != nil {
		return nil, phase
	}
	if readErr != nil {
		return nil, s.failure(ctx, readErr)
	}
	if closeErr != nil {
		return nil, s.failure(ctx, closeErr)
	}
	content, err = MediaServerRequestsContent(ctx, content, response.Header.Get("Content-Encoding"), s.current)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(response.Header))
	for name := range response.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	headers := make([]UgreenHTTPField, 0, len(names))
	for _, name := range names {
		headers = append(headers, UgreenHTTPField{Name: name, Value: strings.Join(response.Header.Values(name), ", ")})
	}
	contentType := response.Header.Get("Content-Type")
	return &UgreenSessionResponse{Headers: headers, JSON: func(call context.Context) (json.RawMessage, error) {
		if err := s.current(call); err != nil {
			return nil, err
		}
		raw, err := ejResponseJSON(content, contentType)
		if guard := s.current(call); guard != nil {
			return nil, guard
		}
		return raw, err
	}}, nil
}

type ugreenCookieRoundTripper struct {
	session *ugreenHTTPSession
	next    http.RoundTripper
}

func (t *ugreenCookieRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	s := t.session
	if err := s.current(request.Context()); err != nil {
		return nil, err
	}
	if request.URL.User != nil || ejOrigin(request.URL) != s.origin {
		return nil, &EmbyJellyfinBoundaryError{Kind: "Ugreen cookie request origin"}
	}
	req := request.Clone(request.Context())
	// These private API requests never supply an explicit Cookie argument. Each
	// redirect rebuilds the header from the persistent jar instead of retaining an
	// earlier path's Cookie header copied by net/http.
	req.Header.Del("Cookie")
	cookie, err := s.cookies.Header(req.Context(), req.URL)
	if err != nil {
		return nil, err
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if err := s.current(req.Context()); err != nil {
		return nil, err
	}
	response, err := t.next.RoundTrip(req)
	if guard := s.current(req.Context()); guard != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, guard
	}
	if err != nil {
		return response, err
	}
	if response == nil {
		return nil, &EmbyJellyfinBoundaryError{Kind: "nil Ugreen transport response"}
	}
	if err := s.cookies.Extract(req.Context(), req.URL, response.Header.Clone()); err != nil {
		if response.Body != nil {
			response.Body.Close()
		}
		return nil, err
	}
	if guard := s.current(req.Context()); guard != nil {
		if response.Body != nil {
			response.Body.Close()
		}
		return nil, guard
	}
	return response, nil
}
