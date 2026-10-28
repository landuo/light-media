package app

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Fixed schemas/context.py MediaInfo is the response DTO of media.detail. It
// excludes private provider payloads and dataclass-only image/runtime fields.
func originalCatalogMediaResponse(raw json.RawMessage) (map[string]any, error) {
	input, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(input) == nil {
		return nil, originalCatalogMediaInvalid()
	}
	defaults, _ := decodeSourceJSON(json.RawMessage(originalCatalogMediaResponseDefaults))
	out := sourceMap(defaults)
	groups := map[string]string{}
	for _, key := range strings.Fields("source scrape_source type title en_title year title_year imdb_id douban_id mediaid_prefix media_id original_language original_title release_date backdrop_path poster_path overview category detail_link first_air_date homepage last_air_date original_name status tagline episode_group") {
		groups[key] = "string"
	}
	for _, key := range strings.Fields("season tmdb_id tvdb_id bangumi_id anilist_id anidb_id collection_id number_of_episodes number_of_seasons vote_count runtime") {
		groups[key] = "int"
	}
	for _, key := range strings.Fields("vote_average popularity") {
		groups[key] = "float"
	}
	for _, key := range strings.Fields("season_info genres") {
		groups[key] = "dict-list"
	}
	for _, key := range strings.Fields("names actors directors created_by episode_run_time languages networks origin_country production_companies production_countries spoken_languages release_dates genre_ids episode_groups") {
		groups[key] = "list"
	}
	groups["adult"], groups["seasons"], groups["next_episode_to_air"] = "bool", "seasons", "dict"
	for key, value := range sourceMap(input) {
		kind, exists := groups[key]
		if !exists {
			continue
		}
		if value == nil {
			if key == "release_dates" {
				return nil, originalCatalogMediaInvalid()
			}
			out[key] = nil
			continue
		}
		switch kind {
		case "string":
			if _, ok := value.(string); !ok {
				return nil, originalCatalogMediaInvalid()
			}
		case "int":
			value, err = originalCatalogPersonInteger(value)
		case "float":
			var n float64
			switch v := value.(type) {
			case json.Number:
				n, err = strconv.ParseFloat(v.String(), 64)
			case string:
				n, err = strconv.ParseFloat(strings.TrimSpace(v), 64)
			case bool:
				if v {
					n = 1
				}
			default:
				return nil, originalCatalogMediaInvalid()
			}
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, originalCatalogMediaInvalid()
			}
			value = n
		case "bool":
			switch v := value.(type) {
			case bool:
			case string:
				switch strings.ToLower(v) {
				case "1", "true", "yes", "y", "on", "t":
					value = true
				case "0", "false", "no", "n", "off", "f":
					value = false
				default:
					return nil, originalCatalogMediaInvalid()
				}
			case json.Number:
				if v == "1" {
					value = true
				} else if v == "0" {
					value = false
				} else {
					return nil, originalCatalogMediaInvalid()
				}
			default:
				return nil, originalCatalogMediaInvalid()
			}
		case "list", "dict-list":
			rows, ok := value.([]any)
			if !ok {
				return nil, originalCatalogMediaInvalid()
			}
			if kind == "dict-list" {
				for _, row := range rows {
					if sourceMap(row) == nil {
						return nil, originalCatalogMediaInvalid()
					}
				}
			}
		case "dict":
			if sourceMap(value) == nil {
				return nil, originalCatalogMediaInvalid()
			}
		case "seasons":
			fields := sourceMap(value)
			if fields == nil {
				return nil, originalCatalogMediaInvalid()
			}
			normalized := map[string]any{}
			for k, v := range fields {
				n, e := originalCatalogPersonInteger(k)
				if e != nil {
					return nil, originalCatalogMediaInvalid()
				}
				if _, ok := v.([]any); !ok {
					return nil, originalCatalogMediaInvalid()
				}
				normalized[n.String()] = v
			}
			value = normalized
		}
		if err != nil {
			return nil, originalCatalogMediaInvalid()
		}
		out[key] = value
	}
	return out, nil
}

const originalCatalogMediaResponseDefaults = `{"source":null,"scrape_source":null,"type":null,"title":null,"en_title":null,"year":null,"title_year":null,"season":null,"tmdb_id":null,"imdb_id":null,"tvdb_id":null,"douban_id":null,"bangumi_id":null,"anilist_id":null,"anidb_id":null,"collection_id":null,"mediaid_prefix":null,"media_id":null,"original_language":null,"original_title":null,"release_date":null,"backdrop_path":null,"poster_path":null,"vote_average":0.0,"overview":null,"category":"","seasons":{},"season_info":[],"names":[],"actors":[],"directors":[],"detail_link":null,"adult":false,"created_by":[],"episode_run_time":[],"genres":[],"first_air_date":null,"homepage":null,"languages":[],"last_air_date":null,"networks":[],"number_of_episodes":0,"number_of_seasons":0,"origin_country":[],"original_name":null,"production_companies":[],"production_countries":[],"spoken_languages":[],"release_dates":[],"status":null,"tagline":null,"genre_ids":[],"vote_count":0,"popularity":0.0,"runtime":null,"next_episode_to_air":{},"episode_groups":[],"episode_group":null}`
