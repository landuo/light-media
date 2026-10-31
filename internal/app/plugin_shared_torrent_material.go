package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/networkproxy"
	"light-media/internal/ocr"
	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/plugins/iyuuautoseed/compat/urlprep"
	"light-media/internal/plugins/torrentmaterial"
	"light-media/internal/rsscontext"
	"light-media/internal/sourcejson"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

// This is the App's private persistent source-helper cache, not a worker path.
// Its in-memory invalid URL region lives for the App lifetime, not one message.
type originalTorrentMaterialCache struct {
	mu     sync.Mutex
	cache  *iyuuautoseed.FileTorrentCache
	closed bool
}

func (a *App) originalMaterialCache(ctx context.Context) (*iyuuautoseed.FileTorrentCache, error) {
	state := &a.pluginOriginalTorrentMaterial
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if state.closed {
		return nil, bad("APP_STOPPING", "种子材料缓存已关闭")
	}
	if state.cache != nil {
		return state.cache, nil
	}
	directory := filepath.Join(a.Dir, "cache", "plugin-torrent-material")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	cache, err := iyuuautoseed.OpenFileTorrentCache(directory)
	if err != nil {
		return nil, err
	}
	state.cache = cache
	return cache, nil
}
func (a *App) closeOriginalMaterialCache() {
	state := &a.pluginOriginalTorrentMaterial
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	state.closed = true
	if state.cache != nil {
		_ = state.cache.Close()
		state.cache = nil
	}
}

var errOriginalMaterialRequestRejected = errors.New("material request preparation rejected")

func originalSharedTorrentMaterialCapability(method string) bool {
	return method == plugins.DownloadTorrentCapability
}
func originalMaterialSourceError(err error) string {
	var wrapper *torrentmaterial.SourceError
	if errors.As(err, &wrapper) {
		return wrapper.Class
	}
	var parsed *ocr.SourceError
	if errors.As(err, &parsed) {
		return parsed.Class
	}
	var transport *autosignin.OrdinaryMaterialSourceError
	if errors.As(err, &transport) {
		return transport.Class
	}
	var helper *iyuuautoseed.SourceFault
	if errors.As(err, &helper) {
		return helper.Class
	}
	if errors.Is(err, networkproxy.ErrRequestsProxyAuthEncoding) {
		return "UnicodeEncodeError"
	}
	return ""
}
func (a *App) originalSharedTorrentMaterialResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	if scope.validate == nil || scope.torrentTransfers == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "种子材料缺少实际调用范围")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "种子材料参数编码无效")
	}
	var request struct {
		Op     string                          `json:"op"`
		Input  *plugins.DownloadTorrentRequest `json:"input,omitempty"`
		Handle *string                         `json:"handle,omitempty"`
		Offset *int                            `json:"offset,omitempty"`
	}
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	marshal := func(value any, allocated string) (json.RawMessage, error) {
		out, err := json.Marshal(value)
		if err == nil && len(out) > plugins.MaxPayloadBytes {
			err = bad("PLUGIN_VALUE_TOO_LARGE", "种子材料结果超过已有传输上限")
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = scope.validate(ctx)
		}
		if err != nil && allocated != "" {
			_ = scope.torrentTransfers.release(context.Background(), allocated)
		}
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	switch request.Op {
	case "read":
		if request.Input != nil || request.Handle == nil || request.Offset == nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "种子材料读取参数无效")
		}
		chunk, err := scope.torrentTransfers.read(ctx, *request.Handle, *request.Offset)
		if err != nil {
			return nil, err
		}
		return marshal(chunk, "")
	case "release":
		if request.Input != nil || request.Handle == nil || request.Offset != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "种子材料释放参数无效")
		}
		if err := scope.torrentTransfers.release(ctx, *request.Handle); err != nil {
			return nil, err
		}
		return marshal(map[string]bool{"released": true}, "")
	case "prepare":
		if request.Input == nil || request.Handle != nil || request.Offset != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "种子材料获取参数无效")
		}
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "未知种子材料操作")
	}
	if !a.admitOriginalBackground() {
		return nil, bad("APP_STOPPING", "服务正在停止")
	}
	defer a.wg.Done()
	result, err := a.originalDownloadTorrentMaterial(ctx, scope, *request.Input)
	if guardErr := scope.validate(ctx); guardErr != nil {
		return nil, guardErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if class := originalMaterialSourceError(err); class != "" {
			return marshal(map[string]string{"kind": "source_error", "source_error": class}, "")
		}
		return nil, err
	}
	fields := map[string]any{"kind": "none", "folder": result.Folder, "files": append([]string{}, result.Files...)}
	if result.Content == nil {
		return marshal(fields, "")
	}
	switch result.Content.Kind {
	case "string":
		fields["kind"] = "string"
		fields["text"] = result.Content.String
		return marshal(fields, "")
	case "bytes":
		// The byte owner refuses a phase closed during asynchronous acquisition;
		// a second validate also prevents returning stale invocation results.
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		prepared, err := scope.torrentTransfers.publish(ctx, result.Content.Bytes)
		if err != nil {
			return nil, err
		}
		fields["kind"] = "bytes"
		fields["handle"] = prepared.Handle
		fields["total"] = prepared.Total
		fields["sha256"] = prepared.SHA256
		return marshal(fields, prepared.Handle)
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "种子材料内容类型无效")
	}
}

