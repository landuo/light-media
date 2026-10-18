package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"light-media/internal/networkproxy"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	encodingdetector "light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
)

// The proxy resolver is an App-owned seam, never a worker URL. The final host
// supplies the common networkproxy resolver for both HTTP/HTTPS roles.
func (o *pluginSeedRescueOwner) proxyConfig(ctx context.Context, site SiteConfig) (networkproxy.Config, error) {
	if !site.Proxy {
		return networkproxy.Config{}, nil
	}
	if resolver := o.a.pluginSeedRescue.proxy; resolver != nil {
		raw, e := resolver(ctx, site.BaseURL)
		if e != nil {
			return networkproxy.Config{}, e
		}
		return networkproxy.Resolve(raw, nil)
	}
	return o.a.proxyConfig()
}
func (o *pluginSeedRescueOwner) siteTransport(ctx context.Context, site sr.Object) (*iyuu.NativeSiteHTTP, string, error) {
	configured, e := o.sourceSite(ctx, site)
	if e != nil {
		return nil, "", e
	}
	proxy, e := o.proxyConfig(ctx, configured)
	if e != nil {
		return nil, "", e
	}
	u, e := url.Parse(configured.BaseURL)
	if e != nil || u.Host == "" {
		return nil, "", siteIdentityError()
	}
	origins := []string{u.Scheme + "://" + u.Host}
	if configured.Kind == "mteam" {
		host := u.Hostname()
		if strings.HasSuffix(host, ".m-team.cc") || host == "m-team.cc" {
			origins = append(origins, "https://api.m-team.cc", "https://api2.m-team.cc", "https://kp.m-team.cc")
		}
		if strings.HasSuffix(host, ".m-team.io") || host == "m-team.io" {
			origins = append(origins, "https://api.m-team.io", "https://api2.m-team.io", "https://kp.m-team.io")
		}
	}
	stamp := tokenHash(string(mustCleanSeedJSON(configured)))
	guard := func(ctx context.Context) error {
		if e := o.active.ioGuard(ctx); e != nil {
			return e
		}
		current, e := o.sourceSite(ctx, site)
		if e != nil || tokenHash(string(mustCleanSeedJSON(current))) != stamp {
			return siteIdentityError()
		}
		currentProxy, e := o.proxyConfig(ctx, current)
		if e != nil || currentProxy != proxy {
			return bad("SEED_RESCUE_PROXY_CHANGED", "全局代理配置已经变化")
		}
		return o.a.waitSiteRequestLimits(ctx, current)
	}
	transport, e := iyuu.NewNativeSiteHTTP([]iyuu.SiteTransportBinding{{Ref: configured.ID, Fields: map[string]any(site), AllowedOrigins: origins, ProxyConfig: proxy}}, iyuu.SiteTransportOptions{Guard: guard, DefaultUA: "Mozilla/5.0", Timeout: time.Duration(seedRescueTimeout(configured.Timeout)) * time.Second})
	return transport, configured.ID, e
}
func seedRescueTimeout(v int) int {
	if v <= 0 {
		return 15
	}
	if v > 20 {
		return 20
	}
	return v
}
func (o *pluginSeedRescueOwner) Detail(ctx context.Context, domain string, site sr.Object, tid string) (sr.DetailResult, error) {
	u, _ := url.Parse(fmt.Sprint(site["url"]))
	if u == nil || u.Hostname() != domain {
		return sr.DetailResult{}, siteIdentityError()
	}
	transport, ref, e := o.siteTransport(ctx, site)
	if e != nil {
		return sr.DetailResult{}, e
	}
	defer transport.Close()
	return sr.FetchDetail(ctx, site, tid, "Mozilla/5.0", seedRescueDetailTransport{transport, ref}), nil
}

type seedRescueDetailTransport struct {
	t   *iyuu.NativeSiteHTTP
	ref string
}

