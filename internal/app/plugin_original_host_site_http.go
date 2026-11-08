package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
)

// originalSiteRequestGuardError keeps an authority failure distinct from an
// ordinary vendor/transport error even if the current guard later recovers.
// This private marker is used only by the finite site HTTP and CAPTCHA paths.
type originalSiteRequestGuardError struct{ cause error }

func (e *originalSiteRequestGuardError) Error() string { return e.cause.Error() }
func (e *originalSiteRequestGuardError) Unwrap() error { return e.cause }
func originalSiteRequestGuardFailure(err error) error {
	if err == nil {
		return nil
	}
	return &originalSiteRequestGuardError{cause: err}
}

type originalSiteHTTPGuardTransport struct {
	next  http.RoundTripper
	guard func(context.Context) error
	sent  *atomic.Bool
}

func (t originalSiteHTTPGuardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.guard(r.Context()); err != nil {
		return nil, originalSiteRequestGuardFailure(err)
	}
	t.sent.Store(true)
	return t.next.RoundTrip(r)
}

func originalSiteHTTPURL(base *url.URL, value string) (*url.URL, error) {
	relative, err := url.Parse(value)
	if err != nil || relative.User != nil || relative.Fragment != "" || relative.Opaque != "" || relative.Host != "" || relative.IsAbs() || strings.ContainsAny(value, "\r\n\x00") {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP路径须为站点内相对路径")
	}
	address := base.ResolveReference(relative)
	if originalOCROrigin(address) != originalOCROrigin(base) {
		return nil, bad("SITE_HTTP_TARGET_DENIED", "站点HTTP请求不能跨来源")
	}
	return address, nil
}

func originalSiteHTTPPairs(pairs []plugins.SiteHTTPPairV1) string {
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, url.QueryEscape(pair.Name)+"="+url.QueryEscape(pair.Value))
	}
	return strings.Join(parts, "&")
}

func originalSiteHTTPForbiddenHeader(name string) bool {
	switch strings.ToLower(name) {
	case "cookie", "authorization", "proxy-authorization", "proxy-authenticate", "host", "connection", "transfer-encoding", "content-length", "te", "trailer", "upgrade", "api-key", "x-api-key", "api-token", "user-agent", "idempotency-key", "x-idempotency-key":
		return true
	}
	return false
}

func (a *App) originalHostSiteHTTPResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var input plugins.SiteHTTPRequestV1
	if err := originalResourceDecodeBounded(raw, &input, plugins.MaxPayloadBytes); err != nil {
		return nil, err
	}
	if scope.object == nil || scope.object.app != a {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点HTTP请求需要当前运行对象")
	}
	if input.ID == "" || len(input.Path) > 8192 || len(input.Query) > 1024 || len(input.Form) > 1024 || len(input.Headers) > 64 || len(input.Credentials) > 8 || len(input.CookieNames) > 128 {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP参数超出允许范围")
	}
	method := strings.ToUpper(input.Method)
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP仅支持GET和POST")
	}
	bodyKinds := 0
	if input.Body != nil {
		bodyKinds++
	}
	if input.Form != nil {
		bodyKinds++
	}
	if input.JSON != nil {
		bodyKinds++
	}
	if bodyKinds > 1 {
		return nil, bad("INVALID_PLUGIN_VALUE", "请求正文只能选择form、json或bytes一种")
	}
	ctx = allowDisabledSiteRequests(ctx)
	site, err := a.siteForRequest(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(site.BaseURL)
	if err != nil || base.Hostname() == "" || base.User != nil || base.Scheme != "http" && base.Scheme != "https" {
		return nil, siteIdentityError()
	}
	address, err := originalSiteHTTPURL(base, input.Path)
	if err != nil {
		return nil, err
	}
	if query := originalSiteHTTPPairs(input.Query); query != "" {
		if address.RawQuery != "" {
			address.RawQuery += "&"
		}
		address.RawQuery += query
	}
	identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	origin, err := nativeConfiguredSiteOrigin(site)
	if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
		return nil, siteIdentityError()
	}
	siteKey, proxySetting := a.managedSiteKey(site)+":proxy="+strconv.FormatBool(site.Proxy), a.Settings().ProxyHost
	guard := func(call context.Context) error {
		if err := call.Err(); err != nil {
			return err
		}
		if err := scope.validate(call); err != nil {
			return err
		}
		current, err := a.siteForRequest(allowDisabledSiteRequests(call), site.ID)
		if err != nil || a.managedSiteKey(current)+":proxy="+strconv.FormatBool(current.Proxy) != siteKey || a.Settings().ProxyHost != proxySetting {
			return bad("SITE_CONFIGURATION_CHANGED", "站点配置已变化")
		}
		currentIdentity, err := a.Store.nativeSiteByAppID(call, site.ID)
		if err != nil || currentIdentity.State != "active" || currentIdentity.CanonicalOrigin != origin {
			return siteIdentityError()
		}
		return nil
	}
	return a.originalExecuteSiteHTTP(ctx, scope, input, method, site, base, address, guard, nil)
}

