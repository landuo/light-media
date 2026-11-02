package app

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func originalCatalogEpisodeProjection(raw json.RawMessage) (map[string]any, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, originalCatalogMediaInvalid()
	}
	out := map[string]any{"air_date": nil, "episode_number": nil, "episode_type": nil, "name": nil, "overview": nil, "runtime": nil, "season_number": nil, "still_path": nil, "vote_average": nil, "crew": []any{}, "guest_stars": []any{}}
	for key, value := range sourceMap(value) {
		if _, present := out[key]; !present {
			continue
		}
		if value == nil {
			out[key] = nil
			continue
		}
		switch key {
		case "episode_number", "runtime", "season_number":
			value, err = originalCatalogPersonInteger(value)
		case "vote_average":
			var n float64
			switch v := value.(type) {
			case bool:
				if v {
					n = 1
				}
			case string:
				n, err = strconv.ParseFloat(strings.TrimSpace(v), 64)
			case json.Number:
				n, err = strconv.ParseFloat(v.String(), 64)
			default:
				return nil, originalCatalogMediaInvalid()
			}
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, originalCatalogMediaInvalid()
			}
			value = n
		case "crew", "guest_stars":
			if _, ok := value.([]any); !ok {
				return nil, originalCatalogMediaInvalid()
			}
		default:
			if _, ok := value.(string); !ok {
				return nil, originalCatalogMediaInvalid()
			}
		}
		if err != nil {
			return nil, originalCatalogMediaInvalid()
		}
		out[key] = value
	}
	return out, nil
}

func (a *App) originalCatalogEpisodes(ctx context.Context, id int64, season int, group any, native func() (json.RawMessage, error)) ([]map[string]any, error) {
	return a.originalCatalogEpisodesMethod(ctx, "async_tmdb_episodes", id, season, group, native, nil)
}

func (a *App) originalCatalogEpisodesMethod(ctx context.Context, method string, id int64, season int, group any, native func() (json.RawMessage, error), groups []originalPluginModuleGroup) ([]map[string]any, error) {
	kwargs, _ := json.Marshal(map[string]any{"tmdbid": id, "season": season, "episode_group": group})
	result, err := a.invokeOriginalPluginModuleGroups(ctx, method, nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return nil, err
	}
	callNative := func() (json.RawMessage, error) {
		raw, err := native()
		if err == nil {
			var rows []json.RawMessage
			if json.Unmarshal(raw, &rows) == nil && string(raw) != "null" {
				models := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					model, e := originalCatalogEpisodeProjection(row)
					if e != nil {
						err = e
						break
					}
					models = append(models, model)
				}
				if err == nil {
					return json.Marshal(models)
				}
			} else {
				return raw, nil
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		a.reportOriginalPluginExecutionError(ctx, "system", "TheMovieDb发生了错误", map[string]any{"type": "module", "module_id": "TheMovieDbModule", "module_name": "TheMovieDb", "module_method": method}, err)
		return nil, nil
	}
	var rows []json.RawMessage
	if originalModuleResultEmpty(result) {
		result, err = callNative()
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(result, &rows) != nil || string(result) == "null" {
			return nil, originalCatalogMediaInvalid()
		}
	} else if json.Unmarshal(result, &rows) == nil && string(result) != "null" {
		core, e := callNative()
		if e != nil {
			return nil, e
		}
		var more []json.RawMessage
		if !originalModuleResultEmpty(core) && (json.Unmarshal(core, &more) != nil || string(core) == "null") {
			return nil, originalCatalogMediaInvalid()
		}
		rows = append(rows, more...)
	} else if tuple, ok := plugins.DecodeModuleResultTuple(result); ok {
		rows = tuple
	} else {
		return nil, originalCatalogMediaInvalid()
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		value, e := originalCatalogEpisodeProjection(row)
		if e != nil {
			return nil, e
		}
		out = append(out, value)
	}
	return out, nil
}

func (a *App) originalCatalogSeason(ctx context.Context, id int64, season int, group any) (catalog.Season, error) {
	if !validMedia("tv", id) || season < 0 || season > 1000 {
		return catalog.Season{}, bad("INVALID_MEDIA", "请选择明确的剧集和季")
	}
	view := catalog.Season{SeriesID: id, SeasonNumber: season, Episodes: []catalog.Episode{}}
	rows, err := a.originalCatalogEpisodes(ctx, id, season, group, func() (json.RawMessage, error) {
		return a.originalCatalogNativeEpisodeRows(ctx, id, season, group, &view)
	})
	if err != nil {
		return catalog.Season{}, err
	}
	return originalCatalogSeasonProjection(view, rows)
}

func originalCatalogSeasonProjection(view catalog.Season, rows []map[string]any) (catalog.Season, error) {
	view.Episodes = []catalog.Episode{}
	for _, row := range rows {
		raw, _ := json.Marshal(row)
		compact := map[string]any{}
		for key, value := range row {
			if key != "guest_stars" {
				compact[key] = value
			}
		}
		encoded, _ := json.Marshal(compact)
		var episode catalog.Episode
		if json.Unmarshal(encoded, &episode) != nil {
			return view, originalCatalogMediaInvalid()
		}
		for _, star := range sourceRows(row["guest_stars"]) {
			encoded, _ := json.Marshal(star)
			var member catalog.CastMember
			if json.Unmarshal(encoded, &member) == nil {
				episode.GuestStars = append(episode.GuestStars, member)
			}
		}
		episode.OriginalEpisode = raw
		view.Episodes = append(view.Episodes, episode)
	}
	return view, nil
}

func (a *App) originalCatalogNativeEpisodeRows(ctx context.Context, id int64, season int, group any, view *catalog.Season) (json.RawMessage, error) {
	info, err := a.catalog().SourceSeasonInformation(catalog.OriginalPluginContext(ctx), id, season, sourceText(group))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		a.logOriginalPluginContext(ctx, "WARN", "episode_provider_failed", "剧集提供方读取失败，沿原版返回空列表")
		return json.RawMessage(`[]`), nil
	}
	value, err := decodeSourceJSON(info)
	if err != nil {
		return nil, originalCatalogMediaInvalid()
	}
	if !sourceSubscriptionTruth(value) {
		return json.RawMessage(`[]`), nil
	}
	fields := sourceMap(value)
	if fields == nil {
		return nil, originalCatalogMediaInvalid()
	}
	if view != nil {
		var header catalog.Season
		if json.Unmarshal(info, &header) == nil {
			view.ID, view.Name, view.Overview, view.AirDate, view.PosterPath = header.ID, header.Name, header.Overview, header.AirDate, header.PosterPath
		}
	}
	if !sourceSubscriptionTruth(fields["episodes"]) {
		return json.RawMessage(`[]`), nil
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(info, &raw)
	return raw["episodes"], nil
}
