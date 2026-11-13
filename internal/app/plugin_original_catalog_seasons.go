package app

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"light-media/internal/plugins"
)

func originalCatalogSeasonSummaryProjection(raw json.RawMessage) (map[string]any, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, originalCatalogMediaInvalid()
	}
	out := map[string]any{"air_date": nil, "episode_count": nil, "name": nil, "overview": nil, "poster_path": nil, "season_number": nil, "vote_average": nil}
	for key, value := range sourceMap(value) {
		if _, ok := out[key]; !ok {
			continue
		}
		if value == nil {
			continue
		}
		switch key {
		case "episode_count", "season_number":
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
func (a *App) originalCatalogSeasons(ctx context.Context, id int64) ([]map[string]any, error) {
	if !validMedia("tv", id) {
		return nil, bad("INVALID_MEDIA", "请选择明确的电视剧身份")
	}
	return a.originalCatalogSeasonsWithNative(ctx, id, func() (json.RawMessage, error) {
		// A fresh detail response already contains the same season array. On a
		// cold picker, fetch only the TV header rather than all appendices.
		var cached struct {
			At   time.Time              `json:"at"`
			Data sourceMediaInformation `json:"data"`
		}
		if a.Store.Get(ctx, "cache", "source-detail:tv:"+strconv.FormatInt(id, 10), &cached) == nil && time.Since(cached.At) < 24*time.Hour {
			var provider map[string]json.RawMessage
			if json.Unmarshal(cached.Data.ProviderInfo, &provider) == nil && len(provider["seasons"]) > 0 && string(provider["seasons"]) != "null" && validateNativeSeasonSummaries(provider["seasons"]) == nil {
				return provider["seasons"], nil
			}
		}
		var rows json.RawMessage
		_, err := a.cached(ctx, "source-season-summaries-v2:"+strconv.FormatInt(id, 10), 12*time.Hour, &rows, func() (any, error) {
			raw, err := a.catalog().SourceSeasonSummaries(ctx, id)
			if err != nil {
				return nil, err
			}
			if err = validateNativeSeasonSummaries(raw); err != nil {
				return nil, err
			}
			return raw, nil
		})
		return rows, err
	})
}
func (a *App) originalCatalogSeasonsWithNative(ctx context.Context, id int64, native func() (json.RawMessage, error)) ([]map[string]any, error) {
	const method = "async_tmdb_seasons"
	kwargs, _ := json.Marshal(map[string]any{"tmdbid": id})
	result, err := a.invokeOriginalPluginModules(ctx, method, nil, json.RawMessage(`[]`), kwargs)
	if err != nil {
		return nil, err
	}
	core := func() []map[string]any {
		raw, e := native()
		rows := []map[string]any{}
		if e == nil {
			var list []json.RawMessage
			e = json.Unmarshal(raw, &list)
			if e == nil {
				for _, item := range list {
					v, parseErr := decodeSourceJSON(item)
					if parseErr != nil || sourceMap(v) == nil {
						e = originalCatalogMediaInvalid()
						break
					}
					if sourceMap(v)["season_number"] == nil {
						continue
					}
					row, parseErr := originalCatalogSeasonSummaryProjection(item)
					if parseErr != nil {
						e = parseErr
						break
					}
					rows = append(rows, row)
				}
			}
		}
		if e != nil {
			a.reportOriginalPluginExecutionError(ctx, "system", "TheMovieDb发生了错误", map[string]any{"type": "module", "module_id": "TheMovieDbModule", "module_name": "TheMovieDb", "module_method": method}, e)
			return nil
		}
		return rows
	}
	if originalModuleResultEmpty(result) {
		rows := core()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		return rows, nil
	}
	var list []json.RawMessage
	var more []map[string]any
	if json.Unmarshal(result, &list) == nil && string(result) != "null" {
		more = core()
	} else if tuple, ok := plugins.DecodeModuleResultTuple(result); ok {
		list = tuple
	} else if !originalBaseJSONTruthy(result) {
		return []map[string]any{}, nil
	} else {
		return nil, originalCatalogMediaInvalid()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	rows := make([]map[string]any, 0, len(list)+len(more))
	for _, raw := range list {
		row, e := originalCatalogSeasonSummaryProjection(raw)
		if e != nil {
			return nil, e
		}
		rows = append(rows, row)
	}
	return append(rows, more...), nil
}

// Validate before persistence, using the same field/null/skip semantics as the
// public projection. A malformed HTTP 200 must not poison subsequent retries.
func validateNativeSeasonSummaries(raw json.RawMessage) error {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return originalCatalogMediaInvalid()
	}
	for _, item := range list {
		value, err := decodeSourceJSON(item)
		if err != nil || sourceMap(value) == nil {
			return originalCatalogMediaInvalid()
		}
		if sourceMap(value)["season_number"] == nil {
			continue
		}
		if _, err := originalCatalogSeasonSummaryProjection(item); err != nil {
			return err
		}
	}
	return nil
}
