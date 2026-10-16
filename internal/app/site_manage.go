package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	"light-media/internal/download"
	"light-media/internal/sites"
)

type managedConnection struct {
	Status     string `json:"status"`
	CheckedAt  string `json:"checked_at"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}
type siteSnapshot struct {
	SiteID     string            `json:"site_id"`
	Name       string            `json:"name,omitempty"`
	Domain     string            `json:"domain,omitempty"`
	Day        string            `json:"updated_day,omitempty"`
	ConfigKey  string            `json:"config_key,omitempty"`
	Connection managedConnection `json:"connection"`
	Stats      *sites.UserStats  `json:"stats"`
}
type SiteTemplate struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Homepage string `json:"homepage"`
}

var nativeSiteTemplates = []SiteTemplate{
	{"mteam", "馒头", "https://api.m-team.cc", "https://kp.m-team.cc"},
	{"hhanclub", "憨憨", "https://hhanclub.net", "https://hhanclub.net"},
	{"ubits", "UBits", "https://ubits.club", "https://ubits.club"},
	{"jiayuan", "家园", "https://hdhome.org", "https://hdhome.org"},
	{"1ptba", "1PTBA", "https://1ptba.com", "https://1ptba.com"},
	{"nexusphp", "NexusPHP", "", ""},
}

func (a *App) managedConnectionKey(namespace string, fields ...string) string {
	// These values only invalidate read-only connection/statistics caches.
	// A keyed digest avoids placing an offline password verifier in the public
	// records table. It is deliberately separate from intent destination IDs.
	mac := hmac.New(sha256.New, a.Store.key)
	_, _ = mac.Write([]byte("light-media/connection-cache/v1/" + namespace))
	for _, field := range fields {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(field))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *App) managedSiteKey(s SiteConfig) string {
	return a.managedConnectionKey("site", s.Kind, s.BaseURL, s.Cookie, s.APIKey, s.UserAgent)
}

func normalizeManagedSite(s *SiteConfig) error {
	s.Name = strings.TrimSpace(s.Name)
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	u, e := url.Parse(s.BaseURL)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return bad("INVALID_SITE", "站点地址无效")
	}
	host := strings.ToLower(u.Hostname())
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		// Existing protocol fixtures use explicit legacy adapters on loopback.
		if s.Kind != "mteam" && s.Kind != "hhanclub" && s.Kind != "ubits" && s.Kind != "jiayuan" && s.Kind != "1ptba" {
			return bad("INVALID_SITE_URL", "站点地址须为公网域名的 HTTPS 根地址")
		}
	} else {
		kind, origin, domain, err := siteImportOrigin(s.BaseURL)
		if err != nil {
			return err
		}
		s.Kind, s.BaseURL = kind, origin
		configuredDomain := strings.ToLower(strings.TrimSpace(s.Domain))
		if configuredDomain == "" || (configuredDomain != domain && configuredDomain != host && !(kind == "nexusphp" && strings.HasSuffix(host, "."+configuredDomain))) {
			s.Domain = domain
		}
	}
	if s.Name == "" {
		s.Name = siteImportName(s.Kind)
		if s.Kind == "nexusphp" || s.Name == "" {
			s.Name = host
		}
	}
	if len([]rune(s.Name)) > 64 || invalidSiteImportText(s.Name, 256) {
		return bad("INVALID_SITE_NAME", "站点名称最长64字且不能包含控制字符")
	}
	if invalidSiteImportText(s.Cookie, 64<<10) || invalidSiteImportText(s.APIKey, 8192) || invalidSiteImportText(s.Token, 8192) || invalidSiteImportText(s.UserAgent, 8192) || invalidSiteImportText(s.RSS, 64<<10) {
		return bad("INVALID_SITE_CREDENTIAL", "站点登录信息格式无效或过长")
	}
	if s.Priority < 0 || s.Priority > 10000 || s.LimitInterval < 0 || s.LimitInterval > 86400 || s.LimitCount < 0 || s.LimitCount > 10000 || s.LimitSeconds < 0 || s.LimitSeconds > 86400 || s.Timeout < 0 || s.Timeout > 120 {
		return bad("INVALID_SITE_OPTIONS", "站点优先级、超时或访问频率超出支持范围")
	}
	if e = validateManagedSiteConfig(s.Config); e != nil {
		return bad("INVALID_SITE", e.Error())
	}
	return nil
}

func validateManagedSiteConfig(config sites.Config) error {
	// Saving a connection is separate from authenticating it. Placeholders are
	// used only for structural validation and are never stored or requested.
	if config.Cookie == "" {
		config.Cookie = "configuration-validation-only"
	}
	if config.APIKey == "" {
		config.APIKey = "configuration-validation-only"
	}
	_, err := sites.New(config)
	return err
}

func (a *App) managedSites(ctx context.Context) (any, error) {
	c := a.Settings()
	sort.SliceStable(c.Sites, func(i, j int) bool { return c.Sites[i].Priority < c.Sites[j].Priority })
	rows := []map[string]any{}
	for _, s := range c.Sites {
		key := a.managedSiteKey(s)
		var latest, last siteSnapshot
		_ = a.Store.Get(ctx, "site_connection", s.ID, &latest)
		_ = a.Store.Get(ctx, "site_stats_latest", s.ID, &last)
		if latest.ConfigKey != key {
			latest = siteSnapshot{}
		}
		if last.ConfigKey != key {
			last = siteSnapshot{}
		}
		configured := s.Cookie != "" || s.APIKey != ""
		credentials := siteSecretState(s)
		hideSiteSecrets(&s)
		var used int
		if e := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_usage WHERE site_id=? AND ts>?`, s.ID, time.Now().Add(-24*time.Hour).Unix()).Scan(&used); e != nil {
			return nil, e
		}
		rows = append(rows, map[string]any{"config": s, "credential_configured": configured, "credential_state": credentials, "connection": latest.Connection, "stats": last.Stats, "stale": last.Stats != nil && latest.Connection.Status != "ok", "usage_24h": used})
	}
	return map[string]any{"sites": rows, "supported": nativeSiteTemplates, "capabilities": map[string]bool{"rss": false, "token": false, "proxy": false, "render": false, "downloader": false, "filter": false, "limit": true, "timeout": true, "user_agent": true}}, nil
}

