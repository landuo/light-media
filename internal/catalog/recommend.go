package catalog

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var recommendTimeZone = time.FixedZone("Asia/Shanghai", 8*60*60)

type RecommendSource struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
}

// RecommendSources returns the original recommendation rows in their default
// order. The returned slice is independent of the routing whitelist.
func RecommendSources() []RecommendSource {
	return []RecommendSource{
		{"tmdb_trending", "流行趋势", "rankings"},
		{"douban_showing", "正在热映", "movie"},
		{"bangumi_calendar", "Bangumi每日放送", "anime"},
		{"anilist_trending", "AniList 当前趋势", "anime"},
		{"anilist_popular_this_season", "AniList 本季热门", "anime"},
		{"tmdb_movies", "TMDB热门电影", "movie"},
		{"tmdb_tvs", "TMDB热门电视剧", "tv"},
		{"douban_movie_hot", "豆瓣热门电影", "movie"},
		{"douban_tv_hot", "豆瓣热门电视剧", "tv"},
		{"douban_tv_animation", "豆瓣热门动漫", "anime"},
		{"douban_movies", "豆瓣最新电影", "movie"},
		{"douban_tvs", "豆瓣最新电视剧", "tv"},
		{"douban_movie_top250", "豆瓣电影TOP250", "rankings"},
		{"douban_tv_weekly_chinese", "豆瓣国产剧集榜", "rankings"},
		{"douban_tv_weekly_global", "豆瓣全球剧集榜", "rankings"},
	}
}

// Recommend fetches a fixed original feed, retaining native provider identities
// so a Douban, Bangumi or AniList ID can never be used as a TMDB ID.
func (e *Explorer) Recommend(ctx context.Context, id string, page int) (ExplorePage, error) {
	if ctx == nil || !validPage(page) {
		return ExplorePage{}, &ExploreError{Code: "invalid_input"}
	}
	switch id {
	case "tmdb_trending":
		p, err := e.tmdb.Browse(ctx, "all", "trending_week", page)
		if err != nil {
			return ExplorePage{}, err
		}
		result := ExplorePage{Results: []ExploreMedia{}, Page: p.Page, TotalPages: p.TotalPages, TotalResults: p.TotalResults, HasMore: p.Page < p.TotalPages}
		for _, m := range p.Results {
			result.Results = append(result.Results, ExploreMedia{Media: m, Source: "themoviedb", SourceID: strconv.FormatInt(m.ID, 10)})
		}
		return result, nil
	case "tmdb_movies", "tmdb_tvs":
		kind := "movie"
		if id == "tmdb_tvs" {
			kind = "tv"
		}
		q, _ := ParseExploreQuery(url.Values{"source": {"themoviedb"}, "type": {kind}, "page": {strconv.Itoa(page)}})
		if kind == "tv" {
			// The original recommendation row supplies this language union even
			// though the general discovery endpoint defaults to all languages.
			q.Params.Set("with_original_language", "zh|en|ja|ko")
		}
		return e.Explore(ctx, q)
	case "douban_movies", "douban_tvs":
		kind := "movie"
		if id == "douban_tvs" {
			kind = "tv"
		}
		q, _ := ParseExploreQuery(url.Values{"source": {"douban"}, "type": {kind}, "page": {strconv.Itoa(page)}, "sort": {"R"}})
		return e.Explore(ctx, q)
	case "douban_showing":
		return e.recommendDoubanCollection(ctx, "movie_showing", "movie", page)
	case "douban_movie_hot":
		return e.recommendDoubanCollection(ctx, "movie_hot_gaia", "movie", page)
	case "douban_tv_hot":
		return e.recommendDoubanCollection(ctx, "tv_hot", "tv", page)
	case "douban_tv_animation":
		return e.recommendDoubanCollection(ctx, "tv_animation", "tv", page)
	case "douban_movie_top250":
		return e.recommendDoubanCollection(ctx, "movie_top250", "movie", page)
	case "douban_tv_weekly_chinese":
		return e.recommendDoubanCollection(ctx, "tv_chinese_best_weekly", "tv", page)
	case "douban_tv_weekly_global":
		return e.recommendDoubanCollection(ctx, "tv_global_best_weekly", "tv", page)
	case "bangumi_calendar":
		return e.recommendBangumiCalendar(ctx, page)
	case "anilist_trending", "anilist_popular_this_season":
		return e.Explore(ctx, recommendAniListQuery(id, page, time.Now()))
	default:
		return ExplorePage{}, &ExploreError{Code: "invalid_input"}
	}
}

