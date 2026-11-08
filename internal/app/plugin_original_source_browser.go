package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"light-media/internal/browserrender"
	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
)

func (a *App) originalHostSourceBrowserResource(ctx context.Context, scope originalBaseCallScope, in plugins.SiteRenderRequest) (json.RawMessage, error) {
	if in.ID != "" || in.TimeoutSeconds != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "源页面渲染不能混用站点ID或普通等待参数")
	}
	// The installed MTeam adapter never invokes the ordinary page helper and
	// derives a separate API origin with token-only requests. Rendering must
	// not transfer its source Cookie to that derived origin.
	if in.OriginProfile == "mteam" || in.OriginProfile == "brush-mteam" || strings.HasPrefix(in.OriginProfile, "iyuu-mteam") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "源页面不能使用MTeam派生API来源")
	}
	source, err := a.resolveOriginalSourceSite(ctx, scope, *in.SourceReference, false)
	if err != nil {
		return nil, err
	}
	base, err := originalSourceSiteTarget(source, in.OriginProfile)
	if err != nil {
		return nil, err
	}
	address, err := originalSiteHTTPURL(base, in.Path)
	if err != nil {
		return nil, err
	}
	cookie, invalidCookie := originalSourceBrowserHeaderValue(source.fields["cookie"])
	agent, invalidAgent := originalSourceBrowserHeaderValue(source.fields["ua"])
	proxySetting := a.Settings().ProxyHost
	guard := func(call context.Context) error {
		if err := scope.validate(call); err != nil {
			return err
		}
		if err := source.guard(call); err != nil {
			return err
		}
		if a.Settings().ProxyHost != proxySetting {
			return bad("SITE_CONFIGURATION_CHANGED", "站点代理配置已变化")
		}
		return nil
	}
	rawTimeout := append(json.RawMessage(nil), in.SourceTimeout...)
	if in.SourceTimeoutFloat != "" {
		if len(rawTimeout) != 0 {
			return nil, bad("INVALID_PLUGIN_VALUE", "源页面等待参数不能混用普通JSON与非有限数值")
		}
		switch in.SourceTimeoutFloat {
		case "nan":
			rawTimeout = json.RawMessage(`NaN`)
		case "infinity":
			rawTimeout = json.RawMessage(`Infinity`)
		case "-infinity":
			rawTimeout = json.RawMessage(`-Infinity`)
		default:
			return nil, bad("INVALID_PLUGIN_VALUE", "未知源页面等待数值")
		}
	}
	request := browserrender.Request{URL: address.String(), Cookie: cookie, UserAgent: agent, Timeout: 60 * time.Second, SourceTimeout: rawTimeout, SourceInvalidCookie: invalidCookie, SourceInvalidUserAgent: invalidAgent, Guard: guard}
	request.SourceLog = func(level, code string) {
		message := "浏览器页面获取失败"
		switch code {
		case "browser_cookie_parse_ignored":
			message = "解析站点Cookie失败，已忽略"
		case "browser_flaresolverr_value_too_large":
			message = "FlareSolverr响应超过插件资源上限"
		case "browser_flaresolverr_json_failed":
			message = "FlareSolverr响应JSON解析失败"
		case "browser_flaresolverr_unconfigured":
			message = "未配置FlareSolverr，尝试浏览器渲染"
		case "browser_flaresolverr_authenticated_session":
			message = "通过FlareSolverr代理会话获取页面"
		case "browser_flaresolverr_cleanup_attempted":
			message = "已尝试清理FlareSolverr会话"
		case "browser_flaresolverr_failed", "browser_flaresolverr_session_failed", "browser_flaresolverr_response_failed", "browser_flaresolverr_page_failed":
			message = "FlareSolverr页面获取失败，尝试浏览器渲染"
		}
		// Retain source helper log stages/levels without copying raw browser
		// stderr, service messages, proxy credentials or source values to logs.
		a.logOriginalPluginContext(ctx, level, code, message)
	}
	if len(request.SourceTimeout) == 0 {
		request.SourceTimeout = json.RawMessage(`60`)
	}
	if in.Headless != nil {
		request.Headless = *in.Headless
	}
	if sourceSiteRawTruth(source.fields["proxy"]) {
		request.SourceProxy, err = originalSourceBrowserProxy(proxySetting, address)
		if err != nil {
			return nil, err
		}
	}
	return a.originalHostRenderBrowser(ctx, request, guard)
}

// Retain the existing ordinary host conversion of PROXY_SERVER, including
// literal percent escapes in credentials; no requests environment fallback.
func originalSourceBrowserProxy(setting string, address *url.URL) (*browserrender.ProxySettings, error) {
	proxy, err := networkproxy.Resolve(setting, nil)
	if err != nil {
		return nil, err
	}
	raw := proxy.URL(address)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	authority := raw[strings.Index(raw, "://")+3:]
	if start := strings.LastIndex(authority, "@") + 1; strings.HasPrefix(authority[start:], "[") {
		if end := strings.Index(authority[start:], "]"); end >= 0 {
			host = authority[start+1 : start+end]
		}
	}
	if zone := strings.Index(host, "%"); zone >= 0 {
		host = strings.ToLower(host[:zone]) + host[zone:]
	} else {
		host = strings.ToLower(host)
	}
	server := u.Scheme + "://" + host
	if u.Port() != "" {
		port, _ := strconv.Atoi(u.Port())
		server += ":" + strconv.Itoa(port)
	}
	out := &browserrender.ProxySettings{Server: server}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		out.Username, out.Password, _ = strings.Cut(authority[:at], ":")
	}
	return out, nil
}

// Cloak's wrapper omits every falsey user_agent; Page omits falsey cookies.
// Truthy nonstrings are retained as source-only deferred validation faults:
// FS ignores UA and catches Cookie parsing errors before a successful response.
func originalSourceBrowserHeaderValue(raw json.RawMessage) (string, bool) {
	if !sourceSiteRawTruth(raw) {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", true
	}
	return value, false
}
