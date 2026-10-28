package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/sourcejson"
)

// The real person-detail HTTP host corresponds to async_tmdb_person_detail,
// never search_persons. Native provider caching remains inside the fallback:
// source plugin discovery/dispatch still runs for each Chain call.
func (a *App) originalCatalogPerson(ctx context.Context, id int64, native func() (json.RawMessage, error)) (any, error) {
	kwargs, _ := json.Marshal(map[string]any{"person_id": id})
	result, err := a.invokeOriginalPluginModules(ctx, "async_tmdb_person_detail", nil, json.RawMessage(`[]`), kwargs)
	if err != nil {
		return nil, err
	}
	if originalModuleResultEmpty(result) {
		raw, err := native()
		if err != nil {
			// Fixed TmdbApi.async_get_person_detail catches provider failures
			// and returns {}; the system module returns default MediaPerson.
			a.logOriginalPluginContext(ctx, "WARN", "person_provider_failed", "人物提供方读取失败，返回原版默认人物信息")
			return originalCatalogPersonProjection(json.RawMessage(`{}`))
		}
		if !originalBaseJSONTruthy(raw) {
			return originalCatalogPersonProjection(json.RawMessage(`{}`))
		}
		value, err := decodeSourceJSON(raw)
		if err != nil || sourceMap(value) == nil {
			return nil, originalCatalogPersonInvalid()
		}
		fields := sourceMap(value)
		fields["source"] = "themoviedb"
		encoded, _ := json.Marshal(fields)
		return originalCatalogPersonProjection(encoded)
	}
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil {
		// Source list accumulation invokes a system module but ignores its scalar
		// MediaPerson. The endpoint then rejects that list, even when empty.
		_, _ = native()
		return nil, originalCatalogPersonInvalid()
	}
	if _, tuple := plugins.DecodeModuleResultTuple(result); tuple {
		return nil, originalCatalogPersonInvalid()
	}
	return originalCatalogPersonProjection(result)
}
func originalCatalogPersonInvalid() error {
	return &APIError{"PLUGIN_MODULE_RESULT_INVALID", "人物插件返回格式无效", 500}
}

// Preserve the source DTO, including nullable/default/unused fields, instead
// of silently dropping it into the compact native Person representation.
func originalCatalogPersonProjection(raw json.RawMessage) (map[string]any, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return nil, originalCatalogPersonInvalid()
	}
	input, ok := value.(map[string]any)
	if !ok {
		return nil, originalCatalogPersonInvalid()
	}
	value, _ = decodeSourceJSON(json.RawMessage(originalCatalogPersonDefaults))
	out := value.(map[string]any)
	stringsOnly := map[string]bool{}
	for _, key := range []string{"source", "name", "character", "profile_path", "original_name", "credit_id", "birthday", "deathday", "imdb_id", "known_for_department", "place_of_birth", "biography", "title", "url", "latin_name", "relation"} {
		stringsOnly[key] = true
	}
	for key, item := range input {
		if _, known := out[key]; !known {
			continue
		}
		if item == nil {
			out[key] = nil
			continue
		}
		if stringsOnly[key] {
			if _, ok := item.(string); !ok {
				return nil, originalCatalogPersonInvalid()
			}
			out[key] = item
			continue
		}
		switch key {
		case "id":
			item, err = originalCatalogPersonInteger(item)
		case "type", "gender":
			if _, text := item.(string); !text {
				item, err = originalCatalogPersonInteger(item)
			}
		case "images":
			if _, ok := item.(map[string]any); !ok {
				err = fmt.Errorf("dict required")
			}
		case "also_known_as", "roles", "career":
			if _, ok := item.([]any); !ok {
				err = fmt.Errorf("list required")
			}
		case "avatar":
			switch item.(type) {
			case string, map[string]any:
			default:
				err = fmt.Errorf("string or dict required")
			}
		case "popularity":
			var number float64
			switch v := item.(type) {
			case bool:
				if v {
					number = 1
				}
			case json.Number:
				number, err = strconv.ParseFloat(v.String(), 64)
			case string:
				number, err = strconv.ParseFloat(strings.TrimSpace(v), 64)
			default:
				err = fmt.Errorf("number required")
			}
			if math.IsNaN(number) || math.IsInf(number, 0) {
				err = fmt.Errorf("nonfinite number")
			}
			item = number
		}
		if err != nil {
			return nil, originalCatalogPersonInvalid()
		}
		out[key] = item
	}
	return out, nil
}
func originalCatalogPersonInteger(value any) (json.Number, error) {
	return sourcejson.ModelInteger(value)
}
