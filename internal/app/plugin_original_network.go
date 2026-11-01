package app

import (
	"net/http"
	"strings"
	"time"

	"light-media/internal/networkproxy"
)

// Reuse the host PROXY configuration. Wiki supplies that mapping directly;
// PluginHelper.__request_with_fallback additionally retries its GET through
// requests' ordinary environment transport after an explicit proxy fails.
// Artifact code retains its own bounded metadata/download contexts.
func (a *App) originalPluginHTTPClient(timeout time.Duration, repositoryFallback bool) (*http.Client, error) {
	return a.originalPluginHTTPClientWithBase(timeout, repositoryFallback, nil)
}

// A base is supplied only by the explicitly named source-compatible consumers.
// The ordinary entry above, including Wiki, retains its existing TLS policy.
func (a *App) originalPluginHTTPClientWithBase(timeout time.Duration, repositoryFallback bool, base *http.Transport) (*http.Client, error) {
	settings := a.Settings()
	if repositoryFallback {
		if err := validateOriginalGitHubSettings(settings); err != nil {
			return nil, err
		}
	}
	config, err := a.proxyConfig()
	if err != nil {
		return nil, err
	}
	transport, err := config.Transport(base)
	if err != nil {
		return nil, err
	}
	if repositoryFallback && strings.TrimSpace(settings.ProxyHost) == "" {
		closeOriginalPluginTransport(transport)
		// Source has no explicit PROXY strategy unless PROXY_HOST is set.
		// Its ordinary requests path still observes ALL_PROXY and NO_PROXY.
		transport = networkproxy.NewRequestsTransport(base, nil)
	} else if repositoryFallback {
		// This is requests' ordinary environment route, not another explicit
		// PROXY mapping. In particular NO_PROXY can bypass the same URL that
		// was just used explicitly; equal proxy strings do not skip this step.
		fallback := networkproxy.NewRequestsTransport(base, nil)
		transport = &originalPluginFallbackTransport{preferred: transport, fallback: fallback}
	}
	if repositoryFallback {
		transport = &originalGitHubTransport{base: transport, mirror: networkproxy.NewRequestsTransport(base, nil), settings: Settings{GitHubProxy: settings.GitHubProxy, GitHubToken: settings.GitHubToken, RepoGitHubToken: settings.RepoGitHubToken}}
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

func closeOriginalPluginTransport(transport http.RoundTripper) {
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type originalPluginFallbackTransport struct {
	preferred, fallback http.RoundTripper
}

// Source get_res(raise_exception=True) advances strategies only on transport
// exceptions. A received HTTP failure is still a response for the caller.
func (t *originalPluginFallbackTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.preferred.RoundTrip(request)
	if request.Method != http.MethodGet || request.Context().Err() != nil || err == nil {
		return response, err
	}
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	return t.fallback.RoundTrip(request)
}

func (t *originalPluginFallbackTransport) CloseIdleConnections() {
	closeOriginalPluginTransport(t.preferred)
	closeOriginalPluginTransport(t.fallback)
}
