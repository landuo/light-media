package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"light-media/internal/metainfo"
	"light-media/internal/plugins"
	"light-media/internal/plugins/brushflow"
	"light-media/internal/sites"
)

func originalHostSitesCapability(method string) bool {
	switch method {
	case plugins.SitesSourceIndexerCapability, plugins.SitesSourceIndexersCapability, plugins.SitesSourceRefreshCapability, plugins.SitesSourceRefreshAllCapability, plugins.SitesSourceAccountsCapability:
		return true
	case plugins.SitesReadCapability, plugins.SitesAccountReadCapability, plugins.SitesRefreshCapability, plugins.SitesResultWriteCapability, plugins.SitesBrowseCapability, plugins.SitesRSSCapability, plugins.SitesTorrentDownloadCapability:
		return true
	}
	return false
}

func (a *App) originalHostSitesResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	switch method {
	case plugins.SitesSourceIndexerCapability, plugins.SitesSourceIndexersCapability, plugins.SitesSourceRefreshCapability, plugins.SitesSourceRefreshAllCapability, plugins.SitesSourceAccountsCapability:
		return a.originalHostSourceSitesResource(ctx, scope, method, raw)
	case plugins.SitesReadCapability:
		var in plugins.SiteHostRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		if in.SourceProjection {
			out, err := a.originalSourceConfiguredRows(ctx, in)
			if err != nil {
				return nil, err
			}
			if err := scope.validate(ctx); err != nil {
				return nil, err
			}
			return json.Marshal(out)
		}
		out := plugins.SiteHostResultV1{SchemaVersion: 1, Items: []plugins.SiteHostRowV1{}}
		nativeOrder := map[string]int64{}
		configured := a.Settings().Sites
		for _, s := range configured {
			if in.ID != "" && s.ID != in.ID || in.ActiveOnly && !s.Enabled {
				continue
			}
			identity, err := a.Store.nativeSiteByAppID(ctx, s.ID)
			if err != nil {
				return nil, err
			}
			origin, err := nativeConfiguredSiteOrigin(s)
			if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
				return nil, siteIdentityError()
			}
			nativeOrder[s.ID] = identity.SourceID
			sourceID, err := a.Store.nativeSourcePublicSiteID(ctx, a.Store.DB, identity)
			if err != nil {
				return nil, err
			}
			out.Items = append(out.Items, plugins.SiteHostRowV1{ID: s.ID, SourceID: sourceID, Name: s.Name, Kind: s.Kind, Domain: originalBrushInventoryDomain(s, origin), URL: origin, Priority: s.Priority, Enabled: s.Enabled, Public: s.Public, Proxy: s.Proxy, Render: s.Render})
		}
		sort.SliceStable(out.Items, func(i, j int) bool {
			if in.OrderByPriority && out.Items[i].Priority != out.Items[j].Priority {
				return out.Items[i].Priority < out.Items[j].Priority
			}
			return nativeOrder[out.Items[i].ID] < nativeOrder[out.Items[j].ID]
		})
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		return json.Marshal(out)
	case plugins.SitesAccountReadCapability:
		var in plugins.SiteAccountHostRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		return a.originalHostSiteAccounts(ctx, scope, in)
	case plugins.SitesRefreshCapability:
		if err := originalHostNestedModuleScope(scope); err != nil {
			return nil, err
		}
		var in plugins.SiteHostRefreshV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		if in.ID == "" {
			return nil, bad("INVALID_PLUGIN_VALUE", "刷新站点须指定准确ID")
		}
		ctx = allowDisabledSiteRequests(ctx)
		site, err := a.siteForRequest(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		snapshot, err := a.collectManagedSiteObserved(ctx, in.ID, "", &siteCollectionHooks{OrdinaryEvent: true, RequestGuard: scope.validate})
		if err != nil {
			return nil, err
		}
		if err = scope.validate(ctx); err != nil {
			return nil, err
		}
		if err = a.originalHostSiteRefreshMessages(ctx, scope, site, snapshot); err != nil {
			return nil, err
		}
		return a.originalHostSiteAccounts(ctx, scope, plugins.SiteAccountHostRequestV1{ID: in.ID, Date: snapshot.Day})
	case plugins.SitesResultWriteCapability:
		var in plugins.SiteHostAccessResultV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		return a.originalHostRecordSiteAccess(ctx, scope, in)
	case plugins.SitesBrowseCapability:
		var in plugins.SiteBrowseHostRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		return a.originalHostBrowseSite(ctx, scope, in)
	case plugins.SitesRSSCapability:
		var in plugins.SiteHostRefreshV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		return a.originalHostSiteRSS(ctx, scope, in.ID)
	case plugins.SitesTorrentDownloadCapability:
		var in plugins.SiteTorrentHostRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		if in.ID == "" || in.ResourceID == "" {
			return nil, bad("INVALID_PLUGIN_VALUE", "获取种子须指定站点和资源ID")
		}
		ctx = allowDisabledSiteRequests(ctx)
		site, err := a.siteForRequest(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var data []byte
		var material *originalHostSiteMaterial
		if strings.HasPrefix(in.ResourceID, "rss_") {
			value, readErr := a.originalHostReadSiteMaterial(ctx, site, in.ResourceID)
			if readErr != nil {
				return nil, readErr
			}
			material = &value
		}
		err = a.withSite(ctx, site, false, func(client *sites.Client) error {
			client.AddRequestGuard(scope.validate)
			var err error
			if material != nil {
				data, err = client.ConfiguredMaterial(ctx, material.URL)
			} else {
				data, err = client.Torrent(ctx, in.ResourceID)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		if err = scope.validate(ctx); err != nil {
			return nil, err
		}
		out, err := json.Marshal(plugins.SiteTorrentHostResultV1{SchemaVersion: 1, Torrent: data})
		if err != nil {
			return nil, err
		}
		if len(out) > plugins.MaxPayloadBytes {
			return nil, bad("PLUGIN_VALUE_TOO_LARGE", "种子材料超过插件资源上限")
		}
		return out, nil
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知站点资源操作")
}

type originalHostSiteMaterial struct {
	SiteID, ConfigKey, URL string
	FailureResourceKey     string `json:",omitempty"`
}

const originalHostSiteMaterialKind = "site_host_material"

func (a *App) originalHostReadSiteMaterial(ctx context.Context, site SiteConfig, id string) (originalHostSiteMaterial, error) {
	var out originalHostSiteMaterial
	if len(id) != 68 {
		return out, bad("INVALID_PLUGIN_VALUE", "RSS材料ID无效")
	}
	var sealed []byte
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, originalHostSiteMaterialKind, site.ID+":"+id).Scan(&sealed); err != nil {
		return out, err
	}
	raw, err := a.pluginStore.unseal(originalHostSiteMaterialKind, "system", site.ID+":"+id, sealed)
	if err != nil || json.Unmarshal(raw, &out) != nil || out.SiteID != site.ID || out.ConfigKey != a.managedSiteKey(site) || id != "rss_"+tokenHash(out.URL) {
		return out, bad("SITE_CHANGED", "RSS材料与当前站点配置不匹配")
	}
	return out, nil
}

// Fetch the configured RSS through the same host site client, then reuse its
// existing pure-Go port of the source Rust RSS parser. The business state
// machine is not involved; only its separately source-tested parser is used.
func (a *App) originalHostSiteRSS(ctx context.Context, scope originalBaseCallScope, id string) (json.RawMessage, error) {
	if id == "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "RSS查询须指定准确站点ID")
	}
	ctx = allowDisabledSiteRequests(ctx)
	site, err := a.siteForRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	out, err := a.originalConfiguredSiteRSS(ctx, site, false, scope.validate)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out.SiteBrowseHostResultV1)
}

type originalConfiguredRSSResult struct {
	plugins.SiteBrowseHostResultV1
	detailURLs []string
}

func (a *App) originalConfiguredSiteRSS(ctx context.Context, site SiteConfig, automatic bool, guard func(context.Context) error) (originalConfiguredRSSResult, error) {
	out := originalConfiguredRSSResult{SiteBrowseHostResultV1: plugins.SiteBrowseHostResultV1{SchemaVersion: 1, Items: []plugins.SiteTorrentHostRowV1{}}}
	if site.RSS == "" {
		return out, nil
	}
	var body []byte
	var contentType string
	err := a.withSite(ctx, site, automatic, func(client *sites.Client) error {
		client.AddRequestGuard(guard)
		var err error
		body, contentType, err = client.ConfiguredRSS(ctx, site.RSS)
		return err
	})
	if err != nil {
		return out, err
	}
	text, err := brushflow.DecodeRSSXML(body, contentType)
	if err != nil {
		return out, bad("SITE_RSS_INVALID", "站点RSS编码无法解析")
	}
	rows, err := brushflow.ParseRustRSS(text, 1000, shanghai())
	if err != nil {
		rows, err = originalRSSRecoverRows(text, time.Now().In(shanghai()))
		if err != nil {
			return out, bad("SITE_RSS_INVALID", "站点RSS内容无法解析")
		}
	}
	if err = guard(ctx); err != nil {
		return out, err
	}
	base, err := url.Parse(site.BaseURL)
	if err != nil {
		return out, bad("SITE_RSS_INVALID", "站点地址无效")
	}
	materials := map[string][]byte{}
	for _, row := range rows {
		title, _ := row["title"].(string)
		if title == "" {
			continue
		}
		enclosure, _ := row["enclosure"].(string)
		if automatic && enclosure == "" {
			continue
		}
		address := enclosure
		if strings.HasPrefix(enclosure, "magnet:") {
			// A magnet is material itself and has no HTTP origin to resolve.
			if _, err := metainfo.MagnetHash(enclosure); err != nil {
				continue
			}
		} else {
			u, err := url.Parse(enclosure)
			if err != nil {
				continue
			}
			u = base.ResolveReference(u)
			if automatic && (u.User != nil || u.Fragment != "" || originalOCROrigin(u) != originalOCROrigin(base)) {
				continue
			}
			address = u.String()
		}
		resourceID := "rss_" + tokenHash(address)
		published, _ := row["pubdate"].(string)
		size := json.Number("0")
		switch value := row["size"].(type) {
		case int64:
			size = json.Number(strconv.FormatInt(value, 10))
		case json.Number:
			size = value
		}
		private := originalHostSiteMaterial{SiteID: site.ID, ConfigKey: a.managedSiteKey(site), URL: address}
		// Source prefers the RSS detail page over its credential-bearing
		// enclosure. Persist only the derived key in the sealed record.
		link, _ := row["link"].(string)
		private.FailureResourceKey = acquisitionResourceURLKey(link)
		if private.FailureResourceKey == "" {
			private.FailureResourceKey = acquisitionResourceURLKey(enclosure)
		}
		if private.FailureResourceKey == "" {
			byteCount, _ := size.Int64()
			private.FailureResourceKey = acquisitionResourceKey(sites.Resource{Title: title, Size: byteCount})
		}
		sealed, err := a.pluginStore.seal(originalHostSiteMaterialKind, "system", site.ID+":"+resourceID, mustCleanSeedJSON(private))
		if err != nil {
			return out, err
		}
		materials[site.ID+":"+resourceID] = sealed
		out.Items = append(out.Items, plugins.SiteTorrentHostRowV1{ID: resourceID, SiteID: site.ID, Title: title, Size: size, PublishedAt: published, DownloadFactor: 1, UploadFactor: 1})
		if automatic {
			out.detailURLs = append(out.detailURLs, originalRSSDetailsURL(base, site, link, address))
		}
	}
	if err = guard(ctx); err != nil {
		return out, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for key, sealed := range materials {
		if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, originalHostSiteMaterialKind, key, sealed, now()); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	if err = guard(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func (a *App) originalHostSiteAccounts(ctx context.Context, scope originalBaseCallScope, in plugins.SiteAccountHostRequestV1) (json.RawMessage, error) {
	return a.originalHostSiteAccountsMode(ctx, scope, in, false)
}

func (a *App) originalHostSiteAccountsMode(ctx context.Context, scope originalBaseCallScope, in plugins.SiteAccountHostRequestV1, source bool) (json.RawMessage, error) {
	if !source && in.Date != "" {
		if _, err := time.Parse("2006-01-02", in.Date); err != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "账户日期须为YYYY-MM-DD")
		}
	}
	if in.Date != "" && in.Latest {
		return nil, bad("INVALID_PLUGIN_VALUE", "账户日期与最新选择不能同时指定")
	}
	configured := []SiteConfig{}
	for _, s := range a.Settings().Sites {
		if in.ID == "" || s.ID == in.ID {
			configured = append(configured, s)
		}
	}
	out := plugins.SiteAccountHostResultV1{SchemaVersion: 1, Items: []plugins.SiteAccountHostRowV1{}}
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	view, err := a.siteAccountViewTx(ctx, tx, configured)
	if err != nil {
		return nil, err
	}
	selected := []accountObservation{}
	if in.Latest {
		latest := map[string]string{}
		for _, o := range view.Rows {
			if in.ID != "" && o.SiteID != in.ID {
				continue
			}
			if o.Header.Good && o.Header.Day > latest[o.Header.Domain] {
				latest[o.Header.Domain] = o.Header.Day
			}
		}
		for _, o := range view.Rows {
			if in.ID != "" && o.SiteID != in.ID {
				continue
			}
			if day := latest[o.Header.Domain]; day != "" && o.Header.Day == day {
				selected = append(selected, o)
			}
		}
		sort.SliceStable(selected, func(i, j int) bool { return accountObservationLess(selected[i], selected[j], view.Rank) })
	} else {
		for _, o := range view.Rows {
			if in.ID != "" && o.SiteID != in.ID {
				continue
			}
			if in.Date != "" && o.Header.Day != in.Date {
				continue
			}
			selected = append(selected, o)
		}
	}
	for _, o := range selected {
		var fields json.RawMessage
		if o.Native == nil {
			row, err := a.pluginStore.sourceAccountRowTx(ctx, tx, *view.Binding, o.Header)
			if err != nil {
				return nil, err
			}
			var logical map[string]json.RawMessage
			if err := json.Unmarshal(mustCleanSeedJSON(row), &logical); err != nil {
				return nil, err
			}
			for key, value := range map[string]*string{"seeding_info": row.SeedingInfo, "message_unread_contents": row.MessageUnreadContents} {
				if value != nil {
					logical[key] = json.RawMessage(*value)
				}
			}
			fields = mustCleanSeedJSON(logical)
		} else {
			s := o.Native
			site := view.Sites[o.SiteID]
			if s.SourceData != nil {
				var row map[string]json.RawMessage
				if err := json.Unmarshal(mustCleanSeedJSON(s.SourceData), &row); err != nil {
					return nil, err
				}
				if source {
					row["name"] = mustCleanSeedJSON(s.Name)
					row["domain"] = mustCleanSeedJSON(o.Header.Domain)
					if s.SourceData.Error == nil {
						row["err_msg"] = json.RawMessage(`""`)
					}
				} else {
					row["name"] = mustCleanSeedJSON(site.Name)
				}
				row["updated_day"] = mustCleanSeedJSON(o.Header.Day)
				row["updated_time"] = mustCleanSeedJSON(o.Header.Time)
				fields = mustCleanSeedJSON(row)
			} else {
				if source {
					site.Name = s.Name
				}
				data, err := statisticData(*s, site)
				if err != nil {
					return nil, err
				}
				row := sourceAccountRow{Domain: &data.Domain, Name: &data.Name, Username: data.Username, UserLevel: data.UserLevel, Bonus: data.Bonus, Upload: data.Upload, Download: data.Download, Ratio: data.Ratio, Seeding: data.Seeding, SeedingSize: data.SeedingSize, Error: &data.Error, Day: &data.UpdatedDay, Time: &data.UpdatedTime}
				fields = mustCleanSeedJSON(row)
			}
		}
		if source {
			// The source table always has a real row id. Imported identities
			// remain original; new host rows use their actual stable SQLite
			// record identity, not an invented counter or a permission grant.
			identifier := o.Header.ID
			if identifier == 0 {
				identifier = o.Order
			}
			if identifier <= 0 {
				return nil, invalidSiteAccountSnapshot()
			}
			var logical map[string]json.RawMessage
			if err = json.Unmarshal(fields, &logical); err != nil {
				return nil, err
			}
			logical["id"] = mustCleanSeedJSON(identifier)
			fields = mustCleanSeedJSON(logical)
			fields, err = originalSourceAccountFields(fields)
			if err != nil {
				return nil, err
			}
		}
		out.Items = append(out.Items, plugins.SiteAccountHostRowV1{SiteID: o.SiteID, Fields: fields})
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// This keeps SiteOper.success's original denominator (before trimming the
// ten-entry note). A more conventional moving average would change the source.
func (a *App) originalHostRecordSiteAccess(ctx context.Context, scope originalBaseCallScope, in plugins.SiteHostAccessResultV1) (json.RawMessage, error) {
	if in.ID == "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点结果须指定准确ID")
	}
	if in.Seconds != nil && *in.Seconds < 0 {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点耗时不能为负")
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	site, err := a.siteForRequest(allowDisabledSiteRequests(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	key := siteStatisticDomain(site)
	out := plugins.SiteHostAccessV1{SchemaVersion: 1}
	err = a.Store.Get(ctx, "site_access_statistics", key, &out)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out = originalHostSiteAccessUpdate(out, exists, in, time.Now().In(shanghai()).Format("2006-01-02 15:04:05"))
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	if err = a.Store.Put(ctx, "site_access_statistics", key, out); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (a *App) originalHostBrowseSite(ctx context.Context, scope originalBaseCallScope, in plugins.SiteBrowseHostRequestV1) (json.RawMessage, error) {
	if err := originalHostNestedModuleScope(scope); err != nil {
		return nil, err
	}
	if in.ID == "" || in.Page < 0 || in.Page > 9999 {
		return nil, bad("INVALID_PLUGIN_VALUE", "站点浏览需要准确ID和有效页码")
	}
	ctx = allowDisabledSiteRequests(ctx)
	site, err := a.siteForRequest(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	var keyword any
	if in.Keyword != "" {
		keyword = in.Keyword
	}
	result, err := a.originalSiteTorrentModule(ctx, site, "refresh_torrents", map[string]any{"keyword": keyword, "cat": in.Category, "page": in.Page}, in.Page+1, func() (sites.SearchResult, error) {
		category, err := originalHostSiteCategoryParams(site, in.Category)
		if err != nil {
			return sites.SearchResult{}, err
		}
		var result sites.SearchResult
		err = a.withSite(ctx, site, false, func(client *sites.Client) error {
			client.AddRequestGuard(scope.validate)
			var err error
			result, err = client.SearchWithCategoryParams(ctx, in.Keyword, in.Page+1, category)
			return err
		})
		return result, err
	})
	if err != nil {
		return nil, err
	}
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	out := plugins.SiteBrowseHostResultV1{SchemaVersion: 1, Page: in.Page, HasMore: result.HasMore, Items: []plugins.SiteTorrentHostRowV1{}}
	for _, r := range result.Resources {
		out.Items = append(out.Items, plugins.SiteTorrentHostRowV1{ID: r.ID, SiteID: r.SiteID, Title: r.Title, Subtitle: r.Subtitle, Size: json.Number(strconv.FormatInt(r.Size, 10)), Seeders: r.Seeders, Leechers: r.Leechers, PublishedAt: r.PublishedAt, DownloadFactor: r.DownloadFactor, UploadFactor: r.UploadFactor, Free: r.Free, Hash: r.Hash})
	}
	return json.Marshal(out)
}

func originalHostSiteAccessUpdate(out plugins.SiteHostAccessV1, exists bool, in plugins.SiteHostAccessResultV1, clock string) plugins.SiteHostAccessV1 {
	out.LastModified = clock
	if in.Success {
		if out.Note == nil {
			out.Note = map[string]int64{}
		}
		out.Success++
		out.LastState = 0
		if in.Seconds != nil {
			v := *in.Seconds
			if v == 0 {
				v = 1
			}
			out.Note[out.LastModified] = v
			n := int64(len(out.Note))
			keys := make([]string, 0, len(out.Note))
			for k := range out.Note {
				keys = append(keys, k)
			}
			sort.Sort(sort.Reverse(sort.StringSlice(keys)))
			if len(keys) > 10 {
				for _, k := range keys[10:] {
					delete(out.Note, k)
				}
			}
			var sum int64
			for _, v := range out.Note {
				sum += v
			}
			avg := sum / n
			if avg != 0 {
				out.Seconds = &avg
			}
		}
		if !exists {
			v := int64(1)
			if in.Seconds != nil && *in.Seconds != 0 {
				v = *in.Seconds
			}
			out.Seconds = &v
		}
	} else {
		out.Fail++
		out.LastState = 1
	}
	return out
}

func (a *App) originalHostSiteRefreshMessages(ctx context.Context, scope originalBaseCallScope, site SiteConfig, snapshot siteSnapshot) error {
	for _, step := range siteMessageBusinesses(site.Name, site.BaseURL, snapshot) {
		messageRaw, err := siteMessageBusinessAt(site.Name, site.BaseURL, snapshot, step)
		if err != nil {
			return err
		}
		var message sourceBusinessNotice
		if err = json.Unmarshal(messageRaw, &message); err != nil {
			return err
		}
		if sourceSubscriptionTruth(message.Envelope["source"]) {
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			exists, err := a.siteMessageSourceExistsTx(ctx, tx, message.Envelope["source"])
			tx.Rollback()
			if err != nil {
				return err
			}
			if exists {
				continue
			}
		}
		if err = scope.validate(ctx); err != nil {
			return err
		}
		if _, err = a.postOriginalPluginMessage(ctx, message.Envelope); err != nil {
			return err
		}
	}

	return nil
}
