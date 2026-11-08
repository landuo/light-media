package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"light-media/internal/browserrender"
	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
)

func (a *App) originalHostBrowserResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var in plugins.SiteRenderRequest
	if err := originalResourceDecode(raw, &in); err != nil {
		return nil, err
	}
	if in.SourceReference != nil {
		return a.originalHostSourceBrowserResource(ctx, scope, in)
	}
	if len(in.SourceTimeout) != 0 || in.SourceTimeoutFloat != "" || in.OriginProfile != "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "原值页面参数须绑定源引用")
	}
	if in.ID == "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "页面渲染须指定准确站点ID")
	}
	ctx = allowDisabledSiteRequests(ctx)
	site, err := a.siteForRequest(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(site.BaseURL)
	if err != nil || base.Hostname() == "" {
		return nil, siteIdentityError()
	}
	relative, err := url.Parse(in.Path)
	if err != nil || relative.IsAbs() || relative.Host != "" || relative.User != nil || relative.Fragment != "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "页面渲染路径须为站点内相对路径")
	}
	address := base.ResolveReference(relative)
	if address.Scheme != base.Scheme || address.Host != base.Host {
		return nil, bad("INVALID_PLUGIN_VALUE", "页面渲染不能变更站点来源")
	}
	identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	origin, err := nativeConfiguredSiteOrigin(site)
	if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
		return nil, siteIdentityError()
	}
	key, proxySetting := a.managedSiteKey(site), a.Settings().ProxyHost
	guard := func(ctx context.Context) error {
		if err := scope.validate(ctx); err != nil {
			return err
		}
		current, err := a.siteForRequest(ctx, site.ID)
		if err != nil || a.managedSiteKey(current) != key || a.Settings().ProxyHost != proxySetting {
			return bad("SITE_CONFIGURATION_CHANGED", "站点配置已变化")
		}
		identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
		if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
			return siteIdentityError()
		}
		return nil
	}
	request := browserrender.Request{URL: address.String(), Cookie: site.Cookie, UserAgent: site.UserAgent, Timeout: 60 * time.Second, Guard: guard}
	if in.Headless != nil {
		request.Headless = *in.Headless
	}
	if in.TimeoutSeconds != nil {
		if *in.TimeoutSeconds < 0 || int64(*in.TimeoutSeconds) > int64((1<<63-1)/int64(time.Second)) {
			return nil, bad("INVALID_PLUGIN_VALUE", "页面等待秒数无效")
		}
		request.Timeout = time.Duration(*in.TimeoutSeconds) * time.Second
	}
	if site.Proxy {
		// The source caller passes PROXY_SERVER, not requests' PROXY. It has
		// no environment fallback and retains percent escapes in userinfo.
		proxy, err := networkproxy.Resolve(proxySetting, nil)
		if err != nil {
			return nil, err
		}
		if raw := proxy.URL(address); raw != "" {
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
			p := &browserrender.ProxySettings{Server: server}
			if at := strings.LastIndex(authority, "@"); at >= 0 {
				p.Username, p.Password, _ = strings.Cut(authority[:at], ":")
			}
			request.SourceProxy = p
		}
	}
	return a.originalHostRenderBrowser(ctx, request, guard)
}

func (a *App) originalHostRenderBrowser(ctx context.Context, request browserrender.Request, guard func(context.Context) error) (json.RawMessage, error) {
	config := browserrender.ConfigFromEnvironment(os.Getenv)
	config.Emulation, config.FlareSolverrURL = a.originalHostBrowserEmulation, a.originalHostFlareSolverrURL
	config.Humanize, config.HumanPreset = a.originalHostBrowserHumanize, a.originalHostBrowserHumanPreset
	config.TempDir = filepath.Join(a.Dir, "work", "browser")
	if err := os.MkdirAll(config.TempDir, 0700); err != nil {
		return nil, err
	}
	var html *string
	var sourceValue []byte
	var renderErr error
	if len(request.SourceTimeout) > 0 {
		var result browserrender.PageResult
		result, renderErr = config.RenderSource(ctx, request)
		html, sourceValue = result.HTML, result.SourceValueJSON
	} else {
		html, renderErr = config.Render(ctx, request)
	}
	if renderErr != nil {
		var revoked *browserrender.GuardError
		if errors.As(renderErr, &revoked) {
			return nil, revoked.Cause
		}
		if err := guard(ctx); err != nil {
			return nil, err
		}
		code := "browser_render_failed"
		var failed *browserrender.Error
		if errors.As(renderErr, &failed) {
			code = failed.Code
			if len(request.SourceTimeout) > 0 && code == "browser_value_too_large" {
				return nil, bad("PLUGIN_VALUE_TOO_LARGE", "插件页面结果超过资源上限")
			}
		}
		if request.SourceLog == nil {
			a.logOriginalPluginContext(ctx, "ERROR", code, "插件页面渲染失败")
		}
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	out, err := json.Marshal(plugins.SiteRenderResult{HTML: html, SourceValueJSON: sourceValue})
	if err != nil {
		return nil, err
	}
	if len(out) > plugins.MaxPayloadBytes {
		if sourceValue != nil {
			return nil, bad("PLUGIN_VALUE_TOO_LARGE", "插件页面结果超过资源上限")
		}
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "渲染HTML超过插件资源上限")
	}
	return out, nil
}

func originalHostBrowserStartup(lookup func(string) (string, bool)) (string, string) {
	emulation, exists := lookup("BROWSER_EMULATION")
	if !exists {
		emulation = "cloakbrowser"
	}
	fs, _ := lookup("FLARESOLVERR_URL")
	return emulation, fs
}

func originalHostBrowserHumanStartup(lookup func(string) (string, bool)) (bool, string, error) {
	humanize := true
	if value, present := lookup("CLOAKBROWSER_HUMANIZE"); present {
		switch strings.ToLower(value) {
		case "true", "1", "yes", "y", "t", "on":
			humanize = true
		case "false", "0", "no", "n", "f", "off":
			humanize = false
		default:
			return false, "", bad("BROWSER_CONFIGURATION_INVALID", "CLOAKBROWSER_HUMANIZE须为布尔值")
		}
	}
	preset, present := lookup("CLOAKBROWSER_HUMAN_PRESET")
	if !present {
		preset = "default"
	}
	return humanize, preset, nil
}
