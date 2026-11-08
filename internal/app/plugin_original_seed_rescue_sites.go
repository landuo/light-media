package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins/brushflow"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
)

type originalSeedRescueSites struct {
	mu        sync.Mutex
	materials map[string]originalSeedRescueMaterial
	bindings  map[int]SiteConfig
}

type originalSeedRescueMaterial struct {
	siteID     int
	address    string
	resourceID string
}

type originalSeedRescueSite struct {
	id     int
	config SiteConfig
}

func (s originalSeedRescueSite) fields() sr.Object {
	schema := "nexusphp"
	if s.config.Kind == "mteam" {
		schema = "mteam"
	}
	address, _ := url.Parse(s.config.BaseURL)
	domain := ""
	if address != nil {
		domain = address.Hostname()
	}
	return sr.Object{"id": s.id, "name": s.config.Name, "url": sr.SafePageURL(strings.TrimRight(s.config.BaseURL, "/") + "/"),
		"domain": domain, "schema": schema, "proxy": s.config.Proxy, "timeout": s.config.Timeout}
}

func (s originalSeedRescueSite) privateFields() sr.Object {
	fields := s.fields()
	fields["url"] = strings.TrimRight(s.config.BaseURL, "/") + "/"
	fields["cookie"] = s.config.Cookie
	fields["apikey"] = s.config.APIKey
	ua := s.config.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0"
	}
	fields["ua"] = ua
	return fields
}

func (s *originalSeedRescueSites) remember(value originalSeedRescueMaterial) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.materials == nil {
		s.materials = map[string]originalSeedRescueMaterial{}
	}
	key := id("srm_")
	s.materials[key] = value
	return key
}

func (s *originalSeedRescueSites) lookup(key string) (originalSeedRescueMaterial, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.materials[key]
	return value, ok
}

func (s *originalSeedRescueSites) bind(site originalSeedRescueSite) (*originalSeedRescueSite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bindings == nil {
		s.bindings = map[int]SiteConfig{}
	}
	if before, ok := s.bindings[site.id]; ok && before != site.config {
		return nil, siteIdentityError()
	}
	s.bindings[site.id] = site.config
	return &site, nil
}

func (a *App) originalSeedRescueSiteResource(ctx context.Context, scope originalBaseCallScope, operation string, raw json.RawMessage) (json.RawMessage, error) {
	if err := originalSeedRescueValidate(ctx, scope); err != nil {
		return nil, err
	}
	var value any
	var err error
	switch operation {
	case "options":
		var request struct{}
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		value, err = a.originalSeedRescueOptions(ctx)
	case "site":
		var request struct {
			ID int `json:"id"`
		}
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		var site *originalSeedRescueSite
		site, err = a.originalSeedRescueSiteByID(ctx, scope, request.ID)
		if site != nil {
			value = site.fields()
		}
	case "indexer", "search":
		var request struct {
			Domain  string `json:"domain"`
			Keyword string `json:"keyword"`
			Page    int    `json:"page"`
		}
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		var site *originalSeedRescueSite
		site, err = a.originalSeedRescueSiteByDomain(ctx, scope, request.Domain)
		if err == nil && operation == "search" {
			value, err = a.originalSeedRescueSearch(ctx, scope, site, request.Keyword, request.Page)
		} else if site != nil {
			value = site.fields()
		}
	case "rss":
		var request struct {
			URL  string    `json:"url"`
			Site sr.Object `json:"site"`
		}
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		value, err = a.originalSeedRescueRSS(ctx, scope, request.URL, sr.PositiveInt(request.Site["id"], 0))
	case "detail":
		var request struct {
			Domain    string    `json:"domain"`
			Site      sr.Object `json:"site"`
			TorrentID string    `json:"torrent_id"`
		}
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		value, err = a.originalSeedRescueDetail(ctx, scope, request.Domain, sr.PositiveInt(request.Site["id"], 0), request.TorrentID)
	case "download_metadata":
		var request struct {
			Torrent sr.Object `json:"torrent"`
		}
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		enclosure, _ := request.Torrent["enclosure"].(string)
		material, ok := scope.seedRescue.sites.lookup(enclosure)
		if !ok {
			return nil, bad("SEED_RESCUE_SITE_UNBOUND", "下载材料未绑定本轮站点结果")
		}
		site, siteErr := a.originalSeedRescueSiteByID(ctx, scope, material.siteID)
		if siteErr != nil {
			return nil, siteErr
		}
		if site == nil {
			return nil, siteIdentityError()
		}
		content, message, metadataErr := a.originalSeedRescueMetadata(ctx, scope, material, site)
		if metadataErr != nil {
			if current := a.originalSeedRescueSiteGuard(ctx, scope, *site); current != nil {
				return nil, current
			}
			message = "站点种子下载失败"
		}
		if content != nil {
			content, err = scope.seedRescue.projectTorrent(content, *site)
			if err != nil {
				message, err = err.Error(), nil
			}
		}
		value = sr.Object{"content": content, "message": message}
	case "iyuu", "seed_info", "torrent_url":
		return a.originalSeedRescueIYUUResource(ctx, scope, operation, raw)
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知稀缺保种资源")
	}
	if err != nil {
		return nil, err
	}
	if err = originalSeedRescueValidate(ctx, scope); err != nil {
		return nil, err
	}
	return originalHostDownloaderMarshal(value)
}

