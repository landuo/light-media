package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

// The native catalog retains its existing provider pagination. Only the raw
// source alias uses the source's first-provider-page/all-module-list slice.
func (a *App) originalCatalogSearch(ctx context.Context, title, kind, source string, page, count int) (catalog.ExplorePage, error) {
	meta, err := a.originalCatalogSearchMeta(ctx, title, kind)
	if err != nil {
		return catalog.ExplorePage{}, err
	}
	fields, err := meta.ToDict()
	if err != nil {
		return catalog.ExplorePage{}, err
	}
	var requested any
	if source != "" {
		requested = source
	}
	result, err := a.originalCatalogFeed(ctx, "async_search_medias", map[string]any{"meta": fields, "source": requested}, page, func() (catalog.ExplorePage, error) {
		var core catalog.Page
		_, err := a.cached(ctx, "search:"+kind+":"+title+":"+fmt.Sprint(page), time.Hour, &core, func() (any, error) { return a.catalog().Search(ctx, title, kind, page) })
		rows := make([]catalog.ExploreMedia, len(core.Results))
		for i, media := range core.Results {
			rows[i] = catalog.ExploreMedia{Media: media, Source: "themoviedb", SourceID: strconv.FormatInt(media.ID, 10)}
		}
		return catalog.ExplorePage{Results: rows, Page: core.Page, TotalPages: core.TotalPages, TotalResults: core.TotalResults, HasMore: core.Page < core.TotalPages}, err
	})
	if err != nil {
		return result, err
	}
	models := make([]catalog.Media, len(result.Results))
	for i, media := range result.Results {
		models[i] = media.Media
	}
	if err = a.decorateMedia(ctx, models); err != nil {
		return result, err
	}
	for i, media := range models {
		result.Results[i].Media = media
	}
	return result, nil
}

func (a *App) originalCatalogSearchMeta(ctx context.Context, title, kind string) (*releasemeta.MetaObject, error) {
	keyword, err := releasemeta.SearchInputKeyword(title)
	if err != nil {
		return nil, err
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return nil, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	meta, err := parser.ParseTitle(keyword.Content, nil, nil)
	if err != nil {
		return nil, err
	}
	if meta.Name() == "" {
		meta.Set("cn_name", keyword.Content)
	}
	if keyword.Type != "" {
		meta.Set("type", keyword.Type)
	}
	if keyword.Season != nil && keyword.Season.Sign() != 0 {
		meta.Set("begin_season", keyword.Season)
	}
	if keyword.Episode != nil && keyword.Episode.Sign() != 0 {
		meta.Set("begin_episode", keyword.Episode)
	}
	if keyword.Year != "" {
		meta.Set("year", keyword.Year)
	}
	// Existing native type chips are a request-level search selection.
	if kind == "movie" {
		meta.Set("type", "电影")
	} else if kind == "tv" {
		meta.Set("type", "电视剧")
	}
	return meta, nil
}

func (a *App) originalCatalogSourceSearch(ctx context.Context, title, kind, source string, page, count int) (catalog.ExplorePage, error) {
	meta, err := a.originalCatalogSearchMeta(ctx, title, kind)
	if err != nil {
		return catalog.ExplorePage{}, err
	}
	fields, err := meta.ToDict()
	if err != nil {
		return catalog.ExplorePage{}, err
	}
	var requestedSource any
	if source != "" {
		requestedSource = source
	}
	result, err := a.originalCatalogFeed(ctx, "async_search_medias", map[string]any{"meta": fields, "source": requestedSource}, 1, func() (catalog.ExplorePage, error) {
		empty := catalog.ExplorePage{Results: []catalog.ExploreMedia{}, Page: 1}
		if source != "" && source != "themoviedb" {
			return empty, nil
		}
		selection := a.Settings().SearchSource
		if selection == nil {
			value := "themoviedb"
			selection = &value
		}
		if source == "" && *selection != "" && !strings.Contains(*selection, "themoviedb") {
			return empty, nil
		}
		mediaKind := ""
		if meta.Get("type") == "电影" {
			mediaKind = "movie"
		} else if meta.Get("type") == "电视剧" {
			mediaKind = "tv"
		}
		var rows []json.RawMessage
		_, e := a.cached(ctx, "source-search:"+mediaKind+":"+meta.Name()+":"+sourceStringValue(meta.Get("year")), time.Hour, &rows, func() (any, error) {
			return a.catalog().SourceSearchRows(ctx, meta.Name(), mediaKind, sourceStringValue(meta.Get("year")))
		})
		if e != nil {
			return empty, e
		}
		for _, row := range rows {
			value, e := decodeSourceJSON(row)
			if e != nil {
				return empty, e
			}
			kind := "movie"
			if sourceMap(value)["media_type"] == "电视剧" {
				kind = "tv"
			}
			live, e := sourceTMDBMediaModel(row, kind, a.Settings().TMDBImageDomain, "")
			if e != nil {
				return empty, e
			}
			if number := meta.Number("begin_season"); number != nil && kind == "tv" {
				value, e = decodeSourceJSON(live)
				if e != nil {
					return empty, e
				}
				fields := sourceMap(value)
				label, e := releasemeta.SearchSeasonChinese(number)
				if e != nil {
					return empty, e
				}
				fields["title"] = sourceText(fields["title"]) + " 第" + label + "季"
				fields["season"] = json.Number(number.String())
				live, _ = json.Marshal(fields)
			}
			public, e := originalPublicCatalogMedia(live)
			if e != nil {
				return empty, e
			}
			view, e := originalCatalogMediaProjection(public)
			if e != nil {
				return empty, e
			}
			empty.Results = append(empty.Results, view)
		}
		return empty, nil
	})
	if err != nil {
		return result, err
	}
	order := map[string]int{}
	sources := "themoviedb"
	if value := a.Settings().SearchSource; value != nil {
		sources = *value
	}
	if sources != "" {
		for index, source := range strings.Split(sources, ",") {
			order[source] = index
		}
	}
	priority := func(source string) int {
		value, present := order[source]
		if !present {
			return 4
		}
		return value
	}
	sort.SliceStable(result.Results, func(i, j int) bool { return priority(result.Results[i].Source) < priority(result.Results[j].Source) })
	total := len(result.Results)
	position := func(n *big.Int) int {
		if n.Sign() < 0 {
			n.Add(n, big.NewInt(int64(total)))
		}
		if n.Sign() < 0 {
			return 0
		}
		if n.Cmp(big.NewInt(int64(total))) > 0 {
			return total
		}
		return int(n.Int64())
	}
	start := position(new(big.Int).Mul(new(big.Int).Sub(big.NewInt(int64(page)), big.NewInt(1)), big.NewInt(int64(count))))
	end := position(new(big.Int).Mul(big.NewInt(int64(page)), big.NewInt(int64(count))))
	if end < start {
		end = start
	}
	result.Results = result.Results[start:end]
	result.Page = page
	result.TotalResults = total
	result.TotalPages = 0
	if count > 0 {
		result.TotalPages = total / count
		if total%count != 0 {
			result.TotalPages++
		}
	}
	result.HasMore = end < total
	models := make([]catalog.Media, len(result.Results))
	for i, item := range result.Results {
		models[i] = item.Media
	}
	if err = a.decorateMedia(ctx, models); err != nil {
		return result, err
	}
	for i, item := range models {
		result.Results[i].Media = item
	}
	return result, nil
}
