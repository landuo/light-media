package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"light-media/internal/catalog"
)

func originalCatalogTMDBDiscover(kind string, page int, params url.Values) map[string]any {
	mediaType := "电影"
	if kind == "tv" {
		mediaType = "电视剧"
	}
	rating, _ := strconv.ParseFloat(params.Get("vote_average.gte"), 64)
	votes, _ := strconv.Atoi(params.Get("vote_count.gte"))
	dateKey := "release_date.gte"
	if kind == "tv" {
		dateKey = "first_air_date.gte"
	}
	sort := params.Get("sort_by")
	if sort == "" {
		sort = "popularity.desc"
	}
	return map[string]any{"mtype": mediaType, "sort_by": sort, "with_genres": params.Get("with_genres"), "with_original_language": params.Get("with_original_language"), "with_keywords": params.Get("with_keywords"), "with_watch_providers": params.Get("with_watch_providers"), "vote_average": rating, "vote_count": votes, "release_date": params.Get(dateKey), "page": page}
}
func originalCatalogExploreModule(q catalog.ExploreQuery, original ...url.Values) (string, map[string]any) {
	switch q.Source {
	case "themoviedb":
		return "async_tmdb_discover", originalCatalogTMDBDiscover(q.Kind, q.Page, q.Params)
	case "douban":
		sort := q.Params.Get("sort")
		if len(original) > 0 && !original[0].Has("sort") {
			sort = "R"
		}
		kind := "电影"
		if q.Kind == "tv" {
			kind = "电视剧"
		}
		return "async_douban_discover", map[string]any{"mtype": kind, "sort": sort, "tags": q.Params.Get("tags"), "page": q.Page, "count": q.Count}
	case "bangumi":
		var cat, year any
		if q.Params.Has("cat") {
			cat, _ = strconv.Atoi(q.Params.Get("cat"))
		}
		if q.Params.Has("year") {
			year = q.Params.Get("year")
		}
		return "async_bangumi_discover", map[string]any{"type": 2, "cat": cat, "sort": q.Params.Get("sort"), "year": year, "limit": q.Count, "offset": (q.Page - 1) * q.Count}
	case "anilist":
		values := map[string]any{"page": q.Page, "count": q.Count}
		for _, key := range []string{"search", "genre", "media_format", "season", "season_year", "status", "country", "sort"} {
			var value any
			param := key
			if key == "media_format" {
				param = "format"
			}
			if key == "season_year" {
				param = "seasonYear"
			}
			present := q.Params.Has(param)
			if len(original) > 0 {
				inputKey := param
				if param == "seasonYear" {
					inputKey = "season_year"
				}
				present = original[0].Has(inputKey)
			}
			if present {
				value = q.Params.Get(param)
				if key == "season_year" {
					value, _ = strconv.Atoi(q.Params.Get(param))
				}
			}
			values[key] = value
		}
		return "async_anilist_discover", values
	}
	return "", nil
}
func originalCatalogRecommendModule(id string, page int) (string, map[string]any) {
	switch id {
	case "tmdb_movies", "tmdb_tvs":
		kind := "movie"
		params := url.Values{}
		if id == "tmdb_tvs" {
			kind = "tv"
			params.Set("with_original_language", "zh|en|ja|ko")
		}
		values := originalCatalogTMDBDiscover(kind, page, params)
		values["raise_exception"] = true
		return "async_tmdb_discover", values
	case "tmdb_trending":
		return "async_tmdb_trending", map[string]any{"page": page, "raise_exception": true}
	case "bangumi_calendar":
		return "async_bangumi_calendar", map[string]any{}
	case "anilist_trending":
		return "async_anilist_trending", map[string]any{"page": page, "count": 20}
	case "anilist_popular_this_season":
		return "async_anilist_popular_this_season", map[string]any{"page": page, "count": 20}
	case "douban_movies", "douban_tvs":
		kind := "电影"
		if id == "douban_tvs" {
			kind = "电视剧"
		}
		return "async_douban_discover", map[string]any{"mtype": kind, "sort": "R", "tags": "", "page": page, "count": 30}
	}
	names := map[string]string{"douban_showing": "async_movie_showing", "douban_movie_top250": "async_movie_top250", "douban_tv_weekly_chinese": "async_tv_weekly_chinese", "douban_tv_weekly_global": "async_tv_weekly_global", "douban_tv_animation": "async_tv_animation", "douban_movie_hot": "async_movie_hot", "douban_tv_hot": "async_tv_hot"}
	return names[id], map[string]any{"page": page, "count": 30}
}

// Fixed RecommendChain cache is outside module discovery, lasts24h, and skips
// empty results. Ordinary explore endpoints have no such wrapper cache.
func (a *App) cachedOriginalRecommendation(ctx context.Context, key string, dst *catalog.ExplorePage, fetch func() (catalog.ExplorePage, error)) (time.Time, error) {
	var cache struct {
		At   time.Time       `json:"at"`
		Data json.RawMessage `json:"data"`
	}
	if a.Store.Get(ctx, "cache", key, &cache) == nil && time.Since(cache.At) < 24*time.Hour && json.Unmarshal(cache.Data, dst) == nil && len(dst.Results) > 0 {
		return cache.At, nil
	}
	value, err := fetch()
	if err != nil {
		return time.Time{}, err
	}
	*dst = value
	at := time.Now().UTC()
	if len(value.Results) == 0 {
		return at, nil
	}
	cache.At = at
	cache.Data, err = json.Marshal(value)
	if err != nil {
		return time.Time{}, err
	}
	return at, a.Store.Put(ctx, "cache", key, cache)
}
func originalCatalogCalendarPage(full catalog.ExplorePage, page, count int) catalog.ExplorePage {
	total := len(full.Results)
	start := min((page-1)*count, total)
	end := min(start+count, total)
	full.Results = full.Results[start:end]
	full.Page = page
	full.TotalResults = total
	full.TotalPages = (total + count - 1) / count
	full.HasMore = end < total
	return full
}
