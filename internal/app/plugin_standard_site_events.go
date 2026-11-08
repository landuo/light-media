package app

import (
	"context"
	"net/url"
	"strings"
)

// The original endpoint has distinct create and edit payloads. Priority-only
// batches and unrelated settings saves do not produce SiteUpdated.
func (a *App) emitStandardSiteUpdated(ctx context.Context, site SiteConfig, created bool) {
	domain := standardSiteEventDomain(site.BaseURL)
	if created {
		a.emitStandardBusinessEvent(ctx, "site.updated", map[string]any{"domain": domain})
		return
	}
	identity, err := a.Store.nativeSiteByAppID(context.WithoutCancel(ctx), site.ID)
	if err != nil || identity.State != "active" {
		a.log("ERROR", "plugins", "business_event_identity", "插件站点更新事件身份无法核对", "", "")
		return
	}
	sourceID, err := a.Store.nativeSourceEventSiteID(context.WithoutCancel(ctx), identity)
	if err != nil {
		a.log("ERROR", "plugins", "business_event_identity", "插件站点更新事件身份无法核对", "", "")
		return
	}
	name := site.Name
	// The source full-save event uses site_in.name literally. Native management
	// has a normalized UI projection; only an admitted authenticated snapshot
	// can supply the preserved original value after the owning commit.
	source, _, _, err := a.Store.nativeSourceSiteFields(context.WithoutCancel(ctx), site, identity)
	if err != nil {
		a.log("ERROR", "plugins", "business_event_identity", "插件站点更新事件字段无法核对", "", "")
		return
	}
	if source != nil {
		name = source.Name
	}
	a.emitStandardBusinessEvent(ctx, "site.updated", map[string]any{
		"site_id":  sourceID,
		"domain":   domain,
		"name":     name,
		"site_url": strings.TrimRight(site.BaseURL, "/") + "/",
	})
}

// Original StringUtils.get_url_domain deliberately differs from publicsuffix
// and from an optional configured Domain. Keep its special-domain and >3-level
// rules on the actual committed URL; do not silently modernize its semantics.
func standardSiteEventDomain(value string) string {
	if value == "" {
		return ""
	}
	for _, special := range []string{"u2.dmhy.org", "pt.ecust.pp.ua", "pt.gtkpw.xyz", "pt.gtk.pw"} {
		if strings.Contains(value, special) {
			return special
		}
	}
	host := value
	if strings.HasPrefix(value, "http") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		host = parsed.Host
	}
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) > 3 || len(parts) < 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}
