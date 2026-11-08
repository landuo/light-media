package app

import (
	"encoding/json"
	"light-media/internal/plugins"
	"net/url"
	"strings"
)

// RouSi's three exact source calls normalize their selected field, while ordinary
// source credentials and BaseHeaderCookie retain their existing raw behavior.
func validateOriginalSourceRouSiRequest(in plugins.SiteSourceHTTPRequestV1, source originalResolvedSourceSite, target *url.URL, method string) error {
	if in.CredentialMode == "" {
		return nil
	}
	denied := func() error { return bad("SITE_HTTP_TARGET_DENIED", "源RouSi认证请求不匹配") }
	if in.CredentialMode != "rousi" {
		return bad("INVALID_PLUGIN_VALUE", "未知源凭据模式")
	}
	input := in.Request
	if !originalSourceRouSiOrigin(source.base) || in.OriginProfile != "rousi" || target.Scheme != "https" || target.Host != "rousi.pro" || !in.ExplicitHeaders || in.UserAgent != "" && in.UserAgent != "source" || !input.NoCookies || in.CookieMode != "none" || len(input.CookieNames) > 0 || input.CookieOverride != nil || len(input.Query) > 0 || input.Body != nil || input.Form != nil || input.Referer != "" || len(input.Credentials) != 1 {
		return denied()
	}
	c := input.Credentials[0]
	if c.Prefix != "" {
		return denied()
	}
	auth := strings.EqualFold(c.Header, "Authorization")
	switch {
	case method == "GET" && input.Path == "/api/v1/profile":
		if c.Credential != "api_key" || !auth || input.JSON != nil {
			return denied()
		}
	case method == "GET" && input.Path == "/api/points/attendance/stats":
		if c.Credential != "token" || !auth || input.JSON != nil {
			return denied()
		}
	case method == "POST" && input.Path == "/api/points/attendance":
		if !(c.Credential == "api_key" && strings.EqualFold(c.Header, "api-token") || c.Credential == "token" && auth) {
			return denied()
		}
		var body map[string]json.RawMessage
		var mode string
		if json.Unmarshal(input.JSON, &body) != nil || len(body) != 1 || json.Unmarshal(body["mode"], &mode) != nil || mode != "fixed" {
			return denied()
		}
	default:
		return denied()
	}
	for name, value := range input.Headers {
		if value == nil {
			return denied()
		}
		switch {
		case strings.EqualFold(name, "Accept"):
			if *value != "application/json, text/plain, */*" {
				return denied()
			}
		case strings.EqualFold(name, "Content-Type"):
			if method != "POST" || *value != "application/json" {
				return denied()
			}
		default:
			return denied()
		}
	}
	return nil
}
func originalSourceRouSiCredential(raw json.RawMessage, header string) (string, error) {
	value, e := originalSourceSiteMTeamToken(raw)
	if e != nil {
		return "", bad("INVALID_SOURCE_SITE", "源RouSi凭据无法转换")
	}
	if value == "" {
		return "", bad("SITE_HTTP_CREDENTIAL_MISSING", "站点未配置所需凭据")
	}
	if strings.EqualFold(header, "Authorization") && !strings.HasPrefix(strings.ToLower(value), "bearer ") {
		value = "Bearer " + value
	}
	return value, nil
}

// The source match uses canonical rouSi hosts without a nondefault port. This
// finite alias must not move a credential from an arbitrary host/port.
func originalSourceRouSiOrigin(u *url.URL) bool {
	return u != nil && (u.Host == "rousi.pro" || u.Host == "www.rousi.pro")
}
