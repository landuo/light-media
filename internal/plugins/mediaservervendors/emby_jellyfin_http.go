package mediaservervendors

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"light-media/internal/plugins/mediaserverruntime"
)

// This is a platform credential-origin boundary, never a swallowable source
// Exception. Error text deliberately contains no host, request URL or API key.
type EmbyJellyfinBoundaryError struct {
	Kind  string
	cause error
}

func (e *EmbyJellyfinBoundaryError) Error() string { return "media vendor boundary: " + e.Kind }
func (e *EmbyJellyfinBoundaryError) Unwrap() error { return e.cause }

type ejRedirectLimitError struct{}

func (e *ejRedirectLimitError) Error() string { return "media vendor redirect limit" }
func ejOrigin(u *url.URL) string              { return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) }

// Only exact standard wrappers may expose an ordinary leaf. No errors.As on
// an arbitrary wrapper/Join and no Timeout() envelope establishes ownership.
func ejOrdinaryNetworkError(err error) bool {
	switch e := err.(type) {
	case *url.Error:
		return ejOrdinaryNetworkError(e.Err)
	case *net.OpError:
		return ejOrdinaryNetworkError(e.Err)
	case *os.SyscallError:
		return ejOrdinaryNetworkError(e.Err)
	case *net.DNSError:
		if e.Unwrap() != nil {
			return ejOrdinaryNetworkError(e.Unwrap())
		}
		return e.IsNotFound
	case syscall.Errno:
		switch e {
		case syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ECONNABORTED, syscall.ETIMEDOUT, syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.EPIPE:
			return true
		}
		return false
	case *ejOwnedIOTimeout, *ejRedirectLimitError, net.InvalidAddrError, *net.AddrError, textproto.ProtocolError, *http.ProtocolError, tls.AlertError, tls.RecordHeaderError, *tls.RecordHeaderError:
		return true
	}
	return ejStandardTLSHandshakeTimeout(err) || err == io.EOF || err == io.ErrUnexpectedEOF || err == net.ErrClosed
}

func ejOwnedDeadline(err error) bool {
	if err == context.DeadlineExceeded || ejStandardConnectTimeout(err) {
		return true
	}
	switch e := err.(type) {
	case *url.Error:
		return ejOwnedDeadline(e.Err)
	case *net.OpError:
		return ejOwnedDeadline(e.Err)
	case *os.SyscallError:
		return ejOwnedDeadline(e.Err)
	}
	return false
}
func (v *ejVendor) current(ctx context.Context) error {
	if err := ejContext(ctx); err != nil {
		return err
	}
	if err := v.deps.Current(ctx); err != nil {
		return &EmbyJellyfinBoundaryError{Kind: "current authority", cause: err}
	}
	return nil
}
func ejAPIQuery(raw json.RawMessage) (string, error) {
	values := []json.RawMessage{raw}
	trim := bytes.TrimSpace(raw)
	if len(trim) > 0 && (trim[0] == '[' || trim[0] == '{') {
		var err error
		values, err = ejIter(trim)
		if err != nil {
			return "", err
		}
	}
	parts := []string{}
	for _, value := range values {
		if ejNull(value) {
			continue
		}
		s, err := ejPythonText(value, false)
		if err != nil {
			return "", err
		}
		parts = append(parts, "api_key="+url.QueryEscape(s))
	}
	return strings.Join(parts, "&"), nil
}

type ejRequestParameter struct {
	Name  string
	Value json.RawMessage
}
type ejHTTPResponse struct {
	Status      int
	Body        []byte
	ContentType string
}

// The actual private client's callback is also inspectable without making
// thirty network requests. Authority and exact origin remain prior to count.
func (v *ejVendor) requestClient(ctx context.Context, transport http.RoundTripper, jar http.CookieJar, origin string) *http.Client {
	return &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if err := v.current(ctx); err != nil {
			return err
		}
		if ejOrigin(next.URL) != origin {
			return &EmbyJellyfinBoundaryError{Kind: "cross-origin redirect"}
		}
		// Go via includes the first request; Requests response.history does not.
		if len(via) > 30 {
			return &ejRedirectLimitError{}
		}
		return nil
	}}
}

