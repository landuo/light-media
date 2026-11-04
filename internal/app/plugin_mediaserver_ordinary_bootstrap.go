package app

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins/mediaservervendors"
)

// NewOriginalMediaServerFactoryAssembly is the ordinary executable's concrete
// bootstrap, not a registry of recording interfaces. It resolves current owned
// runtime facts once, while every later IO uses the assembly's fresh Current.
// The selected dependencies remain independently reviewable; this constructor
// does not claim their unexecuted HTTP/content boundaries have passed.
func NewOriginalMediaServerFactoryAssembly(ctx context.Context, dataDir string) (MediaServerFactoryAssembly, error) {
	if ctx == nil {
		return nil, errors.New("native media bootstrap requires context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profile, location, err := LoadOriginalPlexRuntimeProfile(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	protocol, err := originalNativeTrimeProtocol(originalNativeTrimeAccessHeaders(profile.SessionHeaders))
	if err != nil {
		return nil, err
	}
	defaults, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("native media HTTP transport is not the trusted standard transport")
	}
	base := defaults.Clone()
	// RequestUtils/Plex Session trust-env routing: use the existing uncached
	// Requests resolver, not Go's implicit loopback bypass and not ProxyHost.
	// No environment/global transport mutation or direct fallback is performed.
	// This callback is the existing HTTP(S) seam. Full SOCKS route adapters remain
	// a separately identified consumer composition boundary, not a fake pass.
	base.Proxy = networkproxy.RequestsProxy
	return NewNativeMediaServerFactoryAssembly(MediaServerNativeInputs{
		Transport: base, Superuser: originalNativeMediaServerSuperuser,
		PlexProfile: profile, PlexLocation: location, TrimeProtocol: protocol,
		NewUgreenSession: func(call context.Context, host string, current func(context.Context) error) (mediaservervendors.UgreenPrivateSession, error) {
			return mediaservervendors.NewUgreenHTTPSession(call, host, mediaservervendors.UgreenHTTPSessionDependencies{
				BaseTransport: base, Current: current, Cookies: mediaservervendors.NewUgreenSessionCookies(),
			})
		}, Entropy: rand.Reader, Now: time.Now,
	})
}

// Reuse the fixed selected Requests Session defaults already resolved by the
// ordinary profile producer. RequestUtils(None headers/ua/accept/referer)
// overwrites then removes UA/Accept/referer, keeps the other Session defaults,
// and urllib3 supplies its own UA only at the final wire boundary. Do not use
// a Content-Type-only test profile or forward Plex X-Plex identification here.
func originalNativeTrimeAccessHeaders(session []mediaservervendors.PlexHeader) []mediaservervendors.TrimeHTTPHeader {
	if session == nil {
		return nil
	}
	out := make([]mediaservervendors.TrimeHTTPHeader, 0, len(session)+2)
	for _, field := range session {
		switch strings.ToLower(field.Name) {
		case "user-agent", "accept", "referer", "content-type":
			continue
		}
		out = append(out, mediaservervendors.TrimeHTTPHeader{Name: field.Name, Value: field.Value})
	}
	out = append(out, mediaservervendors.TrimeHTTPHeader{Name: "Content-Type", Value: "application/x-www-form-urlencoded; charset=UTF-8"})
	// Version is the fixed urllib3 dependency bound in the accompanying source
	// contract, not a runtime probe, package import or guessed browser UA.
	out = append(out, mediaservervendors.TrimeHTTPHeader{Name: "User-Agent", Value: "python-urllib3/2.7.0"})
	return out
}
