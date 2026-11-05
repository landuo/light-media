package app

import (
	"context"
	"encoding/json"
	"math/big"
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/sourcezhconv"
)

// StringUtils.clear plus Python str.upper, rather than substring matching.
func originalHostMediaClearName(text string) string {
	var out strings.Builder
	for _, r := range text {
		if strings.ContainsRune("、.。,，·:：;；!！?？'’\"“”()（）[]【】「」-—―+|\\_/&#～~", r) || r >= 0x200b && r <= 0x200d || r == 0xfeff || strings.ContainsRune("\t\n\v\f\r\x1c\x1d\x1e\x1f \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000", r) {
			continue
		}
		out.WriteRune(r)
	}
	return cases.Upper(language.Und).String(out.String())
}

func originalHostMediaCompareNames(name string, value any) bool {
	if name == "" || !sourceSubscriptionTruth(value) {
		return false
	}
	rows, ok := value.([]any)
	if !ok {
		rows = []any{value}
	}
	left := originalHostMediaClearName(name)
	for _, row := range rows {
		if text, ok := row.(string); ok && left == originalHostMediaClearName(text) {
			return true
		}
	}
	return false
}

func originalHostMediaSearchNames(meta *releasemeta.MetaObject) ([]string, error) {
	cn, en := meta.Text("cn_name"), meta.Text("en_name")
	zh := ""
	if cn != "" {
		var err error
		zh, err = sourcezhconv.Hans(cn)
		if err != nil {
			return nil, err
		}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, name := range []string{cn, zh, en} {
		if name != "" && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	return out, nil
}

func originalHostMediaInfoKind(info map[string]any) string {
	switch info["media_type"] {
	case "movie", "电影":
		return "movie"
	case "tv", "电视剧":
		return "tv"
	}
	return ""
}

func (a *App) originalHostMediaTMDBInfo(ctx context.Context, kind string, id any) (map[string]any, error) {
	n, err := originalCatalogRecognitionInteger(id)
	if err != nil {
		return nil, nil
	}
	number, err := legacyanime.Integer(string(n))
	if err != nil || !number.IsInt64() {
		return nil, nil
	}
	key := a.originalHostMediaTMDBProviderKey([]any{"detail", kind, number.String()})
	raw, err := a.originalHostMediaTMDBResponse(ctx, kind, key, func() (catalog.SourceRecognitionResponse, error) {
		return a.catalog().SourceRecognitionInformationResponse(ctx, kind, number.Int64(), a.originalHostMediaEffectiveLocale())
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	if value, decodeErr := decodeSourceJSON(raw); decodeErr != nil || len(sourceMap(value)) == 0 {
		return nil, nil
	}
	projected, err := sourceTMDBMediaInfoLocale(raw, kind, a.Settings().TMDBImageDomain, "", a.originalHostMediaEffectiveLocale())
	if err != nil {
		return nil, err
	}
	value, err := decodeSourceJSON(projected)
	if err != nil {
		return nil, err
	}
	info := sourceMap(sourceMap(value)["tmdb_info"])
	// MediaInfo consumes episode_groups while building the final model. This
	// helper represents TmdbApi.get_info, before that consumer runs.
	if wire, err := decodeSourceJSON(raw); err == nil {
		if groups, present := sourceMap(wire)["episode_groups"]; present {
			info["episode_groups"] = groups
		}
	}
	return info, nil
}

func (a *App) originalHostMediaTMDBInfoByType(ctx context.Context, mtype, id any) (map[string]any, error) {
	if mtype == "电影" {
		return a.originalHostMediaTMDBInfo(ctx, "movie", id)
	}
	if mtype == "电视剧" {
		return a.originalHostMediaTMDBInfo(ctx, "tv", id)
	}
	tv, err := a.originalHostMediaTMDBInfo(ctx, "tv", id)
	if err != nil {
		return nil, err
	}
	movie, err := a.originalHostMediaTMDBInfo(ctx, "movie", id)
	if err != nil {
		return nil, err
	}
	if len(tv) > 0 && len(movie) > 0 {
		return nil, nil
	}
	if len(tv) > 0 {
		return tv, nil
	}
	return movie, nil
}

func (a *App) originalHostMediaSearchRows(ctx context.Context, kind, name string, year any) ([]map[string]any, error) {
	yearText := ""
	if sourceSubscriptionTruth(year) {
		yearText = originalCatalogIdentityString(year)
	}
	key := a.originalHostMediaTMDBProviderKey([]any{"search", kind, name, yearText})
	raw, err := a.originalHostMediaTMDBResponse(ctx, "search", key, func() (catalog.SourceRecognitionResponse, error) {
		return a.catalog().SourceRecognitionSearchResponse(ctx, kind, name, yearText, a.originalHostMediaEffectiveLocale())
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return nil, nil
	}
	rows := []map[string]any{}
	for _, item := range sourceRows(sourceMap(value)["results"]) {
		if sourceMap(item) == nil {
			return nil, nil
		}
		rows = append(rows, sourceMap(item))
	}
	return rows, nil
}

func originalHostMediaSortRows(rows []map[string]any, kind string) {
	key := func(row map[string]any) string {
		field := "release_date"
		if kind == "tv" {
			field = "first_air_date"
		}
		date := sourceText(row[field])
		if kind == "multi" && date == "" {
			date = sourceText(row["first_air_date"])
		}
		if date == "" {
			date = "0000-00-00"
		}
		if kind == "multi" {
			if row["media_type"] == "movie" {
				date = "1" + date
			} else {
				date = "0" + date
			}
		}
		return date
	}
	sort.SliceStable(rows, func(i, j int) bool { return key(rows[i]) > key(rows[j]) })
}

func originalHostMediaTitleMatches(name, kind string, row map[string]any) bool {
	if kind == "movie" {
		return originalHostMediaCompareNames(name, row["title"]) || originalHostMediaCompareNames(name, row["original_title"])
	}
	if kind == "tv" {
		return originalHostMediaCompareNames(name, row["name"]) || originalHostMediaCompareNames(name, row["original_name"])
	}
	return false
}

func (a *App) originalHostMediaMatchByName(ctx context.Context, name, kind string, year any) (map[string]any, error) {
	rows, err := a.originalHostMediaSearchRows(ctx, kind, name, year)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, nil
	}
	originalHostMediaSortRows(rows, kind)
	if sourceSubscriptionTruth(year) {
		field := "release_date"
		if kind == "tv" {
			field = "first_air_date"
		}
		filtered := []map[string]any{}
		for _, row := range rows {
			date := sourceText(row[field])
			if date[:min(4, len(date))] == sourceText(year) {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	for _, row := range rows {
		if originalHostMediaTitleMatches(name, kind, row) {
			return row, nil
		}
	}
	for _, row := range rows {
		if !sourceSubscriptionTruth(row["names"]) {
			row, err = a.originalHostMediaTMDBInfo(ctx, kind, row["id"])
			if err != nil {
				return nil, err
			}
		}
		if len(row) > 0 && originalHostMediaCompareNames(name, row["names"]) {
			return row, nil
		}
	}
	return map[string]any{}, nil
}

func (a *App) originalHostMediaMatchMulti(ctx context.Context, name string) (map[string]any, error) {
	rows, err := a.originalHostMediaSearchRows(ctx, "multi", name, nil)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, nil
	}
	originalHostMediaSortRows(rows, "multi")
	for _, row := range rows {
		kind := originalHostMediaInfoKind(row)
		if originalHostMediaTitleMatches(name, kind, row) {
			if kind == "movie" {
				row["media_type"] = "电影"
			} else {
				row["media_type"] = "电视剧"
			}
			return row, nil
		}
	}
	for _, row := range rows {
		kind := originalHostMediaInfoKind(row)
		if kind == "" {
			continue
		}
		if !sourceSubscriptionTruth(row["names"]) {
			row, err = a.originalHostMediaTMDBInfo(ctx, kind, row["id"])
			if err != nil {
				return nil, err
			}
		}
		if len(row) > 0 && originalHostMediaCompareNames(name, row["names"]) {
			if kind == "movie" {
				row["media_type"] = "电影"
			} else {
				row["media_type"] = "电视剧"
			}
			return row, nil
		}
	}
	return map[string]any{}, nil
}

func originalHostMediaSeasonMatches(info map[string]any, year string, number any, groups []map[string]any) bool {
	if len(info) == 0 {
		return false
	}
	if len(groups) > 0 {
		for _, group := range groups {
			left, _ := json.Marshal(group["order"])
			right, _ := json.Marshal(number)
			if !originalBaseJSONEqual(left, right) {
				continue
			}
			episodes := sourceRows(group["episodes"])
			if len(episodes) == 0 {
				continue
			}
			date := sourceText(sourceMap(episodes[0])["air_date"])
			if originalHostMediaGroupDate.MatchString(date) && date[:4] == year {
				return true
			}
		}
		return false
	}
	n, err := originalCatalogRecognitionInteger(number)
	if err != nil {
		return false
	}
	// Source maps by season_number first, so a duplicate key uses the last row.
	seasons := map[string]map[string]any{}
	for _, value := range sourceRows(info["seasons"]) {
		row := sourceMap(value)
		if row["season_number"] != nil {
			seasons[originalCatalogIdentityString(row["season_number"])] = row
		}
	}
	if row := seasons[string(n)]; row != nil {
		date := sourceText(row["air_date"])
		return date != "" && date[:min(4, len(date))] == year
	}
	return false
}

func (a *App) originalHostMediaMatchSeason(ctx context.Context, name, year string, number any, groups []map[string]any) (map[string]any, error) {
	rows, err := a.originalHostMediaSearchRows(ctx, "tv", name, nil)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, nil
	}
	originalHostMediaSortRows(rows, "tv")
	for _, row := range rows {
		date := sourceText(row["first_air_date"])
		showYear := date[:min(4, len(date))]
		if originalHostMediaTitleMatches(name, "tv", row) && showYear == year {
			return row, nil
		}
		if !sourceSubscriptionTruth(row["names"]) {
			row, err = a.originalHostMediaTMDBInfo(ctx, "tv", row["id"])
			if err != nil {
				return nil, err
			}
		}
		if len(row) == 0 || !(originalHostMediaTitleMatches(name, "tv", row) || originalHostMediaCompareNames(name, row["names"])) {
			continue
		}
		if showYear == year || originalHostMediaSeasonMatches(row, year, number, groups) {
			return row, nil
		}
	}
	return map[string]any{}, nil
}

func (a *App) originalHostMediaMatch(ctx context.Context, name, kind string, year, seasonYear, season any, groups []map[string]any) (map[string]any, error) {
	var result map[string]any
	var err error
	if kind == "tv" && sourceSubscriptionTruth(seasonYear) && season != nil {
		result, err = a.originalHostMediaMatchSeason(ctx, name, originalCatalogIdentityString(seasonYear), season, groups)
		if err != nil {
			return nil, err
		}
	}
	if len(result) == 0 {
		years := []any{year}
		if sourceSubscriptionTruth(year) {
			n, err := legacyanime.Integer(originalCatalogIdentityString(year))
			if err != nil {
				return nil, err
			}
			years = append(years, new(big.Int).Add(n, big.NewInt(1)).String(), new(big.Int).Sub(n, big.NewInt(1)).String())
		}
		for _, value := range years {
			result, err = a.originalHostMediaMatchByName(ctx, name, kind, value)
			if err != nil {
				return nil, err
			}
			if len(result) > 0 {
				break
			}
		}
	}
	if len(result) > 0 {
		if kind == "movie" {
			result["media_type"] = "电影"
		} else {
			result["media_type"] = "电视剧"
		}
	}
	return result, nil
}

func (a *App) originalHostMediaSearchByName(ctx context.Context, name string, meta *releasemeta.MetaObject, groups []map[string]any) (map[string]any, error) {
	year := meta.Get("year")
	kind := meta.Text("type")
	if kind == "未知" && !sourceSubscriptionTruth(year) {
		return a.originalHostMediaMatchMulti(ctx, name)
	}
	if kind == "电视剧" {
		info, err := a.originalHostMediaMatch(ctx, name, "tv", year, year, meta.Get("begin_season"), groups)
		if err != nil {
			return nil, err
		}
		if len(info) == 0 {
			return a.originalHostMediaMatch(ctx, name, "tv", nil, nil, nil, nil)
		}
		return info, nil
	}
	info, err := a.originalHostMediaMatch(ctx, name, "movie", year, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if len(info) == 0 {
		info, err = a.originalHostMediaMatch(ctx, name, "tv", year, nil, nil, groups)
		if err != nil {
			return nil, err
		}
	}
	if len(info) == 0 {
		return a.originalHostMediaMatchMulti(ctx, name)
	}
	return info, nil
}

func (a *App) originalHostMediaBuildTMDBResult(ctx context.Context, info map[string]any, group string, groups []map[string]any, cacheHit bool) (json.RawMessage, error) {
	kind := originalHostMediaInfoKind(info)
	if kind == "" {
		return nil, nil
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil {
		return nil, err
	}
	id, err := originalCatalogRecognitionInteger(info["id"])
	if err != nil {
		return nil, err
	}
	number, _ := legacyanime.Integer(string(id))
	// Reuse the existing host projection/category policy without a second HTTP
	// lookup. The source matcher has already selected its precise identity.
	provider := map[string]any{}
	for key, value := range info {
		provider[key] = value
	}
	provider["media_type"] = kind
	providerRaw, err := json.Marshal(provider)
	if err != nil {
		return nil, err
	}
	media, err := a.catalog().ProjectSourceInformation(kind, number.Int64(), providerRaw, policy.extraFields(kind))
	if err != nil {
		return nil, err
	}
	category, err := policy.categoryWithError(media)
	if err != nil {
		return nil, err
	}
	model, err := sourceTMDBMediaModel(raw, kind, a.Settings().TMDBImageDomain, category)
	if err != nil {
		return nil, err
	}
	value, err := decodeSourceJSON(model)
	if err != nil {
		return nil, err
	}
	fields := sourceMap(value)
	fields["recognize_cache_hit"] = cacheHit
	fields["poster"], fields["backdrop"] = "", ""
	for target, source := range map[string]string{"poster": "poster_path", "backdrop": "backdrop_path"} {
		if path, ok := fields[source].(string); ok && path != "" {
			fields[target] = strings.ReplaceAll(path, "original", "w500")
		}
	}
	model, err = json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return a.originalHostMediaProcessGroups(ctx, model, group, groups)
}

func (a *App) originalHostMediaRecognizeTMDB(ctx context.Context, kwargs map[string]any) (json.RawMessage, error) {
	var meta *releasemeta.MetaObject
	if value := sourceMap(kwargs["meta"]); len(value) > 0 {
		meta = originalHostMediaMeta(value)
	}
	id := kwargs["tmdbid"]
	if !sourceSubscriptionTruth(id) && (meta == nil || meta.Name() == "") {
		return nil, nil
	}
	if meta != nil {
		if sourceSubscriptionTruth(kwargs["mtype"]) {
			meta.Set("type", kwargs["mtype"])
		}
		if sourceSubscriptionTruth(id) {
			meta.Set("tmdbid", id)
		}
	}
	key := ""
	var cached map[string]any
	var err error
	if meta != nil {
		key = originalHostMediaCacheKey(meta, a.originalHostMediaRecognition.Locale)
		if kwargs["cache"] != false {
			cached, err = a.originalHostMediaCacheGet(ctx, key)
			if err != nil {
				return nil, err
			}
		}
	}
	group := sourceText(kwargs["episode_group"])
	groups := []map[string]any{}
	if group != "" {
		groups, err = a.originalHostMediaGroupSeasons(ctx, group)
		if err != nil {
			return nil, err
		}
	}
	var info map[string]any
	cacheHit := len(cached) > 0
	if cacheHit {
		if sourceSubscriptionTruth(cached["title"]) {
			info, err = a.originalHostMediaTMDBInfoByType(ctx, cached["type"], cached["id"])
			if err != nil {
				return nil, err
			}
		}
	} else {
		if sourceSubscriptionTruth(id) {
			if sourceSubscriptionTruth(kwargs["mtype"]) {
				info, err = a.originalHostMediaTMDBInfoByType(ctx, kwargs["mtype"], id)
			} else {
				tv, e := a.originalHostMediaTMDBInfo(ctx, "tv", id)
				if e != nil {
					return nil, e
				}
				movie, e := a.originalHostMediaTMDBInfo(ctx, "movie", id)
				if e != nil {
					return nil, e
				}
				if len(tv) > 0 && len(movie) > 0 {
					tvRaw, _ := json.Marshal(tv)
					movieRaw, _ := json.Marshal(movie)
					var fields map[string]any
					if meta != nil {
						fields = meta.Instance
					}
					switch originalHostMediaDisambiguate(tvRaw, movieRaw, fields) {
					case "tv":
						info = tv
					case "movie":
						info = movie
					}
				} else if len(tv) > 0 {
					info = tv
				} else {
					info = movie
				}
			}
			if err != nil {
				return nil, err
			}
		}
		if len(info) == 0 && meta != nil && !sourceSubscriptionTruth(id) {
			names, e := originalHostMediaSearchNames(meta)
			if e != nil {
				return nil, e
			}
			for _, name := range names {
				info, err = a.originalHostMediaSearchByName(ctx, name, meta, groups)
				if err != nil {
					return nil, err
				}
				if len(info) > 0 {
					break
				}
			}
			if len(info) > 0 && !sourceSubscriptionTruth(info["genres"]) {
				info, err = a.originalHostMediaTMDBInfo(ctx, originalHostMediaInfoKind(info), info["id"])
				if err != nil {
					return nil, err
				}
			}
		} else if len(info) == 0 {
			return nil, nil
		}
		if meta != nil {
			if err := a.originalHostMediaCachePut(ctx, key, info); err != nil {
				return nil, err
			}
		}
	}
	if len(info) == 0 {
		return nil, nil
	}
	return a.originalHostMediaBuildTMDBResult(ctx, info, group, groups, cacheHit)
}