func (a *App) saveManagedSite(ctx context.Context, in SiteConfig) (any, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	c := a.Settings()
	if in.ID == "" {
		in.ID = id("site")
	}
	if e := normalizeManagedSite(&in); e != nil {
		return nil, e
	}
	found := -1
	for i, old := range c.Sites {
		oldOrigin := strings.TrimRight(old.BaseURL, "/")
		if _, canonical, _, err := siteImportOrigin(old.BaseURL); err == nil {
			oldOrigin = canonical
		}
		if old.ID != in.ID && in.BaseURL == oldOrigin {
			return nil, bad("DUPLICATE_SITE", "该站点地址已存在，请编辑已有站点")
		}
		if old.ID == in.ID {
			found = i
			// Adapter selection follows the URL, including legacy configurations.
			// Preserve redacted fields only when that normalized origin is equal.
			if in.BaseURL == oldOrigin {
				preserveSiteSecrets(&in, old)
			}
		}
	}
	if e := normalizeManagedSite(&in); e != nil {
		return nil, e
	}
	if found < 0 {
		c.Sites = append(c.Sites, in)
	} else {
		c.Sites[found] = in
	}
	if e := a.saveSettings(ctx, c); e != nil {
		return nil, e
	}
	return a.managedSites(ctx)
}

type sitePriority struct {
	ID       string `json:"id"`
	Priority int    `json:"pri"`
}

