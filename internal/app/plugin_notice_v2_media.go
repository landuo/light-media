package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/noticetemplate"
	"light-media/internal/releasemeta"
)

// Class defaults copied from the source MediaInfo dataclass, not guessed from
// the compact catalog DTO. Non-TMDB provider identity defaults stay distinct
// from missing provider data. The full actual response is retained separately.
//
//go:embed defaults/source-mediainfo.json
var sourceMediaInfoDefaults []byte

type sourceMediaInformation struct {
	ProviderInfo json.RawMessage
	MediaInfo    json.RawMessage
	Media        catalog.Media
}

func sourceMap(value any) map[string]any { out, _ := value.(map[string]any); return out }
func sourceRows(value any) []any         { out, _ := value.([]any); return out }
func sourceText(value any) string        { out, _ := value.(string); return out }
func sourceHasChinese(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}
func sourceImagePath(path any, domain string) (any, error) {
	if !sourceSubscriptionTruth(path) {
		return nil, nil
	}
	value, ok := path.(string)
	if !ok {
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "原TMDB图片字段类型无法使用")
	}
	if domain == "" {
		domain = "image.tmdb.org"
	}
	return "https://" + domain + "/t/p/original/" + strings.TrimLeft(value, "/"), nil
}

// sourceTMDBMediaInfo follows TmdbApi.get_info enrichment and MediaInfo's
// constructor. It accepts actual TMDB JSON only, not a compact catalog Media.
func sourceTMDBMediaInfo(raw json.RawMessage, kind, domain, category string) (json.RawMessage, error) {
	value, e := decodeSourceJSON(raw)
	if e != nil || sourceMap(value) == nil || kind != "movie" && kind != "tv" {
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "未获取原TMDB媒体详情")
	}
	info := sourceMap(value)
	mediaType, titleKey, originalKey, dateKey := "电影", "title", "original_title", "release_date"
	if kind == "tv" {
		mediaType, titleKey, originalKey, dateKey = "电视剧", "name", "original_name", "first_air_date"
	}
	info["media_type"] = mediaType
	genres := []any{}
	for _, row := range sourceRows(info["genres"]) {
		genres = append(genres, sourceMap(row)["id"])
	}
	info["genre_ids"] = genres
	alternativeKey := "titles"
	if kind == "tv" {
		alternativeKey = "results"
	}
	names, seen := []any{}, map[string]bool{}
	addName := func(value any) {
		if s, ok := value.(string); ok && s != "" && !seen[s] {
			seen[s], names = true, append(names, s)
		}
	}
	for _, row := range sourceRows(sourceMap(info["alternative_titles"])[alternativeKey]) {
		addName(sourceMap(row)["title"])
	}
	translations := sourceRows(sourceMap(info["translations"])["translations"])
	for _, row := range translations {
		addName(sourceMap(sourceMap(row)["data"])[titleKey])
	}
	info["names"] = names
	translation := func(region string) any {
		for _, row := range translations {
			item := sourceMap(row)
			if item["iso_3166_1"] == region {
				return sourceMap(item["data"])[titleKey]
			}
		}
		return nil
	}
	original := info[originalKey]
	enTitle := translation("US")
	if info["original_language"] == "en" || !sourceSubscriptionTruth(enTitle) {
		enTitle = original
	}
	info["en_title"], info["hk_title"], info["tw_title"] = enTitle, translation("HK"), translation("TW")
	info["sg_title"] = translation("SG")
	if !sourceSubscriptionTruth(info["sg_title"]) {
		info["sg_title"] = original
	}
	if !sourceHasChinese(sourceText(info[titleKey])) {
		for _, row := range sourceRows(sourceMap(info["alternative_titles"])[alternativeKey]) {
			item := sourceMap(row)
			if item["iso_3166_1"] == "CN" && sourceHasChinese(sourceText(item["title"])) {
				// This source branch uses full zhconv phrase rules. Never replace
				// it with a character-only guess or silently skip the CN alias.
				return nil, bad("SOURCE_MEDIA_CONVERSION_PENDING", "原TMDB中文别名需要完整简繁转换规则，当前未接入")
			}
		}
		if sourceHasChinese(sourceText(info["sg_title"])) && info["sg_title"] != info[titleKey] {
			info[titleKey] = info["sg_title"]
		}
	}
	ratings := map[string]any{}
	releases := sourceRows(sourceMap(info["release_dates"])["results"])
	if len(releases) > 0 {
		for _, row := range releases {
			item := sourceMap(row)
			dates := sourceRows(item["release_dates"])
			if region := sourceText(item["iso_3166_1"]); region != "" && len(dates) > 0 && sourceSubscriptionTruth(sourceMap(dates[0])["certification"]) {
				ratings[region] = sourceMap(dates[0])["certification"]
			}
		}
	} else {
		for _, row := range sourceRows(sourceMap(info["content_ratings"])["results"]) {
			item := sourceMap(row)
			if region := sourceText(item["iso_3166_1"]); region != "" && sourceSubscriptionTruth(item["rating"]) {
				ratings[region] = item["rating"]
			}
		}
	}
	info["content_rating"] = ratings["CN"]
	if !sourceSubscriptionTruth(info["content_rating"]) {
		info["content_rating"] = ratings["US"]
	}
	defaults, e := decodeSourceJSON(sourceMediaInfoDefaults)
	if e != nil {
		return nil, e
	}
	m := sourceMap(defaults)
	m["source"], m["tmdb_info"], m["type"], m["tmdb_id"] = "themoviedb", info, mediaType, info["id"]
	if !sourceSubscriptionTruth(m["tmdb_id"]) {
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "原TMDB媒体详情缺少身份")
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
	m["poster"], m["backdrop"] = "", ""
	for target, source := range map[string]string{"poster": "poster_path", "backdrop": "backdrop_path"} {
		if path, ok := m[source].(string); ok && path != "" {
			m[target] = strings.ReplaceAll(path, "original", "w500")
		}
	}
	return mustCleanSeedJSON(m), nil
}

