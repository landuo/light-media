package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/brushflow"
	"light-media/internal/sites"
)

type originalBrushSourceCandidate struct {
	site, siteKey, resource, enclosure string
	sourceID                           int64
}

type originalBrushSourceCall struct {
	mu         sync.Mutex
	candidates map[string]originalBrushSourceCandidate
}

func originalBrushSourceCapability(method string) bool {
	switch method {
	case plugins.BrushSourceCandidatesCapability, plugins.BrushSourceTransferCapability, plugins.BrushSourceAddCapability:
		return true
	}
	return false
}

func originalBrushSourceScope(ctx context.Context, scope originalBaseCallScope) bool {
	return scope.object != nil && scope.object.installation.PluginID == "BrushFlowLanduo" &&
		scope.object.originalSubmittedScopeAllowed(ctx)
}

func (a *App) originalBrushSourceResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalBrushSourceScope(ctx, scope) || scope.phase != "brush" || scope.brush == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "刷流资源须绑定实际刷流任务")
	}
	switch method {
	case plugins.BrushSourceCandidatesCapability:
		var request plugins.BrushCandidatesRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		return a.originalBrushSourceCandidates(ctx, scope, request)
	case plugins.BrushSourceTransferCapability:
		var request plugins.BrushTransferRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		return a.originalBrushSourceTransfer(ctx, scope, request)
	case plugins.BrushSourceAddCapability:
		var request plugins.BrushAddRequestV1
		if err := originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		if request.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "刷流添加需要绑定当前站点材料与任务")
		}
		return a.originalBrushSourceAdd(ctx, scope, request)
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知刷流资源")
}

