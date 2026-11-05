package app

import (
	"context"
	"encoding/json"
	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/rsscontext"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

func (a *App) originalHostMediaRecognizeDouban(ctx context.Context, explorer *catalog.Explorer, kwargs map[string]any) (json.RawMessage, error) {
	id := originalCatalogIdentityString(kwargs["doubanid"])
	meta := originalHostMediaMeta(sourceMap(kwargs["meta"]))
	if !sourceSubscriptionTruth(kwargs["doubanid"]) && meta.Name() == "" {
		return nil, nil
	}
	parser, err := a.originalHostMediaSourceParser(ctx)
	if err != nil {
		return nil, err
	}
	kind := sourceText(kwargs["mtype"])
	if kind == "" {
		kind = meta.Text("type")
	}
	if !sourceSubscriptionTruth(kwargs["doubanid"]) {
		if sourceSubscriptionTruth(kwargs["mtype"]) {
			meta.Set("type", kwargs["mtype"])
		}
		names, err := originalHostMediaSearchNames(meta)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			matched, err := a.originalHostDoubanRetry(ctx, func() (map[string]any, error) {
				year := ""
				if sourceSubscriptionTruth(meta.Get("year")) {
					year = originalCatalogIdentityString(meta.Get("year"))
				}
				query := legacyanime.Strip(name + " " + year)
				value, err := a.originalHostMediaProviderJSON(ctx, "douban", "get", []any{"/search/weixin", query, 0, 20, a.originalHostDouban.searchStamp}, false, func() (json.RawMessage, error) {
					return explorer.SourceDoubanSearch(ctx, query, a.originalHostDouban.searchStamp)
				})
				if err != nil {
					return nil, err
				}
				fields, err := originalHostMediaSourceMapping(value, true)
				if err != nil {
					return nil, err
				}
				return originalHostMediaDoubanMatch(fields, name, kind, meta.Get("year"), meta.Get("begin_season"), parser)
			})
			if err != nil {
				return nil, err
			}
			if len(matched) == 0 {
				continue
			}
			info, err := a.originalHostDoubanLimited(ctx, false, func() (map[string]any, error) {
				return a.originalHostMediaDoubanDetail(ctx, explorer, originalCatalogIdentityString(matched["id"]), kind)
			})
			if err != nil {
				return nil, err
			}
			if len(info) > 0 {
				return originalHostMediaDoubanModel(info, parser)
			}
		}
		return nil, nil
	}
	info, err := a.originalHostDoubanLimited(ctx, false, func() (map[string]any, error) { return a.originalHostMediaDoubanDetail(ctx, explorer, id, kind) })
	if err != nil || len(info) == 0 {
		return nil, err
	}
	return originalHostMediaDoubanModel(info, parser)
}

func (a *App) originalHostMediaDoubanDetail(ctx context.Context, e *catalog.Explorer, id, kind string) (map[string]any, error) {
	kinds := []string{"movie", "tv"}
	if kind == "电影" {
		kinds = []string{"movie"}
	} else if kind == "电视剧" {
		kinds = []string{"tv"}
	}
	for _, nativeKind := range kinds {
		value, err := a.originalHostMediaProviderJSON(ctx, "douban", "get", []any{"/" + nativeKind + "/" + id}, false, func() (json.RawMessage, error) { return e.SourceDoubanInformation(ctx, nativeKind, id, false) })
		if err != nil {
			return nil, err
		}
		info, err := originalHostMediaSourceMapping(value, true)
		if err != nil {
			return nil, err
		}
		if len(info) == 0 {
			continue
		}
		if strings.Contains(sourceText(info["msg"]), "subject_ip_rate_limit") {
			return nil, bad("MEDIA_RATE_LIMITED", "豆瓣请求受到频率限制")
		}
		people, peopleErr := a.originalHostMediaProviderJSON(ctx, "douban", "get", []any{"/" + nativeKind + "/" + id + "/celebrities"}, false, func() (json.RawMessage, error) { return e.SourceDoubanInformation(ctx, nativeKind, id, true) })
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if peopleErr != nil {
			return nil, peopleErr
		}
		rows, err := originalHostMediaSourceMapping(people, true)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			info["directors"], info["actors"] = rows["directors"], rows["actors"]
		}
		return info, nil
	}
	return nil, nil
}