func originalMaterialText(value any) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, bad("INVALID_PLUGIN_VALUE", "种子材料文本属性无效")
	}
	return &text, nil
}
func originalMaterialDisplay(raw map[string]json.RawMessage, name string) (*string, error) {
	v, ok := raw[name]
	if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return nil, nil
	}
	text, err := sourcejson.String(v)
	if err != nil {
		return nil, err
	}
	return &text, nil
}
func (a *App) originalDownloadTorrentMaterial(ctx context.Context, scope originalBaseCallScope, input plugins.DownloadTorrentRequest) (plugins.TorrentMaterialResult, error) {
	var rawFields map[string]json.RawMessage
	if json.Unmarshal(input.Torrent, &rawFields) != nil || rawFields == nil {
		return plugins.TorrentMaterialResult{}, bad("INVALID_PLUGIN_VALUE", "种子材料需要原TorrentInfo对象")
	}
	value, err := decodeSourceJSON(input.Torrent)
	if err != nil {
		return plugins.TorrentMaterialResult{}, err
	}
	model, err := rsscontext.NewTorrentInfo(nil)
	if err != nil {
		return plugins.TorrentMaterialResult{}, err
	}
	model.FromDict(sourceMap(value))
	// The source checks enclosure before reading any other TorrentInfo field.
	// Falsey non-text JSON also takes that early None branch; truthy non-text
	// reaches .startswith and raises AttributeError, not annotation validation.
	enclosureValue := model.Get("enclosure")
	if !autosignin.Truth(enclosureValue) {
		return plugins.TorrentMaterialResult{Files: []string{}}, nil
	}
	enclosure, isText := enclosureValue.(string)
	if !isText {
		return plugins.TorrentMaterialResult{}, &torrentmaterial.SourceError{Class: "AttributeError"}
	}
	if strings.HasPrefix(enclosure, "magnet:") {
		return plugins.TorrentMaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "string", String: enclosure}, Files: []string{}}, nil
	}
	torrent := torrentmaterial.Torrent{}
	if torrent.Enclosure, err = originalMaterialText(model.Get("enclosure")); err != nil {
		return plugins.TorrentMaterialResult{}, err
	}
	if torrent.SiteCookie, err = originalMaterialText(model.Get("site_cookie")); err != nil {
		return plugins.TorrentMaterialResult{}, err
	}
	if torrent.SiteUA, err = originalMaterialText(model.Get("site_ua")); err != nil {
		return plugins.TorrentMaterialResult{}, err
	}
	if torrent.Title, err = originalMaterialDisplay(rawFields, "title"); err != nil {
		return plugins.TorrentMaterialResult{}, err
	}
	if torrent.SiteName, err = originalMaterialDisplay(rawFields, "site_name"); err != nil {
		return plugins.TorrentMaterialResult{}, err
	}
	torrent.SiteProxy = autosignin.Truth(model.Get("site_proxy"))
	grant := &originalMaterialGrant{app: a, scope: scope}
	a.cfgMu.RLock()
	grant.proxyHost = a.config.ProxyHost
	a.cfgMu.RUnlock()
	grant.sourceSite = model.Get("site")
	chain := torrentmaterial.Chain{UserAgent: originalPluginServerUserAgent()}
	chain.Log = func(logCtx context.Context, level, message string) {
		a.logOriginalPluginContext(logCtx, level, "torrent_material", message)
	}
	chain.Request = func(requestCtx context.Context, request torrentmaterial.DescriptorRequest) (*torrentmaterial.DescriptorResponse, error) {
		client, err := grant.bind(requestCtx, request.URL, request.UserAgent, request.Cookie, request.Proxy, false)
		if errors.Is(err, errOriginalMaterialRequestRejected) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		response, err := client.DoOrdinaryMaterial(requestCtx, autosignin.OrdinaryMaterialRequest{Method: request.Method, URL: request.URL, Params: request.Params, Headers: request.Headers, AllowRedirects: true})
		if check := grant.validate(requestCtx); check != nil {
			return nil, check
		}
		if err != nil || response == nil {
			return nil, err
		}
		return &torrentmaterial.DescriptorResponse{Status: response.Status, Text: response.Text, TextError: response.TextError, Body: response.Content, ContentType: response.ContentType}, nil
	}
	chain.Material = func(materialCtx context.Context, request torrentmaterial.MaterialRequest) (torrentmaterial.MaterialResult, error) {
		if strings.HasPrefix(request.URL, "magnet:") {
			out, err := (iyuuautoseed.TorrentHelper{}).Download(materialCtx, request.URL, request.CacheInvalid)
			return torrentmaterial.FromHelper(out), err
		}
		ua := request.UserAgent
		if _, err := grant.bind(materialCtx, request.URL, &ua, request.Cookie, request.Proxy, !request.CacheInvalid); err != nil && !errors.Is(err, errOriginalMaterialRequestRejected) {
			return torrentmaterial.MaterialResult{}, err
		}
		cache, err := a.originalMaterialCache(materialCtx)
		if err != nil {
			return torrentmaterial.MaterialResult{}, err
		}
		adapter := &originalMaterialHTTP{grant: grant, userAgent: &ua, cookie: request.Cookie, proxy: request.Proxy}
		out, err := (iyuuautoseed.TorrentHelper{HTTP: adapter, Cache: cache}).Download(materialCtx, request.URL, request.CacheInvalid)
		if check := grant.validate(materialCtx); check != nil {
			return torrentmaterial.MaterialResult{}, check
		}
		return torrentmaterial.FromHelper(out), err
	}
	chain.Notify = func(noticeCtx context.Context, message torrentmaterial.Notification) error {
		if err := scope.validate(noticeCtx); err != nil {
			return err
		}
		var user any
		if len(message.UserID) > 0 {
			var err error
			user, err = decodeSourceJSON(message.UserID)
			if err != nil {
				return err
			}
		}
		nullable := func(v *string) any {
			if v == nil {
				return nil
			}
			return *v
		}
		_, err := a.postOriginalPluginMessage(noticeCtx, map[string]any{"channel": nullable(message.Channel), "source": nullable(message.Source), "mtype": message.Type, "title": message.Title, "text": message.Text, "userid": user})
		return err
	}
	out, err := chain.DownloadTorrent(ctx, torrent, torrentmaterial.MessageContext{Channel: input.Channel, Source: input.Source, UserID: input.UserID})
	if check := grant.validate(ctx); check != nil {
		return plugins.TorrentMaterialResult{}, check
	}
	return out, err
}

