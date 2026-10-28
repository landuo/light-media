package app

import (
	"net/http"
	"net/url"
	"strings"

	"light-media/internal/networkproxy"
)

// Pinned config.py NORMAL_USER_AGENT and REPO_GITHUB_HEADERS. Invalid pairs
// are skipped as in the source, without its credential-bearing print messages.
const originalGitHubUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36"

func originalGitHubHeaders(settings Settings, repository string) http.Header {
	token := settings.GitHubToken
	if repository != "" {
		for _, pair := range strings.Split(settings.RepoGitHubToken, ",") {
			parts := strings.Split(pair, ":")
			if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" && strings.TrimSpace(parts[0]) == repository {
				token = strings.TrimSpace(parts[1])
			}
		}
	}
	headers := make(http.Header)
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
		headers.Set("User-Agent", originalGitHubUserAgent)
	}
	return headers
}
func validateOriginalGitHubSettings(settings Settings) error {
	if len(settings.GitHubToken) > 8192 || len(settings.RepoGitHubToken) > 65536 || strings.ContainsAny(settings.GitHubToken, "\x00\r\n") || strings.ContainsRune(settings.RepoGitHubToken, 0) {
		return bad("INVALID_GITHUB_TOKEN", "GitHub令牌格式无效")
	}
	if _, err := originalGitHubMirrorBase(settings.GitHubProxy); err != nil {
		return err
	}
	return nil
}

// UrlUtils.standardize_base_url retains existing trailing slashes and accepts
// an unschemed host. Validation additionally preserves the host credential URL
// boundary; it never forwards a token through a mirror's userinfo/query.
func originalGitHubMirrorBase(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "://") && !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "", bad("INVALID_GITHUB_PROXY", "GitHub加速地址须为HTTP(S)地址")
	}
	if !strings.HasSuffix(raw, "/") {
		raw += "/"
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", bad("INVALID_GITHUB_PROXY", "GitHub加速地址须不含账号、查询参数或片段")
	}
	return raw, nil
}

// Scope credentials to exact official HTTPS hosts and ports. Signed CDN URLs
// remain ordinary artifact requests and never inherit a GitHub Authorization.
func originalGitHubRequestRepository(u *url.URL) (string, bool, bool) {
	if u == nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return "", false, false
	}
	host := strings.ToLower(u.Hostname())
	api := host == "api.github.com"
	if !api && host != "github.com" && host != "raw.githubusercontent.com" && host != "codeload.github.com" {
		return "", false, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if api {
		if len(parts) < 3 || parts[0] != "repos" {
			return "", true, true
		}
		parts = parts[1:]
	}
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", api, true
	}
	return parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"), api, true
}

type originalGitHubTransport struct {
	base, mirror http.RoundTripper
	settings     Settings
}

func (t *originalGitHubTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	repository, api, github := originalGitHubRequestRepository(request.URL)
	if !github || request.Method != http.MethodGet {
		return t.base.RoundTrip(request)
	}
	preferred := request.Clone(request.Context())
	preferred.Header = request.Header.Clone()
	// Do not let a redirect reuse an earlier repository's selected token.
	preferred.Header.Del("Authorization")
	for key, values := range originalGitHubHeaders(t.settings, repository) {
		preferred.Header[key] = values
	}
	if !api && t.settings.GitHubProxy != "" {
		base, err := originalGitHubMirrorBase(t.settings.GitHubProxy)
		if err != nil {
			return nil, err
		}
		mirror, err := url.Parse(base + request.URL.String())
		if err != nil {
			return nil, err
		}
		mirrored := preferred.Clone(request.Context())
		mirrored.URL = mirror
		mirrored.Host = ""
		// Source forwards the token to the mirror. The project's credential-origin
		// boundary takes precedence: public mirror first, authenticated official
		// origin only on fallback. Never lower global TLS protections.
		mirrored.Header = preferred.Header.Clone()
		mirrored.Header.Del("Authorization")
		response, err := t.mirror.RoundTrip(mirrored)
		if request.Context().Err() != nil {
			return response, err
		}
		if err == nil && response.StatusCode < http.StatusBadRequest {
			return response, nil
		}
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
	}
	return t.base.RoundTrip(preferred)
}
func (t *originalGitHubTransport) CloseIdleConnections() {
	closeOriginalPluginTransport(t.base)
	closeOriginalPluginTransport(t.mirror)
}
func originalPluginEnvironmentTransport() *networkproxy.RequestsTransport {
	return networkproxy.NewRequestsTransport(nil, nil)
}

var _ http.RoundTripper = (*originalGitHubTransport)(nil)
