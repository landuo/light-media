package app

import (
	"net/url"
	"regexp"
	"strings"
)

var originalRSSDetailID = regexp.MustCompile(`^[A-Za-z0-9_.~-]{1,128}$`)

// Only the configured origin and public tracker identity cross the event
// boundary. In some feeds the RSS parser falls back to using link as enclosure.
func originalRSSDetailsURL(base *url.URL, site SiteConfig, link, enclosure string) string {
	if link == "" || len(link) > 4096 {
		return ""
	}
	detail, err := url.Parse(link)
	if err != nil || detail.Opaque != "" {
		return ""
	}
	detail = base.ResolveReference(detail)
	if (detail.Scheme != "http" && detail.Scheme != "https") || detail.Host == "" || detail.User != nil || originalOCROrigin(detail) != originalOCROrigin(base) {
		return ""
	}
	material, _ := url.Parse(enclosure)
	if material != nil && originalOCROrigin(material) == originalOCROrigin(base) && detail.Path == material.Path {
		return ""
	}
	params, err := url.ParseQuery(detail.RawQuery)
	if err != nil || detail.RawPath != "" || detail.Path == "" {
		return ""
	}
	secrets := []string{site.APIKey, site.Token, site.MessageToken, site.Cookie}
	for _, part := range strings.Split(site.Cookie, ";") {
		if _, value, ok := strings.Cut(part, "="); ok {
			secrets = append(secrets, strings.TrimSpace(value))
		}
	}
	feed, _ := url.Parse(site.RSS)
	for _, source := range []*url.URL{detail, material, feed} {
		if source == nil {
			continue
		}
		for key, values := range source.Query() {
			if originalRSSDetailCredential(key) {
				secrets = append(secrets, values...)
			}
		}
	}
	isPublicID := func(value string) bool {
		if !originalRSSDetailID.MatchString(value) || originalRSSDetailCredential(value) {
			return false
		}
		for _, secret := range secrets {
			if secret != "" && (value == secret || len(secret) >= 8 && strings.Contains(value, secret)) {
				return false
			}
		}
		return true
	}
	for _, segment := range strings.Split(detail.Path, "/") {
		if segment != "" && !isPublicID(segment) {
			return ""
		}
	}
	query := url.Values{}
	for _, key := range []string{"id", "torrentid", "torrent_id", "tid", "hash"} {
		if values := params[key]; len(values) == 1 && isPublicID(values[0]) {
			query.Set(key, values[0])
		}
	}
	if hit := params["hit"]; len(hit) == 1 && (hit[0] == "0" || hit[0] == "1") {
		query.Set("hit", hit[0])
	}
	return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: detail.Path, RawQuery: query.Encode()}).String()
}

func originalRSSDetailCredential(key string) bool {
	switch strings.ToLower(key) {
	case "passkey", "pass_key", "pass-key", "rsskey", "rss_key", "auth", "authorization", "authkey", "auth_key", "token", "access_token", "refresh_token", "api_key", "api-key", "apikey", "secret", "cookie", "session", "sessionid", "session_id", "password", "passwd", "uid":
		return true
	}
	return false
}
