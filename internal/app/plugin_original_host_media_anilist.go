package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/rsscontext"
)

type originalHostAniListState struct {
	mu           sync.Mutex
	proxy        bool
	translations map[string]map[string]any
	loaded       bool
}

func originalHostAniListError() error {
	return bad("SOURCE_MEDIA_PROJECTION_UNSUPPORTED", "AniList原始字段格式无效")
}
func originalHostAniListText(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		return string(x)
	default:
		return fmt.Sprint(v)
	}
}
func originalHostAniListObject(v any, allowNull bool) (map[string]any, error) {
	if allowNull && !sourceSubscriptionTruth(v) {
		return map[string]any{}, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return nil, originalHostAniListError()
}
func originalHostAniListMember(m map[string]any, key string) (map[string]any, error) {
	v, ok := m[key]
	if !ok {
		return map[string]any{}, nil
	}
	return originalHostAniListObject(v, false)
}
func originalHostAniListRows(v any) ([]any, error) {
	if !sourceSubscriptionTruth(v) {
		return []any{}, nil
	}
	if rows, ok := v.([]any); ok {
		return rows, nil
	}
	return nil, originalHostAniListError()
}
func originalHostAniListScalarKey(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "none", nil
	case string:
		return "s:" + x, nil
	case bool:
		if x {
			return "n:1", nil
		}
		return "n:0", nil
	case json.Number:
		r, ok := new(big.Rat).SetString(string(x))
		if !ok {
			return "", originalHostAniListError()
		}
		return "n:" + r.RatString(), nil
	case int:
		return "n:" + strconv.Itoa(x), nil
	case int64:
		return "n:" + strconv.FormatInt(x, 10), nil
	}
	return "", originalHostAniListError()
}

func originalHostAniListUnique(rows []any) ([]any, error) {
	seen := map[string]bool{}
	out := []any{}
	for _, v := range rows {
		key, err := originalHostAniListScalarKey(v)
		if err != nil {
			return nil, err
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, v)
		}
	}
	return out, nil
}
func originalHostAniListInject(v any, translations map[string]map[string]any) error {
	switch x := v.(type) {
	case []any:
		for _, row := range x {
			if err := originalHostAniListInject(row, translations); err != nil {
				return err
			}
		}
	case map[string]any:
		key, err := originalHostAniListScalarKey(x["id"])
		if err != nil {
			return err
		}
		translation := translations[key]
		if len(translation) > 0 {
			if title, ok := x["title"].(map[string]any); ok {
				title["chinese"] = translation["title"]
			}
			if synonyms, ok := x["synonyms"].([]any); ok {
				extra, err := originalHostAniListRows(translation["synonyms"])
				if err != nil {
					return err
				}
				merged, err := originalHostAniListUnique(append(append([]any{}, synonyms...), extra...))
				if err != nil {
					return err
				}
				x["synonyms"] = merged
			}
		}
		for _, child := range x {
			if err := originalHostAniListInject(child, translations); err != nil {
				return err
			}
		}
	}
	return nil
}