func sourceJSONType(value any) string {
	switch value := value.(type) {
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			return "float"
		}
		return "int"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func sourceNoticeMeta(raw json.RawMessage) (noticetemplate.Object, error) {
	meta, e := releasemeta.DecodeMetaObjectState(raw)
	if e != nil {
		return nil, e
	}
	projection := noticetemplate.Object{}
	// All nullable MetaBase class attributes exist even when no instance value
	// was assigned. Derived properties are evaluated by the actual native port.
	for _, key := range []string{"org_string", "subtitle", "cn_name", "en_name", "original_name", "year", "begin_season", "end_season", "begin_episode", "end_episode", "part", "resource_type", "resource_effect", "resource_pix", "resource_team", "customization", "web_source", "video_encode", "video_bit", "audio_encode", "apply_words", "tmdbid", "doubanid", "bangumiid", "anilistid", "media_source", "media_id", "episode_group", "fps", "isfile", "title", "type", "total_season", "total_episode"} {
		projection[key] = meta.Get(key)
	}
	for key, value := range meta.Instance {
		projection[key] = value
	}
	derived, e := meta.Derived()
	if e != nil {
		return nil, e
	}
	for key, value := range derived {
		projection[key] = value
	}
	return projection, nil
}

func (a *App) sourceRecognizeTMDBMedia(ctx context.Context, input catalog.Media) (sourceMediaInformation, error) {
	policy, e := loadMediaCategoryPolicy(a.Dir)
	if e != nil {
		return sourceMediaInformation{}, e
	}
	raw, media, e := a.catalog().SourceInformation(ctx, input.MediaType, input.ID, policy.extraFields(input.MediaType))
	if e != nil {
		return sourceMediaInformation{}, e
	}
	projection, e := sourceTMDBMediaInfo(raw, input.MediaType, a.Settings().TMDBImageDomain, policy.category(media))
	if e != nil {
		return sourceMediaInformation{}, e
	}
	projected, e := decodeSourceJSON(projection)
	if e != nil {
		return sourceMediaInformation{}, e
	}
	media.Title, media.OriginalTitle = sourceText(sourceMap(projected)["title"]), sourceText(sourceMap(projected)["original_title"])
	return sourceMediaInformation{ProviderInfo: raw, MediaInfo: projection, Media: media}, nil
}

func (a *App) larkRecognizeSubscriptionMedia(ctx context.Context, core *pluginLarkCoreExecution, input catalog.Media) (sourceMediaInformation, error) {
	return larkCoreRead(ctx, a, core, "subscription.recognize", input, func(ctx context.Context) (sourceMediaInformation, error) {
		return a.sourceRecognizeTMDBMedia(ctx, input)
	})
}
