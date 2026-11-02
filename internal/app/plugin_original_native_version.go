package app

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"light-media/internal/systemcommands"
	"light-media/internal/systemversion"
	"light-media/web"
)

// This is a new private transport, never DefaultTransport and never the
// credential/mirror-bearing repository transport. User 2026-11-02 00:59:28 UTC
// explicitly superseded strict TLS for plugin-module HTTPS including /version.
// Only this transport disables certificate verification. Proxy/origin policy,
// system CAs, Git, browser and process environment are not changed.
func newOriginalNativeVersionTransport() *http.Transport {
	return &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // Explicit private-client user policy; not a trust assertion.
		TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout: 30 * time.Second, MaxIdleConns: 2, MaxIdleConnsPerHost: 2,
	}
}

// The fixed endpoint is not configurable. No Authorization, Cookie, shared
// GitHub token or repository mirror is read or attached. Redirects of every
// kind are left as an unavailable response; never follow a new origin/path.
func originalNativeReleaseFromClient(ctx context.Context, client *http.Client) (*string, string, error) {
	if ctx == nil || client == nil {
		return nil, "", originalSystemAdapterBoundary("native release client unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, systemversion.NativeReleasesURL, nil)
	if err != nil {
		return nil, "", originalSystemAdapterBoundary("native release endpoint invalid")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "light-media/"+appVersion)
	owned := *client
	owned.Jar = nil
	owned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := owned.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "query", nil
	}
	if response == nil || response.Body == nil {
		return nil, "query", nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "status", nil
	}
	// Source reads the complete received metadata. No new arbitrary total body
	// cap is introduced: the dedicated client/context bounds request duration.
	raw, err := io.ReadAll(response.Body)
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	if err != nil {
		return nil, "body", nil
	}
	latest, err := systemversion.SelectNativeRelease(raw)
	if err != nil {
		return nil, "metadata", nil
	}
	if latest == nil {
		return nil, "no-release", nil
	}
	return latest, "", nil
}

func (h *originalSystemCommandAdapters) nativeLatestRelease(ctx context.Context) (*string, error) {
	done, err := h.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	// false explicitly prohibits GitHub credential injection/repository fallback.
	client, err := h.app.originalPluginHTTPClientWithBase(20*time.Second, false, newOriginalNativeVersionTransport())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		h.app.log("WARN", "system", "version_unknown", "光影版本查询配置不可用，远程版本未知", "", "")
		return nil, nil
	}
	defer client.CloseIdleConnections()
	version, reason, err := originalNativeReleaseFromClient(ctx, client)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		h.app.log("WARN", "system", "version_unknown", "光影远程版本未知："+reason, "", "")
	}
	return version, nil
}

func (a *App) newOriginalNativeVersion() (*systemcommands.NativeVersion, error) {
	h := &originalSystemCommandAdapters{app: a}
	return systemcommands.NewNativeVersion(originalSystemRestartMarkerKey, originalSystemChannels[:], systemcommands.NativeVersionPorts{
		Latest: h.nativeLatestRelease,
		LocalBackend: func(ctx context.Context) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return appVersion, nil
		},
		LocalFrontend: func(ctx context.Context) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return web.FrontendVersion()
		},
		Notify: h.Notify, Cache: h, Codec: h,
		Log: func(ctx context.Context, level, message string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			a.log(strings.ToUpper(level), "system", "system_command", message, "", "")
			return nil
		},
	})
}
