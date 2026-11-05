package app

import (
	"context"
	"encoding/json"
	"golang.org/x/text/cases"
	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/rsscontext"
	"strconv"
	"time"
)

func originalHostMediaBangumiType(info map[string]any) string {
	p := cases.Fold().String(legacyanime.Strip(sourceText(info["platform"])))
	return originalHostDoubanType(p == "movie" || p == "电影" || p == "剧场版")
}

func originalHostMediaBangumiMatches(meta *releasemeta.MetaObject, info map[string]any) bool {
	kind := meta.Text("type")
	if (kind == "电影" || kind == "电视剧") && kind != originalHostMediaBangumiType(info) {
		return false
	}
	date := sourceText(info["date"])
	if date == "" {
		date = sourceText(info["air_date"])
	}
	year := meta.Get("year")
	if date == "" || !sourceSubscriptionTruth(year) {
		return true
	}
	r := []rune(date)
	if len(r) > 4 {
		r = r[:4]
	}
	return string(r) == originalCatalogIdentityString(year)
}

func (a *App) originalHostMediaRecognizeBangumi(ctx context.Context, e *catalog.Explorer, kwargs map[string]any) (json.RawMessage, error) {
	if !sourceSubscriptionTruth(kwargs["bangumiid"]) && sourceMap(kwargs["meta"]) == nil {
		return nil, nil
	}
	meta := originalHostMediaMeta(sourceMap(kwargs["meta"]))
	var info map[string]any
	get := func(id any, characters bool) (any, error) {
		stamp := time.Now().Format("20060102")
		identity := originalCatalogIdentityString(id)
		path := "/v0/subjects/" + identity
		if characters {
			path += "/characters"
		}
		return a.originalHostMediaProviderJSON(ctx, "bangumi", "get", []any{path, stamp}, false, func() (json.RawMessage, error) { return e.SourceBangumiInformation(ctx, identity, stamp, characters) })
	}
	detail := func(id any) (map[string]any, error) {
		value, err := get(id, false)
		if err != nil {
			return nil, err
		}
		return originalHostMediaSourceMapping(value, true)
	}
	var err error
	if sourceSubscriptionTruth(kwargs["bangumiid"]) {
		info, err = detail(kwargs["bangumiid"])
	} else {
		value, requestErr := a.originalHostMediaProviderJSON(ctx, "bangumi", "get", []any{"/search/subject/" + meta.Name()}, false, func() (json.RawMessage, error) { return e.SourceBangumiSearch(ctx, meta.Name()) })
		if requestErr != nil {
			return nil, requestErr
		}
		search, err := originalHostMediaSourceMapping(value, true)
		if err != nil {
			return nil, err
		}
		rows, err := originalHostMediaSourceSequence(search["list"])
		if err != nil {
			return nil, err
		}
		if len(rows) > 10 {
			rows = rows[:10]
		}
		for _, row := range rows {
			candidate, err := originalHostMediaSourceMapping(row, false)
			if err != nil {
				return nil, err
			}
			if !sourceSubscriptionTruth(candidate["id"]) {
				continue
			}
			info, err = detail(candidate["id"])
			if err != nil {
				return nil, err
			}
			if len(info) > 0 && originalHostMediaBangumiMatches(meta, info) {
				break
			}
			info = nil
		}
	}
	if err != nil || len(info) == 0 {
		return nil, err
	}
	actors := []any{}
	value, requestErr := get(info["id"], true)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if requestErr == nil {
		rows, err := originalHostMediaSourceSequence(value)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			character, err := originalHostMediaSourceMapping(row, false)
			if err != nil {
				return nil, err
			}
			if !sourceSubscriptionTruth(character["id"]) {
				continue
			}
			cast, err := originalHostMediaSourceSequence(character["actors"])
			if err != nil {
				return nil, err
			}
			if sourceSubscriptionTruth(character["id"]) && len(cast) > 0 && sourceSubscriptionTruth(cast[0]) {
				actor := sourceMap(cast[0])
				if actor == nil {
					return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "Bangumi演职员格式无效")
				}
				actor["career"] = []any{character["name"]}
				actors = append(actors, actor)
			}
		}
	}
	info["actors"] = actors
	parser, err := a.originalHostMediaSourceParser(ctx)
	if err != nil {
		return nil, err
	}
	fields, err := originalHostMediaBangumiFields(info, parser)
	if err != nil {
		return nil, err
	}
	if meta.Get("begin_season") != nil {
		fields["season"] = meta.Get("begin_season")
	}
	return originalHostMediaSourceSerialize(fields)
}