type originalMaterialGrant struct {
	app                       *App
	scope                     originalBaseCallScope
	siteID, origin, proxyHost string
	sourceSite                any
	site                      *SiteConfig
}

func (g *originalMaterialGrant) validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := g.scope.validate(ctx); err != nil {
		return err
	}
	g.app.cfgMu.RLock()
	defer g.app.cfgMu.RUnlock()
	if g.app.config.ProxyHost != g.proxyHost {
		return bad("PLUGIN_SNAPSHOT_STALE", "种子材料代理配置已变化")
	}
	if g.site == nil {
		return nil
	}
	for _, site := range g.app.config.Sites {
		if site.ID == g.site.ID && site == *g.site {
			return nil
		}
	}
	return bad("PLUGIN_SNAPSHOT_STALE", "种子材料站点配置已变化")
}
func (g *originalMaterialGrant) bind(ctx context.Context, address string, ua, cookie *string, useProxy, allowLineage bool) (*autosignin.BoundSiteHTTP, error) {
	if err := g.validate(ctx); err != nil {
		return nil, err
	}
	prepared, err := urlprep.Prepare(address)
	if err != nil {
		switch urlprep.ErrorClass(err) {
		case "UnicodeEncodeError", "UnicodeDecodeError", "ValueError":
			return nil, &torrentmaterial.SourceError{Class: urlprep.ErrorClass(err)}
		}
		return nil, errOriginalMaterialRequestRejected
	}
	parsed, parseErr := url.Parse(prepared)
	if parseErr != nil {
		return nil, errOriginalMaterialRequestRejected
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errOriginalMaterialRequestRejected
	}
	origin, err := originalOCROrigin(prepared)
	if err != nil {
		return nil, err
	}
	if g.site == nil {
		if g.sourceSite != nil {
			id, err := originalResourceInteger(g.sourceSite)
			if err != nil {
				return nil, bad("INVALID_PLUGIN_VALUE", "种子站点编号无效")
			}
			if id != 0 {
				identity, err := g.app.Store.nativeSiteByID(ctx, id)
				if err != nil {
					return nil, err
				}
				g.siteID = identity.SiteID
			}
		}
		g.app.cfgMu.RLock()
		for _, site := range g.app.config.Sites {
			current, e := originalOCROrigin(site.BaseURL)
			if e == nil && current == origin && (g.siteID == "" || g.siteID == site.ID) {
				if g.site != nil {
					g.app.cfgMu.RUnlock()
					return nil, bad("PLUGIN_SITE_AMBIGUOUS", "种子材料站点授权不唯一")
				}
				copySite := site
				g.site = &copySite
				g.origin = current
			}
		}
		g.app.cfgMu.RUnlock()
		if g.site == nil {
			return nil, bad("PLUGIN_SITE_DENIED", "种子材料网络请求需要当前已配置站点")
		}
	} else if origin != g.origin && !allowLineage {
		return nil, bad("PLUGIN_SITE_DENIED", "种子材料请求超出站点授权")
	}
	if err := g.validate(ctx); err != nil {
		return nil, err
	}
	cookieText := ""
	if cookie != nil {
		cookieText = *cookie
	}
	if origin != g.origin {
		cookieText = ""

	}
	if err := originalOCRCookieEncoding(cookieText); err != nil {
		return nil, err
	}
	var userAgent any
	if ua != nil {
		agent := *ua
		if agent == originalPluginServerUserAgent() {
			agent += " Plugin/" + plugins.OriginalPluginLower(g.scope.installation.PluginID)
		}
		userAgent = agent
	}
	policy := &autosignin.RequestsProxyPolicy{Environment: os.Environ()}
	if useProxy {
		config, err := networkproxy.Resolve(g.proxyHost, os.Getenv)
		if err != nil {
			return nil, err
		}
		policy.Explicit = map[string]string{}
		if config.HTTP != "" {
			policy.Explicit["http"] = config.HTTP
		}
		if config.HTTPS != "" {
			policy.Explicit["https"] = config.HTTPS
		}
	}
	return autosignin.BindSiteHTTP(autosignin.SiteHTTPBinding{URL: prepared, Cookie: cookieText, UserAgent: userAgent, RequestsProxy: policy, BeforeRequest: g.validate})
}

