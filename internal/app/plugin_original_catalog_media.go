package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

func originalCatalogMediaInvalid() error {
	return &APIError{"PLUGIN_CATALOG_MEDIA_INVALID", "插件媒体信息返回格式无效", 500}
}

// Source module methods return MediaInfo objects; the Go process boundary uses
// their explicit JSON fields. Keep provider identities outside the native TMDB
// numeric namespace when projecting into the already existing catalog UI DTO.
func originalCatalogMediaProjection(raw json.RawMessage) (catalog.ExploreMedia, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return catalog.ExploreMedia{}, originalCatalogMediaInvalid()
	}
	fields := sourceMap(value)
	if fields == nil {
		return catalog.ExploreMedia{}, originalCatalogMediaInvalid()
	}
	out := catalog.ExploreMedia{Media: catalog.Media{Aliases: []string{}, GenreIDs: []int64{}, Genres: []catalog.Genre{}, Cast: []catalog.CastMember{}, Recommendations: []catalog.Media{}, Seasons: []catalog.SeasonSummary{}}}
	out.OriginalMedia = append(json.RawMessage(nil), raw...)
	out.Source = sourceText(fields["source"])
	if out.Source == "" {
		for _, pair := range []struct{ source, key string }{{"themoviedb", "tmdb_id"}, {"douban", "douban_id"}, {"bangumi", "bangumi_id"}, {"anilist", "anilist_id"}} {
			if fields[pair.key] != nil {
				out.Source = pair.source
				break
			}
		}
	}
	out.SourceID = sourceStringValue(fields["media_id"])
	if out.SourceID == "" {
		key := map[string]string{"themoviedb": "tmdb_id", "douban": "douban_id", "bangumi": "bangumi_id", "anilist": "anilist_id"}[out.Source]
		out.SourceID = sourceStringValue(fields[key])
	}
	if out.Source == "themoviedb" {
		if fields["tmdb_id"] != nil {
			out.ID, err = strconv.ParseInt(sourceStringValue(fields["tmdb_id"]), 10, 64)
			if err != nil {
				return out, originalCatalogMediaInvalid()
			}
		}
	}
	switch sourceText(fields["type"]) {
	case "电影":
		out.MediaType = "movie"
	case "电视剧":
		out.MediaType = "tv"
	case "":
	default:
		return out, originalCatalogMediaInvalid()
	}
	for target, key := range map[*string]string{&out.Title: "title", &out.OriginalTitle: "original_title", &out.OriginalLanguage: "original_language", &out.Overview: "overview", &out.PosterPath: "poster_path", &out.BackdropPath: "backdrop_path", &out.ReleaseDate: "release_date", &out.IMDbID: "imdb_id", &out.Status: "status"} {
		if fields[key] != nil {
			text, ok := fields[key].(string)
			if !ok {
				return out, originalCatalogMediaInvalid()
			}
			*target = text
		}
	}
	for target, key := range map[*int]string{&out.Year: "year", &out.Runtime: "runtime", &out.NumberOfEpisodes: "number_of_episodes", &out.NumberOfSeasons: "number_of_seasons", &out.VoteCount: "vote_count"} {
		if fields[key] != nil {
			number, e := originalCatalogPersonInteger(fields[key])
			if e != nil {
				return out, originalCatalogMediaInvalid()
			}
			n, e := strconv.Atoi(number.String())
			if e != nil {
				return out, originalCatalogMediaInvalid()
			}
			*target = n
		}
	}
	for target, key := range map[*float64]string{&out.VoteAverage: "vote_average", &out.Popularity: "popularity"} {
		if fields[key] != nil {
			number, e := strconv.ParseFloat(sourceStringValue(fields[key]), 64)
			if e != nil {
				return out, originalCatalogMediaInvalid()
			}
			*target = number
		}
	}
	for target, key := range map[*[]string]string{&out.Aliases: "names", &out.OriginCountry: "origin_country"} {
		if fields[key] != nil {
			rows, ok := fields[key].([]any)
			if !ok {
				return out, originalCatalogMediaInvalid()
			}
			for _, row := range rows {
				text, ok := row.(string)
				if !ok {
					return out, originalCatalogMediaInvalid()
				}
				*target = append(*target, text)
			}
		}
	}
	if fields["genres"] != nil {
		encoded, _ := json.Marshal(fields["genres"])
		if json.Unmarshal(encoded, &out.Genres) != nil {
			// These source models use genre names as IDs. Preserve that
			// original value in OriginalMedia and the display name here.
			switch out.Source {
			case "douban", "bangumi", "anilist":
				out.Genres = nil
				genres, ok := fields["genres"].([]any)
				if !ok {
					return out, originalCatalogMediaInvalid()
				}
				for _, row := range genres {
					genre := sourceMap(row)
					name, ok := genre["name"].(string)
					if !ok {
						return out, originalCatalogMediaInvalid()
					}
					out.Genres = append(out.Genres, catalog.Genre{Name: name})
				}
			default:
				return out, originalCatalogMediaInvalid()
			}
		}
	}
	return out, nil
}
func sourceStringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "True"
		}
		return "False"
	}
	return ""
}

