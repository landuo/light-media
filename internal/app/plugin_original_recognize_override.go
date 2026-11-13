package app

import (
	"context"
	"encoding/json"
	"strconv"

	"light-media/internal/catalog"
	"light-media/internal/notice"
	"light-media/internal/releasemeta"
)

// This is the existing explicit-TMDB recognition host, not tmdb_info. The
// pinned ChainBase resolves the source/IDs before asking plugin modules.
func (a *App) originalRecognizeTMDBMedia(ctx context.Context, input catalog.Media, native func() (sourceMediaInformation, error)) (sourceMediaInformation, error) {
	return a.originalRecognizeTMDBModule(ctx, input, "recognize_media", nil, nil, nil, true, native)
}

// Web/API async_add uses its own extension name and resolved source identity.
// Go awaits the existing SDK runner without a coroutine framework.
func (a *App) sourceAsyncRecognizeTMDBMedia(ctx context.Context, input catalog.Media, meta map[string]any) (sourceMediaInformation, error) {
	return a.originalRecognizeTMDBModule(ctx, input, "async_recognize_media", meta, strconv.FormatInt(input.ID, 10), originalRecognitionEpisodeGroup(meta, nil), false, func() (sourceMediaInformation, error) {
		return a.cachedSubscriptionSourceInformation(ctx, input)
	})
}

func originalRecognitionEpisodeGroup(meta map[string]any, explicit any) any {
	if sourceSubscriptionTruth(explicit) {
		return explicit
	}
	if sourceSubscriptionTruth(meta["episode_group"]) {
		return meta["episode_group"]
	}
	return nil
}