func originalHostMediaDoubanMatch(result map[string]any, name, kind string, year, season any, parser *releasemeta.MetaInfoParser) (map[string]any, error) {
	for _, v := range result {
		if text, ok := v.(string); ok && text == "search_access_rate_limit" {
			return nil, bad("MEDIA_RATE_LIMITED", "豆瓣请求受到频率限制")
		}
	}
	items, err := originalHostMediaSourceSequence(result["items"])
	if err != nil {
		return nil, err
	}
	for _, v := range items {
		row, err := originalHostMediaSourceMapping(v, false)
		if err != nil {
			return nil, err
		}
		typ := sourceText(row["type_name"])
		if typ != "电影" && typ != "电视剧" || kind != "" && kind != typ {
			continue
		}
		if kind == "电视剧" && season == nil {
			season = json.Number("1")
		}
		item, err := originalHostMediaSourceMapping(row["target"], false)
		if err != nil {
			return nil, err
		}
		title := sourceText(item["title"])
		if title == "" {
			continue
		}
		meta, err := parser.ParseTitle(title, nil, nil)
		if err != nil {
			return nil, err
		}
		if typ == "电视剧" {
			meta.Set("type", "电视剧")
			if meta.Get("begin_season") == nil {
				meta.Set("begin_season", json.Number("1"))
			}
		}
		if meta.Name() == name && originalCatalogIdentityString(meta.Get("begin_season")) == originalCatalogIdentityString(season) && (!sourceSubscriptionTruth(year) || originalBaseValueEqual(item["year"], year)) {
			return item, nil
		}
	}
	return nil, nil
}

var originalHostDoubanDate = regexp.MustCompile(`\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2}`)
var originalHostDoubanYear = regexp.MustCompile(`\p{Nd}{4}`)
var originalHostDoubanDuration = regexp.MustCompile(`\p{Nd}+`)
var originalHostDoubanAlias = regexp.MustCompile(`\([港台豆友译名]+\)`)
var originalHostDoubanCover = regexp.MustCompile(`imageView2/\p{Nd}/q/\p{Nd}+/w/\p{Nd}+/h/\p{Nd}+/format/webp`)

