package app

import (
	"context"
	"encoding/json"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

// DoubanModule.search_medias uses the signed Frodo search envelope. Its
// result builder consumes the embedded target without a second detail query.
func (a *App) originalChatDoubanMedia(ctx context.Context, meta *releasemeta.MetaObject) (catalog.ExplorePage, error) {
	result := catalog.ExplorePage{Results: []catalog.ExploreMedia{}, Page: 1, TotalPages: 1}
	source := a.Settings().SearchSource
	name := meta.Name()
	if name == "" || source == nil || *source != "" && !strings.Contains(*source, "douban") {
		return result, nil
	}
	proxy, err := a.proxyConfig()
	if err != nil {
		return result, err
	}
	transport, err := proxy.Transport(nil)
	if err != nil {
		return result, err
	}
	explorer := catalog.NewExplorer(a.catalog(), transport)
	defer explorer.CloseIdleConnections()
	stamp := a.originalHostDouban.searchStamp
	response, err := a.originalHostMediaProviderJSON(ctx, "douban", "get", []any{"/search/weixin", name, 0, 20, stamp}, false, func() (json.RawMessage, error) {
		return explorer.SourceDoubanSearch(ctx, name, stamp)
	})
	if err != nil {
		return result, err
	}
	fields, err := originalHostMediaSourceMapping(response, true)
	if err != nil {
		return result, err
	}
	items, err := originalHostMediaSourceSequence(fields["items"])
	if err != nil || len(items) == 0 {
		return result, err
	}
	parser, err := a.originalHostMediaSourceParser(ctx)
	if err != nil {
		return result, err
	}
	kind, season := meta.Text("type"), meta.Number("begin_season")
	for _, item := range items {
		row, err := originalHostMediaSourceMapping(item, false)
		if err != nil {
			return result, err
		}
		typ := sourceText(row["type_name"])
		if (typ != "电影" && typ != "电视剧") || (kind != "" && kind != "未知" && typ != kind) {
			continue
		}
		target, err := originalHostMediaSourceMapping(row["target"], true)
		if err != nil {
			return result, err
		}
		if !strings.Contains(sourceText(target["title"]), name) {
			continue
		}
		raw, err := originalHostMediaDoubanModel(target, parser)
		if err != nil {
			return result, err
		}
		value, err := decodeSourceJSON(raw)
		if err != nil {
			return result, err
		}
		media := sourceMap(value)
		if season != nil && media["type"] == "电视剧" {
			label, err := releasemeta.SearchSeasonChinese(season)
			if err != nil {
				return result, err
			}
			media["title"] = sourceText(media["title"]) + " 第" + label + "季"
			media["season"] = season
			raw, err = originalHostMediaSourceSerialize(media)
			if err != nil {
				return result, err
			}
		}
		view, err := originalCatalogMediaProjection(raw)
		if err != nil {
			return result, err
		}
		view.PosterPath = catalog.ExploreImageURL("douban", view.PosterPath)
		view.BackdropPath = catalog.ExploreImageURL("douban", view.BackdropPath)
		result.Results = append(result.Results, view)
	}
	result.TotalResults = len(result.Results)
	return result, nil
}
