package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	pyre "light-media/internal/plugins/iyuuautoseed/compat/pythonre"
	"math"
	"math/big"
	"net/url"
	"strings"
	"time"

	"light-media/internal/networkproxy"
	bf "light-media/internal/plugins/brushflow"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/sites"
)

type brushFlowCandidate struct {
	Site      SiteConfig
	Row       bf.Row
	Resource  string
	Enclosure string
}

func (a *App) brushFlowSitesTx(ctx context.Context, tx *sql.Tx) ([]bf.Site, error) {
	out := []bf.Site{}
	for _, s := range a.Settings().Sites {
		if !s.Enabled {
			continue
		}
		identity, e := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", s.ID)
		if e != nil {
			return nil, e
		}
		origin, e := nativeConfiguredSiteOrigin(s)
		if e != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
			return nil, siteIdentityError()
		}
		schema := "nexusphp"
		if s.Kind == "hhanclub" {
			schema = "hhanclub"
		}
		if s.Kind == "mteam" {
			schema = "mteam"
		}
		out = append(out, bf.Site{ID: int(identity.SourceID), Name: s.Name, Domain: siteStatisticDomain(s), URL: strings.TrimRight(s.BaseURL, "/") + "/", Schema: schema, Public: s.Public, Raw: bf.Row{"id": identity.publicSourceID(), "name": s.Name, "domain": siteStatisticDomain(s), "url": strings.TrimRight(s.BaseURL, "/") + "/", "cookie": s.Cookie, "apikey": s.APIKey, "ua": seedRescueUA(s.UserAgent), "timeout": s.Timeout, "proxy": s.Proxy, "schema": schema, "_native_ref": s.ID}})
	}
	return out, nil
}
func (o *pluginBrushFlowOwner) Sites(ctx context.Context) ([]bf.Site, error) {
	if e := o.callIOGuard(ctx); e != nil {
		return nil, e
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	return o.a.brushFlowSitesTx(ctx, tx)
}
func (a *App) brushFlowUsersTx(ctx context.Context, tx *sql.Tx) ([]bf.UserData, error) {
	out := []bf.UserData{}
	for _, site := range a.Settings().Sites {
		if !site.Enabled {
			continue
		}
		var raw []byte
		e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='site_stats_latest' AND id=?`, site.ID).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return nil, e
		}
		var v siteSnapshot
		if a.Store.openSiteAccountSnapshot("site_stats_latest", site.ID, raw, &v) != nil {
			return nil, brushFlowEvidenceError()
		}
		if v.ConfigKey != a.managedSiteKey(site) {
			continue
		}
		d, e := statisticData(v, site)
		if e != nil {
			return nil, e
		}
		var ratio, up, down any
		if d.Ratio != nil {
			ratio = *d.Ratio
		}
		if d.Upload != nil {
			up = *d.Upload
		}
		if d.Download != nil {
			down = *d.Download
		}
		at := ""
		if v.Stats != nil {
			at = v.Stats.CapturedAt
		}
		out = append(out, bf.UserData{Domain: d.Domain, Ratio: ratio, Upload: up, Download: down, UpdatedDay: d.UpdatedDay, UpdatedTime: at})
	}
	return out, nil
}
func (o *pluginBrushFlowOwner) SiteUsers(ctx context.Context) ([]bf.UserData, error) {
	if e := o.callIOGuard(ctx); e != nil {
		return nil, e
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	return o.a.brushFlowUsersTx(ctx, tx)
}
func (o *pluginBrushFlowOwner) site(ctx context.Context, id int) (SiteConfig, bf.Site, error) {
	list, e := o.Sites(ctx)
	if e != nil {
		return SiteConfig{}, bf.Site{}, e
	}
	for _, v := range list {
		if v.ID == id {
			for _, s := range o.a.Settings().Sites {
				if s.ID == v.Raw["_native_ref"] {
					return s, v, nil
				}
			}
		}
	}
	return SiteConfig{}, bf.Site{}, bad("BRUSH_FLOW_SITE_UNBOUND", "站点没有有效的原生身份")
}
func (o *pluginBrushFlowOwner) siteProxy(ctx context.Context, s SiteConfig) (networkproxy.Config, error) {
	if !s.Proxy {
		return networkproxy.Config{}, nil
	}
	if resolver := o.a.pluginBrushFlow.proxy; resolver != nil {
		v, e := resolver(ctx, s.BaseURL)
		if e != nil {
			return networkproxy.Config{}, e
		}
		return networkproxy.Resolve(v, nil)
	}
	return o.a.proxyConfig()
}
func (o *pluginBrushFlowOwner) siteTransport(ctx context.Context, s SiteConfig, v bf.Site) (*iyuu.NativeSiteHTTP, error) {
	proxy, e := o.siteProxy(ctx, s)
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(s.BaseURL)
	if e != nil || u.Host == "" {
		return nil, siteIdentityError()
	}
	origins := []string{u.Scheme + "://" + u.Host}
	if s.Kind == "mteam" {
		for _, root := range []string{"m-team.cc", "m-team.io"} {
			if u.Hostname() == root || strings.HasSuffix(u.Hostname(), "."+root) {
				for _, pre := range []string{"api.", "api2.", "kp."} {
					origins = append(origins, "https://"+pre+root)
				}
			}
		}
	}
	stamp := tokenHash(string(mustCleanSeedJSON(s)))
	guard := func(ctx context.Context) error {
		if e := o.callIOGuard(ctx); e != nil {
			return e
		}
		current, _, e := o.site(ctx, v.ID)
		if e != nil || tokenHash(string(mustCleanSeedJSON(current))) != stamp {
			return &brushFlowBoundaryError{siteIdentityError()}
		}
		p, e := o.siteProxy(ctx, current)
		if e != nil || p != proxy {
			return &brushFlowBoundaryError{bad("BRUSH_FLOW_PROXY_CHANGED", "全局代理配置已经变化")}
		}
		return o.a.waitSiteRequestLimits(ctx, current)
	}
	return iyuu.NewNativeSiteHTTP([]iyuu.SiteTransportBinding{{Ref: s.ID, Fields: map[string]any(v.Raw), AllowedOrigins: origins, ProxyConfig: proxy}}, iyuu.SiteTransportOptions{Guard: guard, DefaultUA: "Mozilla/5.0", Timeout: time.Duration(seedRescueTimeout(s.Timeout)) * time.Second})
}
func (o *pluginBrushFlowOwner) SiteRequest(ctx context.Context, id int, method, address string, data bf.Row) (bf.Row, error) {
	s, v, e := o.site(ctx, id)
	if e != nil {
		return nil, e
	}
	t, e := o.siteTransport(ctx, s, v)
	if e != nil {
		return nil, e
	}
	defer t.Close()
	form := map[string]string{}
	for k, val := range data {
		form[k] = brushFlowString(val)
	}
	headers := map[string]string{"User-Agent": seedRescueUA(s.UserAgent)}
	cookie := true
	if method == "POST" {
		u, e := url.Parse(address)
		if e != nil || s.Kind != "mteam" || u.Path != "/api/torrent/detail" || len(form) != 1 || form["id"] == "" {
			return nil, brushFlowEvidenceError()
		}
		headers["x-api-key"] = s.APIKey
		cookie = false
	} else if method != "GET" {
		return nil, brushFlowEvidenceError()
	}
	response, e := t.SeedRescueRequest(ctx, s.ID, method, address, form, headers, cookie, true)
	if e != nil {
		return nil, e
	}
	var object any
	dec := json.NewDecoder(strings.NewReader(response.Text))
	dec.UseNumber()
	_ = dec.Decode(&object)
	return bf.Row{"ok": response.Status >= 200 && response.Status < 400, "status_code": response.Status, "text": response.Text, "json": object}, nil
}
func (o *pluginBrushFlowOwner) Browse(ctx context.Context, id int, rss bool) ([]bf.Row, error) {
	s, site, e := o.site(ctx, id)
	if e != nil {
		return nil, e
	}
	if rss {
		return o.browseRSS(ctx, s, site)
	}
	out := []bf.Row{}
	e = o.a.withSite(ctx, s, false, func(client *sites.Client) error {
		client.AddRequestGuard(o.nativeSiteGuard(s, site.ID))
		result, e := client.Search(ctx, "", 1)
		if e != nil {
			return e
		}
		for _, r := range result.Resources {
			published := r.PublishedAt
			if at, e := time.Parse(time.RFC3339Nano, published); e == nil {
				published = at.Format("2006-01-02 15:04:05")
			}
			var seeders, peers any
			if r.Seeders >= 0 {
				seeders = r.Seeders
			}
			if r.Leechers >= 0 {
				peers = r.Leechers
			}
			row := bf.Row{"site": site.ID, "site_name": site.Name, "title": r.Title, "description": r.Subtitle, "size": r.Size, "pubdate": published, "seeders": seeders, "peers": peers, "page_url": r.DetailsURL, "freedate": r.PromotionEnd, "downloadvolumefactor": r.DownloadFactor, "uploadvolumefactor": r.UploadFactor, "hit_and_run": r.HitAndRun, "enclosure": "native-resource:" + r.ID}
			o.rememberCandidate(ctx, s, row, r.ID, "")
			out = append(out, row)
		}
		return nil
	})
	return out, e
}
func (o *pluginBrushFlowOwner) rememberCandidate(ctx context.Context, s SiteConfig, row bf.Row, resource, enclosure string) {
	brushFlowCandidateProperties(row, o.Now())
	c := o.call(ctx)
	if c.candidates == nil {
		c.candidates = map[string]brushFlowCandidate{}
	}
	token := "candidate_" + o.NewID()
	row["_brush_candidate"] = token
	raw, err := json.Marshal(row)
	if err != nil {
		return
	}
	var copy bf.Row
	if brushFlowDecodeNumbers(raw, &copy) != nil {
		return
	}
	c.candidates[token] = brushFlowCandidate{Site: s, Row: copy, Resource: resource, Enclosure: enclosure}
}
func (o *pluginBrushFlowOwner) browseRSS(ctx context.Context, s SiteConfig, v bf.Site) ([]bf.Row, error) {
	if s.RSS == "" {
		return []bf.Row{}, nil
	}
	u, e := url.Parse(s.RSS)
	if e != nil || u.User != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, bad("BRUSH_FLOW_RSS_INVALID", "站点RSS地址无效")
	}
	proxy, e := o.siteProxy(ctx, s)
	if e != nil {
		return nil, e
	}
	stamp := tokenHash(string(mustCleanSeedJSON(s)))
	t, e := iyuu.NewNativeSiteHTTP([]iyuu.SiteTransportBinding{{Ref: s.ID, Fields: map[string]any(v.Raw), AllowedOrigins: []string{u.Scheme + "://" + u.Host}, ProxyConfig: proxy}}, iyuu.SiteTransportOptions{Guard: func(ctx context.Context) error {
		if e := o.callIOGuard(ctx); e != nil {
			return e
		}
		current, _, e := o.site(ctx, v.ID)
		if e != nil || tokenHash(string(mustCleanSeedJSON(current))) != stamp {
			return siteIdentityError()
		}
		return o.a.waitSiteRequestLimits(ctx, current)
	}, DefaultUA: "Mozilla/5.0", Timeout: time.Duration(brushFlowRSSTimeout(s.Timeout)) * time.Second})
	if e != nil {
		return nil, e
	}
	defer t.Close()
	response, e := t.BrushRSSRead(ctx, s.ID, s.RSS, seedRescueUA(s.UserAgent))
	if e != nil {
		if guard := o.callIOGuard(ctx); guard != nil {
			return nil, guard
		}
		var boundary interface{ StopExternalEffects() bool }
		if errors.As(e, &boundary) && boundary.StopExternalEffects() {
			return nil, e
		}
		o.Log(ctx, "error", "获取RSS失败："+e.Error())
		return []bf.Row{}, nil
	}
	if response.Status != 200 {
		return []bf.Row{}, nil
	}
	if len(response.Content) > 50*1024*1024 {
		o.Log(ctx, "warning", "RSS文件过大，跳过解析")
		return []bf.Row{}, nil
	}
	decoded, e := bf.DecodeRSSXML(response.Content, response.Headers["Content-Type"])
	if e != nil || !strings.HasPrefix(strings.TrimSpace(decoded), "<") {
		o.Log(ctx, "error", "RSS内容不是有效的XML格式")
		return []bf.Row{}, nil
	}
	rows, e := bf.ParseRustRSS(decoded, 1001, o.Now().Location())
	if e != nil {
		o.Log(ctx, "debug", "Rust RSS解析失败，使用 XML 解析兜底："+e.Error())
		rows, e = brushFlowRecoverRSS(decoded, o.Now().Location())
	}
	if e != nil {
		o.Log(ctx, "error", "RSS解析失败："+e.Error())
		return []bf.Row{}, nil
	}
	if len(rows) > 1000 {
		o.Log(ctx, "warning", "RSS条目过多，仅处理前1000个")
		rows = rows[:1000]
	}
	out := []bf.Row{}
	for _, r := range rows {
		published := r["pubdate"]
		if published == "" {
			published = nil
		}
		row := bf.Row{"site": v.ID, "site_name": v.Name, "title": r["title"], "description": nil, "page_url": r["link"], "pubdate": published, "size": r["size"], "seeders": 0, "peers": 0, "downloadvolumefactor": nil, "uploadvolumefactor": nil, "hit_and_run": false, "freedate": nil, "enclosure": r["enclosure"]}
		o.rememberCandidate(ctx, s, row, "", brushFlowString(row["enclosure"]))
		out = append(out, row)
	}
	return out, nil
}
func brushFlowRSSTimeout(v int) int {
	if v == 0 {
		return 30
	}
	return v
}

func (o *pluginBrushFlowOwner) SubscribeTitles(ctx context.Context) ([]string, error) {
	if e := o.callIOGuard(ctx); e != nil {
		return nil, e
	}
	raw, e := o.a.Store.List(ctx, "subscriptions", 100000, 0)
	if e != nil {
		return nil, e
	}
	o.subscriptionMu.Lock()
	defer o.subscriptionMu.Unlock()
	if o.subscriptionNames == nil {
		o.subscriptionNames = map[string][]string{}
	}
	current := map[string]bool{}
	out, seen := []string{}, map[string]bool{}
	for _, r := range raw {
		var v Subscription
		if json.Unmarshal(r, &v) != nil {
			return nil, brushFlowEvidenceError()
		}
		key := v.ID + "_" + v.Media.Title
		current[key] = true
		names, ok := o.subscriptionNames[key]
		if !ok {
			names = []string{v.Media.Title}
			// The native P0 subscription identity is an explicit TMDB type/id.
			// Source cache keys are id_name, including failed recognition until reload.
			if e := o.callIOGuard(ctx); e != nil {
				return nil, e
			}
			found, err := o.a.catalog().SourceNames(ctx, v.Media.MediaType, v.Media.ID)
			if e := o.callIOGuard(ctx); e != nil {
				return nil, e
			}
			if err != nil {
				if e := o.callIOGuard(ctx); e != nil {
					return nil, e
				}
				o.Log(ctx, "error", "识别订阅 "+v.Media.Title+" 失败："+err.Error())
			} else {
				names = append(names, found...)
			}
			cleaned := []string{}
			for _, name := range names {
				if name = strings.TrimSpace(name); name != "" {
					cleaned = append(cleaned, name)
				}
			}
			names = cleaned
			o.subscriptionNames[key] = names
		}
		for _, name := range names {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	for key := range o.subscriptionNames {
		if !current[key] {
			delete(o.subscriptionNames, key)
		}
	}
	return out, nil
}

// TorrentInfo's two consumed computed properties, using the provider's wall
// clock text and the original local-clock subtraction (including negative days).
func brushFlowCandidateProperties(row bf.Row, now time.Time) {
	row["volume_factor"] = "未知"
	if row["uploadvolumefactor"] != nil && row["downloadvolumefactor"] != nil {
		key := fmt.Sprintf("%.2f %.2f", brushFlowNumber(row["uploadvolumefactor"]), brushFlowNumber(row["downloadvolumefactor"]))
		if v, ok := map[string]string{"1.00 1.00": "普通", "1.00 0.00": "免费", "2.00 1.00": "2X", "4.00 1.00": "4X", "2.00 0.00": "2X免费", "4.00 0.00": "4X免费", "1.00 0.50": "50%", "2.00 0.50": "2X 50%", "1.00 0.70": "70%", "1.00 0.30": "30%", "1.00 0.75": "75%", "1.00 0.25": "25%"}[key]; ok {
			row["volume_factor"] = v
		}
	}
	raw := brushFlowString(row["freedate"])
	row["freedate_diff"] = ""
	if raw == "" {
		return
	}
	if at, e := time.Parse(time.RFC3339Nano, raw); e == nil {
		raw = at.Format("2006-01-02 15:04:05")
		row["freedate"] = raw
	}
	at, e := time.ParseInLocation("2006-01-02 15:04:05", raw, now.Location())
	if e != nil {
		row["freedate_diff"] = raw
		return
	}
	total := int64(math.Floor(at.Sub(now).Seconds()))
	days := int64(math.Floor(float64(total) / 86400))
	seconds := total - days*86400
	hours, mins := seconds/3600, (seconds%3600)/60
	if days > 0 {
		row["freedate_diff"] = fmt.Sprintf("%d天%d小时%d分钟", days, hours, mins)
	} else if hours > 0 {
		row["freedate_diff"] = fmt.Sprintf("%d小时%d分钟", hours, mins)
	} else if mins > 0 {
		row["freedate_diff"] = fmt.Sprintf("%d分钟", mins)
	}
}

func (o *pluginBrushFlowOwner) nativeSiteGuard(s SiteConfig, id int) func(context.Context) error {
	stamp := tokenHash(string(mustCleanSeedJSON(s)))
	proxy, initialErr := o.a.proxyConfig()
	return func(ctx context.Context) error {
		if e := o.callIOGuard(ctx); e != nil {
			return e
		}
		current, _, e := o.site(ctx, id)
		if e != nil || tokenHash(string(mustCleanSeedJSON(current))) != stamp {
			return &brushFlowBoundaryError{siteIdentityError()}
		}
		if current.Proxy {
			now, e := o.a.proxyConfig()
			if initialErr != nil || e != nil || now != proxy {
				return &brushFlowBoundaryError{bad("BRUSH_FLOW_PROXY_CHANGED", "站点代理配置已变化")}
			}
		}
		return nil
	}
}

// XMLParser(recover=True,resolve_entities=False,no_network=True) fallback.
// Native0.2.6 succeeded documents never enter this slower recovery path.
type brushRSSXMLNode struct {
	name        xml.Name
	attrs       []xml.Attr
	text        string
	children    []*brushRSSXMLNode
	hasChild    bool
	textInvalid bool
	attrInvalid bool
}

func brushFlowRecoverRSS(doc string, loc *time.Location) ([]bf.Row, error) {
	root := brushRSSXMLTree(doc)
	if len(root.children) == 0 {
		return nil, fmt.Errorf("RSS XML has no root")
	}
	var descendants func(*brushRSSXMLNode, string, bool) []*brushRSSXMLNode
	descendants = func(node *brushRSSXMLNode, name string, local bool) []*brushRSSXMLNode {
		result := []*brushRSSXMLNode{}
		for _, child := range node.children {
			if child.name.Local == name && (local || child.name.Space == "") {
				result = append(result, child)
			}
			result = append(result, descendants(child, name, local)...)
		}
		return result
	}
	attr := func(node *brushRSSXMLNode, name string) string {
		for _, a := range node.attrs {
			if a.Name.Space == "" && a.Name.Local == name {
				return a.Value
			}
		}
		return ""
	}
	nodes := []*brushRSSXMLNode{}
	var visit func(*brushRSSXMLNode)
	visit = func(n *brushRSSXMLNode) {
		for _, child := range n.children {
			if child.name.Space == "" && (child.name.Local == "item" || child.name.Local == "entry") {
				nodes = append(nodes, child)
			}
			visit(child)
		}
	}
	visit(root.children[0])
	if len(nodes) > 1000 {
		nodes = nodes[:1000]
	}
	out := []bf.Row{}
	for _, node := range nodes {
		titles := descendants(node, "title", false)
		if len(titles) == 0 || titles[0].text == "" || titles[0].textInvalid {
			continue
		}
		links, enclosures := descendants(node, "link", false), descendants(node, "enclosure", false)
		link, enclosure := "", ""
		if len(links) > 0 {
			if links[0].textInvalid || links[0].attrInvalid {
				continue
			}
			link = links[0].text
			if link == "" {
				link = attr(links[0], "href")
			}
		}
		var size any = int64(0)
		if len(enclosures) > 0 {
			if enclosures[0].attrInvalid {
				continue
			}
			enclosure = attr(enclosures[0], "url")
			length := attr(enclosures[0], "length")
			normalized := strings.Builder{}
			digits := length != ""
			for _, r := range length {
				digit, ok := pyre.DecimalValue(r)
				if !ok {
					digits = false
					break
				}
				normalized.WriteByte(byte('0' + digit))
			}
			if digits {
				n := new(big.Int)
				if _, ok := n.SetString(normalized.String(), 10); ok {
					size = json.Number(n.String())
				}
			}
		}
		if enclosure == "" {
			enclosure = link
		}
		if enclosure == "" {
			continue
		}
		description := ""
		var firstDesc *brushRSSXMLNode
		var firstDate *brushRSSXMLNode
		var find func(*brushRSSXMLNode)
		find = func(n *brushRSSXMLNode) {
			for _, child := range n.children {
				if firstDesc == nil && child.name.Space == "" && (child.name.Local == "description" || child.name.Local == "summary") {
					firstDesc = child
				}
				find(child)
			}
		}
		find(node)
		if firstDesc != nil {
			if firstDesc.textInvalid {
				continue
			}
			description = firstDesc.text
		}
		for _, child := range node.children {
			if child.name.Space == "" && (child.name.Local == "pubDate" || child.name.Local == "published" || child.name.Local == "updated") {
				firstDate = child
				break
			}
		}
		if firstDate == nil {
			var findDate func(*brushRSSXMLNode)
			findDate = func(n *brushRSSXMLNode) {
				for _, child := range n.children {
					if firstDate == nil && (child.name.Local == "pubDate" || child.name.Local == "published" || child.name.Local == "updated") {
						firstDate = child
					}
					findDate(child)
				}
			}
			findDate(node)
		}
		var date any = ""
		if firstDate != nil && firstDate.text != "" {
			if firstDate.textInvalid {
				continue
			}
			date = brushFlowRSSFallbackDate(firstDate.text, loc)
		}
		row := bf.Row{"title": titles[0].text, "description": description, "link": link, "enclosure": enclosure, "size": size, "pubdate": date}
		creators := descendants(node, "creator", true)
		if len(creators) > 0 && creators[0].text != "" {
			if creators[0].textInvalid {
				continue
			}
			row["nickname"] = creators[0].text
		}
		out = append(out, row)
	}
	return out, nil
}
func brushFlowRSSFallbackDate(value string, loc *time.Location) any {
	// Source StringUtils.get_time uses dateutil.parse, then astimezone(None).
	// Full provider dates are independent of the parser's current-day default.
	// It accepts an incorrect weekday, unlike Chrono's RFC2822 validation.
	value = strings.TrimSpace(value)
	zone := loc
	if strings.HasSuffix(value, " UTC") || strings.HasSuffix(value, " GMT") {
		value = value[:len(value)-4]
		zone = time.UTC
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339Nano,
		"2006-1-2 15:04:05", "2006-1-2T15:04:05", "2006-1-2 15:04", "2006-1-2T15:04",
		"2006/1/2 15:04:05", "2006/1/2 15:04", "2 Jan 2006 15:04:05", "Mon, 2 Jan 2006 15:04:05",
		"2006-1-2", "2006/1/2", "2 Jan 2006"} {
		if at, e := time.ParseInLocation(layout, value, zone); e == nil {
			return at.In(loc).Format("2006-01-02 15:04:05")
		}
	}
	return nil
}
