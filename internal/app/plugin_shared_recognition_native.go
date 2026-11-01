package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/catalog"
	"light-media/internal/networkproxy"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// originalMediaRecognitionSourceError 只在已确定的源算法异常点构造。
// 宿主只接受直接类型；不能广泛解包权限/取消/未实现边界。
type originalMediaRecognitionSourceError struct {
	Class string
	Cause error
}

func (e *originalMediaRecognitionSourceError) Error() string {
	return "source media recognition: " + e.Class
}

// originalSharedTMDBIDNative 是主动识别的固定显式 ID 原生分支，不再发现插件。
func (a *App) originalSharedTMDBIDNative(ctx context.Context, kwargs map[string]any) (json.RawMessage, error) {
	if err := originalMediaRecognitionGuard(ctx); err != nil {
		return nil, err
	}
	if kwargs["meta"] != nil || kwargs["share_meta"] != nil {
		return nil, bad("SOURCE_RECOGNITION_META_UNSUPPORTED", "当前识别分支未接元数据名称识别")
	}
	if !sourceSubscriptionTruth(kwargs["tmdbid"]) {
		return nil, nil
	}
	n, err := originalCatalogRecognitionInteger(kwargs["tmdbid"])
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil || id <= 0 || id > 1<<31-1 {
		return nil, bad("SOURCE_RECOGNITION_ID_UNSUPPORTED", "TMDB编号超出当前原生范围")
	}
	mtype := ""
	if sourceSubscriptionTruth(kwargs["mtype"]) {
		var ok bool
		mtype, ok = kwargs["mtype"].(string)
		if !ok || mtype != "电影" && mtype != "电视剧" {
			return nil, bad("SOURCE_RECOGNITION_ID_UNSUPPORTED", "当前TMDB类型未适配")
		}
	}
	ctx, client, settings, err := a.originalRecognitionClient(ctx)
	if err != nil {
		return nil, err
	}
	namespace := client.SourceRecognitionCacheNamespace()
	// 仅复用现有 BigMemoryMode 对应的源默认 profile。非默认
	// META_CACHE_EXPIRE 尚无目标配置映射；不新增env或声称已迁移。
	capacity, ttl := 256, 24*time.Hour
	if a.bigMemoryMode {
		capacity, ttl = 1024, 72*time.Hour
	}
	load := func(key string, fetch func() (*catalog.SourceRecognitionSnapshot, error)) (*catalog.SourceRecognitionSnapshot, error) {
		key = namespace + ":" + time.Now().Format("20060102") + ":" + key
		return a.originalRecognitionRequests.load(ctx, key, originalMediaRecognitionFresh(ctx), capacity, ttl, time.Now, func() error { return originalMediaRecognitionGuard(ctx) }, fetch)
	}
	log := func(level, message string) {
		if level == "WARNING" {
			level = "WARN"
		}
		a.logOriginalPluginContext(ctx, level, "media.recognize", message)
	}
	read := func(key string, fetch func() (*catalog.SourceRecognitionSnapshot, error)) (map[string]any, error) {
		snapshot, err := load(key, fetch)
		if err != nil {
			if guardErr := originalMediaRecognitionGuard(ctx); guardErr != nil {
				return nil, guardErr
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if typed, ok := err.(*catalog.Error); ok && (typed.Code == "unavailable" || typed.Code == "invalid_response" || typed.Code == "not_configured") {
				log("ERROR", typed.Error())
				return nil, nil
			}
			return nil, err
		}
		if snapshot == nil {
			return nil, nil
		}
		if err := originalMediaRecognitionGuard(ctx); err != nil {
			return nil, err
		}
		// Source header-driven recursive rate-limit waiting is not permission
		// to replace the existing bounded catalog policy with unbounded replay.
		if remaining := snapshot.Headers.Get("X-RateLimit-Remaining"); remaining != "" {
			x, e := strconv.ParseInt(strings.TrimSpace(remaining), 10, 64)
			if e != nil || x < 1 {
				return nil, bad("SOURCE_RECOGNITION_RATE_LIMIT_UNSUPPORTED", "当前原生识别未实现源响应头限速等待")
			}
		}
		return originalRecognitionResponseObject(snapshot.Body, log)

	}
	groups := func(groupID string) ([]any, error) {
		info, e := read("group:"+groupID, func() (*catalog.SourceRecognitionSnapshot, error) {
			return client.SourceRecognitionGroupSnapshot(ctx, groupID, func() error { return originalMediaRecognitionGuard(ctx) })
		})
		if e != nil {
			return nil, e
		}
		if info == nil || !sourceSubscriptionTruth(info["groups"]) {
			return nil, nil
		}
		return originalRecognitionGroupRows(info["groups"], log)

	}
	episodeGroup := ""
	var groupSeasons []any
	if originalMediaRecognitionTruth(kwargs["episode_group"]) {
		var ok bool
		episodeGroup, ok = kwargs["episode_group"].(string)
		if !ok {
			return nil, bad("SOURCE_RECOGNITION_GROUP_UNSUPPORTED", "剧集组编号类型未适配")
		}
		groupSeasons, err = groups(episodeGroup)
		if err != nil {
			return nil, err
		}
	}
	selected, err := originalRecognitionIDInfo(ctx, id, mtype, originalRecognitionIDPorts{Log: log, GetInfo: func(ctx context.Context, mt string, id int64) (json.RawMessage, error) {
		kind := "movie"
		if mt == "电视剧" {
			kind = "tv"
		}
		info, e := read(kind+":"+strconv.FormatInt(id, 10), func() (*catalog.SourceRecognitionSnapshot, error) {
			return client.SourceRecognitionDetailSnapshot(ctx, kind, id, func() error { return originalMediaRecognitionGuard(ctx) })
		})
		if e != nil {
			return nil, e
		}
		if len(info) == 0 {
			return nil, nil
		}
		return originalRecognitionProviderInfo(info, kind)
	}})
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		log("WARN", fmt.Sprintf("tmdb_id:%d 无法确定媒体类型，识别失败", id))
		return nil, nil
	}
	value, err := decodeSourceJSON(selected)
	if err != nil {
		return nil, err
	}
	info := sourceMap(value)
	kind := "movie"
	if info["media_type"] == "电视剧" {
		kind = "tv"
	}
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil {
		return nil, err
	}
	category, err := originalRecognitionCategory(client, policy, kind, info)
	if err != nil {
		return nil, err
	}

	model, err := sourceTMDBRecognitionModel(selected, kind, settings.TMDBImageDomain, category)
	if err != nil {
		return nil, err
	}
	value, err = decodeSourceJSON(model)
	if err != nil {
		return nil, err
	}
	fields := sourceMap(value)
	log("INFO", fmt.Sprintf("%d TMDB识别结果：%s %s", id, sourceText(fields["type"]), sourceText(fields["title_year"])))
	if kind == "tv" && sourceSubscriptionTruth(fields["episode_groups"]) {
		if len(groupSeasons) > 0 {
			seasons := newOriginalRecognitionSeasonValues()
			seasonInfo := []any{}
			years := newOriginalRecognitionSeasonValues()
			for _, row := range groupSeasons {
				g := sourceMap(row)
				episodes := sourceRows(g["episodes"])
				if len(episodes) == 0 {
					continue
				}
				numbers := []any{}
				for _, ep := range episodes {
					numbers = append(numbers, sourceMap(ep)["episode_number"])
				}
				if err := seasons.set(g["order"], numbers); err != nil {
					return nil, err
				}
				seasonInfo = append(seasonInfo, g)
				if year := originalRecognitionGroupYear(sourceMap(episodes[0])["air_date"]); year != "" {
					if err := years.set(g["order"], year); err != nil {
						return nil, err
					}
				}
			}
			if len(seasons.values) > 0 {
				fields["seasons"], fields["number_of_seasons"] = seasons.values, len(seasons.identities)
			}
			if len(seasonInfo) > 0 {
				fields["season_info"] = seasonInfo
			}
			if len(years.values) > 0 {
				fields["season_years"] = years.values
			}
			fields["episode_group"], fields["episode_groups"] = episodeGroup, groupSeasons
		} else {
			years := newOriginalRecognitionSeasonValues()
			for _, row := range sourceRows(fields["episode_groups"]) {
				g := sourceMap(row)
				raw, _ := json.Marshal(g["type"])
				if !sharedSystemConfigEqual(raw, json.RawMessage("6")) {
					continue
				}
				groupID, ok := g["id"].(string)
				if !ok {
					return nil, bad("SOURCE_RECOGNITION_GROUP_UNSUPPORTED", "剧集组编号类型未适配")
				}
				rows, e := groups(groupID)
				if e != nil {
					return nil, e
				}
				for _, item := range rows {
					g := sourceMap(item)
					episodes := sourceRows(g["episodes"])
					if len(episodes) == 0 {
						continue
					}
					if year := originalRecognitionGroupYear(sourceMap(episodes[0])["air_date"]); year != "" {
						if err := years.set(g["order"], year); err != nil {
							return nil, err
						}
					}
				}
			}
			if len(years.values) > 0 {
				fields["season_years"] = years.values
			}
		}
	}
	fields["recognize_cache_hit"] = false // meta=None bypasses identity cache even on HTTP snapshot hit.
	result, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if err := originalMediaRecognitionGuard(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// Group JSON shape failures occur inside the source get_tv_group_seasons try.
// Transport/authority/unsupported path errors never pass through this pure catch.
func originalRecognitionGroupRows(value any, log func(string, string)) ([]any, error) {
	fail := func(class string) ([]any, error) { log("ERROR", "TMDB group source "+class); return nil, nil }
	rows, ok := value.([]any)
	if !ok {
		return fail("TypeError")
	}
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		g := sourceMap(row)
		if g == nil {
			return fail("AttributeError")
		}
		episodes := []any{}
		if raw, present := g["episodes"]; present {
			episodes, ok = raw.([]any)
			if !ok {
				return fail("TypeError")
			}
		}
		copyGroup := map[string]any{}
		for k, v := range g {
			copyGroup[k] = v
		}
		copyEpisodes := make([]any, 0, len(episodes))
		for i, row := range episodes {
			ep := sourceMap(row)
			if ep == nil {
				return fail("TypeError")
			}
			copyEP := map[string]any{}
			for k, v := range ep {
				copyEP[k] = v
			}
			copyEP["episode_number"] = i + 1
			copyEpisodes = append(copyEpisodes, copyEP)
		}
		copyGroup["episodes"] = copyEpisodes
		out = append(out, copyGroup)
	}
	return out, nil
}

// Only source category fields reach the existing Python string/equality matcher.
// Unrelated title/id/IMDb/cast/date validation from the UI model is never applied.
func originalRecognitionCategory(client *catalog.Client, policy mediaCategoryPolicy, kind string, info map[string]any) (string, error) {
	if len(info) == 0 {
		return "", nil
	}
	for _, rule := range policy[kind] {
		if rule.deferredSectionError || rule.deferredItemsError {
			return "", &originalMediaRecognitionSourceError{Class: "AttributeError"}
		}
		matches := true
		for _, condition := range rule.conditions {
			value := info[condition.field]
			if condition.field == "release_year" {
				value = info["release_date"]
				if !originalMediaRecognitionTruth(value) {
					value = info["first_air_date"]
				}
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return "", err
			}
			if condition.field == "release_year" && originalMediaRecognitionTruth(value) {
				text, err := catalog.SourceRecognitionCategoryText(raw)
				if err != nil {
					return "", err
				}
				r := []rune(text)
				if len(r) > 4 {
					r = r[:4]
				}
				raw, _ = json.Marshal(string(r))
			}
			if condition.field == "production_countries" && originalMediaRecognitionTruth(value) {
				rows, ok := value.([]any)
				if !ok {
					return "", &originalMediaRecognitionSourceError{Class: "TypeError"}
				}
				values := make([]string, 0, len(rows))
				for _, row := range rows {
					country := sourceMap(row)
					if country == nil {
						return "", &originalMediaRecognitionSourceError{Class: "AttributeError"}
					}
					r, e := json.Marshal(country["iso_3166_1"])
					if e != nil {
						return "", e
					}
					text, e := catalog.SourceRecognitionCategoryText(r)
					if e != nil {
						return "", e
					}
					values = append(values, text)
				}
				raw, _ = json.Marshal(values)
			}
			field, err := client.SourceRecognitionCategoryField(raw)
			if err != nil {
				return "", err
			}
			matched, err := condition.matchesWithError(nil, field, false)
			if err != nil {
				class := "AttributeError"
				if condition.deferredRangeError {
					class = "ValueError"
				}
				return "", &originalMediaRecognitionSourceError{Class: class, Cause: err}
			}
			if !matched {
				matches = false
			}
		}
		if matches {
			return rule.name, nil
		}
	}
	return "", nil
}

// Python equal numeric keys retain the first key object (True and 1 collide).
// A distinct string key that serializes identically cannot be represented by the
// current JSON object model; reject that explicit finite boundary, not merge it.
type originalRecognitionSeasonValues struct {
	values         map[string]any
	identities     map[string]string
	wireIdentities map[string]string
}

func newOriginalRecognitionSeasonValues() *originalRecognitionSeasonValues {
	return &originalRecognitionSeasonValues{map[string]any{}, map[string]string{}, map[string]string{}}
}
func (s *originalRecognitionSeasonValues) set(key, value any) error {
	identity, wire, err := originalRecognitionSeasonIdentity(key)
	if err != nil {
		return err
	}
	if prior, ok := s.identities[identity]; ok {
		s.values[prior] = value
		return nil
	}
	if prior, ok := s.wireIdentities[wire]; ok && prior != identity {
		return bad("SOURCE_RECOGNITION_GROUP_UNSUPPORTED", "剧集组不同键的JSON拼写冲突")
	}
	s.identities[identity] = wire
	s.wireIdentities[wire] = identity
	s.values[wire] = value
	return nil
}
func originalRecognitionSeasonIdentity(value any) (string, string, error) {
	switch v := value.(type) {
	case nil:
		return "nil", "null", nil
	case string:
		return "string:" + v, v, nil
	case bool:
		if v {
			return "number:1", "true", nil
		}
		return "number:0", "false", nil
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			n, ok := new(big.Int).SetString(string(v), 10)
			if ok {
				return "number:" + n.String(), n.String(), nil
			}
		}
		f, e := strconv.ParseFloat(string(v), 64)
		if e == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			wire, e := catalog.SourceRecognitionCategoryText(json.RawMessage(v))
			if e != nil {
				return "", "", e
			}
			identity := strconv.FormatFloat(f, 'g', -1, 64)
			if f == 0 {
				identity = "0"
			} else if f == math.Trunc(f) {
				identity = new(big.Float).SetFloat64(f).Text('f', 0)
			}
			return "number:" + identity, wire, nil
		}
	}
	return "", "", bad("SOURCE_RECOGNITION_GROUP_UNSUPPORTED", "剧集组季键类型未适配")
}
func originalRecognitionGroupYear(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	r := []rune(s)
	if len(r) != 10 || r[4] != '-' || r[7] != '-' {
		return ""
	}
	for i, c := range r {
		if i == 4 || i == 7 {
			continue
		}
		if !unicode.IsDigit(c) {
			return ""
		}
	}
	return string(r[:4])
}