func (a *App) originalSeedRescueOptions(ctx context.Context) (sr.Object, error) {
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil {
		return nil, err
	}
	siteOptions, downloaderOptions := []sr.Object{}, []sr.Object{}
	for _, row := range rows {
		if row.site.Enabled && !row.site.Public {
			siteOptions = append(siteOptions, sr.Object{"value": row.sourceID, "title": row.site.Name})
		}
	}
	for _, ref := range a.Settings().Downloaders {
		if downloaderEnabled(ref) && (ref.Kind == "qb" || ref.Kind == "qbittorrent") {
			downloaderOptions = append(downloaderOptions, sr.Object{"value": ref.Name, "title": ref.Name})
		}
	}
	return sr.Object{"sites": siteOptions, "downloaders": downloaderOptions}, nil
}

func (a *App) originalSeedRescueSiteByID(ctx context.Context, scope originalBaseCallScope, siteID int) (*originalSeedRescueSite, error) {
	cfg := scope.seedRescue.config
	if siteID <= 0 || siteID != cfg.TargetSiteID && !slices.Contains(cfg.SourceSiteIDs, siteID) {
		return nil, bad("SEED_RESCUE_SITE_UNBOUND", "站点不在当前稀缺保种配置中")
	}
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.sourceID == int64(siteID) && row.site.Enabled {
			return scope.seedRescue.sites.bind(originalSeedRescueSite{id: siteID, config: row.site})
		}
	}
	return nil, nil
}

func (a *App) originalSeedRescueSiteByDomain(ctx context.Context, scope originalBaseCallScope, domain string) (*originalSeedRescueSite, error) {
	cfg := scope.seedRescue.config
	ids := append([]int{cfg.TargetSiteID}, cfg.SourceSiteIDs...)
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		for _, row := range rows {
			site := originalSeedRescueSite{id: id, config: row.site}
			if row.sourceID == int64(id) && row.site.Enabled && site.fields()["domain"] == domain {
				return scope.seedRescue.sites.bind(site)
			}
		}
	}
	return nil, nil
}

func (a *App) originalSeedRescueSiteGuard(ctx context.Context, scope originalBaseCallScope, site originalSeedRescueSite) error {
	if err := originalSeedRescueValidate(ctx, scope); err != nil {
		return err
	}
	current, err := a.originalSeedRescueSiteByID(ctx, scope, site.id)
	if err != nil {
		return err
	}
	if current == nil || current.config != site.config {
		return siteIdentityError()
	}
	return nil
}