// Plugins precede system modules. Lists accumulate system list results; every
// other non-None value is terminal. Wrapper-level falsey results become [] and
// truthy non-list values fail when the fixed caller tries media.to_dict().
func (a *App) originalCatalogFeed(ctx context.Context, method string, kwargs map[string]any, page int, native func() (catalog.ExplorePage, error)) (catalog.ExplorePage, error) {
	encoded, _ := json.Marshal(kwargs)
	result, err := a.invokeOriginalPluginModules(ctx, method, nil, json.RawMessage(`[]`), encoded)
	if err != nil {
		return catalog.ExplorePage{}, err
	}
	empty := catalog.ExplorePage{Results: []catalog.ExploreMedia{}, Page: page, TotalPages: page}
	callNative := func() (catalog.ExplorePage, error) {
		core, err := native()
		if err == nil {
			return core, nil
		}
		if sourceSubscriptionTruth(kwargs["raise_exception"]) {
			return empty, err
		}
		a.reportOriginalPluginExecutionError(ctx, "system", originalCatalogSystemModuleName(method)+"发生了错误", map[string]any{"type": "module", "module_id": originalCatalogSystemModuleID(method), "module_name": originalCatalogSystemModuleName(method), "module_method": method}, err)
		return empty, nil
	}
	if originalModuleResultEmpty(result) {
		return callNative()
	}
	var list []json.RawMessage
	if json.Unmarshal(result, &list) != nil {
		if !originalBaseJSONTruthy(result) {
			return empty, nil
		}
		return empty, originalCatalogMediaInvalid()
	}
	core, err := callNative()
	if err != nil {
		return empty, err
	}
	rows := make([]catalog.ExploreMedia, 0, len(list)+len(core.Results))
	for _, item := range list {
		live, e := originalLiveMediaImagesJSON(item)
		if e != nil {
			return empty, originalCatalogMediaInvalid()
		}
		public, e := originalPublicCatalogMedia(live)
		if e != nil {
			return empty, originalCatalogMediaInvalid()
		}
		media, e := originalCatalogMediaProjection(public)
		if e != nil {
			return empty, e
		}
		rows = append(rows, media)
	}
	core.Results = append(rows, core.Results...)
	core.TotalResults += len(rows)
	return core, nil
}

func originalCatalogSystemModuleID(method string) string {
	switch {
	case method == "recognize_media" || method == "async_recognize_media" || method == "search_medias" || method == "async_search_medias":
		return "TheMovieDbModule"
	case strings.Contains(method, "tmdb"):
		return "TheMovieDbModule"
	case strings.Contains(method, "bangumi"):
		return "BangumiModule"
	case strings.Contains(method, "anilist"):
		return "AniListModule"
	default:
		return "DoubanModule"
	}
}

func originalCatalogSystemModuleName(method string) string {
	return map[string]string{"TheMovieDbModule": "TheMovieDb", "BangumiModule": "Bangumi", "AniListModule": "AniList", "DoubanModule": "豆瓣"}[originalCatalogSystemModuleID(method)]
}
