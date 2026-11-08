package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"light-media/internal/plugins"
	"light-media/internal/plugins/iyuuautoseed"
)

func validateOriginalIYUUMTeamRequest(in plugins.SiteSourceHTTPRequestV1, method string) error {
	request := in.Request
	if method != "POST" || request.Path != "/api/torrent/genDlToken" ||
		len(request.Query) != 1 || request.Query[0].Name != "id" || request.Query[0].Value == "" || len(request.Query[0].Value) > 128 ||
		in.CookieMode != "none" || in.UserAgent != "source" || !in.ExplicitHeaders || in.CredentialMode != "" ||
		request.Form != nil || request.JSON != nil || request.Body != nil || len(request.Headers) != 0 || request.Referer != "" ||
		len(request.Credentials) != 0 || request.CookieNames != nil || request.CookieOverride != nil {
		return bad("SITE_HTTP_TARGET_DENIED", "源MTeam仅允许辅种取种令牌接口")
	}
	return nil
}

// M-Team's observed token material route uses its API origin and one exact
// HTTPS CDN. These GET profiles never carry a cookie, key, or bearer token.
func validateOriginalIYUUMTeamMaterial(in plugins.SiteSourceHTTPRequestV1, method string) error {
	request := in.Request
	deny := bad("SITE_HTTP_TARGET_DENIED", "源MTeam材料仅允许受限GET")
	if method != "GET" || !in.IncludeLocation || request.AllowRedirects == nil || *request.AllowRedirects ||
		in.CookieMode != "none" || !in.ExplicitHeaders || (in.UserAgent != "source" && in.UserAgent != "host") || in.CredentialMode != "" ||
		len(request.Query) != 0 || request.Form != nil || request.JSON != nil || request.Body != nil ||
		len(request.Headers) != 0 || request.Referer != "" || len(request.Credentials) != 0 ||
		request.CookieNames != nil || request.CookieOverride != nil {
		return deny
	}
	if in.OriginProfile == "iyuu-mteam-material-cdn" {
		_, query, _ := strings.Cut(request.Path, "?")
		values, err := url.ParseQuery(query)
		if err != nil {
			return deny
		}
		for _, key := range []string{"app_id", "playload", "sign", "t", "v"} {
			if len(values[key]) != 1 || values[key][0] == "" {
				return deny
			}
			delete(values, key)
		}
		if len(values) != 0 && !(len(values) == 1 && len(values["https"]) == 1 && values.Get("https") == "1") {
			return deny
		}
	}
	return nil
}

// A grant belongs to one reverse SDK scope and one site configuration
// snapshot. An unrelated or already consumed URL cannot open an alias.
type originalIYUUMTeamMaterialGrant struct {
	mu     sync.Mutex
	digest [32]byte
}

func (g *originalIYUUMTeamMaterialGrant) clear() {
	if g != nil {
		g.mu.Lock()
		g.digest = [32]byte{}
		g.mu.Unlock()
	}
}

func originalIYUUMaterialDigest(snapshot string, address *url.URL) [32]byte {
	return sha256.Sum256([]byte(snapshot + "\x00" + originalOCROrigin(address) + address.RequestURI()))
}

func (g *originalIYUUMTeamMaterialGrant) consume(snapshot string, address *url.URL) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.digest != originalIYUUMaterialDigest(snapshot, address) {
		return false
	}
	g.digest = [32]byte{}
	return true
}

func recordOriginalIYUUMTeamMaterial(grant *originalIYUUMTeamMaterialGrant, site originalResolvedSourceSite, raw json.RawMessage, tokenReply bool) {
	if grant == nil {
		return
	}
	var result plugins.SiteHTTPResultV1
	if json.Unmarshal(raw, &result) != nil || result.RequestState != "completed" || result.ScopeChanged || result.Response == nil {
		return
	}
	response := result.Response
	var address string
	if tokenReply {
		var token map[string]json.RawMessage
		body := bytes.TrimPrefix(response.Content, []byte{0xef, 0xbb, 0xbf})
		if response.StatusCode >= 400 || json.Unmarshal(body, &token) != nil || json.Unmarshal(token["data"], &address) != nil || address == "" {
			return
		}
		// IYUU appends the tracker HTTPS option to its initial material URL.
		if strings.Contains(address, "?") {
			address += "&https=1"
		} else {
			address += "?https=1"
		}
	} else {
		if response.Location == nil || (response.StatusCode != 301 && response.StatusCode != 302) {
			return
		}
		address = *response.Location
	}
	if address == "" || len(address) > 8192 {
		return
	}
	target, err := url.Parse(address)
	if err != nil || target.User != nil || target.Fragment != "" || target.Opaque != "" || target.Scheme != "https" {
		return
	}
	for _, profile := range []string{"iyuu-mteam-material-api", "iyuu-mteam-material-cdn"} {
		base, err := originalSourceSiteTarget(site, profile)
		if err != nil || originalOCROrigin(base) != originalOCROrigin(target) {
			continue
		}
		canonical, err := originalSiteHTTPURL(base, target.RequestURI())
		if err == nil {
			grant.mu.Lock()
			grant.digest = originalIYUUMaterialDigest(site.reference.Snapshot, canonical)
			grant.mu.Unlock()
		}
		return
	}
}

func applyOriginalIYUUMTeamHeaders(site originalResolvedSourceSite, headers http.Header) error {
	key, err := originalSourceSiteFieldString(site.fields, "apikey")
	if err != nil {
		return err
	}
	if key == "" {
		return bad("SITE_HTTP_CREDENTIAL_MISSING", "站点未配置所需API密钥")
	}
	key, err = originalOCRHeaderLatin1(key)
	if err != nil {
		return bad("INVALID_PLUGIN_VALUE", "源API密钥无法编码")
	}
	headers.Set("Content-Type", "application/x-www-form-urlencoded")
	headers.Set("Accept", "application/json, text/plain, */*")
	headers.Set("X-Api-Key", key)
	return nil
}

type originalSourceSiteLimit struct {
	config  [3]int
	limiter *iyuuautoseed.SiteRateLimiter
}

func (a *App) originalHostSourceSiteCheck(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var input plugins.SiteSourceCheckRequestV1
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if input.Domain == "" || len(input.Domain) > 512 || strings.ContainsAny(input.Domain, "/?#@\r\n\x00") {
		return nil, bad("INVALID_PLUGIN_VALUE", "源站点流控域名无效")
	}
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil {
		return nil, err
	}
	var config [3]int
	for _, row := range rows {
		if row.fields["domain"] == input.Domain {
			config = [3]int{row.site.LimitInterval, row.site.LimitCount, row.site.LimitSeconds}
			break
		}
	}
	state := &a.pluginOriginalSourceSites
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	out := plugins.SiteSourceCheckResultV1{SchemaVersion: 1}
	if a.originalSourceSiteRegistryLocked().Get(input.Domain, nil) != nil {
		if state.limits == nil {
			state.limits = map[string]originalSourceSiteLimit{}
		}
		limit := state.limits[input.Domain]
		if limit.limiter == nil || limit.config != config {
			limit = originalSourceSiteLimit{config: config, limiter: iyuuautoseed.NewSiteRateLimiter(config[0], config[1], config[2], nil, nil)}
			state.limits[input.Domain] = limit
		}
		out.Limited, out.Message = limit.limiter.Check()
	}
	return json.Marshal(out)
}
