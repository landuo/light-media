package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	iyuu "light-media/internal/plugins/iyuuautoseed"
	native "light-media/internal/plugins/iyuuautoseed/native"
	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
)

func (o *pluginSeedRescueOwner) IYUUAvailable(ctx context.Context) bool {
	var n int
	e := o.a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations WHERE plugin_id='IYUUAutoSeed' AND installed=1`).Scan(&n)
	return e == nil && n == 1
}
func (o *pluginSeedRescueOwner) IYUUEnabled(ctx context.Context) bool {
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return false
	}
	var instance string
	var generation, revision int64
	e = tx.QueryRowContext(ctx, `SELECT i.instance_id,i.generation,v.revision FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.plugin_id='IYUUAutoSeed' AND i.version='2.19' AND i.installed=1 AND i.desired_enabled=1`).Scan(&instance, &generation, &revision)
	if e != nil {
		tx.Rollback()
		return false
	}
	root, _, e := o.a.pluginStore.iyuuRootReadTx(ctx, tx, instance, generation)
	if e != nil {
		tx.Rollback()
		return false
	}
	_, e = o.a.pluginStore.iyuuRootChainTx(ctx, tx, instance, generation, root.Journal, true)
	if e != nil && !errors.Is(e, iyuu.ErrUnconfirmed) {
		tx.Rollback()
		return false
	}
	if tx.Commit() != nil {
		return false
	}
	verifier, e := iyuu.NewJournalVerifier(o.a.Store.DB, o.a.pluginStore.iyuuJournalKey(instance, generation))
	if e != nil {
		return false
	}
	checkpoint, found, e := verifier.ReadCheckpoint(ctx, instance, uint64(generation))
	if e != nil || !found || checkpoint.Revision != uint64(revision) {
		return false
	}
	config, e := iyuu.RestoreConfig(checkpoint.Core.Config)
	return e == nil && config.State()
}
func seedRescueDownloader(settings Settings, name string) (DownloaderConfig, error) {
	var out DownloaderConfig
	found := 0
	for _, d := range settings.Downloaders {
		if d.Name == name && downloaderEnabled(d) {
			out = d
			found++
		}
	}
	if found != 1 {
		return out, bad("SEED_RESCUE_DOWNLOADER_UNBOUND", "下载器名称没有唯一的已启用配置")
	}
	return out, nil
}
func seedRescueNativeConfig(d DownloaderConfig) native.Config {
	kind := d.Kind
	switch kind {
	case "qb":
		kind = "qbittorrent"
	case "tr":
		kind = "transmission"
	}
	return native.Config{ID: d.ID, Kind: kind, BaseURL: d.BaseURL, Username: d.Username, Password: d.Password}
}
func (o *pluginSeedRescueOwner) downloader(ctx context.Context, name string) (*native.IYUUClient, DownloaderConfig, error) {
	c := o.active
	if c == nil {
		return nil, DownloaderConfig{}, seedRescueEvidenceError()
	}
	if e := c.ioGuard(ctx); e != nil {
		return nil, DownloaderConfig{}, e
	}
	if name != sr.ParseConfig(c.config).Downloader {
		return nil, DownloaderConfig{}, bad("SEED_RESCUE_SCOPE_DENIED", "下载器没有绑定当前稀缺保种配置")
	}
	d, e := o.boundDownloader(ctx, name)
	if e != nil {
		return nil, d, e
	}
	digest := seedRescueDownloaderSHA(d)
	client, e := native.NewIYUUClient(seedRescueNativeConfig(d), native.IYUUOptions{APIKey: d.APIKey, AutoCategory: d.AutoCategory, Sequential: d.Sequential, FirstLast: d.FirstLast, Guard: func(ctx context.Context) error {
		if e := c.ioGuard(ctx); e != nil {
			return e
		}
		current, e := o.boundDownloader(ctx, name)
		if e != nil || seedRescueDownloaderSHA(current) != digest {
			return bad("SEED_RESCUE_SCOPE_DENIED", "下载器配置已经变化")
		}
		return nil
	}})
	return client, d, e
}
func (o *pluginSeedRescueOwner) Service(ctx context.Context, name string) (*sr.Service, error) {
	client, d, e := o.downloader(ctx, name)
	if e != nil {
		if seedRescueErrorCode(e) == "SEED_RESCUE_DOWNLOADER_UNBOUND" {
			return nil, nil
		}
		return nil, e
	}
	defer client.Close()
	e = client.SeedRescueConnected(ctx)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if x := o.active.Guard(ctx); x != nil {
			return nil, x
		}
	}
	return &sr.Service{Name: name, Kind: seedRescueNativeConfig(d).Kind, Connected: e == nil}, nil
}
func (o *pluginSeedRescueOwner) QueryTorrents(ctx context.Context, name string, hashes []string) ([]sr.Object, error) {
	client, _, e := o.downloader(ctx, name)
	if e != nil {
		return nil, e
	}
	defer client.Close()
	raw, e := client.Torrents(ctx, hashes, false)
	if e != nil {
		return nil, e
	}
	var out []sr.Object
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e = d.Decode(&out); e != nil {
		return nil, e
	}
	return out, nil
}
func (o *pluginSeedRescueOwner) DownloadingTorrents(ctx context.Context, name, tag string) ([]sr.Object, error) {
	if tag != sr.GlobalTag {
		return nil, seedRescueEvidenceError()
	}
	client, _, e := o.downloader(ctx, name)
	if e != nil {
		return nil, e
	}
	defer client.Close()
	raw, e := client.SeedRescueDownloading(ctx, tag)
	if e != nil {
		return nil, e
	}
	var out []sr.Object
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e = d.Decode(&out); e != nil {
		return nil, e
	}
	return out, nil
}
func (o *pluginSeedRescueOwner) AddTorrent(ctx context.Context, r sr.AddRequest) (bool, error) {
	c := o.active
	if c == nil {
		return false, seedRescueEvidenceError()
	}
	conf := sr.ParseConfig(c.config)
	if r.Downloader != conf.Downloader || r.Category != sr.Category || len(r.Tags) != 1 || r.Tags[0] != sr.GlobalTag || !seedRescueSamePath(r.SavePath, conf.SavePath) {
		return false, seedRescueEvidenceError()
	}
	hash, e := sr.InfoHashFromTorrent(r.Content)
	if e != nil {
		return false, e
	}
	client, d, e := o.downloader(ctx, r.Downloader)
	if e != nil {
		return false, e
	}
	defer client.Close()
	args := map[string]any{"hash": hash, "downloader_id": d.ID, "downloader_sha": seedRescueDownloaderSHA(d), "torrent_sha": tokenHash(string(r.Content)), "save_path": r.SavePath, "category": r.Category, "tags": r.Tags, "paused": false, "ignore_category_check": true}
	ordinal, e := c.beginEffect(ctx, "add_torrent", args)
	if e != nil {
		return false, e
	}
	category := r.Category
	result, callErr := client.SeedRescueAddDownloading(ctx, native.IYUUAddSpec{Torrent: r.Content, SavePath: r.SavePath, Category: &category, Tags: append([]string{}, r.Tags...), IgnoreCategoryCheck: true})
	if callErr != nil && !native.MutationNotDispatched(callErr) {
		c.uncertain.Store(true)
		return false, callErr
	}
	if hook := o.a.pluginSeedRescue.afterEffect; hook != nil {
		if e = hook("add_torrent"); e != nil {
			c.uncertain.Store(true)
			return false, e
		}
	}
	e = c.finishEffect(ctx, ordinal, "add_torrent", map[string]any{"accepted": result.Accepted, "source_truthy": result.SourceTruthy, "not_dispatched": callErr != nil, "failure_class": native.MutationFailureClass(callErr)})
	if e != nil {
		return false, e
	}
	return result.SourceTruthy, callErr
}
func seedRescueSamePath(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func (c *seedRescueCall) beginEffect(ctx context.Context, kind string, args any) (int, error) {
	if e := c.ioGuard(ctx); e != nil {
		return 0, e
	}
	ordinal := 0
	e := c.o.update(ctx, func(s *seedRescueState) error {
		r := s.Runs[c.run.ID]
		if r.Phase != "started" {
			return seedRescueEvidenceError()
		}
		ordinal = len(r.Effects)
		r.Effects = append(r.Effects, seedRescueEffect{Kind: kind, Phase: "started", Args: mustCleanSeedJSON(args)})
		s.Runs[c.run.ID] = r
		return nil
	})
	return ordinal, e
}
func (c *seedRescueCall) finishEffect(ctx context.Context, ordinal int, kind string, result any) error {
	finish, cancel := seedRescueFinishContext(ctx)
	defer cancel()
	e := c.o.update(finish, func(s *seedRescueState) error {
		r := s.Runs[c.run.ID]
		if ordinal < 0 || ordinal >= len(r.Effects) || r.Effects[ordinal].Kind != kind || r.Effects[ordinal].Phase != "started" {
			return seedRescueEvidenceError()
		}
		r.Effects[ordinal].Phase = "confirmed"
		r.Effects[ordinal].Result = mustCleanSeedJSON(result)
		s.Runs[c.run.ID] = r
		return nil
	})
	if e != nil {
		c.uncertain.Store(true)
	}
	return e
}
func (a *App) seedRescueOptionsTx(ctx context.Context, tx *sql.Tx, instance string) (sr.Object, error) {
	binding, e := a.pluginStore.seedRescueNamespaceTx(ctx, tx, instance)
	if e != nil {
		return nil, e
	}
	sitesOut := []sr.Object{}
	for _, site := range a.Settings().Sites {
		if !site.Enabled || site.Public {
			continue
		}
		id, e := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
		if e != nil {
			return nil, e
		}
		origin, e := nativeConfiguredSiteOrigin(site)
		if e != nil || id.State != "active" || id.CanonicalOrigin != origin {
			return nil, siteIdentityError()
		}
		alias, e := seedRescueSiteAlias(binding, id)
		if e != nil {
			return nil, e
		}
		sitesOut = append(sitesOut, sr.Object{"value": alias, "title": site.Name})
	}
	downloaders, e := seedRescueDownloaderOptions(a.Settings(), binding)
	if e != nil {
		return nil, e
	}
	return sr.Object{"sites": sitesOut, "downloaders": downloaders}, nil
}
func (o *pluginSeedRescueOwner) Options(ctx context.Context) (sr.Object, error) {
	if e := o.Guard(ctx); e != nil {
		return nil, e
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	return o.a.seedRescueOptionsTx(ctx, tx, o.instance)
}
func (o *pluginSeedRescueOwner) Site(ctx context.Context, id int) (sr.Object, error) {
	if e := o.active.ioGuard(ctx); e != nil {
		return nil, e
	}
	cfg := sr.ParseConfig(o.active.config)
	allowed := id == cfg.TargetSiteID
	for _, x := range cfg.SourceSiteIDs {
		allowed = allowed || x == id
	}
	if !allowed || id < 1 {
		return nil, bad("SEED_RESCUE_SITE_UNBOUND", "站点不在当前稀缺保种配置中")
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	binding, e := o.a.pluginStore.seedRescueNamespaceTx(ctx, tx, o.instance)
	if e != nil {
		return nil, e
	}
	identity, e := o.a.pluginStore.seedRescueSiteIdentityTx(ctx, tx, binding, int64(id))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	for _, s := range o.a.Settings().Sites {
		if s.ID != identity.SiteID {
			continue
		}
		origin, e := nativeConfiguredSiteOrigin(s)
		if e != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
			return nil, siteIdentityError()
		}
		if !s.Enabled {
			return nil, nil
		}
		schema := "nexusphp"
		if s.Kind == "mteam" {
			schema = "mteam"
		}
		return sr.Object{"id": id, "name": s.Name, "url": strings.TrimRight(s.BaseURL, "/") + "/", "domain": seedRescueDomain(s.BaseURL), "cookie": s.Cookie, "apikey": s.APIKey, "ua": seedRescueUA(s.UserAgent), "timeout": s.Timeout, "proxy": s.Proxy, "schema": schema, "_native_ref": s.ID}, nil
	}
	return nil, nil
}
func (o *pluginSeedRescueOwner) Indexer(ctx context.Context, domain string) (sr.Object, error) {
	cfg := sr.ParseConfig(o.active.config)
	ids := cfg.SourceSiteIDs
	if cfg.TargetSiteID > 0 {
		ids = append([]int{cfg.TargetSiteID}, ids...)
	}
	for _, id := range ids {
		site, e := o.Site(ctx, id)
		if e != nil {
			return nil, e
		}
		u, _ := url.Parse(fmt.Sprint(site["url"]))
		if u != nil && u.Hostname() == domain {
			return site, nil
		}
	}
	return nil, nil
}
func (o *pluginSeedRescueOwner) sourceSite(ctx context.Context, object sr.Object) (SiteConfig, error) {
	number, e := strconv.Atoi(fmt.Sprint(object["id"]))
	if e != nil {
		return SiteConfig{}, siteIdentityError()
	}
	bound, e := o.Site(ctx, number)
	if e != nil || bound == nil {
		return SiteConfig{}, siteIdentityError()
	}
	for _, k := range []string{"url", "cookie", "apikey", "ua", "proxy", "_native_ref"} {
		if fmt.Sprint(bound[k]) != fmt.Sprint(object[k]) {
			return SiteConfig{}, siteIdentityError()
		}
	}
	for _, s := range o.a.Settings().Sites {
		if s.ID == bound["_native_ref"] {
			return s, nil
		}
	}
	return SiteConfig{}, siteIdentityError()
}
func (o *pluginSeedRescueOwner) Search(ctx context.Context, domain, keyword string, page int) ([]sr.Object, error) {
	site, e := o.Indexer(ctx, domain)
	if e != nil || site == nil {
		return nil, e
	}
	s, e := o.sourceSite(ctx, site)
	if e != nil {
		return nil, e
	}
	out := []sr.Object{}
	e = o.a.withSite(ctx, s, false, func(client *sites.Client) error {
		client.AddRequestGuard(o.active.ioGuard)
		result, e := client.Search(ctx, keyword, page+1)
		if e != nil {
			return e
		}
		for _, r := range result.Resources {
			var seeders, peers, factor any
			if r.Seeders >= 0 {
				seeders = r.Seeders
			}
			if r.Leechers >= 0 {
				peers = r.Leechers
			}
			if r.DownloadFactor >= 0 {
				factor = r.DownloadFactor
			}
			out = append(out, sr.Object{"title": r.Title, "description": r.Subtitle, "page_url": r.DetailsURL, "_native_resource_id": r.ID, "_native_site_id": s.ID, "enclosure": "native-resource:" + r.ID, "seeders": seeders, "peers": peers, "size": r.Size, "downloadvolumefactor": factor})
		}
		return nil
	})
	return out, e
}
func (o *pluginSeedRescueOwner) SeedInfo(ctx context.Context, hashes []string) (any, string, error) {
	helper, e := o.seedHelper(ctx)
	if e != nil {
		return nil, "", e
	}
	result, e := helper.SeedInfo(ctx, hashes)
	if e != nil {
		return nil, "", e
	}
	if result.Failure != nil {
		if iyuu.SourceNoReseedData(result.Failure) {
			return nil, "请求IYUU失败，状态码：404，未查询到可辅种数据", nil
		}
		return nil, "请求IYUU失败 (" + result.Failure.Kind + ")", nil
	}
	return result.Data, "", nil
}
func (o *pluginSeedRescueOwner) TorrentURL(ctx context.Context, sid any) (string, string, error) {
	helper, e := o.seedHelper(ctx)
	if e != nil {
		return "", "", e
	}
	base, page, e := helper.TorrentURL(ctx, sid)
	if e != nil {
		return "", "", e
	}
	b, bok := base.(string)
	p, pok := page.(string)
	if !bok || !pok {
		return "", "", nil
	}
	return b, p, nil
}
func (o *pluginSeedRescueOwner) seedHelper(ctx context.Context) (*iyuu.Helper, error) {
	if e := o.active.ioGuard(ctx); e != nil {
		return nil, e
	}
	if o.iyuu == nil {
		var e error
		o.iyuu, e = iyuu.NewHelper(seedRescueIYUUAPI{o}, o.Now)
		if e != nil {
			return nil, e
		}
	}
	return o.iyuu, nil
}

type seedRescueIYUUAPI struct{ o *pluginSeedRescueOwner }

func (api seedRescueIYUUAPI) Call(ctx context.Context, r iyuu.APIRequest) (*iyuu.APIResponse, error) {
	c := api.o.active
	if c == nil {
		return nil, seedRescueEvidenceError()
	}
	if e := c.ioGuard(ctx); e != nil {
		return nil, e
	}
	transport, e := iyuu.NewAPITransport(iyuu.TrustedAPIBinding{Token: sr.ParseConfig(c.config).Token}, iyuu.APITransportOptions{Guard: c.ioGuard, LoopbackOrigin: api.o.a.pluginSeedRescue.loopbackAPI})
	if e != nil {
		return nil, e
	}
	defer transport.Close()
	if r.Operation != iyuu.ReportExisting {
		return transport.Call(ctx, r)
	}
	ordinal, e := c.beginEffect(ctx, "iyuu_report_existing", map[string]any{"operation": r.Operation, "params_sha": tokenHash(string(mustCleanSeedJSON(r.Params)))})
	if e != nil {
		return nil, e
	}
	result, e := transport.Call(ctx, r)
	if e != nil {
		c.uncertain.Store(true)
		return nil, e
	}
	if e = c.finishEffect(ctx, ordinal, "iyuu_report_existing", map[string]any{"status": result.Status, "body_sha": tokenHash(string(result.Body))}); e != nil {
		return nil, e
	}
	return result, nil
}

func seedRescueFinishContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
}

func seedRescueDomain(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	return u.Hostname()
}

func seedRescueUA(value string) string {
	if value == "" {
		return "Mozilla/5.0"
	}
	return value
}

// App directory mappings and SharedPaths are not consumed by this native
// downloader call; startup normalizes them. Bind only actual source authority.
func seedRescueDownloaderSHA(d DownloaderConfig) string {
	return tokenHash(string(mustCleanSeedJSON(map[string]any{"id": d.ID, "name": d.Name, "enabled": downloaderEnabled(d), "config": seedRescueNativeConfig(d), "api_key": d.APIKey, "auto_category": d.AutoCategory, "sequential": d.Sequential, "first_last": d.FirstLast})))
}
