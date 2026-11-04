package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/plugins/mediaservervendors"
)

// These are resolved trusted runtime inputs, not new public settings. Native
// bootstrap must supply the real route, explicit original setting/profile facts
// and concrete Ugreen HTTP session. An omitted value is a construction error.
// No test recorder, legacy FNOS map or successful zero-vendor default is here.
type MediaServerNativeInputs struct {
	Transport        *http.Transport
	Superuser        func(context.Context, *App) (json.RawMessage, error)
	PlexProfile      mediaservervendors.PlexSDKProfile
	PlexLocation     *time.Location
	TrimeProtocol    mediaservervendors.TrimeAPIProtocol
	NewUgreenSession func(context.Context, string, func(context.Context) error) (mediaservervendors.UgreenPrivateSession, error)
	Entropy          io.Reader
	Now              func() time.Time
}

// NewNativeMediaServerFactoryAssembly assembles genuine stateful providers. The
// final ordinary server still needs its trusted resolved inputs; this function
// does not guess Python SDK host/useragent/default-header/timezone provenance.
func NewNativeMediaServerFactoryAssembly(input MediaServerNativeInputs) (MediaServerFactoryAssembly, error) {
	if input.Transport == nil || input.Superuser == nil || input.PlexLocation == nil || input.NewUgreenSession == nil || input.Entropy == nil || input.Now == nil || input.TrimeProtocol.Nonce == nil || input.TrimeProtocol.Seconds == nil {
		return nil, errors.New("native media factories require every concrete runtime dependency")
	}
	if len(input.PlexProfile.BaseHeaders) == 0 || input.PlexProfile.SessionHeaders == nil || input.PlexProfile.TimeoutSeconds <= 0 {
		return nil, errors.New("native Plex profile is unresolved")
	}
	input.PlexProfile.BaseHeaders = append([]mediaservervendors.PlexHeader(nil), input.PlexProfile.BaseHeaders...)
	input.PlexProfile.SessionHeaders = append([]mediaservervendors.PlexHeader{}, input.PlexProfile.SessionHeaders...)
	if input.TrimeProtocol.AccessHeaders != nil {
		input.TrimeProtocol.AccessHeaders = append([]mediaservervendors.TrimeHTTPHeader{}, input.TrimeProtocol.AccessHeaders...)
	}
	return func(a *App, current func(context.Context) error) (MediaServerFactories, error) {
		var out MediaServerFactories
		if a == nil || current == nil || a.pluginStore == nil {
			return out, errors.New("native media factories require actual App and current lifecycle")
		}
		report := func(kind string, value any) {
			raw, e := json.Marshal(value)
			if e != nil {
				return
			}
			a.log("DEBUG", "mediaservers", kind, string(raw), "", "")
		}
		ej := mediaservervendors.EmbyJellyfinDependencies{BaseTransport: input.Transport, Current: current, Superuser: func(ctx context.Context) (json.RawMessage, error) {
			if err := current(ctx); err != nil {
				return nil, err
			}
			value, err := input.Superuser(ctx, a)
			if guard := current(ctx); guard != nil {
				return nil, guard
			}
			return value, err
		}, Report: func(d mediaservervendors.EmbyJellyfinDiagnostic) { report("vendor", d) }}
		var err error
		out.Emby, err = mediaservervendors.EmbyFactory(ej)
		if err != nil {
			return out, err
		}
		out.Jellyfin, err = mediaservervendors.JellyfinFactory(ej)
		if err != nil {
			return out, err
		}
		decoder, err := mediaservervendors.NewPlexSDKTextDecoder(mediaservervendors.MediaServerRequestsApparentEncoding)
		if err != nil {
			return out, err
		}
		out.Plex, err = mediaservervendors.PlexSDKFactory(input.Transport, input.PlexProfile, current, decoder, input.PlexLocation, func(d mediaservervendors.PlexDiagnostic) { report("plex", d) })
		if err != nil {
			return out, err
		}
		protocol := input.TrimeProtocol
		protocol.Report = func(d mediaservervendors.TrimeAPIDiagnostic) { report("trimemedia_api", d) }
		out.TrimeMedia, err = mediaservervendors.TrimeMediaFactory(mediaservervendors.TrimeDependencies{
			NewAPI: func(ctx context.Context, host, key string, access *string) (mediaservervendors.TrimeAPI, error) {
				return mediaservervendors.NewTrimeNativeAPI(ctx, input.Transport, host, key, access, current, mediaservervendors.MediaServerRequestsJSON, protocol)
			}, Report: func(d mediaservervendors.TrimeDiagnostic) { report("trimemedia", d) },
		})
		if err != nil {
			return out, err
		}
		// One concrete sealed sync-cache/SQL store per App, shared across its Ugreen
		// constructors. This depends on C's exact real store/API/crypto candidate;
		// it deliberately cannot compile by substituting the recording test store.
		sessions, err := newOriginalUgreenSessionStore(context.Background(), a.pluginStore)
		if err != nil {
			return out, err
		}
		out.Ugreen, err = mediaservervendors.UgreenFactory(mediaservervendors.UgreenDependencies{
			Current: current, Sessions: sessions, Now: input.Now, Report: func(d mediaservervendors.UgreenDiagnostic) { report("ugreen", d) },
			NewAPI: func(ctx context.Context, options mediaservervendors.UgreenAPIOptions) (mediaservervendors.UgreenAPI, error) {
				return mediaservervendors.NewUgreenAPI(ctx, options, mediaservervendors.UgreenAPIDependencies{
					Current: current, Entropy: input.Entropy, Report: func(d mediaservervendors.UgreenDiagnostic) { report("ugreen_api", d) },
					NewSession: func(ctx context.Context, host string) (mediaservervendors.UgreenPrivateSession, error) {
						return input.NewUgreenSession(ctx, host, current)
					},
				})
			},
		})
		if err != nil {
			return out, err
		}
		// Create a private request closure for each actual vendor object. Binding
		// origin is deferred until the wrapper issues its first concrete request:
		// original kwargs/type/falsey early-return order is not pre-parsed or changed.
		out.ZSpace = func(ctx context.Context, config plugins.MediaServerConfig) (mediaserverruntime.Vendor, error) {
			var mu sync.Mutex
			var port func(context.Context, mediaservervendors.ZSpaceRequest) (*mediaservervendors.ZSpaceResponse, error)
			request := func(ctx context.Context, req mediaservervendors.ZSpaceRequest) (*mediaservervendors.ZSpaceResponse, error) {
				if err := current(ctx); err != nil {
					return nil, err
				}
				mu.Lock()
				if port == nil {
					created, err := mediaservervendors.NewZSpaceHTTPPort(mediaservervendors.ZSpaceHTTPDependencies{BaseTransport: input.Transport, Origin: append(json.RawMessage(nil), req.Origin...), Current: current, Report: func(d mediaservervendors.ZSpaceDiagnostic) { report("zspace_http", d) }})
					if err != nil {
						mu.Unlock()
						return nil, err
					}
					port = created
				}
				actual := port
				mu.Unlock()
				return actual(ctx, req)
			}
			factory, err := mediaservervendors.ZSpaceFactory(mediaservervendors.ZSpaceDependencies{Current: current, Request: request, Report: func(d mediaservervendors.ZSpaceDiagnostic) { report("zspace", d) }})
			if err != nil {
				return nil, err
			}
			return factory(ctx, config)
		}
		return out, nil
	}, nil
}
