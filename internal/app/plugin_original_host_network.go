package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
)

func originalHostNetworkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u == nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, bad("INVALID_PLUGIN_VALUE", "远程文本地址须为不含账号或片段的HTTP(S)地址")
	}
	return u, nil
}

// The installed ordinary caller uses RequestUtils(proxies=settings.PROXY,
// headers=settings.GITHUB_HEADERS if selected, timeout=15).get_res(url).
// It does not use PluginHelper's mirror/fallback strategy or a shared Session.
func (a *App) originalHostNetworkResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request plugins.NetworkGetRequestV1
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	address, err := originalHostNetworkURL(request.URL)
	if err != nil {
		return nil, err
	}
	settings := a.Settings()
	proxy, err := a.proxyConfig()
	if err != nil {
		return nil, err
	}
	explicit := map[string]string{}
	if proxy.HTTP != "" {
		explicit["http"] = proxy.HTTP
	}
	if proxy.HTTPS != "" {
		explicit["https"] = proxy.HTTPS
	}
	base := networkproxy.NewRequestsTimeoutTransport(15 * time.Second)
	transport, err := networkproxy.NewRequestsProxyTransport(base, os.Environ(), explicit, address)
	if err != nil {
		return nil, bad("NETWORK_CONFIGURATION_INVALID", "宿主代理配置无效")
	}
	defer transport.CloseIdleConnections()
	guard := func() error {
		if err := scope.validate(ctx); err != nil {
			return err
		}
		current := a.Settings()
		if current.ProxyHost != settings.ProxyHost || current.GitHubToken != settings.GitHubToken {
			return bad("NETWORK_CONFIGURATION_CHANGED", "宿主网络配置已变化")
		}
		return nil
	}
	jar, _ := cookiejar.New(nil)
	crossedOrigin := false
	client := &http.Client{Jar: jar, Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) > 30 {
			return bad("NETWORK_REDIRECT_LIMIT", "远程文本重定向次数过多")
		}
		if _, err := originalHostNetworkURL(next.URL.String()); err != nil {
			return err
		}
		// A caller must never cause a saved host token to cross origins or
		// be reintroduced after the source requests library would remove it.
		if next.URL.Scheme != address.Scheme || !strings.EqualFold(next.URL.Host, address.Host) {
			crossedOrigin = true
		}
		if crossedOrigin {
			next.Header.Del("Authorization")
		}
		return guard()
	}}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "远程文本地址无效")
	}
	// RequestUtils' default User-Agent=None removes requests' session UA;
	// the pinned vendor then supplies its own urllib3 wire default.
	httpRequest.Header.Set("User-Agent", "python-urllib3/2.7.0")
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	if _, _, official := originalGitHubRequestRepository(address); request.GitHubHeaders && official && settings.GitHubToken != "" {
		httpRequest.Header = originalGitHubHeaders(Settings{GitHubToken: settings.GitHubToken}, "")
		httpRequest.Header.Set("Accept", "*/*")
	}
	httpRequest.Header.Set("Accept-Encoding", "gzip, deflate, br")
	httpRequest.Header.Set("Connection", "keep-alive")
	if err := guard(); err != nil {
		return nil, err
	}
	response, requestErr := client.Do(httpRequest)
	if response != nil {
		defer response.Body.Close()
	}
	out := plugins.NetworkGetResultV1{SchemaVersion: 1}
	if requestErr == nil {
		// Reserve JSON/base64 overhead before allocation. The full serialized
		// result is checked as well; no response headers or cookie jar escape.
		const limit = originalHostNetworkContentLimit
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if len(body) > limit {
			return nil, bad("PLUGIN_VALUE_TOO_LARGE", "远程文本超出共享传输上限")
		}
		if readErr == nil {
			body, readErr = originalHostNetworkDecode(body, response.Header.Get("Content-Encoding"))
			if bounded, ok := readErr.(*APIError); ok {
				return nil, bounded
			}
		}
		if readErr == nil {
			if body == nil {
				body = []byte{}
			}
			out.Response = &plugins.NetworkResponseV1{StatusCode: response.StatusCode, Content: body}
		}
	}
	if err := guard(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(out)
	if err == nil && len(encoded) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "远程文本超出共享传输上限")
	}
	return encoded, err
}
