package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

// The chat chain calls the synchronous source module and retains the whole
// ordered result. The interaction owns pagination over that result.
func (a *App) originalChatSearchMedia(ctx context.Context, keyword string) (*releasemeta.MetaObject, []catalog.ExploreMedia, error) {
	meta, err := a.originalCatalogSearchMeta(ctx, keyword, "")
	if err != nil {
		return nil, nil, err
	}
	fields, err := meta.ToDict()
	if err != nil {
		return meta, nil, err
	}
	result, err := a.originalCatalogFeed(ctx, "search_medias", map[string]any{"meta": fields, "source": nil}, 1, func() (catalog.ExplorePage, error) {
		return a.originalChatNativeMedia(ctx, meta)
	})
	if ctx.Err() != nil {
		return meta, nil, ctx.Err()
	}
	return meta, result.Results, err
}

func (a *App) originalChatNativeMedia(ctx context.Context, meta *releasemeta.MetaObject) (catalog.ExplorePage, error) {
	out := catalog.ExplorePage{Results: []catalog.ExploreMedia{}, Page: 1}
	configured := a.Settings().SearchSource
	for _, provider := range []struct {
		source, method string
		search         func(context.Context, *releasemeta.MetaObject) (catalog.ExplorePage, error)
	}{
		{"themoviedb", "tmdb", a.originalChatTMDBMedia},
		{"douban", "douban", a.originalChatDoubanMedia},
		{"bangumi", "bangumi", a.originalChatBangumiMedia},
		{"anilist", "anilist", a.originalChatAniListMedia},
	} {
		if configured == nil && provider.source != "themoviedb" ||
			configured != nil && *configured != "" && !strings.Contains(*configured, provider.source) {
			continue
		}
		page, err := provider.search(ctx, meta)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			name := originalCatalogSystemModuleName(provider.method)
			a.reportOriginalPluginExecutionError(ctx, "system", name+"发生了错误", map[string]any{
				"type": "module", "module_id": originalCatalogSystemModuleID(provider.method), "module_name": name, "module_method": "search_medias",
			}, err)
			continue
		}
		out.Results = append(out.Results, page.Results...)
	}
	out.TotalResults = len(out.Results)
	return out, nil
}

func (a *App) originalChatTMDBMedia(ctx context.Context, meta *releasemeta.MetaObject) (catalog.ExplorePage, error) {
	result := catalog.ExplorePage{Results: []catalog.ExploreMedia{}, Page: 1}
	settings := a.Settings()
	if meta.Name() == "" {
		return result, nil
	}
	kind := map[string]string{"电影": "movie", "电视剧": "tv"}[meta.Text("type")]
	year := sourceStringValue(meta.Get("year"))
	var rows []json.RawMessage
	_, err := a.cached(ctx, "source-search:"+kind+":"+meta.Name()+":"+year, time.Hour, &rows, func() (any, error) {
		return a.catalog().SourceSearchRows(ctx, meta.Name(), kind, year)
	})
	if err != nil || len(rows) == 0 {
		return result, err
	}
	season := meta.Number("begin_season")
	var seasonLabel string
	if season != nil {
		seasonLabel, err = releasemeta.SearchSeasonChinese(season)
		if err != nil {
			return result, err
		}
	}
	for _, row := range rows {
		value, err := decodeSourceJSON(row)
		if err != nil {
			return result, err
		}
		rowKind := "movie"
		if sourceMap(value)["media_type"] == "电视剧" {
			rowKind = "tv"
		}
		live, err := sourceTMDBMediaModel(row, rowKind, settings.TMDBImageDomain, "")
		if err != nil {
			return result, err
		}
		public, err := originalPublicCatalogMedia(live)
		if err != nil {
			return result, err
		}
		media, err := originalCatalogMediaProjection(public)
		if err != nil {
			return result, err
		}
		if season != nil && media.MediaType == "tv" {
			media.Title += " 第" + seasonLabel + "季"
		}
		result.Results = append(result.Results, media)
	}
	return result, nil
}