func (a *App) originalSeedRescueSearch(ctx context.Context, scope originalBaseCallScope, site *originalSeedRescueSite, keyword string, page int) ([]sr.Object, error) {
	out := []sr.Object{}
	if site == nil {
		return out, nil
	}
	if page < 0 || page > 100 || len(keyword) > 4096 || strings.TrimSpace(keyword) == "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "稀缺保种搜索参数无效")
	}
	err := a.withSite(ctx, site.config, false, func(client *sites.Client) error {
		client.AddRequestGuard(func(ctx context.Context) error { return a.originalSeedRescueSiteGuard(ctx, scope, *site) })
		result, err := client.Search(ctx, keyword, page+1)
		if err != nil {
			return err
		}
		for _, item := range result.Resources {
			row := sr.Object{"title": item.Title, "description": item.Subtitle, "page_url": sr.SafePageURL(item.DetailsURL), "size": item.Size}
			if item.ID != "" {
				row["enclosure"] = scope.seedRescue.sites.remember(originalSeedRescueMaterial{siteID: site.id, resourceID: item.ID})
			}
			if item.Seeders >= 0 {
				row["seeders"] = item.Seeders
			}
			if item.Leechers >= 0 {
				row["peers"] = item.Leechers
			}
			if item.DownloadFactor >= 0 {
				row["downloadvolumefactor"] = item.DownloadFactor
			}
			out = append(out, row)
		}
		return nil
	})
	return out, err
}

func (a *App) originalSeedRescueProxy(site SiteConfig) (networkproxy.Config, error) {
	if !site.Proxy {
		return networkproxy.Config{}, nil
	}
	return a.proxyConfig()
}

func originalSeedRescueOrigins(site originalSeedRescueSite, base *url.URL) []string {
	origins := []string{base.Scheme + "://" + base.Host}
	if site.config.Kind == "mteam" && sr.IsMTeam(site.privateFields()) {
		suffix := "cc"
		if strings.HasSuffix(base.Hostname(), ".io") {
			suffix = "io"
		}
		for _, subdomain := range []string{"api", "api2", "kp"} {
			origins = append(origins, "https://"+subdomain+".m-team."+suffix)
		}
	}
	return origins
}

func (a *App) originalSeedRescueTransport(ctx context.Context, scope originalBaseCallScope, site originalSeedRescueSite, rss *url.URL) (*iyuu.NativeSiteHTTP, error) {
	if err := a.originalSeedRescueSiteGuard(ctx, scope, site); err != nil {
		return nil, err
	}
	proxy, err := a.originalSeedRescueProxy(site.config)
	if err != nil {
		return nil, err
	}
	fields := site.privateFields()
	address, err := url.Parse(site.config.BaseURL)
	if err != nil || address.Hostname() == "" {
		return nil, siteIdentityError()
	}
	timeout := site.config.Timeout
	if timeout <= 0 {
		timeout = 15
	}
	if timeout > 20 {
		timeout = 20
	}
	origins := originalSeedRescueOrigins(site, address)
	if rss != nil {
		fields = sr.Object{"url": rss.Scheme + "://" + rss.Host + "/", "ua": fields["ua"], "proxy": fields["proxy"]}
		origins = []string{rss.Scheme + "://" + rss.Host}
		timeout = site.config.Timeout
		if timeout <= 0 {
			timeout = 30
		}
	}
	guard := func(ctx context.Context) error {
		if err := a.originalSeedRescueSiteGuard(ctx, scope, site); err != nil {
			return err
		}
		current, err := a.originalSeedRescueProxy(site.config)
		if err != nil || current != proxy {
			return bad("SEED_RESCUE_PROXY_CHANGED", "全局代理配置已经变化")
		}
		if rss == nil {
			return a.waitSiteRequestLimits(ctx, site.config)
		}
		return nil
	}
	return iyuu.NewNativeSiteHTTP([]iyuu.SiteTransportBinding{{Ref: site.config.ID, Fields: fields, AllowedOrigins: origins, ProxyConfig: proxy}},
		iyuu.SiteTransportOptions{Guard: guard, DefaultUA: "Mozilla/5.0", Timeout: time.Duration(timeout) * time.Second})
}

type originalSeedRescueDetailTransport struct {
	http *iyuu.NativeSiteHTTP
	ref  string
}

func (s originalSeedRescueDetailTransport) DoDetail(ctx context.Context, request sr.DetailRequest) (*sr.DetailResponse, error) {
	response, err := s.http.SeedRescueRequest(ctx, s.ref, request.Method, request.URL, request.Form, request.Headers, request.Cookie != nil, request.AllowRedirects)
	if err != nil || response == nil {
		return nil, err
	}
	var object any
	decoder := json.NewDecoder(strings.NewReader(response.Text))
	decoder.UseNumber()
	_ = decoder.Decode(&object)
	return &sr.DetailResponse{Status: response.Status, Text: response.Text, JSON: object}, nil
}

