package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"light-media/internal/plugins"
)

type originalSourceSiteHTTPValues struct {
	cookie                 string
	cookiePresent          bool
	retainCookieOnRedirect bool
	includeLocation        bool
	headers                http.Header
	explicitProxy          map[string]string
}

func originalSourceSiteDomain(address *url.URL) string {
	for _, v := range []string{"u2.dmhy.org", "pt.ecust.pp.ua", "pt.gtkpw.xyz", "pt.gtk.pw"} {
		if strings.Contains(address.String(), v) {
			return v
		}
	}
	parts := strings.Split(address.Host, ".")
	if len(parts) > 3 {
		return address.Host
	}
	if len(parts) > 1 {
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return address.Host
}
func sourceSiteOriginMatch(address *url.URL, name string) bool {
	return strings.ReplaceAll(address.Host, "www.", "") == name
}
func originalSourceSiteTarget(value originalResolvedSourceSite, profile string) (*url.URL, error) {
	base := *value.base
	switch profile {
	case "", "site":
		return &base, nil
	case "tjupt":
		if !sourceSiteOriginMatch(value.base, "tjupt.org") {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "源站点别名不匹配")
		}
		base.Scheme = "https"
		base.Host = "www.tjupt.org"
	case "opencd":
		if !sourceSiteOriginMatch(value.base, "open.cd") {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "源站点别名不匹配")
		}
		base.Scheme = "https"
		base.Host = "www.open.cd"
	case "hdsky":
		if !sourceSiteOriginMatch(value.base, "hdsky.me") {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "源站点别名不匹配")
		}
		base.Scheme = "https"
		base.Host = "hdsky.me"
	case "52pt", "hares", "btschool", "hdcity", "hdupt", "pttime", "pter", "chdbits", "hdarea", "nexushd", "rousi", "ttg", "hdchina", "zhuque", "u2":
		target := map[string]string{"52pt": "52pt.site", "hares": "club.hares.top", "btschool": "pt.btschool.club", "hdcity": "hdcity.city", "hdupt": "pt.hdupt.com", "pttime": "www.pttime.org", "pter": "pterclub.com", "chdbits": "ptchdbits.co", "hdarea": "hdarea.club", "nexushd": "v6.nexushd.org", "rousi": "rousi.pro", "ttg": "totheglory.im", "hdchina": "hdchina.org", "zhuque": "zhuque.in", "u2": "u2.dmhy.org"}[profile]
		if !sourceSiteOriginMatch(value.base, strings.ReplaceAll(target, "www.", "")) {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "源固定站点别名不匹配")
		}
		base.Scheme = "https"
		base.Host = target
	case "brush-mteam":
		host, ok := originalBrushMTeamAPIHost(value.base)
		if !ok {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "刷流 M-Team 来源未获允许")
		}
		base.Scheme, base.Host = "https", host
	case "mteam", "iyuu-mteam", "iyuu-mteam-material-api", "iyuu-mteam-material-cdn":
		matched := false
		for _, p := range strings.Split(value.base.String(), ".") {
			if p == "m-team" {
				matched = true
			}
		}
		if !matched {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "源站点别名不匹配")
		}
		host := strings.ToLower("api." + originalSourceSiteDomain(value.base))
		if host != "api.m-team.cc" && host != "api.m-team.io" && host != "api.m-team.cc:443" && host != "api.m-team.io:443" {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "源站点API来源未获允许")
		}
		base.Scheme = "https"
		base.Host = host
		if profile == "iyuu-mteam-material-cdn" {
			base.Host = "fr1.halomt.com"
		}
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "未知源站点来源模式")
	}
	base.Path = "/"
	base.RawPath = ""
	base.RawQuery = ""
	base.ForceQuery = false
	return &base, nil
}
func originalSourceSiteHeaderOrigin(value string, source, target *url.URL) bool {
	u, e := url.Parse(value)
	return e == nil && u.User == nil && u.Opaque == "" && u.Fragment == "" && (originalOCROrigin(u) == originalOCROrigin(source) || originalOCROrigin(u) == originalOCROrigin(target))
}
func (a *App) originalHostSourceSiteHTTPResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var in plugins.SiteSourceHTTPRequestV1
	if err := originalResourceDecodeBounded(raw, &in, plugins.MaxPayloadBytes); err != nil {
		return nil, err
	}
	input := in.Request
	if input.ID != "" || input.UseSiteUserAgent != nil || input.UseHostUserAgent || len(input.Path) > 8192 || len(input.Query) > 1024 || len(input.Form) > 1024 || len(input.Headers) > 64 || len(input.Credentials) > 8 || len(input.CookieNames) > 128 {
		return nil, bad("INVALID_PLUGIN_VALUE", "源站点HTTP参数无效")
	}
	method := strings.ToUpper(input.Method)
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return nil, bad("INVALID_PLUGIN_VALUE", "源站点HTTP仅支持GET和POST")
	}
	kinds := 0
	if input.Body != nil {
		kinds++
	}
	if input.JSON != nil {
		kinds++
	}
	if input.Form != nil {
		kinds++
	}
	if kinds > 1 {
		return nil, bad("INVALID_PLUGIN_VALUE", "请求正文只能选择一种")
	}
	brushMTeam := in.OriginProfile == "brush-mteam"
	if brushMTeam {
		if err := validateOriginalBrushMTeamRequest(ctx, scope, in, method); err != nil {
			return nil, err
		}
		allow := false
		input.AllowRedirects = &allow
	}
	source, err := a.resolveOriginalSourceSite(ctx, scope, in.Reference, brushMTeam)
	if err != nil {
		return nil, err
	}
	base, err := originalSourceSiteTarget(source, in.OriginProfile)
	if err != nil {
		return nil, err
	}
	if in.OriginProfile == "mteam" {
		if method != "POST" || input.Path != "/api/member/updateLastBrowse" || len(input.Credentials) != 0 || len(input.Query) != 0 {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "源MTeam仅允许原访问更新接口")
		}
	}
	if in.IncludeLocation && (method != "GET" || input.AllowRedirects == nil || *input.AllowRedirects) {
		return nil, bad("INVALID_PLUGIN_VALUE", "源Location仅用于禁止跳转的GET")
	}
	iyuuMTeam := in.OriginProfile == "iyuu-mteam"
	if iyuuMTeam {
		if err := validateOriginalIYUUMTeamRequest(in, method); err != nil {
			return nil, err
		}
		// Keep the token endpoint grant exact even if the server redirects.
		allow := false
		input.AllowRedirects = &allow
	}
	mteamMaterial := in.OriginProfile == "iyuu-mteam-material-api" || in.OriginProfile == "iyuu-mteam-material-cdn"
	if mteamMaterial {
		if err := validateOriginalIYUUMTeamMaterial(in, method); err != nil {
			return nil, err
		}
	}
	if err := validateOriginalSourceRouSiRequest(in, source, base, method); err != nil {
		return nil, err
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
	if mteamMaterial && !scope.iyuuMaterial.consume(source.reference.Snapshot, address) {
		return nil, bad("SITE_HTTP_TARGET_DENIED", "源MTeam材料地址未由当前站点请求授权")
	}
	if iyuuMTeam {
		scope.iyuuMaterial.clear()
	}
	values := &originalSourceSiteHTTPValues{headers: http.Header{}, explicitProxy: map[string]string{}, includeLocation: in.IncludeLocation}
	cookie := ""
	if in.CookieMode != "none" && !input.NoCookies && in.OriginProfile != "mteam" {
		cookie, err = originalSourceSiteFieldString(source.fields, "cookie")
		if err != nil {
			return nil, err
		}
	}
	switch in.CookieMode {
	case "", "parsed":
		values.cookie = originalOCRCookieHeader(cookie)
		values.retainCookieOnRedirect = true
	case "raw":
		values.cookie = cookie
		_, ok := sourceSiteString(source.fields["cookie"])
		values.cookiePresent = ok
	case "none":
		input.NoCookies = true
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "未知源Cookie模式")
	}
	if input.NoCookies {
		values.cookie = ""
		values.cookiePresent = false
	}
	explicitHeaders := in.ExplicitHeaders || in.OriginProfile == "mteam"
	if explicitHeaders {
		values.headers.Set("Accept", "*/*")
	} else {
		values.headers.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	for name, ptr := range input.Headers {
		if originalSiteHTTPForbiddenHeader(name) || !originalSiteHTTPHeaderName(name) {
			return nil, bad("INVALID_PLUGIN_VALUE", "源站点请求头未获允许")
		}
		if ptr == nil {
			values.headers.Del(name)
			continue
		}
		v, e := originalOCRHeaderLatin1(*ptr)
		if e != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "源站点请求头无法编码")
		}
		if (strings.EqualFold(name, "Origin") || strings.EqualFold(name, "Referer")) && !originalSourceSiteHeaderOrigin(v, source.base, base) {
			return nil, bad("SITE_HTTP_TARGET_DENIED", "源站点来源头未获允许")
		}
		values.headers.Set(name, v)
	}
	uaRaw := source.fields["ua"]
	agent := "python-urllib3/2.7.0"
	useUA := in.UserAgent
	if !sourceSiteRawTruth(uaRaw) {
		switch in.OriginProfile {
		case "mteam":
			useUA = "session"
		case "iyuu-mteam":
			useUA = "host"
		}
	}
	switch useUA {
	case "", "source":
		if len(uaRaw) > 0 && string(uaRaw) != "null" {
			v, ok := sourceSiteString(uaRaw)
			if !ok {
				return nil, bad("INVALID_SOURCE_SITE", "源User-Agent不是字符串")
			}
			agent = v
		}
	case "none":
	case "session":
		agent = "python-requests/2.34.2"
	case "host":
		agent = originalPluginServerUserAgent()
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "未知源User-Agent模式")
	}
	if !explicitHeaders && agent == originalPluginServerUserAgent() {
		agent += " Plugin/" + strings.ToLower(scope.installation.InstanceID)
	}
	agent, err = originalOCRHeaderLatin1(agent)
	if err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "源User-Agent无法编码")
	}
	values.headers.Set("User-Agent", agent)
	token, apikey := "", ""
	if in.OriginProfile == "mteam" {
		token, err = originalSourceSiteMTeamToken(source.fields["token"])
		if err != nil {
			return nil, bad("INVALID_SOURCE_SITE", "源Token无法转换")
		}
	}
	for _, credential := range input.Credentials {
		if in.CredentialMode == "rousi" {
			key := "token"
			if credential.Credential == "api_key" {
				key = "apikey"
			}
			value, e := originalSourceRouSiCredential(source.fields[key], credential.Header)
			if e != nil {
				return nil, e
			}
			if credential.Credential == "api_key" {
				apikey = value
			} else {
				token = value
			}
			continue
		}
		switch credential.Credential {
		case "token":
			token, err = originalSourceSiteFieldString(source.fields, "token")
		case "api_key":
			apikey, err = originalSourceSiteFieldString(source.fields, "apikey")
		}
		if err != nil {
			return nil, err
		}
	}
	if in.OriginProfile == "mteam" {
		token = sourceSiteTrim(token)
		if token == "" {
			return nil, bad("SITE_HTTP_CREDENTIAL_MISSING", "站点未配置所需凭据")
		}
		token, err = originalOCRHeaderLatin1(token)
		if err != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "源Token无法编码")
		}
		original, ok := sourceSiteString(source.fields["url"])
		if !ok {
			return nil, bad("INVALID_SOURCE_SITE", "源站点地址无效")
		}
		original = strings.TrimRight(original, "/")
		values.headers.Set("Authorization", token)
		values.headers.Set("Origin", original)
		values.headers.Set("Referer", original+"/index")
		values.cookie = ""
		values.cookiePresent = false
		input.NoCookies = true
	}
	if iyuuMTeam {
		if err := applyOriginalIYUUMTeamHeaders(source, values.headers); err != nil {
			return nil, err
		}
	}
	if brushMTeam {
		if err := applyOriginalBrushMTeamRequest(source, &input, values.headers); err != nil {
			return nil, err
		}
	}
	site := SiteConfig{}
	site.Cookie = cookie
	site.APIKey = apikey
	site.Token = token
	proxySetting := a.Settings().ProxyHost
	if !iyuuMTeam && sourceSiteRawTruth(source.fields["proxy"]) {
		proxy, e := a.proxyConfig()
		if e != nil {
			return nil, e
		}
		if proxy.HTTP != "" {
			values.explicitProxy["http"] = proxy.HTTP
		}
		if proxy.HTTPS != "" {
			values.explicitProxy["https"] = proxy.HTTPS
		}
	}
	guard := func(call context.Context) error {
		if err := source.guard(call); err != nil {
			return err
		}
		if a.Settings().ProxyHost != proxySetting {
			return bad("SITE_CONFIGURATION_CHANGED", "宿主代理配置已变化")
		}
		return nil
	}
	result, err := a.originalExecuteSiteHTTP(ctx, scope, input, method, site, base, address, guard, values)
	if err == nil && (iyuuMTeam || mteamMaterial) {
		recordOriginalIYUUMTeamMaterial(scope.iyuuMaterial, source, result, iyuuMTeam)
	}
	return result, err
}