// Fixed context.py:604-744, initialized from MediaInfo's source defaults.
func originalHostMediaDoubanModel(info map[string]any, parser *releasemeta.MetaInfoParser) (json.RawMessage, error) {
	model, _ := rsscontext.NewMediaInfo(nil)
	f := model.Fields
	f["source"], f["douban_id"] = "douban", originalCatalogIdentityString(info["id"])
	switch {
	case info["media_type"] == rsscontext.MediaType("电影") || info["media_type"] == rsscontext.MediaType("电视剧"):
		f["type"] = string(info["media_type"].(rsscontext.MediaType))
	case sourceSubscriptionTruth(info["subtype"]):
		f["type"] = originalHostDoubanType(info["subtype"] == "movie")
	case sourceSubscriptionTruth(info["target_type"]):
		f["type"] = originalHostDoubanType(info["target_type"] == "movie")
	case sourceSubscriptionTruth(info["type_name"]):
		f["type"] = info["type_name"]
	case sourceSubscriptionTruth(info["uri"]):
		f["type"] = originalHostDoubanType(strings.Contains(sourceText(info["uri"]), "/movie/"))
	case info["type"] == "movie" || info["type"] == "tv":
		f["type"] = originalHostDoubanType(info["type"] == "movie")
	}
	f["title"], f["en_title"], f["original_title"] = info["title"], info["original_title"], info["original_title"]
	if year, ok := info["year"].(string); ok && year != "" {
		r := []rune(year)
		if len(r) > 4 {
			r = r[:4]
		}
		f["year"] = string(r)
	} else if sourceSubscriptionTruth(info["year"]) {
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "豆瓣年份格式无效")
	}
	if !sourceSubscriptionTruth(f["year"]) {
		f["year"] = sourceMap(info["extra"])["year"]
	}
	meta, err := parser.ParseTitle(sourceText(info["title"]), nil, nil)
	if err != nil {
		return nil, err
	}
	f["season"] = meta.Get("begin_season")
	if f["season"] != nil {
		f["type"] = "电视剧"
	} else if !sourceSubscriptionTruth(f["type"]) {
		f["type"] = "电影"
	}
	f["vote_average"] = float64(0)
	if rating := sourceMap(info["rating"]); len(rating) > 0 {
		n, err := strconv.ParseFloat(originalCatalogIdentityString(rating["value"]), 64)
		if err != nil {
			return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "豆瓣评分格式无效")
		}
		f["vote_average"] = n
	}
	if sourceSubscriptionTruth(info["release_date"]) {
		f["release_date"] = info["release_date"]
	} else if dates := sourceRows(info["pubdate"]); len(dates) > 0 {
		if match := originalHostDoubanDate.FindString(sourceText(dates[0])); match != "" {
			f["release_date"] = match
		}
	}
	f["poster_path"] = sourceMap(info["pic"])["large"]
	if !sourceSubscriptionTruth(f["poster_path"]) && sourceSubscriptionTruth(info["cover_url"]) {
		f["poster_path"] = originalHostDoubanCover.ReplaceAllString(sourceText(info["cover_url"]), "imageView2/1/w/500/h/750/format/webp")
	}
	if !sourceSubscriptionTruth(f["poster_path"]) {
		cover := sourceMap(info["cover"])
		f["poster_path"] = cover["url"]
		if !sourceSubscriptionTruth(f["poster_path"]) {
			f["poster_path"] = sourceMap(cover["large"])["url"]
		}
	}
	f["overview"] = info["intro"]
	if !sourceSubscriptionTruth(f["overview"]) {
		f["overview"] = info["card_subtitle"]
	}
	if !sourceSubscriptionTruth(f["overview"]) {
		f["overview"] = ""
		var parts []string
		for _, row := range sourceRows(sourceMap(info["extra"])["info"]) {
			var pair []string
			for _, v := range sourceRows(row) {
				pair = append(pair, sourceText(v))
			}
			parts = append(parts, strings.Join(pair, "："))
		}
		if len(parts) > 0 {
			f["overview"] = strings.Join(parts, "，")
		}
	}
	if !sourceSubscriptionTruth(f["year"]) {
		if year := originalHostDoubanYear.FindString(sourceText(f["overview"])); year != "" {
			f["year"] = year
		}
	}
	for _, key := range []string{"directors", "actors"} {
		if sourceSubscriptionTruth(info[key]) {
			f[key] = info[key]
		}
	}
	for _, alias := range sourceRows(info["aka"]) {
		f["names"] = append(sourceRows(f["names"]), originalHostDoubanAlias.ReplaceAllString(sourceText(alias), ""))
	}
	if f["type"] == "电视剧" {
		season := f["season"]
		if season == nil {
			season = json.Number("1")
		}
		key := originalCatalogIdentityString(season)
		f["season_years"] = map[string]any{key: f["year"]}
		if sourceSubscriptionTruth(info["episodes_count"]) {
			episodes, err := originalHostMediaEpisodeList(info["episodes_count"])
			if err != nil {
				return nil, err
			}
			f["seasons"] = map[string]any{key: episodes}
		}
	}
	for _, g := range sourceRows(info["genres"]) {
		f["genres"] = append(sourceRows(f["genres"]), map[string]any{"id": g, "name": g})
	}
	if durations := sourceRows(info["durations"]); len(durations) > 0 {
		if n := originalHostDoubanDuration.FindString(sourceText(durations[0])); n != "" {
			integer, err := legacyanime.Integer(n)
			if err != nil {
				return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "豆瓣片长格式无效")
			}
			f["runtime"] = json.Number(integer.String())
		}
	}
	for _, c := range sourceRows(info["countries"]) {
		f["production_countries"] = append(sourceRows(f["production_countries"]), map[string]any{"id": c, "name": c})
	}
	for key, value := range info {
		current, exists := f[key]
		if exists && sourceSubscriptionTruth(value) && !sourceSubscriptionTruth(current) && (current == nil || sourceJSONType(current) == sourceJSONType(value)) {
			f[key] = value
		}
	}
	return originalHostMediaSourceSerialize(f)
}

func originalHostDoubanType(movie bool) string {
	if movie {
		return "电影"
	}
	return "电视剧"
}

func originalHostMediaEpisodeList(value any) ([]any, error) {
	// context.py adds one and passes the value directly to range; unlike ID
	// coercion, numeric strings and integral-looking floats raise TypeError.
	switch number := value.(type) {
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, *big.Int:
	case json.Number:
		if strings.ContainsAny(string(number), ".eE") {
			return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "剧集总数须为整数")
		}
	default:
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "剧集总数须为整数")
	}
	n, err := originalCatalogRecognitionInteger(value)
	if err != nil {
		return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "剧集总数格式无效")
	}
	if strings.HasPrefix(string(n), "-") {
		return []any{}, nil
	}
	count, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || count > (plugins.MaxPayloadBytes-1)/2 {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "媒体剧集数量超过SDK响应上限")
	}
	if count == 0 {
		return []any{}, nil
	}
	// Count the exact JSON array bytes before allocation. The full media model
	// still passes through the existing SDK payload check after serialization.
	bytes := count + 1
	for first, width := int64(1), int64(1); first <= count; first, width = first*10, width+1 {
		last := first*10 - 1
		if last > count {
			last = count
		}
		bytes += (last - first + 1) * width
	}
	if bytes > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "媒体剧集数量超过SDK响应上限")
	}
	rows := make([]any, int(count))
	for i := range rows {
		rows[i] = int64(i + 1)
	}
	return rows, nil
}