func ejQueryFields(fields []ejRequestParameter) (string, error) {
	parts := []string{}
	for _, field := range fields {
		encoded, err := ejAPIQuery(field.Value)
		if err != nil {
			return "", err
		}
		if encoded == "" {
			continue
		}
		for _, part := range strings.Split(encoded, "&") {
			parts = append(parts, url.QueryEscape(field.Name)+"="+strings.TrimPrefix(part, "api_key="))
		}
	}
	return strings.Join(parts, "&"), nil
}
func (v *ejVendor) getJSON(ctx context.Context, path string) (json.RawMessage, bool, error) {
	return v.getJSONParameters(ctx, path, []ejRequestParameter{{Name: "api_key", Value: ejCopy(v.apiKey)}})
}
func (v *ejVendor) getJSONParameters(ctx context.Context, path string, parameters []ejRequestParameter) (json.RawMessage, bool, error) {
	response, err := v.requestResponse(ctx, http.MethodGet, path, parameters)
	if err != nil {
		if ordinary, ok := err.(*mediaserverruntime.SourceError); ok {
			v.report(path, "error", ordinary.Class)
			return nil, false, nil
		}
		return nil, false, err
	}
	if response == nil || response.Status >= 400 {
		v.report(path, "error", "MissingResponse")
		return nil, false, nil
	}
	// requestResponse captures Content-Type as received, not a synthesized header.
	decoded, err := ejResponseJSON(response.Body, response.ContentType)
	if current := v.current(ctx); current != nil {
		return nil, false, current
	}
	if err != nil {
		if ordinary, ok := err.(*mediaserverruntime.SourceError); ok {
			v.report(path, "error", ordinary.Class)
			return nil, false, nil
		}
		return nil, false, err
	}
	return decoded, true, nil
}

func (v *ejVendor) requestResponse(ctx context.Context, method, path string, parameters []ejRequestParameter) (*ejHTTPResponse, error) {
	if method != http.MethodGet && method != http.MethodPost {
		return nil, &EmbyJellyfinBoundaryError{Kind: "unsupported internal media method"}
	}
	if err := v.current(ctx); err != nil {
		return nil, err
	}
	query, err := ejQueryFields(parameters)
	if err != nil {
		return nil, err
	}
	if !v.host.encodable {
		return nil, ejSource("UnicodeEncodeError")
	}
	target := v.host.text + path
	request, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		v.report(path, "error", "InvalidURL")
		return nil, nil
	}
	if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
		v.report(path, "error", "InvalidSchema")
		return nil, nil
	}
	if request.URL.Host == "" {
		v.report(path, "error", "InvalidURL")
		return nil, nil
	}
	// Requests' fixed default decoder profile is gzip + deflate. Explicitly
	// advertise it so Go cannot auto-decode gzip before the shared source codec.
	request.Header.Set("Accept-Encoding", "gzip, deflate")
	request.Header.Set("Connection", "keep-alive")
	if query != "" {
		if request.URL.RawQuery != "" {
			request.URL.RawQuery += "&"
		}
		request.URL.RawQuery += query
	}
	if v.kind == "jellyfin" {
		// Nonempty Authorization headers preserve the Requests Session defaults.
		request.Header.Set("User-Agent", "python-requests/2.34.2")
		request.Header.Set("Accept", "*/*")
		key, e := ejPythonText(v.apiKey, false)
		if e != nil {
			return nil, e
		}
		for _, r := range key {
			if r == '\r' || r == '\n' {
				v.report(path, "error", "InvalidHeader")
				return nil, nil
			}
			if r > 255 {
				return nil, ejSource("UnicodeEncodeError")
			}
		}
		encoded := make([]byte, 0, len(key))
		for _, r := range key {
			encoded = append(encoded, byte(r))
		}
		request.Header.Set("Authorization", `MediaBrowser Token="`+string(encoded)+`"`)
	} else {
		// RequestUtils UA=None removes the Session UA; urllib3 supplies its own.
		request.Header.Set("User-Agent", "python-urllib3/2.7.0")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		// Original RequestUtils Accept=None removes the Session default.
	}
	// Private request transport: source RequestUtils has no supplied Session,
	// hence no cross-call cookies/cache/keepalive retry. Proxy and dial stay copied.
	transport, err := ejPrivateTransport(ctx, v.deps.BaseTransport, 20*time.Second)
	if err != nil {
		return nil, err
	}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	origin := ejOrigin(request.URL)
	client := v.requestClient(ctx, transport, jar, origin)
	if err := v.current(ctx); err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if authority := v.current(ctx); authority != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, authority
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if ejOrdinaryNetworkError(err) {
			v.report(path, "error", "RequestException")
			return nil, nil
		}
		var boundary *EmbyJellyfinBoundaryError
		if errors.As(err, &boundary) {
			return nil, boundary
		}
		// Unknown transport/host failures are not silently converted to source None.
		return nil, &EmbyJellyfinBoundaryError{Kind: "unclassified transport failure", cause: err}
	}
	defer response.Body.Close()
	body, err := MediaServerRequestsReadContent(ctx, response.Body, response.Header.Get("Content-Encoding"), RequestsContentCurrent(v.current))
	if authority := v.current(ctx); authority != nil {
		return nil, authority
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if source, ok := err.(*mediaserverruntime.SourceError); ok && source.Class == "ContentDecodingError" {
			v.report(path, "error", "RequestException")
			return nil, nil
		}
		if ejOrdinaryNetworkError(err) {
			v.report(path, "error", "RequestException")
			return nil, nil
		}
		return nil, &EmbyJellyfinBoundaryError{Kind: "unclassified response read failure", cause: err}
	}
	return &ejHTTPResponse{Status: response.StatusCode, Body: append([]byte(nil), body...), ContentType: response.Header.Get("Content-Type")}, nil
}