// One existing physical owner serves both entry points. Source-only values are
// resolved and authorized before this call; the ordinary entry passes nil.
func (a *App) originalExecuteSiteHTTP(ctx context.Context, scope originalBaseCallScope, input plugins.SiteHTTPRequestV1, method string, site SiteConfig, base, address *url.URL, guard func(context.Context) error, source *originalSourceSiteHTTPValues) (json.RawMessage, error) {
	var err error
	cookie := originalOCRCookieHeader(site.Cookie)
	if source != nil {
		cookie = source.cookie
	}
	if input.NoCookies {
		cookie = ""
	}
	if input.CookieOverride != nil {
		if input.NoCookies || input.CookieNames != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "Cookie替换不能与其他Cookie选项混用")
		}
		parts := []string{}
		for _, pair := range *input.CookieOverride {
			if strings.ContainsAny(pair.Name, ";=\r\n\x00") || pair.Name == "" || strings.ContainsAny(pair.Value, "\r\n\x00") {
				return nil, bad("INVALID_PLUGIN_VALUE", "站点Cookie替换无效")
			}
			parts = append(parts, pair.Name+"="+pair.Value)
		}
		cookie = strings.Join(parts, "; ")
		if source != nil {
			cookie = originalOCRCookieHeader(cookie)
		}
	}

	if input.CookieNames != nil {
		selected := map[string]bool{}
		for _, name := range input.CookieNames {
			selected[name] = true
		}
		parts := []string{}
		for _, part := range strings.Split(cookie, "; ") {
			name, _, _ := strings.Cut(part, "=")
			if selected[name] {
				parts = append(parts, part)
			}
		}
		cookie = strings.Join(parts, "; ")
	}
	headers := http.Header{}
	if source != nil {
		headers = source.headers.Clone()
	} else if len(input.Headers) == 0 {
		headers.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	} else {
		headers.Set("Accept", "*/*")
		for name, value := range input.Headers {
			if originalSiteHTTPForbiddenHeader(name) || !originalSiteHTTPHeaderName(name) {
				return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP请求头未获允许")
			}
			if value == nil {
				headers.Del(name)
				continue
			}
			v, err := originalOCRHeaderLatin1(*value)
			if err != nil {
				return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP请求头无法编码")
			}
			if strings.EqualFold(name, "Origin") || strings.EqualFold(name, "Referer") {
				u, e := url.Parse(v)
				if e != nil || u.User != nil || originalOCROrigin(u) != originalOCROrigin(base) {
					return nil, bad("SITE_HTTP_TARGET_DENIED", "站点HTTP来源头不能跨来源")
				}
			}
			headers.Set(name, v)
		}
	}
	if source == nil {
		agent := "python-urllib3/2.7.0"
		if len(input.Headers) > 0 {
			agent = "python-requests/2.34.2"
		}
		useSiteUA := len(input.Headers) == 0
		if input.UseSiteUserAgent != nil {
			useSiteUA = *input.UseSiteUserAgent
		}
		if useSiteUA && site.UserAgent != "" {
			agent = site.UserAgent
		}
		if input.UseSiteUserAgent != nil && !*input.UseSiteUserAgent {
			agent = "python-urllib3/2.7.0"
		}
		if input.UseHostUserAgent {
			if input.UseSiteUserAgent != nil {
				return nil, bad("INVALID_PLUGIN_VALUE", "宿主与站点User-Agent选择不能混用")
			}
			agent = originalPluginServerUserAgent()
		}
		if len(input.Headers) == 0 && agent == originalPluginServerUserAgent() {
			agent += " Plugin/" + strings.ToLower(scope.installation.InstanceID)
		}

		agent, err = originalOCRHeaderLatin1(agent)
		if err != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "站点User-Agent无法编码")
		}
		headers.Set("User-Agent", agent)
	}
	if input.Referer != "" {
		referer, err := originalSiteHTTPURL(base, input.Referer)
		if err != nil {
			return nil, err
		}
		headers.Set("Referer", referer.String())
	}
	for _, credential := range input.Credentials {
		header := strings.ToLower(credential.Header)
		authHeader := header == "authorization" || header == "api-token" || header == "x-api-key" || header == "api-key"
		if !originalSiteHTTPHeaderName(header) || !authHeader && (originalSiteHTTPForbiddenHeader(header) || header == "origin" || header == "referer") {
			return nil, bad("INVALID_PLUGIN_VALUE", "站点凭据请求头未获允许")
		}
		value := ""
		switch credential.Credential {
		case "api_key":
			value = site.APIKey
		case "token":
			value = site.Token
		default:
			return nil, bad("INVALID_PLUGIN_VALUE", "站点凭据字段未获允许")
		}
		if value == "" {
			return nil, bad("SITE_HTTP_CREDENTIAL_MISSING", "站点未配置所需凭据")
		}
		value, err = originalOCRHeaderLatin1(credential.Prefix + value)
		if err != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "站点凭据请求头无法编码")
		}
		headers.Set(header, value)
	}
	cookie, err = originalOCRHeaderLatin1(cookie)
	if err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点Cookie无法编码")
	}
	if cookie != "" || source != nil && source.cookiePresent && !input.NoCookies {
		headers.Set("Cookie", cookie)
	}
	body := input.Body
	if input.Form != nil {
		body = []byte(originalSiteHTTPPairs(input.Form))
		if len(body) > 0 && headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if input.JSON != nil {
		body, err = originalSiteHTTPJSON(input.JSON)
		if err != nil {
			return nil, err
		}
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/json")
		}
	}
	if len(body) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "站点HTTP正文超出允许范围")
	}
	idle := 20 * time.Second
	if input.TimeoutSeconds != nil && *input.TimeoutSeconds != 0 {
		seconds := *input.TimeoutSeconds
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > float64((1<<63-1)/int64(time.Second)) {
			return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP超时无效")
		}
		idle = time.Duration(seconds * float64(time.Second))
		if idle <= 0 {
			return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP超时无效")
		}
	}
	explicit := map[string]string{}
	if site.Proxy {
		proxy, err := a.proxyConfig()
		if err != nil {
			return nil, err
		}
		if proxy.HTTP != "" {
			explicit["http"] = proxy.HTTP
		}
		if proxy.HTTPS != "" {
			explicit["https"] = proxy.HTTPS
		}
	}
	if source != nil {
		explicit = source.explicitProxy
	}
	transport, err := networkproxy.NewRequestsProxyTransport(networkproxy.NewRequestsTimeoutTransport(idle), os.Environ(), explicit, address)
	if err != nil {
		return nil, bad("NETWORK_CONFIGURATION_INVALID", "宿主代理配置无效")
	}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	var physical http.RoundTripper = transport
	if source != nil {
		physical = originalSourceSiteHTTPTransport{next: transport, includeLocation: source.includeLocation}
	}
	var sent atomic.Bool
	client := &http.Client{Jar: jar, Transport: originalSiteHTTPGuardTransport{physical, guard, &sent}, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if input.AllowRedirects != nil && !*input.AllowRedirects {
			return http.ErrUseLastResponse
		}
		if len(via) > 30 {
			return errors.New("site_http_redirect_limit")
		}
		if next.URL.User != nil || originalOCROrigin(next.URL) != originalOCROrigin(base) || source != nil && (next.URL.Fragment != "" || next.URL.Opaque != "") {
			err := bad("SITE_HTTP_TARGET_DENIED", "站点HTTP请求不能跨来源跳转")
			if source != nil {
				return originalSiteRequestGuardFailure(err)
			}
			return err
		}
		// requests keeps an explicit initial Referer and does not synthesize one.
		next.Header.Del("Referer")
		if value := headers.Get("Referer"); value != "" {
			next.Header.Set("Referer", value)
		}
		// requests preserves its initial unscoped cookies alongside redirect Set-Cookie.
		if cookie != "" && (source == nil || source.retainCookieOnRedirect) {
			next.Header.Set("Cookie", cookie)
		} else {
			next.Header.Del("Cookie")
		}
		if next.Response != nil && next.Response.StatusCode != 307 && next.Response.StatusCode != 308 {
			next.Header.Del("Content-Type")
			next.Header.Del("Content-Length")
			next.Header.Del("Transfer-Encoding")
		}
		return originalSiteRequestGuardFailure(guard(next.Context()))
	}}
	req, err := http.NewRequestWithContext(ctx, method, address.String(), bytes.NewReader(body))
	if err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点HTTP请求无效")
	}
	req.Header = headers
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	if err := guard(ctx); err != nil {
		return nil, err
	}
	response, requestErr := client.Do(req)
	if response != nil {
		defer response.Body.Close()
	}
	var denied *originalSiteRequestGuardError
	revoked := errors.As(requestErr, &denied)
	if revoked && (method != "POST" || !sent.Load()) {
		return nil, denied.cause
	}
	out := plugins.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "unknown", ScopeChanged: revoked}
	if requestErr == nil {
		content, readErr := io.ReadAll(io.LimitReader(response.Body, originalHostNetworkContentLimit+1))
		if len(content) > originalHostNetworkContentLimit {
			return nil, bad("PLUGIN_VALUE_TOO_LARGE", "站点响应超出共享传输上限")
		}
		if readErr == nil {
			content, readErr = originalHostNetworkDecode(content, response.Header.Get("Content-Encoding"))
		}
		if readErr == nil {
			if content == nil {
				content = []byte{}
			}
			cookies := originalSiteHTTPResponseCookies(response.Request.URL, response.Cookies())
			out.Response = &plugins.SiteHTTPResponseV1{StatusCode: response.StatusCode, Content: content, ContentType: response.Header.Get("Content-Type"), Cookies: cookies}
			if source != nil && source.includeLocation && (response.StatusCode == 301 || response.StatusCode == 302) {
				if locations := response.Header.Values("Location"); len(locations) != 0 {
					location := strings.Join(locations, ", ")
					if len(location) > 8192 {
						return nil, bad("PLUGIN_VALUE_TOO_LARGE", "站点跳转地址超出共享传输上限")
					}
					out.Response.Location = &location
				}
			}
			out.RequestState = "completed"
		} else {
			requestErr = readErr
		}
	}
	if err := guard(ctx); err != nil {
		if method != "POST" || !sent.Load() {
			return nil, err
		}
		out.ScopeChanged = true // Keep completed/unknown write facts; never claim it was unsent.
	}
	if requestErr != nil && input.RaiseException && !out.ScopeChanged {
		return nil, bad("SITE_HTTP_REQUEST_FAILED", "站点HTTP请求失败，执行结果可能未知")
	}
	encoded, err := json.Marshal(out)
	if err == nil && len(encoded) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "站点响应超出共享传输上限")
	}
	return encoded, err
}

func originalSiteHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r > 127 || !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", r) {
			return false
		}
	}
	return true
}

func originalSiteHTTPResponseCookies(target *url.URL, values []*http.Cookie) []plugins.SiteHTTPPairV1 {
	result := []plugins.SiteHTTPPairV1{}
	positions := map[string]int{}
	for _, cookie := range values {
		// requests.Response.cookies ignores rejected/expired cookies rather than
		// applying a deletion to an earlier accepted same-response cookie.
		jar, _ := cookiejar.New(nil)
		jar.SetCookies(target, []*http.Cookie{cookie})
		lookup := *target
		lookup.Scheme = "https"
		if strings.HasPrefix(cookie.Path, "/") {
			lookup.Path = cookie.Path
		}
		accepted := false
		for _, value := range jar.Cookies(&lookup) {
			if value.Name == cookie.Name {
				accepted = true
				break
			}
		}
		if !accepted {
			continue
		}
		copyCookie := plugins.SiteHTTPPairV1{Name: cookie.Name, Value: cookie.Value}
		if n, exists := positions[cookie.Name]; exists {
			result[n] = copyCookie
		} else {
			positions[cookie.Name] = len(result)
			result = append(result, copyCookie)
		}
	}
	return result
}

func (a *App) originalHostSiteCredentialsResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var input plugins.SiteCredentialsRequestV1
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if scope.object == nil || scope.object.app != a || input.ID == "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "私有站点凭据需要当前对象与准确站点ID")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	site, err := a.siteForRequest(allowDisabledSiteRequests(ctx), input.ID)
	if err != nil {
		return nil, err
	}
	identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	origin, err := nativeConfiguredSiteOrigin(site)
	if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
		return nil, siteIdentityError()
	}
	key := a.managedSiteKey(site)
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	current, err := a.siteForRequest(allowDisabledSiteRequests(ctx), site.ID)
	if err != nil || a.managedSiteKey(current) != key {
		return nil, bad("SITE_CONFIGURATION_CHANGED", "站点配置已变化")
	}
	return json.Marshal(plugins.SiteCredentialsResultV1{SchemaVersion: 1, ID: site.ID, Cookie: site.Cookie, UserAgent: site.UserAgent, APIKey: site.APIKey, Token: site.Token})
}
