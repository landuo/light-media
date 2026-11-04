package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins/mediaserverruntime"
	"net/http"
	"time"
)

// PlexSDKFactory assembles the retained wrapper with the concrete SDK XML /
// library executor and a DISTINCT adapted session. The resolved header/timeout
// profile and exact requests response-text decoder remain mandatory, not guessed
// historical host/environment values. A broader Requests codec/refresh adapter
// is still a separately stated unfinished dependency, not a fake successful port.
func PlexSDKFactory(base *http.Transport, profile PlexSDKProfile, guard PlexSDKCurrentGuard, text PlexSDKTextDecoder, location *time.Location, report func(PlexDiagnostic)) (mediaserverruntime.Factory, error) {
	if base == nil || guard == nil || text == nil || report == nil {
		return nil, errors.New("Plex SDK factory requires concrete owner dependencies")
	}
	// Freeze mutable caller slices at construction, then each SDK session receives
	// its own transport clone. No normal global client or unrelated pool changes.
	profile.BaseHeaders = append([]PlexHeader(nil), profile.BaseHeaders...)
	if profile.SessionHeaders != nil {
		profile.SessionHeaders = append([]PlexHeader{}, profile.SessionHeaders...)
	}
	connect := func(ctx context.Context, host, token string) (PlexServer, error) {
		resolved := profile
		resolved.BaseURL, resolved.Token = host, token
		executor, err := NewPlexSDKHTTP(base, resolved, guard, text)
		if err != nil {
			return nil, err
		}
		server, err := NewPlexSDKServer(ctx, executor, location)
		if err != nil {
			executor.CloseIdleConnections()
			return nil, err
		}
		return server, nil
	}
	return PlexFactory(PlexDependencies{
		Connect: connect,
		ConnectRaw: func(ctx context.Context, hostRaw, tokenRaw json.RawMessage) (PlexServer, error) {
			if err := plexContext(ctx); err != nil {
				return nil, err
			}
			if err := plexSDKCurrentAuthority(ctx, guard); err != nil {
				return nil, err
			}
			if err := constructorPlexSecret(tokenRaw); err != nil {
				return nil, err
			}
			host, err := constructorLosslessString(hostRaw, "AttributeError")
			if err != nil {
				return nil, err
			}
			// Requests prepares URL before headers. Keep an invalid IDNA host's
			// source error ahead of a hashable non-string token's InvalidHeader.
			if _, err = constructorHTTPURL(host); err != nil {
				return nil, err
			}
			token, err := constructorLosslessString(tokenRaw, "InvalidHeader")
			if err != nil {
				return nil, err
			}
			return connect(ctx, host, token)
		},
		NewSession: func(ctx context.Context) (PlexSession, error) {
			if err := plexContext(ctx); err != nil {
				return nil, err
			}
			if err := plexSDKCurrentAuthority(ctx, guard); err != nil {
				return nil, err
			}
			return newPlexAdaptedSession(base)
		},
		Report: report,
	})
}