func originalHostAniListGuard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if guard, ok := ctx.Value(originalHostMediaInvocationGuardKey{}).(func(context.Context) error); ok {
		return guard(ctx)
	}
	return nil
}
func (a *App) originalHostAniListInvoke(ctx context.Context, e *catalog.Explorer, detail bool, value any) (any, error) {
	proxy, err := a.proxyConfig()
	if err != nil {
		return nil, err
	}
	key := "anilist-client:" + tokenHash(string(mustCleanSeedJSON(proxy)))
	cached, _ := a.originalHostMediaProviderCaches.LoadOrStore(key, &originalHostAniListState{proxy: true})
	state := cached.(*originalHostAniListState)
	call := func(useProxy bool) (any, bool, error) {
		if err := originalHostAniListGuard(ctx); err != nil {
			return nil, false, err
		}
		raw, err := e.SourceAniListRecognition(ctx, detail, useProxy, value)
		if current := originalHostAniListGuard(ctx); current != nil {
			return nil, false, current
		}
		if err != nil {
			return nil, false, nil
		}
		decoded, err := decodeSourceJSON(raw)
		if err != nil {
			return nil, false, nil
		}
		envelope, err := originalHostAniListObject(decoded, false)
		if err != nil {
			return nil, false, err
		}
		if sourceSubscriptionTruth(envelope["errors"]) || envelope["data"] == nil {
			return nil, false, nil
		}
		return envelope["data"], true, nil
	}
	translations := func() (map[string]map[string]any, error) {
		state.mu.Lock()
		ready, values := state.loaded, state.translations
		state.mu.Unlock()
		if ready {
			return values, nil
		}
		if err := originalHostAniListGuard(ctx); err != nil {
			return nil, err
		}
		raw, loadErr := e.SourceAniListTranslationData(ctx)
		if err := originalHostAniListGuard(ctx); err != nil {
			return nil, err
		}
		values = map[string]map[string]any{}
		if loadErr == nil {
			decoded, _ := decodeSourceJSON(raw)
			if rows, ok := decoded.([]any); ok {
				for _, row := range rows {
					m, ok := row.(map[string]any)
					if !ok || !sourceSubscriptionTruth(m["id"]) {
						continue
					}
					key, err := originalHostAniListScalarKey(m["id"])
					if err != nil {
						return nil, err
					}
					values[key] = m
				}
			}
		}
		state.mu.Lock()
		state.translations, state.loaded = values, true
		state.mu.Unlock()
		return values, nil
	}
	state.mu.Lock()
	useProxy := state.proxy
	state.mu.Unlock()
	if useProxy {
		result, ok, err := call(true)
		if err != nil {
			return nil, err
		}
		if ok {
			words, err := translations()
			if err != nil {
				return nil, err
			}
			if err = originalHostAniListInject(result, words); err != nil {
				return nil, err
			}
			return result, nil
		}
		state.mu.Lock()
		state.proxy = false
		state.mu.Unlock()
	}
	result, _, err := call(false)
	if err != nil {
		return nil, err
	}
	if sourceSubscriptionTruth(result) {
		words, err := translations()
		if err != nil {
			return nil, err
		}
		if err = originalHostAniListInject(result, words); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func originalHostAniListModuleType(info map[string]any) string {
	if info["format"] == "MOVIE" {
		return "电影"
	}
	return "电视剧"
}
func originalHostAniListMatches(meta *releasemeta.MetaObject, info map[string]any) (bool, error) {
	kind := meta.Text("type")
	if (kind == "电影" || kind == "电视剧") && originalHostAniListModuleType(info) != kind {
		return false, nil
	}
	start, err := originalHostAniListMember(info, "startDate")
	if err != nil {
		return false, err
	}
	year := start["year"]
	if !sourceSubscriptionTruth(year) {
		year = info["seasonYear"]
	}
	parsed := meta.Get("year")
	return !sourceSubscriptionTruth(parsed) || !sourceSubscriptionTruth(year) || originalHostAniListText(parsed) == originalHostAniListText(year), nil
}
func originalHostAniListEnrich(info map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range info {
		out[k] = v
	}
	actors, directors := []any{}, []any{}
	characters, err := originalHostAniListMember(info, "characters")
	if err != nil {
		return nil, err
	}
	edges, err := originalHostAniListRows(characters["edges"])
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		row, err := originalHostAniListObject(edge, false)
		if err != nil {
			return nil, err
		}
		character, err := originalHostAniListObject(row["node"], true)
		if err != nil {
			return nil, err
		}
		voices, err := originalHostAniListRows(row["voiceActors"])
		if err != nil {
			return nil, err
		}
		actor := map[string]any{}
		if len(voices) > 0 {
			actor, err = originalHostAniListObject(voices[0], false)
			if err != nil {
				return nil, err
			}
		}
		names, err := originalHostAniListMember(actor, "name")
		if err != nil {
			return nil, err
		}
		if !sourceSubscriptionTruth(names["full"]) {
			continue
		}
		cn, err := originalHostAniListMember(character, "name")
		if err != nil {
			return nil, err
		}
		name := cn["full"]
		if !sourceSubscriptionTruth(name) {
			name = cn["native"]
		}
		image, err := originalHostAniListMember(actor, "image")
		if err != nil {
			return nil, err
		}
		actors = append(actors, map[string]any{"id": actor["id"], "name": names["full"], "character": name, "avatar": map[string]any{"large": image["large"]}, "url": actor["siteUrl"]})
	}
	staff, err := originalHostAniListMember(info, "staff")
	if err != nil {
		return nil, err
	}
	edges, err = originalHostAniListRows(staff["edges"])
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		row, err := originalHostAniListObject(edge, false)
		if err != nil {
			return nil, err
		}
		role := row["role"]
		if !sourceSubscriptionTruth(role) {
			role = ""
		}
		text, ok := role.(string)
		if !ok {
			return nil, originalHostAniListError()
		}
		if !strings.Contains(text, "Director") {
			continue
		}
		person, err := originalHostAniListObject(row["node"], true)
		if err != nil {
			return nil, err
		}
		name, err := originalHostAniListMember(person, "name")
		if err != nil {
			return nil, err
		}
		image, err := originalHostAniListMember(person, "image")
		if err != nil {
			return nil, err
		}
		directors = append(directors, map[string]any{"id": person["id"], "name": name["full"], "job": role, "avatar": map[string]any{"large": image["large"]}, "url": person["siteUrl"]})
	}
	out["actors"], out["directors"] = actors, directors
	return out, nil
}