func (a *App) originalBrushSourceCandidates(ctx context.Context, scope originalBaseCallScope, request plugins.BrushCandidatesRequestV1) (json.RawMessage, error) {
	ctx = allowDisabledSiteRequests(ctx)
	site, err := a.siteForRequest(ctx, request.SiteID)
	if err != nil {
		return nil, err
	}
	if site.Public {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "刷流仅支持已配置的私有站点")
	}
	identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	sourceID, err := a.Store.nativeSourcePublicSiteID(ctx, a.Store.DB, identity)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(site.BaseURL)
	if err != nil || base.Host == "" {
		return nil, siteIdentityError()
	}
	key := a.managedSiteKey(site)
	candidates := map[string]originalBrushSourceCandidate{}
	out := plugins.BrushCandidatesResultV1{SchemaVersion: 1, Candidates: []plugins.BrushCandidateV1{}}
	add := func(row brushflow.Row, resource, enclosure string) error {
		material := id("brush_")
		row["site"], row["site_name"] = sourceID, site.Name
		row["page_url"] = originalBrushSourcePage(base, sourceText(row["page_url"]))
		row["enclosure"], row["_brush_candidate"] = material, material
		originalBrushCandidateProperties(row, time.Now())
		fields, err := json.Marshal(row)
		if err != nil {
			return err
		}
		candidates[material] = originalBrushSourceCandidate{site: site.ID, siteKey: key, sourceID: sourceID, resource: resource, enclosure: enclosure}
		out.Candidates = append(out.Candidates, plugins.BrushCandidateV1{MaterialID: material, Fields: fields})
		return nil
	}
	if request.RSS {
		if site.RSS != "" {
			var body []byte
			var contentType string
			err = a.withSite(ctx, site, false, func(client *sites.Client) error {
				client.AddRequestGuard(scope.validate)
				var err error
				body, contentType, err = client.ConfiguredRSS(ctx, site.RSS)
				return err
			})
			if err != nil {
				return nil, err
			}
			text, err := brushflow.DecodeRSSXML(body, contentType)
			if err != nil {
				return nil, bad("SITE_RSS_INVALID", "站点RSS编码无法解析")
			}
			at := time.Now()
			rows, err := brushflow.ParseRustRSS(text, 1000, at.Location())
			if err != nil {
				rows, err = originalRSSRecoverRows(text, at)
			}
			if err != nil {
				return nil, bad("SITE_RSS_INVALID", "站点RSS内容无法解析")
			}
			for _, row := range rows {
				if err := add(brushflow.Row{"title": row["title"], "description": nil, "page_url": row["link"], "pubdate": row["pubdate"], "size": row["size"], "seeders": 0, "peers": 0, "downloadvolumefactor": nil, "uploadvolumefactor": nil, "hit_and_run": false, "freedate": nil}, "", sourceText(row["enclosure"])); err != nil {
					return nil, err
				}
			}
		}
	} else {
		rows, err := a.originalSiteTorrentModule(ctx, site, "refresh_torrents", map[string]any{"keyword": nil, "cat": nil, "page": 0}, 1, func() (sites.SearchResult, error) {
			var result sites.SearchResult
			err := a.withSite(ctx, site, false, func(client *sites.Client) error {
				client.AddRequestGuard(scope.validate)
				var err error
				result, err = client.Search(ctx, "", 1)
				return err
			})
			return result, err
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rows.Resources {
			if r.SiteID != site.ID {
				continue
			}
			published := r.PublishedAt
			if at, err := time.Parse(time.RFC3339Nano, published); err == nil {
				published = at.Format(time.DateTime)
			}
			var seeders, peers any
			if r.Seeders >= 0 {
				seeders = r.Seeders
			}
			if r.Leechers >= 0 {
				peers = r.Leechers
			}
			row := brushflow.Row{"title": r.Title, "description": r.Subtitle, "size": r.Size, "pubdate": published, "seeders": seeders, "peers": peers, "page_url": r.DetailsURL, "freedate": r.PromotionEnd, "downloadvolumefactor": r.DownloadFactor, "uploadvolumefactor": r.UploadFactor, "hit_and_run": r.HitAndRun}
			resource, enclosure := r.ID, ""
			if len(r.ModuleSourceFields) > 0 {
				var fields map[string]any
				if json.Unmarshal(r.ModuleSourceFields, &fields) != nil {
					return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "刷流站点资源字段无效")
				}
				for _, name := range []string{"description", "pubdate", "seeders", "peers", "page_url", "freedate", "downloadvolumefactor", "uploadvolumefactor", "hit_and_run"} {
					if value, exists := fields[name]; exists {
						row[name] = value
					}
				}
				if value, ok := fields["enclosure"].(string); ok && value != "" && !(site.Kind == "mteam" && strings.HasPrefix(value, "[") && resource != "") {
					resource, enclosure = "", value
				}
			}
			if err := add(row, resource, enclosure); err != nil {
				return nil, err
			}
		}
	}
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	encoded, err := originalHostDownloaderMarshal(out)
	if err != nil {
		return nil, err
	}
	scope.brush.mu.Lock()
	scope.brush.candidates = candidates
	scope.brush.mu.Unlock()
	return encoded, nil
}

var originalBrushDetailID = regexp.MustCompile(`^[0-9]{1,20}$`)

func originalBrushSourcePage(base *url.URL, raw string) string {
	if raw == "" || len(raw) > 8192 {
		return ""
	}
	page, err := url.Parse(raw)
	if err != nil || page.User != nil || page.Opaque != "" {
		return ""
	}
	page = base.ResolveReference(page)
	if page.Scheme != base.Scheme || !strings.EqualFold(page.Host, base.Host) {
		baseAPI, baseOK := originalBrushMTeamAPIHost(base)
		pageAPI, pageOK := originalBrushMTeamAPIHost(page)
		_, baseDomain, _ := strings.Cut(baseAPI, ".")
		_, pageDomain, _ := strings.Cut(pageAPI, ".")
		detailID, detail := strings.CutPrefix(page.Path, "/detail/")
		if !baseOK || !pageOK || baseDomain != pageDomain || !detail || !originalBrushDetailID.MatchString(detailID) {
			return ""
		}
	}
	query := page.Query()
	page.RawQuery, page.Fragment, page.RawFragment, page.ForceQuery = "", "", "", false
	if id := query.Get("id"); originalBrushDetailID.MatchString(id) {
		page.RawQuery = url.Values{"id": {id}}.Encode()
	}
	return page.String()
}

