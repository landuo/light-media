package app

import (
	"net/http"
	"strings"
	"time"
)

// Reuse the host PROXY configuration. Wiki supplies that mapping directly;
// PluginHelper.__request_with_fallback additionally retries its GET through
// requests' ordinary environment transport after an explicit proxy fails.
// Artifact code retains its own bounded metadata/download contexts.
func (a *App) originalPluginHTTPClient(timeout time.Duration, repositoryFallback bool) (*http.Client, error) {
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
	transport, err := config.Transport(nil)
	if err != nil {
		return nil, err
	}
	if repositoryFallback && strings.TrimSpace(settings.ProxyHost) == "" {
		closeOriginalPluginTransport(transport)
		// Source has no explicit PROXY strategy unless PROXY_HOST is set.
		// Its ordinary requests path still observes ALL_PROXY and NO_PROXY.
		transport = originalPluginEnvironmentTransport()
	} else if repositoryFallback {
		// This is requests' ordinary environment route, not another explicit
		// PROXY mapping. In particular NO_PROXY can bypass the same URL that
		// was just used explicitly; equal proxy strings do not skip this step.
		fallback := originalPluginEnvironmentTransport()
		transport = &originalPluginFallbackTransport{preferred: transport, fallback: fallback}
	}
	if repositoryFallback {
		transport = &originalGitHubTransport{base: transport, mirror: originalPluginEnvironmentTransport(), settings: Settings{GitHubProxy: settings.GitHubProxy, GitHubToken: settings.GitHubToken, RepoGitHubToken: settings.RepoGitHubToken}}
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

func (t *originalPluginFallbackTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.preferred.RoundTrip(request)
	if request.Method != http.MethodGet || request.Context().Err() != nil || (err == nil && response.StatusCode < http.StatusBadRequest) {
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
