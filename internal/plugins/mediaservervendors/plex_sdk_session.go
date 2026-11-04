package mediaservervendors

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Every SDK instance owns a real source-shaped Requests jar. This deliberately
// reuses the already source-bound state, not a filtered name map, exported data
// snapshot, global jar or a newly invented successful cookie port.
type plexSDKCookieTransport struct {
	base    *http.Transport
	origin  *url.URL
	current PlexSDKCurrentGuard
	cookies UgreenSessionCookieStore
}

func (t *plexSDKCookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if err := plexSDKCurrentAuthority(ctx, t.current); err != nil {
		return nil, err
	}
	if req.URL.Scheme != t.origin.Scheme || !strings.EqualFold(req.URL.Host, t.origin.Host) || req.URL.User != nil {
		return nil, errors.New("Plex private cookie origin changed")
	}
	cookie, err := t.cookies.Header(ctx, req.URL)
	if check := plexSDKCurrentAuthority(ctx, t.current); check != nil {
		return nil, check
	}
	if err != nil {
		return nil, err
	}
	owned := req.Clone(ctx)
	owned.Header = req.Header.Clone()
	// Original resolved Session/Base headers have no Cookie key. Redirects must
	// regenerate it from current jar state instead of retaining the old path.
	owned.Header.Del("Cookie")
	if cookie != "" {
		owned.Header.Set("Cookie", cookie)
	}
	response, err := t.base.RoundTrip(owned)
	if check := plexSDKCurrentAuthority(ctx, t.current); check != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, check
	}
	if err != nil {
		return response, err
	}
	if response == nil {
		return nil, errors.New("Plex transport returned no response")
	}
	err = t.cookies.Extract(ctx, req.URL, response.Header)
	if check := plexSDKCurrentAuthority(ctx, t.current); check != nil {
		response.Body.Close()
		return nil, check
	}
	if err != nil {
		response.Body.Close()
		return nil, err
	}
	return response, nil
}
func (t *plexSDKCookieTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

// Compile-time constraint only; this file does not introduce a request-context
// flag or caller-selectable transport policy.
var _ http.RoundTripper = (*plexSDKCookieTransport)(nil)
