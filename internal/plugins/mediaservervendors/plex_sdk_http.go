package mediaservervendors

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/mediaserverruntime"
)

// PlexSDKProfile is an explicitly resolved private SDK configuration, not
// inferred historical machine/env settings. BaseHeaders must be supplied from
// the bound source-header profile; this layer never probes uname/getnode/home.
// The concrete original 4.18.1 default timeout is 30 seconds, but environment /
// PlexConfig may override it, so this constructor does not silently select it.
type PlexSDKProfile struct {
	BaseURL, Token string
	BaseHeaders    []PlexHeader
	SessionHeaders []PlexHeader // explicitly resolved requests.Session defaults, including UA/Accept/encoding
	TimeoutSeconds int64
	ShowSecrets    bool
}

// PlexSDKTextDecoder must implement the separately bound requests.Response.text
// semantics. Raw UTF-8 or Go XML is not silently substituted for that behavior.
type PlexSDKTextDecoder func([]byte, http.Header) (string, error)
type PlexSDKCurrentGuard func(context.Context) error

// PlexSDKHTTP is the concrete private per-SDK request executor. QueryText ends
// at source response.text/status handling; source parseXMLString + object load
// remain a distinct mandatory decoder slice, not a successful empty XML stub.
type PlexSDKHTTP struct {
	profile       PlexSDKProfile
	client        *http.Client
	guard         PlexSDKCurrentGuard
	text          PlexSDKTextDecoder
	redirectLimit *plexSDKRedirectLimit
}

// Only this client-owned sentinel may cross net/http's *url.Error wrapper
// into a source ordinary exception. Never unwrap arbitrary guard/context errors.
type plexSDKRedirectLimit struct{ marker byte }

func (*plexSDKRedirectLimit) Error() string { return "Plex SDK redirect limit" }

func NewPlexSDKHTTP(base *http.Transport, profile PlexSDKProfile, guard PlexSDKCurrentGuard, text PlexSDKTextDecoder) (*PlexSDKHTTP, error) {
	if base == nil || guard == nil || text == nil {
		return nil, errors.New("Plex SDK HTTP requires owned transport, guard and exact text decoder")
	}
	if profile.TimeoutSeconds <= 0 || profile.TimeoutSeconds > int64((1<<63-1)/int64(time.Second)) {
		return nil, &PlexArgumentBoundary{Field: "SDK timeout profile"}
	}
	encodedURL, err := constructorHTTPURL(profile.BaseURL)
	if err != nil {
		return nil, err
	}
	profile.BaseURL = encodedURL
	origin, err := url.Parse(profile.BaseURL)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Fragment != "" || origin.RawQuery != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, &PlexArgumentBoundary{Field: "SDK origin profile"}
	}
	if len(profile.BaseHeaders) == 0 || profile.SessionHeaders == nil {
		return nil, errors.New("Plex SDK header profile is not resolved")
	}
	profile.BaseHeaders = append([]PlexHeader(nil), profile.BaseHeaders...)
	profile.SessionHeaders = append([]PlexHeader(nil), profile.SessionHeaders...)
	profile.BaseURL = strings.TrimRight(profile.BaseURL, "/") // PlexServer.__init__, not URLJoin
	transport := base.Clone()                                 // Retain the trusted owner's Proxy/Dial/route settings.
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = true // Explicit current plugin-private HTTPS decision; never global.
	duration := time.Duration(profile.TimeoutSeconds) * time.Second
	transport, err = plexSDKPhaseTransport(transport, duration, guard)
	if err != nil {
		return nil, err
	}
	wire := &plexSDKCookieTransport{base: transport, origin: origin, current: guard, cookies: NewUgreenSessionCookies()}
	c := &http.Client{Transport: wire} // Source timeout is per socket phase, never a total wall-clock budget.
	redirectLimit := &plexSDKRedirectLimit{marker: 1}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := plexSDKCurrentAuthority(req.Context(), guard); err != nil {
			return err
		}
		if req.URL.Scheme != origin.Scheme || !strings.EqualFold(req.URL.Host, origin.Host) || req.URL.User != nil {
			return errors.New("Plex private credential origin changed")
		}
		// requests.Session's source default is 30 redirects. Cross-origin credential
		// forwarding is not authorized; do not use source custom-header forwarding
		// as permission to disclose X-Plex-Token to another origin.
		if len(via) > 30 {
			return redirectLimit
		}
		return nil
	}
	return &PlexSDKHTTP{profile: profile, client: c, guard: guard, text: text, redirectLimit: redirectLimit}, nil
}
func (c *PlexSDKHTTP) QueryText(ctx context.Context, key string) (string, error) {
	return c.queryText(ctx, key, false)
}