func originalRecognitionResponseObject(raw json.RawMessage, log func(string, string)) (map[string]any, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return nil, err
	}
	info := sourceMap(value)
	if info == nil {
		if originalMediaRecognitionTruth(value) {
			log("ERROR", "TMDB provider 返回了非对象响应")
		}
		return nil, nil
	}
	if _, present := info["errors"]; present {
		log("ERROR", "TMDB provider 返回错误（原响应保留在私有缓存，不写入日志）")
		return nil, nil
	}
	if success, ok := info["success"].(bool); ok && !success {
		log("ERROR", "TMDB provider 返回失败（原响应保留在私有缓存，不写入日志）")
		return nil, nil
	}
	return info, nil
}

// Freeze one actual configuration before selecting the client. The additional
// guard composes with the original resource snapshot; an A→B→A transition cannot
// bind a B client to A's authority or cache namespace. Old catalog() is unchanged.
func (a *App) originalRecognitionClient(ctx context.Context) (context.Context, *catalog.Client, Settings, error) {
	if err := originalMediaRecognitionGuard(ctx); err != nil {
		return ctx, nil, Settings{}, err
	}
	settings := a.Settings()
	proxy, err := networkproxy.Resolve(settings.ProxyHost, os.Getenv)
	if err != nil {
		return ctx, nil, Settings{}, bad("SOURCE_RECOGNITION_CONFIG_UNSUPPORTED", "识别代理配置不可用")
	}
	parentGuard := ctx.Value(originalMediaRecognitionGuardKey{}).(func(context.Context) error)
	guard := func(current context.Context) error {
		if err := current.Err(); err != nil {
			return err
		}
		if err := parentGuard(current); err != nil {
			return err
		}
		now := a.Settings()
		if now.TMDB != settings.TMDB || now.ProxyHost != settings.ProxyHost || now.TMDBImageDomain != settings.TMDBImageDomain {
			return bad("PLUGIN_SNAPSHOT_STALE", "识别原生客户端配置已变化")
		}
		return nil
	}
	ctx = context.WithValue(ctx, originalMediaRecognitionGuardKey{}, guard)
	if err = guard(ctx); err != nil {
		return ctx, nil, Settings{}, err
	}
	a.catMu.Lock()
	if a.catClient == nil || a.catCreds != settings.TMDB || a.catProxy != proxy {
		if a.catClient != nil {
			a.catClient.CloseIdleConnections()
		}
		a.catClient = catalog.NewWithProxy(catalog.Credentials{APIKey: settings.TMDB.APIKey, AccessToken: settings.TMDB.AccessToken}, proxy)
		a.catCreds = settings.TMDB
		a.catProxy = proxy
	}
	client := a.catClient
	a.catMu.Unlock()
	if err = guard(ctx); err != nil {
		return ctx, nil, Settings{}, err
	}
	return ctx, client, settings, nil
}

// This definite source exception is outside the inner detail catch: get_info's
// __get_genre_ids invokes genre.get. Preserve it for the outer module catch,
// without changing the older augmentation helper or classifying host failures.
func originalRecognitionProviderInfo(info map[string]any, kind string) (json.RawMessage, error) {
	if rows, ok := info["genres"].([]any); ok {
		for _, row := range rows {
			if sourceMap(row) == nil {
				return nil, &originalMediaRecognitionSourceError{Class: "AttributeError"}
			}
		}
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	return sourceTMDBProviderInfo(raw, kind)
}
