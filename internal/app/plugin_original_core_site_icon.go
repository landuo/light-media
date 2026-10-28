package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"light-media/internal/sites"
)

type originalSiteIcon struct {
	SiteID, ConfigKey, URL string
	Data                   []byte
}

func (a *App) consumeOriginalCoreSiteIcon(ctx context.Context, raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return bad("PLUGIN_EVENT_INVALID", "站点更新事件数据不是对象")
	}
	if !originalBaseJSONTruthy(fields["domain"]) {
		return nil
	}
	var domain string
	if json.Unmarshal(fields["domain"], &domain) != nil {
		return nil
	}
	if strings.HasPrefix(domain, "http") {
		domain = standardSiteEventDomain(domain)
	}
	for _, site := range a.Settings().Sites {
		if standardSiteEventDomain(site.BaseURL) != domain {
			continue
		}
		value := originalSiteIcon{SiteID: site.ID, ConfigKey: a.managedSiteKey(site)}
		err := a.withSite(allowDisabledSiteRequests(ctx), site, false, func(client *sites.Client) error {
			var readErr error
			value.URL, value.Data, readErr = client.ReadFavicon(ctx, originalPluginServerUserAgent())
			return readErr
		})
		if value.URL == "" {
			return err
		}
		// Source caches the chosen URL even when its HTTP request fails. Keep
		// that private URL encrypted; the browser only receives an owned route.
		a.settingsMu.Lock()
		current, currentErr := a.siteForRequest(allowDisabledSiteRequests(ctx), site.ID)
		if currentErr != nil || a.managedSiteKey(current) != value.ConfigKey {
			a.settingsMu.Unlock()
			return bad("SITE_CHANGED", "站点设置已变化，请重新缓存图标")
		}
		sealed, sealErr := a.Store.Seal(value)
		if sealErr == nil {
			sealErr = a.Store.Put(ctx, "site_icon", site.ID, struct{ Sealed []byte }{sealed})
		}
		a.settingsMu.Unlock()
		if sealErr != nil {
			return sealErr
		}
		if err != nil {
			a.logOriginalPluginContext(ctx, "WARN", "site_icon_unavailable", "站点图标未能缓存，保留现有图标回退")
		}
		return nil
	}
	return nil
}

func (a *App) serveManagedSiteIcon(w http.ResponseWriter, r *http.Request, id string) {
	site, err := a.siteForRequest(allowDisabledSiteRequests(r.Context()), id)
	if err != nil {
		a.writeError(w, r, &APIError{"NOT_FOUND", "站点图标不存在", 404})
		return
	}
	var record struct{ Sealed []byte }
	var icon originalSiteIcon
	if a.Store.Get(r.Context(), "site_icon", id, &record) != nil || a.Store.Unseal(record.Sealed, &icon) != nil || icon.SiteID != id || icon.ConfigKey != a.managedSiteKey(site) || len(icon.Data) == 0 {
		a.writeError(w, r, &APIError{"NOT_FOUND", "站点图标不存在", 404})
		return
	}
	kind := http.DetectContentType(icon.Data)
	if strings.Contains(string(icon.Data[:min(len(icon.Data), 512)]), "<svg") {
		kind = "image/svg+xml"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(icon.Data)
}