func originalBrushCandidateProperties(row brushflow.Row, at time.Time) {
	row["volume_factor"] = "未知"
	if row["uploadvolumefactor"] != nil && row["downloadvolumefactor"] != nil {
		key := fmt.Sprintf("%.2f %.2f", originalBrushNumber(row["uploadvolumefactor"]), originalBrushNumber(row["downloadvolumefactor"]))
		if value, ok := map[string]string{"1.00 1.00": "普通", "1.00 0.00": "免费", "2.00 1.00": "2X", "4.00 1.00": "4X", "2.00 0.00": "2X免费", "4.00 0.00": "4X免费", "1.00 0.50": "50%", "2.00 0.50": "2X 50%", "1.00 0.70": "70%", "1.00 0.30": "30%", "1.00 0.75": "75%", "1.00 0.25": "25%"}[key]; ok {
			row["volume_factor"] = value
		}
	}
	raw := sourceText(row["freedate"])
	row["freedate_diff"] = ""
	if raw == "" {
		return
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		raw = parsed.Format(time.DateTime)
		row["freedate"] = raw
	}
	deadline, err := time.ParseInLocation(time.DateTime, raw, at.Location())
	if err != nil {
		row["freedate_diff"] = raw
		return
	}
	total := int64(math.Floor(deadline.Sub(at).Seconds()))
	days := int64(math.Floor(float64(total) / 86400))
	seconds := total - days*86400
	hours, minutes := seconds/3600, (seconds%3600)/60
	if days > 0 {
		row["freedate_diff"] = fmt.Sprintf("%d天%d小时%d分钟", days, hours, minutes)
	} else if hours > 0 {
		row["freedate_diff"] = fmt.Sprintf("%d小时%d分钟", hours, minutes)
	} else if minutes > 0 {
		row["freedate_diff"] = fmt.Sprintf("%d分钟", minutes)
	}
}

func originalBrushNumber(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case json.Number:
		out, _ := value.Float64()
		return out
	case string:
		out, _ := strconv.ParseFloat(value, 64)
		return out
	}
	return 0
}

func (a *App) originalBrushSourceTransfer(ctx context.Context, scope originalBaseCallScope, request plugins.BrushTransferRequestV1) (json.RawMessage, error) {
	client, kind, guard, err := a.originalHostDownloaderBoundSourceClient(ctx, scope, request.DownloaderID, true, true)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	raw, err := client.BrushFlowTransfer(ctx)
	if guardErr := guard(ctx); guardErr != nil {
		return nil, guardErr
	}
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, bad("DOWNLOADER_INVALID_RESPONSE", "下载器传输统计无法解析")
	}
	out := plugins.BrushTransferResultV1{SchemaVersion: 1, DownloaderID: request.DownloaderID}
	if kind == "qbittorrent" {
		out.UploadSpeed, out.DownloadSpeed = originalBrushNumber(fields["up_info_speed"]), originalBrushNumber(fields["dl_info_speed"])
		out.UploadSize, out.DownloadSize = originalBrushNumber(fields["up_info_data"]), originalBrushNumber(fields["dl_info_data"])
	} else {
		out.UploadSpeed, out.DownloadSpeed = originalBrushNumber(originalBrushValue(fields, "upload_speed", "uploadSpeed")), originalBrushNumber(originalBrushValue(fields, "download_speed", "downloadSpeed"))
		stats, _ := originalBrushValue(fields, "cumulative_stats", "cumulative-stats").(map[string]any)
		out.UploadSize, out.DownloadSize = originalBrushNumber(originalBrushValue(stats, "uploadedBytes", "uploaded_bytes")), originalBrushNumber(originalBrushValue(stats, "downloadedBytes", "downloaded_bytes"))
	}
	return originalHostDownloaderMarshal(out)
}

func originalBrushValue(fields map[string]any, name, fallback string) any {
	if value := fields[name]; value != nil {
		return value
	}
	return fields[fallback]
}