func (a *App) originalSeedRescueDetail(ctx context.Context, scope originalBaseCallScope, domain string, siteID int, torrentID string) (sr.Object, error) {
	site, err := a.originalSeedRescueSiteByID(ctx, scope, siteID)
	if err != nil {
		return nil, err
	}
	if site == nil || site.fields()["domain"] != domain || len(torrentID) > 128 {
		return nil, siteIdentityError()
	}
	transport, err := a.originalSeedRescueTransport(ctx, scope, *site, nil)
	if err != nil {
		return nil, err
	}
	defer transport.Close()
	result := sr.FetchDetail(ctx, site.privateFields(), torrentID, "Mozilla/5.0", originalSeedRescueDetailTransport{transport, site.config.ID})
	if result.Torrent != nil {
		address, _ := result.Torrent["enclosure"].(string)
		if address != "" {
			result.Torrent["enclosure"] = scope.seedRescue.sites.remember(originalSeedRescueMaterial{siteID: siteID, address: address})
		}
		if page, ok := result.Torrent["page_url"].(string); ok {
			result.Torrent["page_url"] = sr.SafePageURL(page)
		}
	}
	return sr.Object{"torrent": result.Torrent, "status": result.Status, "error": result.Error}, nil
}

func (a *App) originalSeedRescueRSS(ctx context.Context, scope originalBaseCallScope, address string, siteID int) (sr.Object, error) {
	out := []sr.Object{}
	result := sr.Object{"items": out, "complete": false}
	cfg := scope.seedRescue.config
	if siteID != cfg.TargetSiteID || !slices.Contains(cfg.RSSURLs, address) {
		return nil, bad("SEED_RESCUE_SITE_UNBOUND", "RSS 不在当前稀缺保种配置中")
	}
	site, err := a.originalSeedRescueSiteByID(ctx, scope, siteID)
	if err != nil || site == nil {
		return result, err
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || parsed.Hostname() == "" ||
		parsed.Scheme != "https" && parsed.Scheme != "http" {
		return result, nil
	}
	if _, err = nativeSiteCanonicalOrigin(parsed.Scheme + "://" + parsed.Host); err != nil {
		return result, nil
	}
	transport, err := a.originalSeedRescueTransport(ctx, scope, *site, parsed)
	if err != nil {
		return nil, err
	}
	defer transport.Close()
	response, err := transport.SeedRescueRequest(ctx, site.config.ID, "GET", address, nil, nil, false, true)
	if current := originalSeedRescueValidate(ctx, scope); current != nil {
		return nil, current
	}
	if err != nil || response == nil || response.Status != 200 {
		return result, nil
	}
	text, err := brushflow.DecodeRSSXML(response.Content, response.Headers["Content-Type"])
	if err != nil {
		return result, nil
	}
	rows, err := brushflow.ParseRustRSS(text, 1000, shanghai())
	if err != nil {
		rows, err = originalRSSRecoverRows(text, time.Now().In(shanghai()))
		if err != nil {
			return result, nil
		}
	}
	for _, item := range rows {
		title, _ := item["title"].(string)
		link, _ := item["link"].(string)
		address, _ := item["enclosure"].(string)
		handle := ""
		if address != "" {
			handle = scope.seedRescue.sites.remember(originalSeedRescueMaterial{siteID: siteID, address: address})
		}
		idSource := link
		if idSource == "" {
			idSource = address
		}
		out = append(out, sr.Object{"title": title, "link": sr.SafePageURL(link), "enclosure": handle,
			"size": item["size"], "source_feed_key": sr.FeedKey(address, link, title), "source_torrent_id": sr.ExtractTorrentID(idSource)})
	}
	return sr.Object{"items": out, "complete": true}, nil
}

func (a *App) originalSeedRescueMetadata(ctx context.Context, scope originalBaseCallScope, material originalSeedRescueMaterial, site *originalSeedRescueSite) ([]byte, string, error) {
	if material.resourceID != "" {
		var content []byte
		err := a.withSite(ctx, site.config, false, func(client *sites.Client) error {
			client.AddRequestGuard(func(ctx context.Context) error { return a.originalSeedRescueSiteGuard(ctx, scope, *site) })
			var err error
			content, err = client.Torrent(ctx, material.resourceID)
			return err
		})
		return content, "", err
	}
	transport, err := a.originalSeedRescueTransport(ctx, scope, *site, nil)
	if err != nil {
		return nil, "", err
	}
	defer transport.Close()
	address := material.address
	if strings.HasPrefix(address, "[") {
		address, err = originalSeedRescueDynamicTorrent(ctx, transport, *site, address)
		if err != nil {
			return nil, "", err
		}
		if address == "" {
			return nil, "站点动态下载链接解析失败", nil
		}
	}
	parsed, err := url.Parse(address)
	base, baseErr := url.Parse(site.config.BaseURL)
	if err != nil || baseErr != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" ||
		!slices.Contains(originalSeedRescueOrigins(*site, base), parsed.Scheme+"://"+parsed.Host) {
		return nil, "", bad("SEED_RESCUE_SITE_UNBOUND", "种子下载地址不属于当前已授权站点")
	}
	directory := filepath.Join(a.Dir, "plugin-cache", "seedrescue", tokenHash(scope.installation.InstanceID), strconv.FormatInt(scope.installation.Generation, 10))
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, "", err
	}
	cache, err := iyuu.OpenFileTorrentCache(directory)
	if err != nil {
		return nil, "", err
	}
	defer cache.Close()
	result, err := (iyuu.TorrentHelper{HTTP: originalSeedRescueTorrentHTTP{transport, site.config.ID}, Cache: cache}).Download(ctx, address, false)
	if err != nil {
		return nil, "", err
	}
	return result.Content, result.ErrorMessage, nil
}

