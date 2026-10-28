package app

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// MediaInfo(tmdb_info=...) constructor only. Search/provider-list callers do
// not execute get_info's translation/content-rating augmentation first.
func sourceTMDBMediaModel(raw json.RawMessage, kind, domain, category string) (json.RawMessage, error) {
	value, e := decodeSourceJSON(raw)
	if e != nil || sourceMap(value) == nil || kind != "movie" && kind != "tv" {
		return nil, originalCatalogMediaInvalid()
	}
	info := sourceMap(value)
	mediaType, titleKey, originalKey, dateKey := "电影", "title", "original_title", "release_date"
	if kind == "tv" {
		mediaType, titleKey, originalKey, dateKey = "电视剧", "name", "original_name", "first_air_date"
	}
	genres, names := sourceRows(info["genre_ids"]), sourceRows(info["names"])
	if genres == nil {
		genres = []any{}
	}
	if names == nil {
		names = []any{}
	}
	enTitle := info["en_title"]
	releases := sourceRows(sourceMap(info["release_dates"])["results"])
	defaults, e := decodeSourceJSON(sourceMediaInfoDefaults)
	if e != nil {
		return nil, e
	}
	m := sourceMap(defaults)
	delete(m, "recognize_cache_hit") // class default, absent from vars until assigned
	m["source"], m["tmdb_info"], m["type"], m["tmdb_id"] = "themoviedb", info, mediaType, info["id"]
	if !sourceSubscriptionTruth(m["tmdb_id"]) {
		return mustCleanSeedJSON(m), nil
	}
	if external := sourceMap(info["external_ids"]); len(external) > 0 {
		m["tvdb_id"], m["imdb_id"] = external["tvdb_id"], external["imdb_id"]
	}
	m["collection_id"], m["overview"], m["genre_ids"], m["original_language"] = info["collection_id"], info["overview"], genres, info["original_language"]
	m["en_title"], m["hk_title"], m["tw_title"], m["sg_title"] = enTitle, info["hk_title"], info["tw_title"], info["sg_title"]
	m["vote_average"] = json.Number("0")
	if sourceSubscriptionTruth(info["vote_average"]) {
		n, e := strconv.ParseFloat(fmt.Sprint(info["vote_average"]), 64)
		if e != nil {
			return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "原TMDB评分字段类型无法使用")
		}
		m["vote_average"] = json.Number(strconv.FormatFloat(n, 'f', 1, 64))
	}
	m["title"], m["original_title"], m["release_date"] = info[titleKey], info[originalKey], info[dateKey]
	if date := sourceText(m["release_date"]); date != "" {
		m["year"] = date[:min(4, len(date))]
	}
	if kind == "movie" {
		m["release_dates"] = []any{}
		for _, row := range releases {
			item := sourceMap(row)
			for _, date := range sourceRows(item["release_dates"]) {
				d := sourceMap(date)
				if sourceSubscriptionTruth(d["release_date"]) {
					m["release_dates"] = append(m["release_dates"].([]any), map[string]any{"date": d["release_date"], "iso_code": item["iso_3166_1"], "note": d["note"], "type": d["type"]})
				}
			}
		}
	} else {
		if seasons := sourceRows(info["seasons"]); len(seasons) > 0 {
			m["season_info"] = seasons
			for _, row := range seasons {
				season := sourceMap(row)
				if season["season_number"] == nil {
					continue
				}
				count, e := strconv.Atoi(fmt.Sprint(season["episode_count"]))
				if e != nil || count < 0 || count > 10000 {
					return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "原TMDB季集详情无法使用")
				}
				episodes := []any{}
				for ep := 1; ep <= count; ep++ {
					episodes = append(episodes, ep)
				}
				key := fmt.Sprint(season["season_number"])
				sourceMap(m["seasons"])[key] = episodes
				if date := sourceText(season["air_date"]); date != "" {
					sourceMap(m["season_years"])[key] = date[:min(4, len(date))]
				}
			}
		}
		if group := sourceMap(info["episode_groups"]); len(group) > 0 {
			m["episode_groups"] = sourceRows(group["results"])
			delete(info, "episode_groups")
		}
	}
	for _, key := range []string{"poster_path", "backdrop_path"} {
		if sourceSubscriptionTruth(info[key]) {
			m[key], e = sourceImagePath(info[key], domain)
			if e != nil {
				return nil, e
			}
		}
	}
	m["directors"], m["actors"] = []any{}, []any{}
	credits := sourceMap(info["credits"])
	for _, row := range sourceRows(credits["cast"]) {
		if sourceMap(row)["known_for_department"] == "Acting" {
			m["actors"] = append(m["actors"].([]any), row)
		}
	}
	for _, row := range sourceRows(credits["crew"]) {
		job := sourceMap(row)["job"]
		if job == "Director" || job == "Writer" || job == "Editor" || job == "Producer" {
			m["directors"] = append(m["directors"].([]any), row)
		}
	}
	m["names"] = names
	for key, value := range info {
		current, exists := m[key]
		if exists && sourceSubscriptionTruth(value) && !sourceSubscriptionTruth(current) && (current == nil || sourceJSONType(current) == sourceJSONType(value)) {
			m[key] = value
		}
	}
	m["category"] = category
	m["title_year"] = sourceText(m["title"])
	if sourceSubscriptionTruth(m["title"]) && sourceSubscriptionTruth(m["year"]) {
		m["title_year"] = fmt.Sprint(m["title"]) + " (" + fmt.Sprint(m["year"]) + ")"
	}
	if info["recognize_cache_hit"] == true {
		m["recognize_cache_hit"] = true
	}
	return mustCleanSeedJSON(m), nil
}