func (s seedRescueDetailTransport) DoDetail(ctx context.Context, r sr.DetailRequest) (*sr.DetailResponse, error) {
	res, e := s.t.SeedRescueRequest(ctx, s.ref, r.Method, r.URL, r.Form, r.Headers, r.Cookie != nil, r.AllowRedirects)
	if e != nil || res == nil {
		return nil, e
	}
	var object any
	d := json.NewDecoder(strings.NewReader(res.Text))
	d.UseNumber()
	_ = d.Decode(&object)
	return &sr.DetailResponse{Status: res.Status, Text: res.Text, JSON: object}, nil
}
func (o *pluginSeedRescueOwner) RSS(ctx context.Context, address string, site sr.Object) ([]sr.Object, bool, error) {
	conf := sr.ParseConfig(o.active.config)
	found := false
	for _, raw := range conf.RSSURLs {
		found = found || raw == address
	}
	if !found {
		return nil, false, seedRescueEvidenceError()
	}
	if _, e := o.sourceSite(ctx, site); e != nil {
		return nil, false, e
	}
	u, e := url.Parse(address)
	if e != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, false, bad("SEED_RESCUE_RSS_INVALID", "RSS地址无效")
	}
	configured, e := o.sourceSite(ctx, site)
	if e != nil {
		return nil, false, e
	}
	proxy, e := o.proxyConfig(ctx, configured)
	if e != nil {
		return nil, false, e
	}
	rssFields := map[string]any{"url": u.Scheme + "://" + u.Host + "/", "ua": site["ua"], "proxy": site["proxy"]}
	transport, e := iyuu.NewNativeSiteHTTP([]iyuu.SiteTransportBinding{{Ref: "rss", Fields: rssFields, AllowedOrigins: []string{u.Scheme + "://" + u.Host}, ProxyConfig: proxy}}, iyuu.SiteTransportOptions{Guard: o.active.ioGuard, DefaultUA: "Mozilla/5.0", Timeout: 20 * time.Second})
	if e != nil {
		return nil, false, e
	}
	defer transport.Close()
	response, e := transport.SeedRescueRequest(ctx, "rss", "GET", address, nil, nil, false, true)
	if e != nil {
		return nil, false, e
	}
	if response == nil || response.Status != 200 {
		return nil, false, nil
	}
	// XML is parsed from bytes, retaining its declared charset. No document URL,
	// external entity, executable or filesystem access is provided to the parser.
	decoder := xml.NewDecoder(bytes.NewReader(response.Content))
	decoder.CharsetReader = seedRescueXMLCharset
	type rssItem struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		Date        string `xml:"pubDate"`
		Enclosure   struct {
			URL    string `xml:"url,attr"`
			Length string `xml:"length,attr"`
		} `xml:"enclosure"`
		Size string `xml:"size"`
	}
	type feed struct {
		Items []rssItem `xml:"channel>item"`
	}
	var parsed feed
	if e = decoder.Decode(&parsed); e != nil {
		return nil, false, e
	}
	out := []sr.Object{}
	for _, item := range parsed.Items {
		var size any
		raw := item.Enclosure.Length
		if raw == "" {
			raw = item.Size
		}
		if n, e := strconv.ParseInt(raw, 10, 64); e == nil {
			size = n
		}
		out = append(out, sr.Object{"title": item.Title, "link": item.Link, "enclosure": item.Enclosure.URL, "description": item.Description, "pubdate": item.Date, "size": size})
	}
	return out, true, nil
}
func (o *pluginSeedRescueOwner) metadataSite(ctx context.Context, torrent sr.Object, address string) (sr.Object, error) {
	conf := sr.ParseConfig(o.active.config)
	ids := append([]int{conf.TargetSiteID}, conf.SourceSiteIDs...)
	u, e := url.Parse(address)
	if e != nil {
		return nil, e
	}
	for _, id := range ids {
		site, e := o.Site(ctx, id)
		if e != nil {
			return nil, e
		}
		if site == nil {
			continue
		}
		base, _ := url.Parse(fmt.Sprint(site["url"]))
		same := base != nil && base.Scheme == u.Scheme && base.Host == u.Host
		if !same && sr.IsMTeam(site) {
			host := u.Hostname()
			root := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(base.Hostname(), "kp."), "api2."), "api.")
			same = u.Scheme == "https" && (host == "api."+root || host == "api2."+root || host == "kp."+root)
		}
		if !same {
			continue
		}
		if raw, ok := torrent["site_cookie"]; ok && fmt.Sprint(raw) != fmt.Sprint(site["cookie"]) {
			continue
		}
		return site, nil
	}
	return nil, bad("SEED_RESCUE_SITE_UNBOUND", "种子下载地址不属于当前已授权站点")
}
func (o *pluginSeedRescueOwner) DownloadMetadata(ctx context.Context, torrent sr.Object) ([]byte, string, error) {
	if e := o.active.ioGuard(ctx); e != nil {
		return nil, "", e
	}
	enclosure, _ := torrent["enclosure"].(string)
	if strings.HasPrefix(enclosure, "native-resource:") {
		siteID, _ := torrent["_native_site_id"].(string)
		resourceID, _ := torrent["_native_resource_id"].(string)
		for _, s := range o.a.Settings().Sites {
			if s.ID != siteID {
				continue
			}
			site, e := o.metadataSite(ctx, torrent, s.BaseURL)
			if e != nil {
				return nil, "", e
			}
			if _, e = o.sourceSite(ctx, site); e != nil {
				return nil, "", e
			}
			var content []byte
			e = o.a.withSite(ctx, s, false, func(client *sites.Client) error {
				client.AddRequestGuard(o.active.ioGuard)
				var e error
				content, e = client.Torrent(ctx, resourceID)
				return e
			})
			return content, "", e
		}
		return nil, "", siteIdentityError()
	}
	address := enclosure
	var recipe sr.Object
	if strings.HasPrefix(address, "[") {
		end := strings.IndexByte(address, ']')
		if end < 2 {
			return nil, "下载配方无效", nil
		}
		raw, e := base64.StdEncoding.DecodeString(address[1:end])
		if e != nil || len(raw) > 16384 {
			return nil, "下载配方无效", nil
		}
		recipe, e = seedRescueConfig(raw)
		if e != nil {
			return nil, "下载配方无效", nil
		}
		address = address[end+1:]
	}
	site, e := o.metadataSite(ctx, torrent, address)
	if e != nil {
		return nil, "", e
	}
	transport, ref, e := o.siteTransport(ctx, site)
	if e != nil {
		return nil, "", e
	}
	defer transport.Close()
	if recipe != nil {
		if !sr.IsMTeam(site) || recipe["method"] != "post" || recipe["cookie"] != false || recipe["result"] != "data" {
			return nil, "", seedRescueEvidenceError()
		}
		parsed, e := url.Parse(address)
		if e != nil || parsed.Path != "/api/torrent/genDlToken" || parsed.RawQuery != "" {
			return nil, "", seedRescueEvidenceError()
		}
		params, ok := recipe["params"].(map[string]any)
		if !ok || len(params) != 1 {
			return nil, "", seedRescueEvidenceError()
		}
		tid := fmt.Sprint(params["id"])
		if tid == "" || strings.Trim(tid, "0123456789") != "" {
			return nil, "", seedRescueEvidenceError()
		}
		headers, ok := recipe["header"].(map[string]any)
		if !ok || len(headers) != 2 || headers["x-api-key"] != site["apikey"] || headers["User-Agent"] != site["ua"] || recipe["proxy"] != site["proxy"] {
			return nil, "", seedRescueEvidenceError()
		}
		query := parsed.Query()
		query.Set("id", tid)
		parsed.RawQuery = query.Encode()
		response, e := transport.SeedRescueRequest(ctx, ref, "POST", parsed.String(), nil, map[string]string{"x-api-key": fmt.Sprint(site["apikey"]), "User-Agent": fmt.Sprint(site["ua"])}, false, true)
		if e != nil {
			return nil, "", e
		}
		if response == nil || response.Status != 200 {
			return nil, "未取得种子下载链接", nil
		}
		var object map[string]any
		if json.Unmarshal([]byte(response.Text), &object) != nil {
			return nil, "下载链接响应无效", nil
		}
		tokenURL, ok := object["data"].(string)
		if !ok {
			return nil, "下载链接响应无效", nil
		}
		address = tokenURL
	}
	directory := filepath.Join(o.a.Dir, "plugin-cache", "seedrescue", tokenHash(o.instance), strconv.FormatInt(o.generation, 10))
	if e = os.MkdirAll(directory, 0700); e != nil {
		return nil, "", e
	}
	cache, e := iyuu.OpenFileTorrentCache(directory)
	if e != nil {
		return nil, "", e
	}
	defer cache.Close()
	helper := iyuu.TorrentHelper{HTTP: seedRescueTorrentHTTP{transport, ref}, Cache: cache}
	result, e := helper.Download(ctx, address, false)
	if e != nil {
		return nil, "", e
	}
	return result.Content, result.ErrorMessage, nil
}

type seedRescueTorrentHTTP struct {
	t   *iyuu.NativeSiteHTTP
	ref string
}

func (s seedRescueTorrentHTTP) Get(ctx context.Context, url string) (*iyuu.TorrentResponse, error) {
	return s.t.SeedRescueRequest(ctx, s.ref, "GET", url, nil, nil, true, false)
}
func (s seedRescueTorrentHTTP) Post(ctx context.Context, url string, form map[string]string) (*iyuu.TorrentResponse, error) {
	return s.t.SeedRescueRequest(ctx, s.ref, "POST", url, form, nil, true, true)
}
func seedRescueXMLCharset(label string, r io.Reader) (io.Reader, error) {
	raw, e := io.ReadAll(io.LimitReader(r, 16<<20))
	if e != nil {
		return nil, e
	}
	text, e := encodingdetector.Decode(raw, label, true)
	if e != nil {
		return nil, e
	}
	return strings.NewReader(text), nil
}