// This is only the source entry's physical wrapper. A nil-bodied GET must not
// become transport replayable on a reused-socket EOF. The private opaque empty
// body follows the independently reviewed source-public GET mechanism, and
// preserves the caller's nil body for normal307/308 handling. POSTs are untouched.
type originalSourceSiteHTTPTransport struct {
	next            http.RoundTripper
	includeLocation bool
}

func (t originalSourceSiteHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// Requests validates str header values before making a connection. The
	// values here are already host-validated Latin-1; reject Python whitespace
	// at the first byte so net/http cannot silently trim and send that request.
	for _, values := range request.Header {
		for _, value := range values {
			if len(value) > 0 && (unicode.IsSpace(rune(value[0])) || value[0] >= 0x1c && value[0] <= 0x1f) {
				return nil, originalSourceRequestsHeaderError{}
			}
		}
	}
	copy := request.Clone(request.Context())
	// Requests keeps the default HTTPS port in its connection URL/CONNECT
	// authority, but its physical HTTP Host header omits that default port.
	if copy.URL.Scheme == "https" && copy.URL.Port() == "443" {
		copy.Host = copy.URL.Hostname()
		if strings.ContainsRune(copy.Host, ':') {
			copy.Host = "[" + copy.Host + "]"
		}
	}
	copy.GetBody = nil
	if request.Method == "GET" && (request.Body == nil || request.Body == http.NoBody) {
		copy.Body = originalSourcePublicGetBody{}
		copy.GetBody = nil
		copy.ContentLength = 0
	}
	if values, ok := copy.Header["User-Agent"]; ok && len(values) == 1 && values[0] == "" {
		// net/http treats its canonical empty UA as suppression. A second raw-case
		// key emits the empty field while the canonical key suppresses Go's default;
		// header names are case-insensitive and no supplied header can enter here.
		copy.Header["user-agent"] = []string{""}
	}
	response, err := t.next.RoundTrip(copy)
	if err != nil || response == nil {
		return response, err
	}
	if t.includeLocation {
		return response, nil
	}
	switch response.StatusCode {
	case 301, 302, 303, 307, 308:
	default:
		return response, nil
	}
	raw := response.Header.Get("Location")
	if raw == "" {
		return response, nil
	}
	prepared, err := originalSourcePublicRedirectLocation(raw, request.URL)
	if err != nil {
		response.Body.Close()
		return nil, &originalSourcePublicRedirectError{err}
	}
	response.Header = response.Header.Clone()
	response.Header.Set("Location", prepared)
	return response, nil
}

type originalSourceRequestsHeaderError struct{}

func (originalSourceRequestsHeaderError) Error() string { return "source_requests_invalid_header" }
