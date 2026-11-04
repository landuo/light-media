package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins/mediaserverruntime"
)

// This host-owned constructor supplies the concrete Request port to
// ZSpaceFactory. Origin is the selected configuration, never a worker URL.
// No global client/CA/proxy setting, cross-call session or direct fallback is
// introduced. BaseTransport must be the host's private context-dial transport.
type ZSpaceHTTPDependencies struct {
	BaseTransport *http.Transport
	Origin        json.RawMessage
	Current       func(context.Context) error
	Report        func(ZSpaceDiagnostic)
}

func zspaceRequestClient(transport http.RoundTripper, jar http.CookieJar, boundOrigin string, allowRedirects bool, current func(context.Context) error, ctx context.Context) *http.Client {
	return &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if err := current(ctx); err != nil {
			return err
		}
		if ejOrigin(next.URL) != boundOrigin || next.URL.User != nil {
			return &EmbyJellyfinBoundaryError{Kind: "cross-origin ZSpace redirect"}
		}
		if !allowRedirects {
			return http.ErrUseLastResponse
		}
		// Go via includes the first request; Requests response.history does not.
		if len(via) > 30 {
			return &ejRedirectLimitError{}
		}
		return nil
	}}
}

func NewZSpaceHTTPPort(deps ZSpaceHTTPDependencies) (func(context.Context, ZSpaceRequest) (*ZSpaceResponse, error), error) {
	if deps.BaseTransport == nil || deps.Current == nil || deps.Report == nil {
		return nil, &EmbyJellyfinBoundaryError{Kind: "missing ZSpace HTTP dependency"}
	}
	originText, err := ejText(deps.Origin)
	if err != nil {
		return nil, err
	}
	origin, err := url.Parse(originText)
	if err != nil || origin == nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil {
		return nil, &EmbyJellyfinBoundaryError{Kind: "invalid selected ZSpace origin"}
	}
	boundOrigin := ejOrigin(origin)
	current := func(ctx context.Context) error {
		if err := ejContext(ctx); err != nil {
			return err
		}
		if err := deps.Current(ctx); err != nil {
			return &EmbyJellyfinBoundaryError{Kind: "ZSpace current authority", cause: err}
		}
		return ctx.Err()
	}
	failure := func(ctx context.Context, err error) (*ZSpaceResponse, error) {
		if guard := current(ctx); guard != nil {
			return nil, guard
		}
		if zspaceHTTPNetworkError(err) {
			deps.Report(ZSpaceDiagnostic{Operation: "request", Severity: "debug", Class: "RequestException"})
			return nil, nil
		}
		return nil, &EmbyJellyfinBoundaryError{Kind: "unclassified ZSpace HTTP failure", cause: err}
	}
	return func(ctx context.Context, input ZSpaceRequest) (*ZSpaceResponse, error) {
		if err := current(ctx); err != nil {
			return nil, err
		}
		if input.Timeout != 20*time.Second || input.VerifyCertificate || input.UseSession {
			return nil, &EmbyJellyfinBoundaryError{Kind: "ZSpace request-port contract"}
		}
		selected, err := ejText(input.Origin)
		if err != nil {
			return nil, err
		}
		selectedURL, err := url.Parse(selected)
		if err != nil || selectedURL == nil || ejOrigin(selectedURL) != boundOrigin || selectedURL.User != nil {
			return nil, &EmbyJellyfinBoundaryError{Kind: "ZSpace request origin"}
		}
		target, err := ejText(input.URL)
		if err != nil {
			return nil, err
		}
		body, err := zspaceHTTPBody(input.PythonJSONData)
		if err != nil {
			return nil, err
		}
		method := strings.ToUpper(input.Method)
		if method != http.MethodGet && method != http.MethodPost {
			return nil, &EmbyJellyfinBoundaryError{Kind: "ZSpace request method"}
		}
		request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
		if err != nil {
			deps.Report(ZSpaceDiagnostic{Operation: "request", Severity: "debug", Class: "InvalidURL"})
			return nil, nil
		}
		if ejOrigin(request.URL) != boundOrigin || request.URL.User != nil {
			return nil, &EmbyJellyfinBoundaryError{Kind: "ZSpace target origin"}
		}
		headers, err := zspaceHTTPHeaders(input.Headers)
		if err != nil {
			if source, ok := err.(*mediaserverruntime.SourceError); ok && source.Class == "InvalidHeader" {
				deps.Report(ZSpaceDiagnostic{Operation: "request", Severity: "debug", Class: "RequestException"})
				return nil, nil
			}
			return nil, err
		}
		request.Header = headers
		params, err := zspaceHTTPParams(input.Params)
		if err != nil {
			return nil, err
		}
		if params != "" {
			if request.URL.RawQuery != "" {
				request.URL.RawQuery += "&"
			}
			request.URL.RawQuery += params
		}
		transport, err := ejPrivateTransport(ctx, deps.BaseTransport, input.Timeout)
		if err != nil {
			return nil, err
		}
		defer transport.CloseIdleConnections()
		// Requests Session is fresh for every request(), and trust_env merges the
		// current process environment. This takes a snapshot, never clears it.
		routed, err := networkproxy.NewRequestsProxyTransport(transport, os.Environ(), nil, request.URL)
		if err != nil {
			return nil, &EmbyJellyfinBoundaryError{Kind: "ZSpace proxy configuration", cause: err}
		}
		defer routed.CloseIdleConnections()
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		client := zspaceRequestClient(routed, jar, boundOrigin, input.AllowRedirects, current, ctx)
		if err := current(ctx); err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if guard := current(ctx); guard != nil {
			if response != nil && response.Body != nil {
				response.Body.Close()
			}
			return nil, guard
		}
		if err != nil {
			if response != nil && response.Body != nil {
				response.Body.Close()
			}
			return failure(ctx, err)
		}
		if response == nil || response.Body == nil {
			return nil, &EmbyJellyfinBoundaryError{Kind: "nil ZSpace HTTP response"}
		}
		content, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if guard := current(ctx); guard != nil {
			return nil, guard
		}
		if readErr != nil {
			return failure(ctx, readErr)
		}
		if closeErr != nil {
			return failure(ctx, closeErr)
		}
		// Preserve this port's read/close/current/read-error/close-error ordering.
		// Shared decoding checks current while processing the complete bytes;
		// the preceding wire read remains this port's existing io.ReadAll.
		content, err = MediaServerRequestsContent(ctx, content, response.Header.Get("Content-Encoding"), RequestsContentCurrent(current))
		if err != nil {
			if source, ok := err.(*mediaserverruntime.SourceError); ok && source.Class == "ContentDecodingError" {
				deps.Report(ZSpaceDiagnostic{Operation: "request", Severity: "debug", Class: "RequestException"})
				return nil, nil
			}
			return nil, err
		}
		contentType := response.Header.Get("Content-Type")
		return &ZSpaceResponse{StatusCode: response.StatusCode, JSON: func() (json.RawMessage, error) {
			if err := current(ctx); err != nil {
				return nil, err
			}
			raw, err := ejResponseJSON(content, contentType)
			if guard := current(ctx); guard != nil {
				return nil, guard
			}
			return raw, err
		}}, nil
	}, nil
}

// requestsTransportError is a private, exact wrapper in the bound local
// networkproxy package. It only wraps a real RoundTrip failure, not proxy
// selection/auth errors. No arbitrary Unwrap/errors.As/Join grants admission.
func zspaceHTTPNetworkError(err error) bool {
	if e, ok := err.(*url.Error); ok {
		return zspaceHTTPNetworkError(e.Err)
	}
	if err != nil {
		typ := reflect.TypeOf(err)
		if typ.PkgPath() == "light-media/internal/networkproxy" && typ.Name() == "requestsTransportError" {
			wrapped, ok := err.(interface{ Unwrap() error })
			return ok && ejOrdinaryNetworkError(wrapped.Unwrap())
		}
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return ejOrdinaryNetworkError(err)
}
