package app

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"light-media/internal/plugins"
)

func originalBrushMTeamAPIHost(base *url.URL) (string, bool) {
	if base.Scheme != "https" || base.Port() != "" && base.Port() != "443" {
		return "", false
	}
	host := strings.ToLower(base.Hostname())
	for _, suffix := range []string{".m-team.cc", ".m-team.io"} {
		if host == "api"+suffix || host == "api2"+suffix {
			return host, true
		}
		if host == "kp"+suffix || host == "www"+suffix || host == strings.TrimPrefix(suffix, ".") {
			return "api" + suffix, true
		}
	}
	return "", false
}

func validateOriginalBrushMTeamRequest(ctx context.Context, scope originalBaseCallScope, in plugins.SiteSourceHTTPRequestV1, method string) error {
	deny := bad("SITE_HTTP_TARGET_DENIED", "刷流 M-Team 仅允许当前任务核对种子促销")
	if !originalBrushSourceScope(ctx, scope) {
		return deny
	}
	switch scope.phase {
	case "brush", "check", "promotion_expiry":
	default:
		return deny
	}
	r := in.Request
	if in.Reference.SiteID == "" || in.Reference.CustomOrdinal != nil || in.CookieMode != "none" || !in.ExplicitHeaders || in.IncludeLocation || in.CredentialMode != "" || in.UserAgent != "" && in.UserAgent != "source" ||
		method != "POST" || r.Path != "/api/torrent/detail" || len(r.Form) != 1 || r.Form[0].Name != "id" || !originalBrushDetailID.MatchString(r.Form[0].Value) ||
		len(r.Query) != 0 || r.Body != nil || r.JSON != nil || len(r.Headers) != 0 || len(r.Credentials) != 0 || len(r.CookieNames) != 0 || r.CookieOverride != nil ||
		r.Referer != "" || r.TimeoutSeconds != nil || r.AllowRedirects != nil && *r.AllowRedirects || r.RaiseException {
		return deny
	}
	return nil
}

func applyOriginalBrushMTeamRequest(source originalResolvedSourceSite, input *plugins.SiteHTTPRequestV1, headers http.Header) error {
	key, err := originalSourceSiteFieldString(source.fields, "apikey")
	if err != nil {
		return err
	}
	if key == "" {
		return bad("SITE_HTTP_CREDENTIAL_MISSING", "M-Team 站点未配置 API Key")
	}
	key, err = originalOCRHeaderLatin1(key)
	if err != nil {
		return bad("INVALID_SOURCE_SITE", "M-Team API Key 无法编码")
	}
	headers.Set("x-api-key", key)
	seconds := float64(15)
	if raw := source.fields["timeout"]; sourceSiteRawTruth(raw) {
		var n json.Number
		var parsed float64
		if len(raw) > 0 && raw[0] == '"' {
			value, _ := sourceSiteString(raw)
			var integer int64
			integer, err = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			parsed = float64(integer)
		} else if string(raw) == "true" {
			parsed = 1
		} else if err = json.Unmarshal(raw, &n); err == nil {
			parsed, err = n.Float64()
		}
		if err != nil || parsed <= 0 {
			return bad("INVALID_SOURCE_SITE", "M-Team 站点超时无效")
		}
		seconds = math.Min(math.Trunc(parsed), 30)
		if seconds <= 0 {
			return bad("INVALID_SOURCE_SITE", "M-Team 站点超时无效")
		}
	}
	input.TimeoutSeconds = &seconds
	return nil
}