func recommendAniListQuery(id string, page int, now time.Time) ExploreQuery {
	now = now.In(recommendTimeZone)
	q := ExploreQuery{Source: "anilist", Kind: "anime", Page: page, Count: 20, Params: url.Values{"sort": {"TRENDING_DESC"}}}
	if id == "anilist_popular_this_season" {
		q.Params.Set("sort", "POPULARITY_DESC")
		q.Params.Set("season", []string{"WINTER", "SPRING", "SUMMER", "FALL"}[(int(now.Month())-1)/3])
		q.Params.Set("seasonYear", strconv.Itoa(now.Year()))
	}
	return q
}

type recommendDoubanMedia struct {
	doubanExploreMedia
	Cover struct {
		URL   string `json:"url"`
		Large struct {
			URL string `json:"url"`
		} `json:"large"`
	} `json:"cover"`
}

func (e *Explorer) recommendDoubanCollection(ctx context.Context, collection, kind string, page int) (ExplorePage, error) {
	q := ExploreQuery{Source: "douban", Kind: kind, Page: page, Count: 30}
	params := url.Values{"start": {strconv.Itoa((page - 1) * q.Count)}, "count": {strconv.Itoa(q.Count)}}
	var raw struct {
		Items []recommendDoubanMedia `json:"subject_collection_items"`
		Total *int                   `json:"total"`
		Start *int                   `json:"start"`
	}
	if err := e.request(ctx, "douban", http.MethodGet, "/api/v2/subject_collection/"+collection+"/items", params, nil, &raw); err != nil {
		return ExplorePage{}, err
	}
	if raw.Items == nil || len(raw.Items) > q.Count || raw.Start != nil && *raw.Start != (page-1)*q.Count || raw.Total != nil && (*raw.Total < 0 || *raw.Total > 10_000_000) {
		return ExplorePage{}, &ExploreError{Source: "douban", Code: "invalid_response"}
	}
	p := explorePagination(q, len(raw.Items), raw.Total)
	for _, item := range raw.Items {
		if item.CoverURL == "" {
			item.CoverURL = item.Cover.URL
			if item.CoverURL == "" {
				item.CoverURL = item.Cover.Large.URL
			}
		}
		m, err := decodeDoubanExplore(item.doubanExploreMedia, kind)
		if err != nil {
			return ExplorePage{}, err
		}
		p.Results = append(p.Results, m)
	}
	return p, nil
}

type recommendBangumiMedia struct {
	bangumiExploreMedia
	AirDate string `json:"air_date"`
}

func (e *Explorer) recommendBangumiCalendar(ctx context.Context, page int) (ExplorePage, error) {
	var raw []struct {
		Weekday struct {
			ID int `json:"id"`
		} `json:"weekday"`
		Items []recommendBangumiMedia `json:"items"`
	}
	if err := e.request(ctx, "bangumi", http.MethodGet, "/calendar", url.Values{"_ts": {time.Now().In(recommendTimeZone).Format("20060102")}}, nil, &raw); err != nil {
		return ExplorePage{}, err
	}
	invalid := func() (ExplorePage, error) {
		return ExplorePage{}, &ExploreError{Source: "bangumi", Code: "invalid_response"}
	}
	if raw == nil || len(raw) > 7 {
		return invalid()
	}
	items := []recommendBangumiMedia{}
	for _, day := range raw {
		if day.Weekday.ID < 1 || day.Weekday.ID > 7 || day.Items == nil || len(items)+len(day.Items) > 15_000 {
			return invalid()
		}
		items = append(items, day.Items...)
	}
	total, count := len(items), 30
	start := min((page-1)*count, total)
	end := min(start+count, total)
	p := explorePagination(ExploreQuery{Page: page, Count: count}, end-start, &total)
	for _, item := range items[start:end] {
		if item.Date == "" {
			item.Date = item.AirDate
		}
		// The legacy calendar still emits HTTP image URLs. Upgrade only the
		// provider's fixed CDN; the shared decoder rechecks all URL restrictions.
		for _, path := range []*string{&item.Images.Large, &item.Images.Common, &item.Images.Medium} {
			if strings.HasPrefix(*path, "http://lain.bgm.tv/") {
				*path = "https://" + strings.TrimPrefix(*path, "http://")
			}
		}
		m, err := decodeBangumiExplore(item.bangumiExploreMedia)
		if err != nil {
			return ExplorePage{}, err
		}
		p.Results = append(p.Results, m)
	}
	return p, nil
}
