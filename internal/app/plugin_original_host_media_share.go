package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"light-media/internal/releasemeta"
)

func validateHostMediaRecognizeShareAPI(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	_, err := originalPluginServerURL(raw)
	if err != nil {
		return bad("INVALID_RECOGNIZE_SHARE_API", "共享识别地址须为不含账号、查询参数或片段的完整HTTP(S)地址")
	}
	return nil
}

func (a *App) originalHostMediaShareEnabled() bool {
	value := a.Settings().MediaRecognizeShare
	return value == nil || *value
}

type originalHostMediaInvocationGuardKey struct{}

func (a *App) originalHostMediaShareRequest(ctx context.Context, method string, query url.Values, payload any) (json.RawMessage, error) {
	if guard, ok := ctx.Value(originalHostMediaInvocationGuardKey{}).(func(context.Context) error); ok {
		if err := guard(ctx); err != nil {
			return nil, err
		}
	}
	settings := a.Settings()
	custom := strings.TrimRight(strings.TrimSpace(settings.MediaRecognizeShareAPI), "/")
	if custom == "" {
		if settings.PluginServerURL == nil || strings.TrimSpace(*settings.PluginServerURL) == "" {
			return nil, nil
		}
		body, err := a.requestOriginalPluginServer(ctx, method, "/recognize/share", query, payload, 5*time.Second)
		return body, err
	}
	if err := validateHostMediaRecognizeShareAPI(custom); err != nil {
		return nil, err
	}
	target, err := url.Parse(custom)
	if err != nil {
		return nil, err
	}
	target.RawQuery = query.Encode()
	var raw []byte
	if payload != nil {
		raw, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, target.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "*/*")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// The source includes the installation UID only for its configured center
	// origin. A custom external recognizer must not inherit that identifier.
	if settings.PluginServerURL != nil {
		center, e := url.Parse(*settings.PluginServerURL)
		if e == nil && center.Scheme == target.Scheme && strings.EqualFold(center.Host, target.Host) {
			uid, e := a.originalPluginServerUID(ctx)
			if e != nil {
				return nil, e
			}
			if uid != "" {
				request.Header.Set("X-MoviePilot-User-Uid", uid)
			}
			request.Header.Set("User-Agent", originalPluginServerUserAgent())
		}
	}
	client, err := a.originalPluginHTTPClient(5*time.Second, false)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > 20 || next.URL.Scheme != target.Scheme || !strings.EqualFold(next.URL.Host, target.Host) {
			return errors.New("recognize share redirect rejected")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("recognize share response rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return nil, errors.New("recognize share response exceeded limit")
	}
	if !json.Valid(body) {
		return nil, errors.New("recognize share invalid JSON")
	}
	return body, nil
}

func originalHostMediaShareType(meta map[string]any, mtype any, info map[string]any) string {
	for _, value := range []any{mtype, info["type"], meta["type"]} {
		switch value {
		case "电影":
			return "movie"
		case "电视剧":
			return "tv"
		}
		if text, ok := value.(string); ok {
			switch strings.ToLower(strings.TrimSpace(text)) {
			case "movie":
				return "movie"
			case "tv":
				return "tv"
			}
		}
	}
	if meta["begin_season"] != nil || meta["begin_episode"] != nil {
		return "tv"
	}
	return ""
}
func originalHostMediaShareKeyword(meta map[string]any) string {
	if value := sourceText(meta["original_name"]); value != "" {
		return strings.TrimSpace(value)
	}
	if value, present := meta["name"]; present {
		return strings.TrimSpace(sourceText(value))
	}
	parsed := releasemeta.EmptyMetaObject()
	for key, value := range meta {
		parsed.Set(key, value)
	}
	return strings.TrimSpace(parsed.Name())
}
func originalHostMediaShareYear(meta, info map[string]any) any {
	value := meta["year"]
	if !sourceSubscriptionTruth(value) {
		value = info["year"]
	}
	if value == nil {
		return nil
	}
	text := strings.TrimSpace(sourceStringValue(value))
	if text == "" {
		return nil
	}
	return text
}
func originalHostMediaShareSeason(kind string, meta, info map[string]any) any {
	if kind != "tv" {
		return nil
	}
	value := meta["begin_season"]
	if value == nil {
		value = info["season"]
	}
	if value == nil {
		return nil
	}
	number, err := originalCatalogRecognitionInteger(value)
	if err != nil {
		return nil
	}
	return number
}

func (a *App) originalHostMediaShareReport(ctx context.Context, meta, keywordMeta map[string]any, result json.RawMessage) {
	if !a.originalHostMediaShareEnabled() || len(meta) == 0 {
		return
	}
	value, err := decodeSourceJSON(result)
	if err != nil {
		return
	}
	info := sourceMap(value)
	if sourceSubscriptionTruth(info["recognize_cache_hit"]) {
		return
	}
	kind := originalHostMediaShareType(meta, nil, info)
	keyword := originalHostMediaShareKeyword(keywordMeta)
	if kind == "" || keyword == "" {
		return
	}
	source, id := originalHostMediaShareIdentity(info)
	if id == "" {
		return
	}
	title := info["title"]
	if !sourceSubscriptionTruth(title) {
		title = keyword
	}
	payload := map[string]any{"keyword": keyword, "type": kind, "title": title, "year": originalHostMediaShareYear(meta, info), "season": originalHostMediaShareSeason(kind, meta, info), "tmdbid": info["tmdb_id"], "doubanid": info["douban_id"], "bangumiid": info["bangumi_id"], "anilistid": info["anilist_id"], "media_source": source, "media_id": id}
	_, _ = a.originalHostMediaShareRequest(ctx, http.MethodPost, nil, payload)
}

func originalHostMediaShareIdentity(info map[string]any) (string, string) {
	source := sourceText(info["source"])
	if source == "" {
		source = sourceText(info["media_source"])
	}
	source = originalCatalogSource(strings.TrimSpace(source))
	if source != "" {
		if id := strings.TrimSpace(sourceStringValue(info["media_id"])); id != "" {
			return source, id
		}
	}
	if source == "" {
		if legacy := sourceText(info["mediaid"]); strings.Contains(legacy, ":") {
			prefix, id, _ := strings.Cut(legacy, ":")
			if normalized := originalCatalogSource(prefix); normalized != "" && strings.TrimSpace(id) != "" {
				return normalized, strings.TrimSpace(id)
			}
		}
	}
	for _, pair := range []struct{ source, key string }{{"themoviedb", "tmdb_id"}, {"douban", "douban_id"}, {"bangumi", "bangumi_id"}, {"anilist", "anilist_id"}} {
		if source != "" && source != pair.source {
			continue
		}
		if value := info[pair.key]; value != nil {
			if id := strings.TrimSpace(originalCatalogIdentityString(value)); id != "" && id != "null" {
				return pair.source, id
			}
		}
	}
	return source, ""
}

func (a *App) originalHostMediaShareFallback(ctx context.Context, kwargs map[string]any, keywordMeta map[string]any) (json.RawMessage, error) {
	meta := sourceMap(kwargs["meta"])
	if !a.originalHostMediaShareEnabled() || len(meta) == 0 || sourceSubscriptionTruth(kwargs["source"]) {
		return nil, nil
	}
	for _, key := range []string{"tmdbid", "doubanid", "bangumiid", "anilistid"} {
		if sourceSubscriptionTruth(kwargs[key]) {
			return nil, nil
		}
	}
	keyword := originalHostMediaShareKeyword(keywordMeta)
	if keyword == "" {
		return nil, nil
	}
	kind := originalHostMediaShareType(meta, kwargs["mtype"], nil)
	query := url.Values{"keyword": {keyword}}
	if kind != "" {
		query.Set("type", kind)
	}
	if year := originalHostMediaShareYear(meta, nil); year != nil {
		query.Set("year", year.(string))
	}
	if season := originalHostMediaShareSeason(kind, meta, nil); season != nil {
		query.Set("season", originalCatalogIdentityString(season))
	}
	raw, err := a.originalHostMediaShareRequest(ctx, http.MethodGet, query, nil)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(raw) == 0 {
		return nil, nil
	}
	var envelope struct {
		Code json.RawMessage `json:"code"`
		Data struct {
			Item map[string]any `json:"item"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&envelope) != nil || !originalBaseJSONEqual(envelope.Code, json.RawMessage(`0`)) || envelope.Data.Item == nil {
		return nil, nil
	}
	item := envelope.Data.Item
	anyID := false
	for _, key := range []string{"tmdbid", "doubanid", "bangumiid", "anilistid", "media_id"} {
		anyID = anyID || sourceSubscriptionTruth(item[key])
	}
	if !anyID {
		return nil, nil
	}
	explicit := map[string]any{"mtype": kwargs["mtype"], "source": item["media_source"], "mediaid": item["media_id"], "tmdbid": item["tmdbid"], "doubanid": item["doubanid"], "bangumiid": item["bangumiid"], "anilistid": item["anilistid"], "episode_group": kwargs["episode_group"], "cache": kwargs["cache"]}
	if kind := originalHostMediaShareType(nil, item["type"], nil); kind != "" {
		if kind == "tv" {
			explicit["mtype"] = "电视剧"
		} else {
			explicit["mtype"] = "电影"
		}
	}
	resolved, err := originalCatalogRecognitionCallKwargs(meta, explicit)
	if err != nil {
		return nil, nil
	}
	result, err := a.originalCatalogRecognizeMethod(ctx, "recognize_media", resolved, func() (json.RawMessage, error) { return a.originalHostMediaRecognizeNative(ctx, resolved) })
	if err != nil || !originalModuleResultTruthy(result) {
		return result, err
	}
	// The fixed chain broadcasts cache refill to module providers, preserving
	// a snapshot of the original name meta rather than shared-service IDs.
	encoded, _ := json.Marshal(map[string]any{"meta": meta, "mediainfo": json.RawMessage(result)})
	_, err = a.invokeOriginalPluginModules(ctx, "update_recognize_cache", nil, json.RawMessage(`[]`), encoded)
	if err != nil {
		return nil, err
	}
	// Fixed TheMovieDbModule.update_recognize_cache accepts only real TMDB
	// provider info. A plugin's foreign model must not become a fake identity
	// cache entry; the existing provider broadcast above still receives it.
	if value, decodeErr := decodeSourceJSON(result); decodeErr == nil {
		info := sourceMap(value)
		if info["source"] == "themoviedb" && len(sourceMap(info["tmdb_info"])) > 0 {
			_ = a.originalHostMediaCachePut(ctx, originalHostMediaCacheKey(originalHostMediaMeta(meta), a.originalHostMediaRecognition.Locale), sourceMap(info["tmdb_info"]))
		}
	}
	_ = a.originalHostMediaShareIncrement(ctx)
	return result, nil
}

func (a *App) originalHostMediaShareIncrement(ctx context.Context) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='plugin_host_media_recognize_count' AND id='main'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	count := int64(0)
	if len(raw) > 0 && json.Unmarshal(raw, &count) != nil {
		return errors.New("invalid recognize share counter")
	}
	if count == int64(^uint64(0)>>1) {
		return errors.New("recognize share counter overflow")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('plugin_host_media_recognize_count','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, []byte(strconv.FormatInt(count+1, 10)), now())
	if err != nil {
		return err
	}
	return tx.Commit()
}