func originalSeedRescueDynamicTorrent(ctx context.Context, transport *iyuu.NativeSiteHTTP, site originalSeedRescueSite, material string) (string, error) {
	end := strings.IndexByte(material, ']')
	if end < 2 || end > 22000 || !sr.IsMTeam(site.privateFields()) {
		return "", siteIdentityError()
	}
	raw, err := base64.StdEncoding.DecodeString(material[1:end])
	if err != nil {
		return "", siteIdentityError()
	}
	var recipe struct {
		Method string            `json:"method"`
		Cookie bool              `json:"cookie"`
		Params map[string]string `json:"params"`
		Header map[string]string `json:"header"`
		Result string            `json:"result"`
		Proxy  bool              `json:"proxy"`
	}
	fields := site.privateFields()
	if json.Unmarshal(raw, &recipe) != nil || recipe.Method != "post" || recipe.Cookie || recipe.Result != "data" ||
		len(recipe.Params) != 1 || len(recipe.Header) != 2 || recipe.Header["x-api-key"] != fields["apikey"] ||
		recipe.Header["User-Agent"] != fields["ua"] || recipe.Proxy != site.config.Proxy {
		return "", siteIdentityError()
	}
	address, err := url.Parse(material[end+1:])
	if err != nil || address.Path != "/api/torrent/genDlToken" || address.RawQuery != "" {
		return "", siteIdentityError()
	}
	torrentID := recipe.Params["id"]
	if torrentID == "" || strings.Trim(torrentID, "0123456789") != "" {
		return "", siteIdentityError()
	}
	query := url.Values{"id": {torrentID}}
	address.RawQuery = query.Encode()
	response, err := transport.SeedRescueRequest(ctx, site.config.ID, "POST", address.String(), nil, recipe.Header, false, true)
	if err != nil || response == nil || response.Status != 200 {
		return "", err
	}
	var result struct {
		Data string `json:"data"`
	}
	if json.Unmarshal(response.Content, &result) != nil {
		return "", nil
	}
	return result.Data, nil
}

type originalSeedRescueTorrentHTTP struct {
	http *iyuu.NativeSiteHTTP
	ref  string
}

func (s originalSeedRescueTorrentHTTP) Get(ctx context.Context, address string) (*iyuu.TorrentResponse, error) {
	return s.http.SeedRescueRequest(ctx, s.ref, "GET", address, nil, nil, true, false)
}
func (s originalSeedRescueTorrentHTTP) Post(ctx context.Context, address string, form map[string]string) (*iyuu.TorrentResponse, error) {
	return s.http.SeedRescueRequest(ctx, s.ref, "POST", address, form, nil, true, true)
}