// Reuse the same build_subscribe_meta state for ordinary precheck/completion;
// the source overwrites parsed values even when the stored column is None.
func (a *App) originalSubscriptionRecognitionMeta(ctx context.Context, source map[string]any) (*releasemeta.MetaObject, error) {
	name, ok := source["name"].(string)
	if !ok {
		return nil, notice.ErrTemplateDependency
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return nil, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	meta, err := parser.ParseTitle(name, nil, nil)
	if err != nil {
		return nil, err
	}
	for target, key := range map[string]string{"year": "year", "begin_season": "season", "type": "type", "tmdbid": "tmdbid", "doubanid": "doubanid", "bangumiid": "bangumiid", "anilistid": "anilistid", "media_source": "media_source", "media_id": "media_id"} {
		value, err := noticeV2NormalizeNumberValues(source[key])
		if err != nil {
			return nil, err
		}
		meta.Set(target, value)
	}
	return meta, nil
}

func (a *App) originalRecognizeSubscriptionTMDBMedia(ctx context.Context, input catalog.Media, meta *releasemeta.MetaObject, source map[string]any) (sourceMediaInformation, error) {
	fields, err := meta.ToDict()
	if err != nil {
		return sourceMediaInformation{}, err
	}
	mediaID := sourceText(source["media_id"])
	if mediaID == "" {
		mediaID = strconv.FormatInt(input.ID, 10)
	}
	return a.originalRecognizeTMDBModule(ctx, input, "recognize_media", fields, mediaID, originalRecognitionEpisodeGroup(fields, source["episode_group"]), false, func() (sourceMediaInformation, error) {
		return a.sourceRecognizeTMDBCore(ctx, input)
	})
}

func (a *App) sourceRecognizeSubscriptionTMDBMedia(ctx context.Context, s Subscription) (sourceMediaInformation, error) {
	raw, err := a.subscriptionSourceProjection(ctx, s)
	if err != nil {
		return sourceMediaInformation{}, err
	}
	value, err := decodeSourceJSON(raw)
	if err != nil || sourceMap(value) == nil {
		return sourceMediaInformation{}, notice.ErrTemplateDependency
	}
	source := sourceMap(value)
	meta, err := a.originalSubscriptionRecognitionMeta(ctx, source)
	if err != nil {
		return sourceMediaInformation{}, err
	}
	return a.originalRecognizeSubscriptionTMDBMedia(ctx, s.Media, meta, source)
}

func (a *App) originalRecognizeTMDBModule(ctx context.Context, input catalog.Media, method string, meta, mediaID, episodeGroup any, cache bool, native func() (sourceMediaInformation, error)) (sourceMediaInformation, error) {
	return a.originalRecognizeTMDBModuleGroups(ctx, input, method, meta, mediaID, episodeGroup, cache, native, nil)
}

func (a *App) originalRecognizeTMDBModuleGroups(ctx context.Context, input catalog.Media, method string, meta, mediaID, episodeGroup any, cache bool, native func() (sourceMediaInformation, error), groups []originalPluginModuleGroup) (sourceMediaInformation, error) {
	if input.MediaType != "movie" && input.MediaType != "tv" || input.ID <= 0 {
		return sourceMediaInformation{}, bad("INVALID_MEDIA", "请选择电影或电视剧")
	}
	kind := "电影"
	if input.MediaType == "tv" {
		kind = "电视剧"
	}
	kwargs, _ := json.Marshal(map[string]any{"meta": meta, "mtype": kind, "source": "themoviedb", "mediaid": mediaID, "tmdbid": input.ID, "doubanid": nil, "bangumiid": nil, "anilistid": nil, "episode_group": episodeGroup, "cache": cache})
	result, err := a.invokeOriginalPluginModuleGroups(ctx, method, nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return sourceMediaInformation{}, err
	}
	if originalModuleResultEmpty(result) {
		return native()
	}
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil {
		// A source list causes system modules to run; their scalar MediaInfo
		// result is ignored by list accumulation. It is never a media override.
		if _, err = native(); err != nil {
			return sourceMediaInformation{}, err
		}
		if len(list) == 0 {
			return sourceMediaInformation{}, originalRecognitionMissing()
		}
		return sourceMediaInformation{}, bad("PLUGIN_MODULE_RESULT_INVALID", "识别插件返回的媒体信息不是单个对象")
	}
	if !originalModuleResultTruthy(result) {
		return sourceMediaInformation{}, originalRecognitionMissing()
	}
	return a.originalRecognitionProjection(input, result)
}

func originalRecognitionMissing() error {
	return &APIError{"MEDIA_NOT_FOUND", "插件未识别到媒体信息", 404}
}

// The typed Go host consumes a source MediaInfo JSON object and retains all
// its fields. Its native compact projection reuses the real TMDB decoder; no
// external response is fetched or fabricated to validate the plugin's output.
func (a *App) originalRecognitionProjection(input catalog.Media, result json.RawMessage) (sourceMediaInformation, error) {
	invalid := func() (sourceMediaInformation, error) {
		return sourceMediaInformation{}, bad("PLUGIN_MODULE_RESULT_INVALID", "识别插件返回的媒体信息无法用于当前TMDB身份")
	}
	value, err := decodeSourceJSON(result)
	if err != nil || sourceMap(value) == nil {
		return invalid()
	}
	fields := sourceMap(value)
	if fields["source"] != nil && sourceText(fields["source"]) != "themoviedb" {
		return invalid()
	}
	typeName := "电影"
	if input.MediaType == "tv" {
		typeName = "电视剧"
	}
	if sourceText(fields["type"]) != typeName {
		return invalid()
	}
	id, err := strconv.ParseInt(sourceNumberString(fields["tmdb_id"]), 10, 64)
	if err != nil || id != input.ID {
		return invalid()
	}
	provider, err := json.Marshal(fields["tmdb_info"])
	if err != nil {
		return invalid()
	}
	// A plugin MediaInfo need not contain an upstream provider response. Keep
	// that original value (including null) in ProviderInfo. The following wire
	// object is only an internal compact-model projection, never fake evidence
	// that TMDB was queried or returned these fields.
	wire := map[string]any{}
	for key, value := range sourceMap(fields["tmdb_info"]) {
		wire[key] = value
	}
	wire["id"] = fields["tmdb_id"]
	// Source MediaInfo keeps Chinese MediaType.value in tmdb_info; the native
	// compact decoder uses TMDB's movie/tv discriminator. ProviderInfo itself
	// remains the untouched source value.
	wire["media_type"] = input.MediaType
	titleKey, originalKey, dateKey := "title", "original_title", "release_date"
	if input.MediaType == "tv" {
		titleKey, originalKey, dateKey = "name", "original_name", "first_air_date"
	}
	for native, source := range map[string]string{titleKey: "title", originalKey: "original_title", dateKey: "release_date", "overview": "overview", "original_language": "original_language", "vote_average": "vote_average", "genre_ids": "genre_ids", "seasons": "season_info"} {
		if value := fields[source]; value != nil {
			wire[native] = value
		}
	}
	if actors := fields["actors"]; actors != nil {
		wire["credits"] = map[string]any{"cast": actors}
	}
	compact, err := json.Marshal(wire)
	if err != nil {
		return invalid()
	}
	media, err := a.catalog().ProjectSourceInformation(input.MediaType, input.ID, compact, nil)
	if err != nil {
		return invalid()
	}
	media.Title, media.OriginalTitle = sourceText(fields["title"]), sourceText(fields["original_title"])
	if media.Title == "" {
		return invalid()
	}
	if year := fields["year"]; year != nil {
		media.Year, err = strconv.Atoi(sourceNumberString(year))
		if err != nil {
			return invalid()
		}
	}
	media.Overview = sourceText(fields["overview"])
	defaults, err := decodeSourceJSON(sourceMediaInfoDefaults)
	if err != nil {
		return sourceMediaInformation{}, err
	}
	full := sourceMap(defaults)
	for key, value := range fields {
		full[key] = value
	}
	return sourceMediaInformation{ProviderInfo: provider, MediaInfo: mustCleanSeedJSON(full), Media: media}, nil
}

func sourceNumberString(value any) string {
	if number, ok := value.(json.Number); ok {
		return number.String()
	}
	return sourceText(value)
}
