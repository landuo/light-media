package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"light-media/internal/plugins"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	sr "light-media/internal/plugins/seedrescue"
)

func (a *App) originalSeedRescueIYUUResource(ctx context.Context, scope originalBaseCallScope, operation string, raw json.RawMessage) (json.RawMessage, error) {
	var value any
	switch operation {
	case "iyuu":
		var request struct{}
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		object, loaded := a.pluginRunningInstances.Load("IYUUAutoSeed")
		enabled := false
		if loaded {
			var err error
			enabled, err = object.(*originalPluginInstance).getState(ctx)
			if err != nil {
				enabled = false
			}
		}
		value = sr.Object{"available": loaded, "enabled": enabled}
	case "seed_info":
		var request struct {
			Hashes []string `json:"hashes"`
		}
		if err := originalResourceDecodeBounded(raw, &request, 1<<20); err != nil {
			return nil, err
		}
		if len(request.Hashes) == 0 || len(request.Hashes) > 10000 {
			return nil, bad("INVALID_PLUGIN_VALUE", "IYUU 查询需要有限且明确的种子哈希")
		}
		for _, hash := range request.Hashes {
			if len(hash) != 40 || !plugins.DownloaderTaskHashValid(hash) {
				return nil, bad("INVALID_PLUGIN_VALUE", "IYUU 查询必须使用准确种子哈希")
			}
		}
		helper, err := a.originalSeedRescueIYUUHelper(ctx, scope)
		if err != nil {
			return nil, err
		}
		result, err := helper.SeedInfo(ctx, request.Hashes)
		if err != nil {
			return nil, err
		}
		var payload any
		message := ""
		switch {
		case result.Failure == nil:
			payload = originalSeedRescueIYUUMatches(result.Data, request.Hashes)
		case iyuu.SourceNoReseedData(result.Failure):
			message = "请求IYUU失败，状态码：404，未查询到可辅种数据"
		default:
			message = "请求IYUU失败 (" + result.Failure.Kind + ")"
		}
		value = sr.Object{"payload": payload, "message": message}
	case "torrent_url":
		var request struct {
			SID json.RawMessage `json:"sid"`
		}
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		sid, err := decodeSourceJSON(request.SID)
		if err != nil || !originalSeedRescueIYUUScalar(sid) {
			return nil, bad("INVALID_PLUGIN_VALUE", "IYUU 站点编号无效")
		}
		helper, err := a.originalSeedRescueIYUUHelper(ctx, scope)
		if err != nil {
			return nil, err
		}
		rawBase, rawPage, err := helper.TorrentURL(ctx, sid)
		if err != nil {
			return nil, err
		}
		base, _ := rawBase.(string)
		page, _ := rawPage.(string)
		base, page = originalSeedRescueIYUUSiteURL(base, page)
		value = sr.Object{"url": base, "page": page}
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知 IYUU 稀缺保种资源")
	}
	if err := originalSeedRescueValidate(ctx, scope); err != nil {
		return nil, err
	}
	return originalHostDownloaderMarshal(value)
}

func originalSeedRescueIYUUScalar(value any) bool {
	var text string
	switch item := value.(type) {
	case string:
		text = item
	case json.Number:
		text = item.String()
	default:
		return false
	}
	return len(text) > 0 && len(text) < 32 && strings.Trim(text, "0123456789") == ""
}

func originalSeedRescueIYUUMatches(value any, hashes []string) any {
	if _, ok := value.(map[string]any); !ok {
		return nil
	}
	out := sr.Object{}
	for _, hash := range hashes {
		matches := []sr.Object{}
		for _, match := range sr.MatchesForHash(value, hash) {
			if originalSeedRescueIYUUScalar(match.SID) && originalSeedRescueIYUUScalar(match.TorrentID) && plugins.DownloaderTaskHashValid(match.InfoHash) {
				matches = append(matches, sr.Object{"sid": match.SID, "torrent_id": match.TorrentID, "info_hash": match.InfoHash})
			}
		}
		if len(matches) != 0 {
			out[hash] = sr.Object{"torrent": matches}
		}
	}
	return out
}

func originalSeedRescueIYUUSiteURL(base, page string) (string, string) {
	base = sr.SafePageURL(base)
	if base == "" || page == "" {
		return "", ""
	}
	origin, err := url.Parse(base)
	if err != nil {
		return "", ""
	}
	// Matching needs the public domain and the presence of the page template.
	// It never fetches an IYUU address or needs its query credentials.
	origin.RawQuery, origin.Fragment = "", ""
	template, err := url.Parse(page)
	if err != nil || template.User != nil || template.Opaque != "" {
		return "", ""
	}
	if template.Host != "" {
		template.Host, template.Scheme = "", ""
	}
	template.RawQuery, template.Fragment = "", ""
	template.ForceQuery = strings.Contains(page, "download.php?")
	page = template.String()
	if page == "" {
		page = "/"
	}
	return origin.String(), page
}

func (a *App) originalSeedRescueIYUUHelper(ctx context.Context, scope originalBaseCallScope) (*iyuu.Helper, error) {
	if err := originalSeedRescueValidate(ctx, scope); err != nil {
		return nil, err
	}
	call := scope.seedRescue
	if call.config.Token == "" {
		return nil, bad("SEED_RESCUE_IYUU_UNBOUND", "稀缺保种尚未配置 IYUU")
	}
	call.iyuuMu.Lock()
	defer call.iyuuMu.Unlock()
	if call.iyuuHelper == nil {
		helper, err := iyuu.NewHelper(originalSeedRescueIYUUAPI{app: a, scope: scope}, time.Now)
		if err != nil {
			return nil, err
		}
		call.iyuuHelper = helper
	}
	return call.iyuuHelper, nil
}

type originalSeedRescueIYUUAPI struct {
	app   *App
	scope originalBaseCallScope
}

func (s originalSeedRescueIYUUAPI) Call(ctx context.Context, request iyuu.APIRequest) (*iyuu.APIResponse, error) {
	guard := func(ctx context.Context) error { return originalSeedRescueValidate(ctx, s.scope) }
	if err := guard(ctx); err != nil {
		return nil, err
	}
	call := s.scope.seedRescue
	transport, err := iyuu.NewAPITransport(iyuu.TrustedAPIBinding{Token: call.config.Token},
		iyuu.APITransportOptions{Guard: guard, LoopbackOrigin: call.iyuuOrigin})
	if err != nil {
		return nil, err
	}
	defer transport.Close()
	if request.Operation != iyuu.ReportExisting {
		return transport.Call(ctx, request)
	}
	params, err := json.Marshal(request.Params)
	if err != nil {
		return nil, err
	}
	effectKey := tokenHash(tokenHash(call.config.Token) + "\x00" + string(params))
	finish, err := s.app.beginOriginalSeedRescueEffect(ctx, s.scope, "iyuu_report_existing", effectKey)
	if err != nil {
		return nil, err
	}
	response, err := transport.Call(ctx, request)
	if err != nil {
		return nil, err
	}
	if err = finish(true); err != nil {
		return nil, err
	}
	return response, nil
}
