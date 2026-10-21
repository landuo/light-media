package app

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/unicode"
)

// sourcePluginDefaultLink follows the installed base plugin's false/empty link
// fallback, using the plugin class rather than an installation/clone ID.
// MP_DOMAIN/UrlUtils behavior is based on the public MoviePilot v2 config.py
// and utils/url.py inspected 2026-10-21, not a pinned installed-core version.
// This helper is for fixed plugin fragments, not a general Python URL library.
func sourcePluginDefaultLink(domain, class, explicit string) (any, error) {
	if explicit != "" {
		return explicit, nil
	}
	if domain == "" {
		return nil, nil
	}
	if !strings.HasSuffix(domain, "/") {
		domain += "/"
	}
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "http://" + domain
	}
	// urljoin with a fragment keeps the base path/query and replaces its fragment.
	// Do not ResolveReference here: Go also cleans dot segments in the base path.
	domain = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(domain)
	base, _, _ := strings.Cut(domain, "#")
	if _, err := url.Parse(base); err != nil {
		return nil, errors.New("plugin default link cannot parse configured domain")
	}
	path, query, _ := strings.Cut(base, "?")
	query = pluginDefaultLinkQuery(query)
	if query != "" {
		path += "?" + query
	}
	return path + "#/plugins?tab=installed&id=" + class, nil
}

// Python parse_qs drops blank values, groups repeated keys in first-seen order,
// and urlencode(doseq=True) uses quote_plus. url.Values.Encode sorts keys and
// Go ParseQuery rejects semicolons/invalid escapes, so neither matches here.
func pluginDefaultLinkQuery(raw string) string {
	keys := []string{}
	values := map[string][]string{}
	for _, part := range strings.Split(raw, "&") {
		key, value, found := strings.Cut(part, "=")
		if !found || value == "" {
			continue
		}
		key, value = pluginDefaultLinkUnquote(key), pluginDefaultLinkUnquote(value)
		if _, ok := values[key]; !ok {
			keys = append(keys, key)
		}
		values[key] = append(values[key], value)
	}
	pairs := []string{}
	for _, key := range keys {
		for _, value := range values[key] {
			pairs = append(pairs, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	return strings.Join(pairs, "&")
}

func pluginDefaultLinkUnquote(raw string) string {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '%' && i+2 < len(raw) {
			if b, err := strconv.ParseUint(raw[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(b))
				i += 2
				continue
			}
		}
		if raw[i] == '+' {
			out = append(out, ' ')
		} else {
			out = append(out, raw[i])
		}
	}
	// Decode malformed UTF-8 as Python unquote(errors="replace") does.
	decoded, _ := unicode.UTF8.NewDecoder().String(string(out))
	return decoded
}
