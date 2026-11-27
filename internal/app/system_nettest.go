package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"light-media/internal/networkproxy"
)

// Only the first three fields are sent to the browser. All outbound requests
// are selected by ID; no URL, credentials or matching rules come from the caller.
type systemNetTestTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`

	requestPath, redirectPrefix, extraPrefix string
	expectedText                             string
	direct, githubMirror                     bool
}

var systemNetTestTargets = []systemNetTestTarget{
	{ID: "tmdb_api", Name: "api.themoviedb.org", Icon: "tmdb", requestPath: "/3/movie/550", redirectPrefix: "/3/"},
	{ID: "tmdb_api_alt", Name: "api.tmdb.org", Icon: "tmdb", requestPath: "/3/movie/550", redirectPrefix: "/3/"},
	{ID: "tmdb_web", Name: "www.themoviedb.org", Icon: "tmdb"},
	{ID: "tvdb_api", Name: "api.thetvdb.com", Icon: "tvdb", requestPath: "/series/81189"},
	{ID: "fanart_api", Name: "webservice.fanart.tv", Icon: "fanart"},
	{ID: "telegram_api", Name: "api.telegram.org", Icon: "telegram", extraPrefix: "https://core.telegram.org/"},
	{ID: "wechat_api", Name: "qyapi.weixin.qq.com", Icon: "wechat", requestPath: "/cgi-bin/gettoken", direct: true},
	{ID: "douban_api", Name: "frodo.douban.com", Icon: "douban", extraPrefix: "https://www.douban.com/doubanapp/frodo", direct: true},
	{ID: "slack_api", Name: "slack.com", Icon: "slack", extraPrefix: "https://www.slack.com/", direct: true},
	{ID: "pip_proxy", Name: "pypi.org", Icon: "python", requestPath: "/simple/rsa/", redirectPrefix: "/simple/", expectedText: "pypi:repository-version"},
	{ID: "github_proxy_web", Name: "github.com", Icon: "github", requestPath: "/jxxghp/MoviePilot/blob/v2/README.md", expectedText: "MoviePilot", githubMirror: true},
	{ID: "github_api", Name: "api.github.com", Icon: "github"},
	{ID: "github_codeload", Name: "codeload.github.com", Icon: "github", extraPrefix: "https://github.com/"},
	{ID: "github_proxy_raw", Name: "raw.githubusercontent.com", Icon: "github", requestPath: "/jxxghp/MoviePilot/v2/README.md", expectedText: "MoviePilot", githubMirror: true},
}

type systemNetTestResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	TimeMS  int64  `json:"time_ms"`
}

func (a *App) testNetworkTarget(ctx context.Context, targetID, requestID string) (systemNetTestResult, error) {
	index := slices.IndexFunc(systemNetTestTargets, func(t systemNetTestTarget) bool { return t.ID == targetID })
	if index < 0 {
		return systemNetTestResult{}, bad("NETTEST_UNKNOWN_TARGET", "测试目标不存在")
	}
	target := systemNetTestTargets[index]
	settings := a.Settings()
	start := time.Now()
	transport, err := target.transport(settings)
	result := systemNetTestResult{Message: "网络代理配置无效，请检查代理设置"}
	if err == nil {
		defer transport.CloseIdleConnections()
		result.Message = target.probe(ctx, settings, transport)
		result.Success = result.Message == ""
		if result.Success {
			result.Message = "连接正常"
		}
	}
	result.TimeMS = time.Since(start).Milliseconds()
	level := "INFO"
	if !result.Success && ctx.Err() == nil {
		level = "WARN"
	}
	a.writeLog(map[string]any{
		"id": id("log_"), "time": now(), "level": level, "module": "system", "event": "network_test",
		"message":    fmt.Sprintf("测试 %s 网络连通性：%s（%d ms）", target.Name, result.Message, result.TimeMS),
		"request_id": requestID, "duration_ms": result.TimeMS, "app_version": appVersion,
	})
	return result, nil
}

func (target systemNetTestTarget) transport(settings Settings) (*http.Transport, error) {
	proxy := ""
	if !target.direct {
		config, err := resolveProxyConfig(settings.ProxyHost)
		if err != nil {
			return nil, err
		}
		proxy = config.HTTPS
	}
	// A diagnostic must exercise the selected route even if the business
	// transport would fall back to a direct connection.
	transport, err := networkproxy.Apply(nil, proxy)
	if err == nil && proxy != "" {
		// A rejected CONNECT is an HTTP response from the proxy, not from
		// the target. Keep just the numeric code and discard its reason text.
		transport.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, response *http.Response) error {
			if response.StatusCode != http.StatusOK {
				return netTestProxyStatus(response.StatusCode)
			}
			return nil
		}
	}
	return transport, err
}

type netTestProxyStatus int

func (s netTestProxyStatus) Error() string {
	return fmt.Sprintf("网络代理未建立连接隧道（HTTP %d）", s)
}

var (
	errNetTestRedirect = errors.New("测试目标发生了未授权跳转")
	errNetTestHops     = errors.New("测试目标重定向超过 3 次")
)

func (target systemNetTestTarget) probe(ctx context.Context, settings Settings, transport http.RoundTripper) string {
	origin := "https://" + target.Name
	address := origin + target.requestPath
	prefixes := []string{origin + target.redirectPrefix}
	if target.extraPrefix != "" {
		prefixes = append(prefixes, target.extraPrefix)
	}
	mirror := target.githubMirror && settings.GitHubProxy != ""
	if mirror {
		base, err := originalGitHubMirrorBase(settings.GitHubProxy)
		if err != nil || !strings.HasPrefix(base, "https://") {
			return "GitHub 加速代理须使用有效的 HTTPS 地址"
		}
		address = base + address
		prefixes = append(prefixes, base+origin+"/")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "测试目标地址无效"
	}
	tmdbAPI := target.ID == "tmdb_api" || target.ID == "tmdb_api_alt"
	if tmdbAPI {
		if settings.TMDB.AccessToken != "" {
			req.Header.Set("Authorization", "Bearer "+settings.TMDB.AccessToken)
		} else if settings.TMDB.APIKey != "" {
			req.URL.RawQuery = url.Values{"api_key": {settings.TMDB.APIKey}}.Encode()
		}
	}
	if target.Icon == "github" && !mirror {
		req.Header = originalGitHubHeaders(settings, "")
	}
	req.Header.Set("User-Agent", originalGitHubUserAgent)
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return errNetTestHops
		}
		if !slices.ContainsFunc(prefixes, func(prefix string) bool { return netTestPrefixAllowed(next.URL, prefix) }) {
			return errNetTestRedirect
		}
		next.Header.Del("Referer")
		if !strings.EqualFold(next.URL.Host, req.URL.Host) {
			next.Header.Del("Authorization")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return netTestConnectionError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail := "目标返回错误状态"
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			detail = "认证失败"
			if tmdbAPI && settings.TMDB.APIKey == "" && settings.TMDB.AccessToken == "" {
				detail = "未配置 TMDB 凭据"
			} else if target.Icon == "github" && !mirror {
				detail = "GitHub 认证失败，请检查令牌配置"
			}
		case http.StatusForbidden:
			detail = "访问被拒绝"
		case http.StatusTooManyRequests:
			detail = "请求过于频繁"
		}
		return fmt.Sprintf("已收到 HTTP %d，%s", resp.StatusCode, detail)
	}
	if target.expectedText == "" && !tmdbAPI {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "已收到 HTTP 200，读取响应内容失败"
	}
	if tmdbAPI {
		var movie struct{ ID int }
		if json.Unmarshal(body, &movie) != nil || movie.ID != 550 {
			return "已收到 HTTP 200，响应中缺少预期的 TMDB 影片"
		}
	} else if !bytes.Contains(bytes.ToLower(body), bytes.ToLower([]byte(target.expectedText))) {
		if mirror {
			return "已收到 HTTP 200，GitHub 加速代理未返回预期的仓库内容"
		}
		return "已收到 HTTP 200，响应内容与预期不符"
	}
	return ""
}

func netTestPrefixAllowed(u *url.URL, prefix string) bool {
	p, err := url.Parse(prefix)
	if err != nil || u.Scheme != "https" || u.User != nil || !strings.EqualFold(strings.TrimSuffix(p.Host, ":443"), strings.TrimSuffix(u.Host, ":443")) {
		return false
	}
	// Refuse decoded traversal, including inside an accelerator's embedded
	// upstream path, before testing the permitted path prefix.
	if strings.Contains(u.Path, "\\") || slices.Contains(strings.Split(u.Path, "/"), "..") {
		return false
	}
	scope := strings.TrimSuffix(p.Path, "/")
	return scope == "" || u.Path == scope || strings.HasPrefix(u.Path, scope+"/")
}

func netTestConnectionError(err error) string {
	var proxyStatus netTestProxyStatus
	var op *net.OpError
	var dns *net.DNSError
	var network net.Error
	var tlsError *tls.CertificateVerificationError
	switch {
	case errors.Is(err, errNetTestRedirect):
		return errNetTestRedirect.Error()
	case errors.Is(err, errNetTestHops):
		return errNetTestHops.Error()
	case errors.As(err, &proxyStatus):
		return proxyStatus.Error()
	case errors.Is(err, context.Canceled):
		return "测试已取消"
	case errors.As(err, &network) && network.Timeout():
		return "连接超时"
	case errors.As(err, &dns):
		return "域名解析失败"
	case errors.As(err, &tlsError):
		return "TLS 证书验证失败"
	case errors.As(err, &op) && op.Op == "proxyconnect":
		return "无法连接网络代理"
	default:
		return "无法建立网络连接"
	}
}