// refresh is supplied only by this package's two source-owned refresh builders;
// it is not a request/RPC flag and does not change credentials or routing.
func (c *PlexSDKHTTP) queryText(ctx context.Context, key string, refresh bool) (string, error) {
	if err := plexContext(ctx); err != nil {
		return "", err
	}
	if err := c.current(ctx); err != nil {
		return "", err
	}
	switch key {
	case "/", "/library", "/library/sections/", "/library/sections":
	default:
		if !refresh {
			return "", errors.New("Plex constructor endpoint not in this finite adapter")
		}
	}
	address := c.profile.BaseURL + key
	if c.profile.Token != "" && c.profile.ShowSecrets {
		delim := "?"
		if strings.Contains(key, "?") {
			delim = "&"
		}
		address += delim + "X-Plex-Token=" + c.profile.Token
	}
	// ShowSecrets, when explicitly resolved true, also places the original
	// token in the URL. Encode Python surrogate codepoints at that consumer.
	address, err := constructorHTTPURL(address)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	for _, h := range c.profile.SessionHeaders {
		req.Header.Set(h.Name, h.Value)
	}
	for _, h := range c.profile.BaseHeaders {
		req.Header.Set(h.Name, h.Value)
	}
	if c.profile.Token != "" {
		header, err := constructorHeader(c.profile.Token)
		if err != nil {
			return "", err
		}
		req.Header.Set("X-Plex-Token", header)
	}
	requestContext, finish, phases := plexSDKTrace(req.Context())
	defer finish()
	req = req.WithContext(requestContext)
	response, err := c.client.Do(req)
	if err != nil {
		if current := c.current(ctx); current != nil {
			return "", current
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if wrapped, ok := err.(*url.Error); ok && wrapped.Err == c.redirectLimit {
			return "", &mediaserverruntime.SourceError{Class: "TooManyRedirects", Cause: errors.New("Plex SDK redirect limit")}
		}
		if failure := phases.failure(); failure != nil {
			return "", failure
		}
		return "", plexSDKResponseNetworkError(err, false, phases.connected.Load())
	}
	defer response.Body.Close()
	if err = c.current(ctx); err != nil {
		return "", err
	}
	raw, err := MediaServerRequestsReadContent(ctx, response.Body, response.Header.Get("Content-Encoding"), RequestsContentCurrent(c.current))
	if err != nil {
		if current := c.current(ctx); current != nil {
			return "", current
		}
		if failure := phases.failure(); failure != nil {
			return "", failure
		}
		return "", plexSDKResponseNetworkError(err, true, true)
	}
	if failure := phases.failure(); failure != nil {
		return "", failure
	}
	if err = c.current(ctx); err != nil {
		return "", err
	}
	// Source looks up the status name before evaluating response.text on errors.
	if response.StatusCode != 200 && response.StatusCode != 201 && response.StatusCode != 204 {
		if _, present := plexRequestsStatusNames[response.StatusCode]; !present {
			return "", &mediaserverruntime.SourceError{Class: "TypeError", Cause: errors.New("'NoneType' object is not subscriptable")}
		}
	}
	decoded, err := c.text(raw, response.Header.Clone())
	if current := c.current(ctx); current != nil {
		return "", current
	}
	if current := plexContext(ctx); current != nil {
		return "", current
	}
	if err != nil {
		return "", err
	}
	if response.StatusCode == 200 || response.StatusCode == 201 || response.StatusCode == 204 {
		return decoded, nil
	}
	// Match the original query's internal exception text. Never emit it through
	// Report: URLs/body can contain private data; wrapper diagnostics stay typed.
	responseURL := address
	if response.Request != nil && response.Request.URL != nil {
		responseURL = response.Request.URL.String()
	}
	message := fmt.Sprintf("(%d) %s; %s %s", response.StatusCode, plexRequestsStatusNames[response.StatusCode], responseURL, strings.ReplaceAll(decoded, "\n", " "))
	class := "BadRequest"
	if response.StatusCode == 401 {
		class = "Unauthorized"
	} else if response.StatusCode == 404 {
		class = "NotFound"
	}
	return "", &mediaserverruntime.SourceError{Class: class, Cause: errors.New(message)}

}
func (c *PlexSDKHTTP) current(ctx context.Context) error {
	return plexSDKCurrentAuthority(ctx, c.guard)
}
func (c *PlexSDKHTTP) CloseIdleConnections() { c.client.CloseIdleConnections() }

// plexAdaptedSession is a distinct retained private session. It is not the
// SDK client above and is never passed into PlexServer after construction.
// This first block exposes no request method: the scoped source close/header
// behavior is complete here, later Plex media operations are not fabricated.
type plexAdaptedSession struct {
	mu         sync.Mutex
	client     *http.Client
	headers    []PlexHeader
	rawHeaders []PlexRawHeader
}

func newPlexAdaptedSession(base *http.Transport) (PlexSession, error) {
	if base == nil {
		return nil, errors.New("Plex adapted session requires owned transport")
	}
	transport := base.Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	return &plexAdaptedSession{client: &http.Client{Transport: transport}}, nil
}
func (s *plexAdaptedSession) ReplaceHeaders(ctx context.Context, headers []PlexHeader) error {
	if err := plexContext(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.headers = append([]PlexHeader(nil), headers...)
	s.rawHeaders = nil
	s.mu.Unlock()
	return nil
}
func (s *plexAdaptedSession) Close(ctx context.Context) error {
	// Closing this owned pool is local cleanup, not a new vendor request, logout,
	// terminal flag or removal of the source session reference.
	s.client.CloseIdleConnections()
	return plexContext(ctx)
}

func (s *plexAdaptedSession) ReplaceRawHeaders(ctx context.Context, headers []PlexRawHeader) error {
	if err := plexContext(ctx); err != nil {
		return err
	}
	owned := make([]PlexRawHeader, len(headers))
	for i, h := range headers {
		owned[i] = PlexRawHeader{h.Name, constructorJSON(h.Value)}
	}
	s.mu.Lock()
	s.rawHeaders = owned
	s.headers = nil
	s.mu.Unlock()
	return nil
}