func (a *App) saveSitePriorities(ctx context.Context, priorities []sitePriority) (any, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	config := a.Settings()
	seen := map[string]bool{}
	for _, entry := range priorities {
		if entry.Priority < 0 || entry.Priority > 10000 || seen[entry.ID] {
			return nil, bad("INVALID_SITE_PRIORITY", "站点排序值无效或站点重复")
		}
		seen[entry.ID] = true
		found := false
		for i := range config.Sites {
			if config.Sites[i].ID == entry.ID {
				config.Sites[i].Priority = entry.Priority
				found = true
				break
			}
		}
		if !found {
			return nil, &APIError{"SITE_NOT_FOUND", "排序站点不存在，请刷新后重试", 404}
		}
	}
	if err := a.saveSettings(ctx, config); err != nil {
		return nil, err
	}
	return a.managedSites(ctx)
}

func (a *App) deleteManagedSite(ctx context.Context, key string) (any, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	c := a.Settings()
	found := false
	out := make([]SiteConfig, 0, len(c.Sites))
	for _, s := range c.Sites {
		if s.ID == key {
			found = true
		} else {
			out = append(out, s)
		}
	}
	if !found {
		return nil, &APIError{"SITE_NOT_FOUND", "站点不存在", 404}
	}
	c.Sites = out
	if e := a.saveSettings(ctx, c); e != nil {
		return nil, e
	}
	// Old statistical snapshots are private application data, not site data.
	_, e := a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE (kind IN ('site_connection','site_stats_latest','site_cooldown') AND id=?) OR (kind='site_stats_history' AND substr(id,1,?)=?)`, key, len(key)+1, key+":")
	if e != nil {
		return nil, e
	}
	return a.managedSites(ctx)
}

func (a *App) refreshManagedSite(ctx context.Context, key string) (any, error) {
	ctx = allowDisabledSiteRequests(ctx)
	if _, err := a.collectManagedSite(ctx, key, "", nil); err != nil {
		return nil, err
	}
	return a.managedSites(ctx)
}

// Site HTTP requests happen outside settings/plugin locks. The observation,
// daily history and outbox fact publish together after checking the current
// connection and optional owning job. A failed response never replaces data.
type siteCollectionHooks struct {
	Auto   bool
	Guard  func(*sql.Tx) error
	Commit func(*sql.Tx, siteSnapshot) error
}

func (a *App) collectManagedSite(ctx context.Context, key, eventKey string, hooks *siteCollectionHooks) (siteSnapshot, error) {
	s, e := a.siteForRequest(ctx, key)
	if e != nil {
		return siteSnapshot{}, e
	}
	started := time.Now()
	snapshot := siteSnapshot{SiteID: key, Name: s.Name, Domain: siteStatisticDomain(s), ConfigKey: a.managedSiteKey(s)}
	var observed sites.UserStats
	e = a.withSite(ctx, s, hooks != nil && hooks.Auto, func(client *sites.Client) error { var err error; observed, err = client.UserStats(ctx); return err })
	snapshot.Connection = managedConnection{Status: "ok", CheckedAt: now(), DurationMS: time.Since(started).Milliseconds()}
	if e != nil {
		snapshot.Connection.Status = "failed"
		snapshot.Connection.Error = a.clean(e.Error())
	} else {
		snapshot.Stats = &observed
	}
	snapshot.Day = time.Now().In(shanghai()).Format("2006-01-02")
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	current, err := a.siteForRequest(ctx, key)
	if err != nil || a.managedSiteKey(current) != snapshot.ConfigKey {
		return siteSnapshot{}, bad("SITE_CHANGED", "站点设置已变化，请重新刷新")
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return siteSnapshot{}, err
	}
	defer tx.Rollback()
	if hooks != nil && hooks.Guard != nil {
		if err = hooks.Guard(tx); err != nil {
			return siteSnapshot{}, err
		}
	}
	if err = txPut(ctx, tx, "site_connection", key, snapshot); err != nil {
		return siteSnapshot{}, err
	}
	if e == nil {
		if err = txPut(ctx, tx, "site_stats_latest", key, snapshot); err != nil {
			return siteSnapshot{}, err
		}
		if err = txPut(ctx, tx, "site_stats_history", key+":"+snapshot.Day, snapshot); err != nil {
			return siteSnapshot{}, err
		}
		if eventKey == "" {
			eventKey = id("site_refresh_")
		}
		if err = recordDomainEventTx(ctx, tx, DomainEvent{EventID: domainEventID("site.refreshed", eventKey), Type: "site.refreshed", SiteID: key}); err != nil {
			return siteSnapshot{}, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='site_stats_history' AND updated_at<?`, time.Now().Add(-90*24*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
			return siteSnapshot{}, err
		}
	}
	if hooks != nil && hooks.Commit != nil {
		if err = hooks.Commit(tx, snapshot); err != nil {
			return siteSnapshot{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return siteSnapshot{}, err
	}
	a.log("INFO", "site", "userdata_refreshed", "站点用户数据刷新完成："+snapshot.Connection.Status, "", "")
	return snapshot, nil
}

func (a *App) managedSiteHistory(ctx context.Context, key string) (any, error) {
	var s SiteConfig
	for _, configured := range a.Settings().Sites {
		if configured.ID == key {
			s = configured
			break
		}
	}
	if s.ID == "" {
		return nil, &APIError{"SITE_NOT_FOUND", "站点不存在", 404}
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT payload FROM records WHERE kind='site_stats_history' AND substr(id,1,?)=? ORDER BY id DESC LIMIT 90`, len(key)+1, key+":")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []siteSnapshot{}
	for rows.Next() {
		var raw []byte
		var item siteSnapshot
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		if item.ConfigKey == a.managedSiteKey(s) {
			item.ConfigKey = ""
			items = append(items, item)
		}
	}
	return map[string]any{"site_id": key, "items": items}, rows.Err()
}

type ManagedDownloaderRequest struct {
	DownloaderConfig
	Default bool `json:"default"`
}

type managedDownloaderConnection struct {
	managedConnection
	ConfigKey string `json:"config_key"`
}

func (a *App) managedDownloaderKey(d DownloaderConfig) string {
	return a.managedConnectionKey("downloader", d.Kind, d.BaseURL, d.Username, d.Password)
}

func (a *App) managedDownloaders(ctx context.Context) (any, error) {
	c := a.Settings()
	rows := []map[string]any{}
	for _, d := range c.Downloaders {
		var observed managedDownloaderConnection
		_ = a.Store.Get(ctx, "downloader_connection", d.ID, &observed)
		if observed.ConfigKey != a.managedDownloaderKey(d) {
			observed = managedDownloaderConnection{}
		}
		configured := d.Password != ""
		d.Password = ""
		if !downloaderEnabled(d) {
			observed.Status = "disabled"
			observed.Error = ""
		}
		rows = append(rows, map[string]any{"config": map[string]any{"id": d.ID, "name": d.Name, "kind": d.Kind, "base_url": d.BaseURL, "username": d.Username, "enabled": downloaderEnabled(d)}, "credential_configured": configured, "default": d.ID == c.DefaultDownloader, "connection": observed.managedConnection})
	}
	return map[string]any{"downloaders": rows, "default_downloader": c.DefaultDownloader}, nil
}

func (a *App) saveManagedDownloader(ctx context.Context, in ManagedDownloaderRequest) (any, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	c := a.Settings()
	in.SharedPaths = true
	if in.ID == "" {
		in.ID = id("downloader")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 64 {
		return nil, bad("INVALID_DOWNLOADER_NAME", "下载器名称不能为空且最长64字")
	}
	found := -1
	for i, old := range c.Downloaders {
		if old.ID == in.ID {
			found = i
			if in.Enabled == nil {
				in.Enabled = old.Enabled
			}
			// Legacy transport paths remain encrypted for already reviewed tasks.
			// They are never exposed as fields required by the new UI.
			if old.BaseURL == in.BaseURL && old.Kind == in.Kind {
				if in.Mappings == nil {
					in.Mappings = old.Mappings
				}
				if in.DownloadPath == "" {
					in.DownloadPath = old.DownloadPath
				}
			}
			if old.BaseURL == in.BaseURL && old.Username == in.Username && old.Kind == in.Kind && in.Password == "" {
				in.Password = old.Password
			}
		} else if strings.EqualFold(old.Name, in.Name) {
			return nil, bad("DUPLICATE_DOWNLOADER_NAME", "下载器名称已存在")
		}
	}
	if found < 0 {
		c.Downloaders = append(c.Downloaders, in.DownloaderConfig)
	} else {
		c.Downloaders[found] = in.DownloaderConfig
	}
	if in.Default || c.DefaultDownloader == "" {
		c.DefaultDownloader = in.ID
	}
	if e := a.saveSettings(ctx, c); e != nil {
		return nil, e
	}
	return a.managedDownloaders(ctx)
}

func (a *App) deleteManagedDownloader(ctx context.Context, key string) (any, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	var references int
	e := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE ((kind='intent' AND json_extract(payload,'$.state') NOT IN ('imported','cancelled')) OR kind='subscription') AND json_extract(payload,'$.downloader_id')=?`, key).Scan(&references)
	if e != nil {
		return nil, e
	}
	if references > 0 {
		return nil, &APIError{"DOWNLOADER_IN_USE", "下载器仍被订阅或未结束下载使用，请先修改关联订阅并处理下载任务", 409}
	}
	c := a.Settings()
	found := false
	out := make([]DownloaderConfig, 0, len(c.Downloaders))
	for _, d := range c.Downloaders {
		if d.ID == key {
			found = true
		} else {
			out = append(out, d)
		}
	}
	if !found {
		return nil, &APIError{"DOWNLOADER_NOT_FOUND", "下载器不存在", 404}
	}
	c.Downloaders = out
	if c.DefaultDownloader == key {
		c.DefaultDownloader = ""
		if len(out) > 0 {
			c.DefaultDownloader = out[0].ID
		}
	}
	if e = a.saveSettings(ctx, c); e != nil {
		return nil, e
	}
	_, _ = a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='downloader_connection' AND id=?`, key)
	return a.managedDownloaders(ctx)
}

func (a *App) testManagedDownloader(ctx context.Context, key string) (any, error) {
	d, e := a.configuredDownloader(key)
	if e != nil {
		return nil, e
	}
	started := time.Now()
	status := managedConnection{Status: "ok", CheckedAt: now()}
	client, e := download.New(d.Config)
	var tasks []download.Task
	if e == nil {
		tasks, e = client.List(ctx)
	}
	status.DurationMS = time.Since(started).Milliseconds()
	if e != nil {
		status.Status = "failed"
		status.Error = a.clean(e.Error())
	}
	current, err := a.configuredDownloader(key)
	if err != nil || a.managedDownloaderKey(current) != a.managedDownloaderKey(d) {
		return nil, bad("DOWNLOADER_CHANGED", "下载器设置已变化，请重新检测")
	}
	if err := a.Store.Put(ctx, "downloader_connection", key, managedDownloaderConnection{managedConnection: status, ConfigKey: a.managedDownloaderKey(d)}); err != nil {
		return nil, err
	}
	var uploadSpeed, downloadSpeed int64
	for _, task := range tasks {
		uploadSpeed += task.UploadSpeed
		downloadSpeed += task.DownloadSpeed
	}
	return map[string]any{"connection": status, "task_count": len(tasks), "upload_speed": uploadSpeed, "download_speed": downloadSpeed}, nil
}