func (a *App) originalHostMediaRecognizeAniList(ctx context.Context, e *catalog.Explorer, kwargs map[string]any) (json.RawMessage, error) {
	id := kwargs["anilistid"]
	metaValue := sourceMap(kwargs["meta"])
	meta := originalHostMediaMeta(metaValue)
	source := sourceText(kwargs["source"])
	if source == "" {
		source = a.originalHostMediaRecognition.Source
	}
	if !sourceSubscriptionTruth(id) && (metaValue == nil || source != "anilist") {
		return nil, nil
	}
	detail := sourceSubscriptionTruth(id)
	region := "search"
	var arg any = meta.Name()
	keyArgs := []any{arg, 20}
	if arg == "" {
		arg = nil
		keyArgs = []any{nil, 20}
	}
	if detail {
		region = "detail"
		arg = id
		keyArgs = []any{id}
	}
	value, err := a.originalHostMediaProviderCached(ctx, "anilist", region, string(mustCleanSeedJSON(keyArgs)), true, func() (any, error) {
		data, err := a.originalHostAniListInvoke(ctx, e, detail, arg)
		if err != nil || !sourceSubscriptionTruth(data) {
			if detail {
				return nil, err
			}
			return []any{}, err
		}
		root, err := originalHostAniListObject(data, false)
		if err != nil {
			return nil, err
		}
		if detail {
			return root["Media"], nil
		}
		page, err := originalHostAniListMember(root, "Page")
		if err != nil {
			return nil, err
		}
		if !sourceSubscriptionTruth(page["media"]) {
			return []any{}, nil
		}
		return page["media"], nil
	})
	if err != nil {
		return nil, err
	}
	var info map[string]any
	if detail {
		if !sourceSubscriptionTruth(value) {
			return nil, nil
		}
		info, err = originalHostAniListObject(value, false)
	} else {
		rows, err := originalHostAniListRows(value)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			candidate, err := originalHostAniListObject(row, false)
			if err != nil {
				return nil, err
			}
			match, err := originalHostAniListMatches(meta, candidate)
			if err != nil {
				return nil, err
			}
			if match {
				info = candidate
				break
			}
		}
	}
	if err != nil || !sourceSubscriptionTruth(info) {
		return nil, err
	}
	enriched, err := originalHostAniListEnrich(info)
	if err != nil {
		return nil, err
	}
	parser, err := a.originalHostMediaSourceParser(ctx)
	if err != nil {
		return nil, err
	}
	fields, err := originalHostAniListFields(enriched, parser)
	if err != nil {
		return nil, err
	}
	if meta.Get("begin_season") != nil {
		fields["season"] = meta.Get("begin_season")
	}
	return originalHostMediaSourceSerialize(fields)
}

var originalHostAniListHTML = regexp.MustCompile(`<[^>]+>`)
var originalHostAniListDigits = regexp.MustCompile(legacyanime.DecimalClass() + `+`)

