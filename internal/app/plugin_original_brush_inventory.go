package app

import (
	"context"
	"database/sql"
	"net/url"
	"sort"
	"strings"

	"light-media/internal/plugins"
)

// originalBrushInventory projects existing core state. The resource caller
// holds pluginMu so settings and their identities have one publication boundary.
// It never connects to a site/downloader or allocates an identity. All reads in
// the transaction use tx: Store.DB has one connection.
func (a *App) originalBrushInventory(ctx context.Context) (plugins.BrushInventoryV1, error) {
	out := plugins.BrushInventoryV1{SchemaVersion: 1, Sites: []plugins.BrushSiteReferenceV1{}, Downloaders: []plugins.BrushDownloaderReferenceV1{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	settings := a.Settings()
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	view, err := a.siteAccountViewTx(ctx, tx, settings.Sites)
	if err != nil {
		return out, err
	}
	for _, site := range settings.Sites {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		identity, err := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
		if err != nil {
			return out, err
		}
		origin, err := nativeConfiguredSiteOrigin(site)
		if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
			return out, siteIdentityError()
		}
		sourceID, err := a.Store.nativeSourcePublicSiteID(ctx, tx, identity)
		if err != nil {
			return out, err
		}
		ref := plugins.BrushSiteReferenceV1{SourceID: sourceID, Name: site.Name, Kind: site.Kind, Public: site.Public, Enabled: site.Enabled, HasAPIKey: !site.Public && site.APIKey != "", LocalStatistics: plugins.BrushLocalStatisticsV1{Status: "missing"}}
		if origin != "" {
			ref.URL = &origin
			domain := originalBrushInventoryDomain(site, origin)
			ref.Domain = &domain
		}
		// Keep the existing view's exact ordering, including later error rows
		// on a day with an observation. Do not silently choose a different row.
		if latest := view.latest(site.ID); len(latest) != 0 {
			data, err := a.siteAccountStatisticTx(ctx, tx, view, latest[0])
			if err != nil {
				return out, err
			}
			ref.LocalStatistics = plugins.BrushLocalStatisticsV1{Status: "available", Ratio: data.Ratio, Upload: data.Upload, Download: data.Download, UpdatedDay: &data.UpdatedDay, UpdatedTime: &data.UpdatedTime}
		}
		out.Sites = append(out.Sites, ref)
	}
	for _, downloader := range settings.Downloaders {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		out.Downloaders = append(out.Downloaders, plugins.BrushDownloaderReferenceV1{ID: downloader.ID, Name: downloader.Name, Kind: downloader.Kind, Enabled: downloaderEnabled(downloader)})
	}
	sort.Slice(out.Sites, func(i, j int) bool { return out.Sites[i].SourceID < out.Sites[j].SourceID })
	sort.Slice(out.Downloaders, func(i, j int) bool { return out.Downloaders[i].ID < out.Downloaders[j].ID })
	if err := ctx.Err(); err != nil {
		return out, err
	}
	// Explicit completion is necessary before the caller validates its live
	// scope through Store.DB. A deferred rollback alone would deadlock there.
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

// Only a hostname derived from the authenticated canonical origin or its
// reviewed registry domain is exposed. Never return raw BaseURL, userinfo,
// query, RSS, UA, cookies, tokens or downloader connection settings.
func originalBrushInventoryDomain(site SiteConfig, origin string) string {
	u, _ := url.Parse(origin) // nativeConfiguredSiteOrigin already validated it.
	host := strings.ToLower(u.Hostname())
	domain := host
	if _, _, registered, err := siteImportOrigin(origin); err == nil {
		domain = registered
	}
	candidate := strings.ToLower(site.Domain)
	if candidate == host || candidate == domain {
		return candidate
	}
	if candidate != "" && !strings.ContainsAny(candidate, "/:@?#\\ \t\r\n") && strings.HasSuffix(host, "."+candidate) {
		if _, _, _, err := siteImportOrigin("https://" + candidate); err == nil {
			return candidate
		}
	}
	return domain
}