func originalHostMediaBangumiFields(info map[string]any, parser *releasemeta.MetaInfoParser) (map[string]any, error) {
	model, _ := rsscontext.NewMediaInfo(nil)
	f := model.Fields
	f["source"], f["bangumi_id"], f["type"] = "bangumi", info["id"], originalHostMediaBangumiType(info)
	f["title"] = info["name_cn"]
	if !sourceSubscriptionTruth(f["title"]) {
		f["title"] = info["name"]
	}
	f["original_title"] = info["name"]
	meta, err := parser.ParseTitle(sourceText(f["title"]), nil, nil)
	if err != nil {
		return nil, err
	}
	f["season"] = meta.Get("begin_season")
	f["vote_average"] = float64(0)
	if rating := sourceMap(info["rating"]); len(rating) > 0 {
		n, err := strconv.ParseFloat(originalCatalogIdentityString(rating["score"]), 64)
		if err != nil {
			return nil, bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "Bangumi评分格式无效")
		}
		f["vote_average"] = n
	}
	f["release_date"] = info["date"]
	if !sourceSubscriptionTruth(f["release_date"]) {
		f["release_date"] = info["air_date"]
	}
	if date := sourceText(f["release_date"]); date != "" {
		r := []rune(date)
		if len(r) > 4 {
			r = r[:4]
		}
		f["year"] = string(r)
	}
	f["poster_path"] = sourceMap(info["images"])["large"]
	if !sourceSubscriptionTruth(f["poster_path"]) {
		f["poster_path"] = info["image"]
	}
	f["overview"] = info["summary"]
	for _, row := range sourceRows(info["infobox"]) {
		item := sourceMap(row)
		if item["key"] != "别名" {
			continue
		}
		switch aliases := item["value"].(type) {
		case string:
			f["names"] = []any{aliases}
		case []any:
			names := []any{}
			for _, alias := range aliases {
				if value, ok := alias.(map[string]any); ok {
					names = append(names, value["v"])
				} else {
					names = append(names, alias)
				}
			}
			f["names"] = names
		}
		break
	}
	count := info["total_episodes"]
	if !sourceSubscriptionTruth(count) {
		count = info["eps"]
	}
	if f["type"] == "电视剧" && sourceSubscriptionTruth(count) {
		episodes, err := originalHostMediaEpisodeList(count)
		if err != nil {
			return nil, err
		}
		season := f["season"]
		if season == nil {
			season = json.Number("1")
		}
		f["seasons"] = map[string]any{originalCatalogIdentityString(season): episodes}
		f["number_of_episodes"], f["number_of_seasons"] = count, 1
	}
	for _, row := range sourceRows(info["tags"]) {
		tag := sourceMap(row)
		if sourceSubscriptionTruth(tag["name"]) {
			f["genres"] = append(sourceRows(f["genres"]), map[string]any{"id": tag["name"], "name": tag["name"]})
		}
	}
	for _, row := range sourceRows(info["infobox"]) {
		item := sourceMap(row)
		target := ""
		switch item["key"] {
		case "动画制作", "制作":
			target = "production_companies"
		case "导演":
			target = "directors"
		}
		if target == "" {
			continue
		}
		values, ok := item["value"].([]any)
		if !ok {
			values = []any{item["value"]}
		}
		for _, v := range values {
			if !sourceSubscriptionTruth(v) {
				continue
			}
			name := v
			if fields, ok := v.(map[string]any); ok {
				name = fields["v"]
			}
			f[target] = append(sourceRows(f[target]), map[string]any{"name": name})
		}
	}
	if sourceSubscriptionTruth(info["actors"]) {
		f["actors"] = info["actors"]
	}
	return f, nil
}