func originalHostAniListChinese(info map[string]any) any {
	title := sourceMap(info["title"])["chinese"]
	if !sourceSubscriptionTruth(title) {
		return nil
	}
	isChinese := func(s string) bool {
		han, kana := false, false
		for _, r := range s {
			han = han || r >= 0x3400 && r <= 0x4dbf || r >= 0x4e00 && r <= 0x9fff
			kana = kana || r >= 0x3040 && r <= 0x30ff
		}
		return han && !kana
	}
	text := originalHostAniListText(title)
	if isChinese(text) {
		return text
	}
	rows := sourceRows(info["synonyms"])
	for i := len(rows) - 1; i >= 0; i-- {
		text = originalHostAniListText(rows[i])
		if isChinese(text) {
			return text
		}
	}
	return originalHostAniListText(title)
}
func originalHostAniListDate(v any) (any, error) {
	if !sourceSubscriptionTruth(v) {
		return nil, nil
	}
	m, err := originalHostAniListObject(v, false)
	if err != nil {
		return nil, err
	}
	if !sourceSubscriptionTruth(m["year"]) {
		return nil, nil
	}
	out := originalHostAniListText(m["year"])
	for _, key := range []string{"month", "day"} {
		if sourceSubscriptionTruth(m[key]) {
			text := originalHostAniListText(m[key])
			pad := max(0, 2-utf8.RuneCountInString(text))
			if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
				text = text[:1] + strings.Repeat("0", pad) + text[1:]
			} else {
				text = strings.Repeat("0", pad) + text
			}
			out += "-" + text
		}
	}
	return out, nil
}
func originalHostAniListFields(info map[string]any, parser *releasemeta.MetaInfoParser) (map[string]any, error) {
	model, _ := rsscontext.NewMediaInfo(nil)
	f := model.Fields
	f["source"], f["anilist_id"] = "anilist", info["id"]
	f["type"] = "电视剧"
	if strings.ToUpper(originalHostAniListText(info["format"])) == "MOVIE" {
		f["type"] = "电影"
	}
	titles, err := originalHostAniListObject(info["title"], true)
	if err != nil {
		return nil, err
	}
	f["title"] = originalHostAniListChinese(info)
	for _, key := range []string{"native", "romaji", "english"} {
		if !sourceSubscriptionTruth(f["title"]) {
			f["title"] = titles[key]
		}
	}
	f["en_title"], f["original_title"] = titles["english"], titles["native"]
	if !sourceSubscriptionTruth(f["original_title"]) {
		f["original_title"] = titles["romaji"]
	}
	names := []any{}
	for _, key := range []string{"english", "romaji", "native"} {
		names = append(names, titles[key])
	}
	synonyms, err := originalHostAniListRows(info["synonyms"])
	if err != nil {
		return nil, err
	}
	names = append(names, synonyms...)
	filtered := []any{}
	for _, name := range names {
		if sourceSubscriptionTruth(name) && !originalBaseValueEqual(name, f["title"]) {
			filtered = append(filtered, name)
		}
	}
	f["names"], err = originalHostAniListUnique(filtered)
	if err != nil {
		return nil, err
	}
	f["release_date"], err = originalHostAniListDate(info["startDate"])
	if err != nil {
		return nil, err
	}
	f["first_air_date"] = f["release_date"]
	f["last_air_date"], err = originalHostAniListDate(info["endDate"])
	if err != nil {
		return nil, err
	}
	start, err := originalHostAniListMember(info, "startDate")
	if err != nil {
		return nil, err
	}
	year := start["year"]
	if !sourceSubscriptionTruth(year) {
		year = info["seasonYear"]
	}
	if sourceSubscriptionTruth(year) {
		f["year"] = originalHostAniListText(year)
	}
	cover, err := originalHostAniListObject(info["coverImage"], true)
	if err != nil {
		return nil, err
	}
	f["poster_path"] = cover["extraLarge"]
	if !sourceSubscriptionTruth(f["poster_path"]) {
		f["poster_path"] = cover["large"]
	}
	f["backdrop_path"] = info["bannerImage"]
	description := info["description"]
	if !sourceSubscriptionTruth(description) {
		description = ""
	}
	f["overview"] = legacyanime.Strip(originalHostAniListHTML.ReplaceAllString(strings.ReplaceAll(strings.ReplaceAll(originalHostAniListText(description), "<br>", "\n"), "<br />", "\n"), ""))
	f["vote_average"] = float64(0)
	if info["averageScore"] != nil {
		raw := originalHostAniListText(info["averageScore"])
		if raw == "True" {
			raw = "1"
		}
		if raw == "False" {
			raw = "0"
		}
		score, err := strconv.ParseFloat(legacyanime.Strip(raw), 64)
		if err != nil {
			return nil, originalHostAniListError()
		}
		rounded, err := strconv.ParseFloat(strconv.FormatFloat(score/10, 'f', 1, 64), 64)
		if err != nil {
			return nil, err
		}
		f["vote_average"] = rounded
	}
	f["popularity"], f["runtime"], f["status"] = info["popularity"], info["duration"], info["status"]
	f["adult"] = sourceSubscriptionTruth(info["isAdult"])
	if info["countryOfOrigin"] == "JP" {
		f["original_language"] = "ja"
	}
	if sourceSubscriptionTruth(info["countryOfOrigin"]) {
		f["origin_country"] = []any{info["countryOfOrigin"]}
	}
	studios, err := originalHostAniListMember(info, "studios")
	if err != nil {
		return nil, err
	}
	rows, err := originalHostAniListRows(studios["nodes"])
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		studio, err := originalHostAniListObject(row, false)
		if err != nil {
			return nil, err
		}
		if sourceSubscriptionTruth(studio["name"]) {
			f["production_companies"] = append(sourceRows(f["production_companies"]), map[string]any{"name": studio["name"]})
		}
	}
	genres, err := originalHostAniListRows(info["genres"])
	if err != nil {
		return nil, err
	}
	for _, genre := range genres {
		f["genres"] = append(sourceRows(f["genres"]), map[string]any{"id": genre, "name": genre})
	}
	for _, key := range []string{"actors", "directors"} {
		if sourceSubscriptionTruth(info[key]) {
			f[key] = info[key]
		}
	}
	if sourceSubscriptionTruth(f["title"]) {
		title, ok := f["title"].(string)
		if !ok {
			return nil, originalHostAniListError()
		}
		meta, err := parser.ParseTitle(title, nil, nil)
		if err != nil {
			return nil, err
		}
		f["season"] = meta.Get("begin_season")
	}
	count := info["episodes"]
	if f["type"] == "电视剧" && sourceSubscriptionTruth(count) {
		switch v := count.(type) {
		case json.Number:
			if strings.ContainsAny(string(v), ".eE") {
				return nil, originalHostAniListError()
			}
		case int, int64, bool:
		default:
			return nil, originalHostAniListError()
		}
		episodes, err := originalHostMediaEpisodeList(count)
		if err != nil {
			return nil, err
		}
		season := f["season"]
		if season == nil {
			season = json.Number("1")
		}
		key := originalHostAniListText(season)
		f["seasons"] = map[string]any{key: episodes}
		f["number_of_episodes"], f["number_of_seasons"] = count, 1
		if sourceSubscriptionTruth(f["year"]) {
			f["season_years"] = map[string]any{key: f["year"]}
		}
	}
	links, err := originalHostAniListRows(info["externalLinks"])
	if err != nil {
		return nil, err
	}
	for _, row := range links {
		link, err := originalHostAniListObject(row, false)
		if err != nil {
			return nil, err
		}
		site := link["site"]
		if !sourceSubscriptionTruth(site) {
			site = ""
		}
		if !strings.EqualFold(originalHostAniListText(site), "anidb") {
			continue
		}
		u := link["url"]
		if !sourceSubscriptionTruth(u) {
			u = ""
		}
		text, ok := u.(string)
		if !ok {
			return nil, originalHostAniListError()
		}
		if digits := originalHostAniListDigits.FindString(text); digits != "" {
			number, err := legacyanime.Integer(digits)
			if err != nil {
				return nil, err
			}
			f["anidb_id"] = json.Number(number.String())
			break
		}
	}
	return f, nil
}