type originalMaterialHTTP struct {
	grant             *originalMaterialGrant
	userAgent, cookie *string
	proxy             bool
}

func (h *originalMaterialHTTP) Get(ctx context.Context, address string) (*iyuuautoseed.TorrentResponse, error) {
	return h.request(ctx, "GET", address, nil, nil, false)
}
func (h *originalMaterialHTTP) Post(ctx context.Context, address string, form map[string]string) (*iyuuautoseed.TorrentResponse, error) {
	return h.request(ctx, "POST", address, form, nil, true)
}
func (h *originalMaterialHTTP) PostOrdered(ctx context.Context, address string, form map[string]string, order []string) (*iyuuautoseed.TorrentResponse, error) {
	return h.request(ctx, "POST", address, form, order, true)
}
func (h *originalMaterialHTTP) request(ctx context.Context, method, address string, form map[string]string, order []string, redirects bool) (*iyuuautoseed.TorrentResponse, error) {
	client, err := h.grant.bind(ctx, address, h.userAgent, h.cookie, h.proxy, true)
	if errors.Is(err, errOriginalMaterialRequestRejected) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	response, err := client.DoOrdinaryMaterial(ctx, autosignin.OrdinaryMaterialRequest{Method: method, URL: address, Params: plugins.OCRValue{Kind: "null"}, Headers: plugins.OCRValue{Kind: "null"}, Form: form, FormOrder: order, AllowRedirects: redirects})
	if check := h.grant.validate(ctx); check != nil {
		return nil, check
	}
	if err != nil || response == nil {
		return nil, err
	}
	// Text decoding failure matters only to helper branches that actually read
	// response.text; ordinary nonempty binary content must remain available.
	if method == "GET" && response.Status == 200 && response.TextError != nil && (bytes.HasPrefix(response.Content, []byte("magnet:")) || bytes.Contains(response.Content, []byte("下载种子文件"))) {
		return nil, response.TextError
	}
	return &iyuuautoseed.TorrentResponse{Status: response.Status, Content: response.Content, Text: response.Text, Headers: response.Headers}, nil
}
