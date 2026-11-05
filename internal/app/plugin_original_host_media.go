package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"light-media/internal/fnos"
	"light-media/internal/plugins"
)

func originalHostMediaCapability(method string) bool {
	return method == plugins.MediaServersListCapability || method == plugins.MediaServersRefreshCapability || method == plugins.MediaServerLibraryRefreshCapability || method == plugins.MediaRecognizeCapability
}

func (a *App) originalHostMediaResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	var value any
	var err error
	switch method {
	case plugins.MediaServersListCapability:
		var request plugins.MediaServersRequest
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		value, err = a.originalHostMediaServers(ctx, scope, request)
	case plugins.MediaServersRefreshCapability:
		var request plugins.MediaRefreshRequest
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		value, err = a.originalHostMediaRefresh(ctx, scope, request)
	case plugins.MediaServerLibraryRefreshCapability:
		var request plugins.MediaRefreshLibraryRequest
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		value, err = a.originalHostMediaRefreshLibrary(ctx, scope, request)
	case plugins.MediaRecognizeCapability:
		var request plugins.MediaRecognizeRequest
		if err = originalResourceDecode(raw, &request); err != nil {
			return nil, err
		}
		if err = originalHostNestedModuleScope(scope); err != nil {
			return nil, err
		}
		ctx = context.WithValue(ctx, originalHostMediaInvocationGuardKey{}, scope.validate)
		value, err = a.originalHostMediaRecognize(ctx, request)
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知媒体宿主方法")
	}
	if err != nil {
		var serviceError *fnos.ServiceError
		if errors.As(err, &serviceError) {
			a.writeOriginalPluginCallLog(scope.installation, scope.logCall, "ERROR", "media_server_"+serviceError.Code, serviceError.Error())
		} else {
			var api *APIError
			if errors.As(err, &api) {
				a.writeOriginalPluginCallLog(scope.installation, scope.logCall, "ERROR", "media_host_"+api.Code, "媒体宿主调用失败："+api.Code)
			}
		}
		return nil, err
	}
	if err = scope.validate(ctx); err != nil {
		return nil, err
	}
	result, err := json.Marshal(value)
	if len(result) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "媒体宿主响应超过上限")
	}
	return result, err
}

func (a *App) originalHostMediaServers(ctx context.Context, scope originalBaseCallScope, request plugins.MediaServersRequest) ([]plugins.MediaServer, error) {
	config := a.Settings().FNOS
	result := []plugins.MediaServer{}
	if !config.Enabled && !request.IncludeDisabled || request.Type != "" && request.Type != "fnos" || len(request.Names) > 0 && !slices.Contains(request.Names, "fnos") {
		return result, nil
	}
	server := plugins.MediaServer{Name: "fnos", Type: "fnos", Enabled: config.Enabled, Configured: config.Host != "" && config.Username != "" && config.Password != ""}
	if request.Probe {
		service, err := fnos.NewService(fnos.Config{Host: config.Host, Username: config.Username, Password: config.Password})
		if err != nil {
			return nil, err
		}
		libraries, err := service.Libraries(ctx)
		if err != nil {
			return nil, err
		}
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		if !sameHostMediaConfiguration(config, a.Settings().FNOS) {
			return nil, bad("MEDIA_SERVER_CHANGED", "媒体服务配置已变化")
		}
		connected := true
		server.Connected = &connected
		server.Libraries = []plugins.MediaServerLibrary{}
		for _, library := range libraries {
			server.Libraries = append(server.Libraries, plugins.MediaServerLibrary{ID: library.GUID, Name: library.Name, Directories: library.Directories})
		}
	}
	return append(result, server), nil
}

func sameHostMediaConfiguration(a, b FNConfig) bool {
	return a.Host == b.Host && a.Username == b.Username && a.Password == b.Password && a.Enabled == b.Enabled
}

func (a *App) originalHostMediaRefresh(ctx context.Context, scope originalBaseCallScope, request plugins.MediaRefreshRequest) (plugins.MediaRefreshResult, error) {
	result := plugins.MediaRefreshResult{Paths: []string{}, LibraryIDs: []string{}}
	if request.LibraryID != "" || request.Root && (len(request.Paths) > 0 || len(request.Items) > 0) || !request.Root && (request.Paths == nil && request.Items == nil || request.Paths != nil && request.Items != nil) {
		return result, bad("INVALID_PLUGIN_VALUE", "按原语义选择全库刷新或明确items/paths；指定GUID请使用refresh_library")
	}
	service, guard, err := a.originalHostMediaRefreshService(ctx, scope, request.Name)
	if err != nil {
		var source *fnos.ServiceError
		if errors.As(err, &source) && source.Code == "not_configured" {
			return result, nil
		}
		return result, err
	}
	var observed fnos.RefreshResult
	if request.Root {
		observed, err = service.RefreshSourceRoot(ctx, guard)
	} else {
		items := []*string{}
		if request.Items != nil {
			for _, item := range request.Items {
				items = append(items, item.TargetPath)
				if item.TargetPath != nil {
					result.Paths = append(result.Paths, *item.TargetPath)
				}
			}
		} else {
			for _, path := range request.Paths {
				value := path
				items = append(items, &value)
				result.Paths = append(result.Paths, path)
			}
		}
		observed, err = service.RefreshSourceItems(ctx, items, guard)
	}
	result.AllLibraries, result.LibraryIDs, result.Result = observed.AllLibraries, observed.LibraryGUIDs, observed.SourceResult
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if currentErr := guard(); currentErr != nil {
			return result, currentErr
		}
		var source *fnos.ServiceError
		if errors.As(err, &source) {
			a.writeOriginalPluginCallLog(scope.installation, scope.logCall, "ERROR", "media_server_"+source.Code, source.Error())
			value := false
			result.Result = &value
			return result, nil
		}
		return result, err
	}
	if err = guard(); err != nil {
		return result, err
	}
	result.Accepted = result.Result != nil && *result.Result
	return result, nil
}

func (a *App) originalHostMediaRefreshService(ctx context.Context, scope originalBaseCallScope, name string) (*fnos.Service, func() error, error) {
	if name != "fnos" {
		return nil, nil, bad("MEDIA_SERVER_NOT_FOUND", "媒体服务不存在")
	}
	config := a.Settings().FNOS
	if !config.Enabled {
		return nil, nil, bad("MEDIA_SERVER_DISABLED", "媒体服务未启用")
	}
	service, err := fnos.NewService(fnos.Config{Host: config.Host, Username: config.Username, Password: config.Password})
	if err != nil {
		return nil, nil, err
	}
	guard := func() error {
		if err := scope.validate(ctx); err != nil {
			return err
		}
		if !sameHostMediaConfiguration(config, a.Settings().FNOS) {
			return bad("MEDIA_SERVER_CHANGED", "媒体服务配置已变化")
		}
		return nil
	}
	return service, guard, nil
}

func (a *App) originalHostMediaRefreshLibrary(ctx context.Context, scope originalBaseCallScope, request plugins.MediaRefreshLibraryRequest) (plugins.MediaRefreshResult, error) {
	result := plugins.MediaRefreshResult{Paths: []string{}, LibraryIDs: []string{}}
	service, guard, err := a.originalHostMediaRefreshService(ctx, scope, request.Name)
	if err != nil {
		return result, err
	}
	observed, err := service.RefreshExplicitLibrary(ctx, request.LibraryID, guard)
	if err != nil {
		return result, err
	}
	if err = guard(); err != nil {
		return result, err
	}
	result.Result, result.AllLibraries, result.LibraryIDs = observed.SourceResult, observed.AllLibraries, observed.LibraryGUIDs
	result.Accepted = result.Result != nil && *result.Result
	return result, nil
}

func (a *App) originalHostMediaRecognize(ctx context.Context, request plugins.MediaRecognizeRequest) (json.RawMessage, error) {
	if request.Type != "" && request.Type != "movie" && request.Type != "tv" && request.Type != "电影" && request.Type != "电视剧" && request.Type != "未知" {
		return nil, bad("INVALID_PLUGIN_VALUE", "识别媒体类型无效")
	}
	explicit := map[string]any{}
	for key, value := range map[string]any{"mtype": request.Type, "source": request.Source, "mediaid": request.MediaID, "tmdbid": request.TMDBID, "doubanid": request.DoubanID, "bangumiid": request.BangumiID, "anilistid": request.AniListID, "episode_group": request.EpisodeGroup} {
		if sourceSubscriptionTruth(value) {
			explicit[key] = value
		}
	}
	if request.Type == "movie" {
		explicit["mtype"] = "电影"
	} else if request.Type == "tv" {
		explicit["mtype"] = "电视剧"
	}
	if request.Cache != nil {
		explicit["cache"] = *request.Cache
	}
	kwargs, err := originalCatalogRecognitionCallKwargs(request.Meta, explicit)
	if err != nil {
		return nil, err
	}
	if kwargs["cache"] == false {
		ctx = context.WithValue(ctx, originalHostMediaFreshContextKey{}, true)
	}
	ctx = context.WithValue(ctx, originalHostMediaNativeSourceKey{}, a.originalHostMediaRecognitionSource(kwargs))
	result, err := a.originalCatalogRecognizeMethod(ctx, "recognize_media", kwargs, func() (json.RawMessage, error) {
		return a.originalHostMediaRecognizeNative(ctx, kwargs)
	})
	if err != nil {
		return result, err
	}
	keywordMeta := request.ShareMeta
	if len(keywordMeta) == 0 {
		keywordMeta = sourceMap(kwargs["meta"])
	}
	if originalModuleResultTruthy(result) {
		a.originalHostMediaShareReport(ctx, sourceMap(kwargs["meta"]), keywordMeta, result)
		return result, nil
	}
	return a.originalHostMediaShareFallback(ctx, kwargs, keywordMeta)
}

type originalHostMediaNativeSourceKey struct{}

func (a *App) originalHostMediaRecognitionSource(kwargs map[string]any) string {
	source := sourceText(kwargs["source"])
	if source == "" {
		source = a.originalHostMediaRecognition.Source
	}
	return source
}

func (a *App) originalHostMediaRecognizeNative(ctx context.Context, kwargs map[string]any) (json.RawMessage, error) {
	source := a.originalHostMediaRecognitionSource(kwargs)
	if source == "themoviedb" {
		return a.originalHostMediaRecognizeTMDB(ctx, kwargs)
	}
	switch source {
	case "douban", "bangumi", "anilist":
		return a.originalHostMediaRecognizeSource(ctx, source, kwargs)
	default:
		return nil, nil
	}
}

// Fixed TheMovieDbModule._disambiguate_by_meta: titles contribute two points,
// year contributes one, then the parsed media type breaks an exact tie.
func originalHostMediaDisambiguate(television, movie json.RawMessage, meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	score := func(raw json.RawMessage) int {
		value, err := decodeSourceJSON(raw)
		if err != nil {
			return 0
		}
		info := sourceMap(value)
		titles := []string{}
		for _, key := range []string{"title", "name", "original_title", "original_name"} {
			if value := sourceText(info[key]); value != "" {
				titles = append(titles, value)
			}
		}
		for _, value := range sourceRows(info["names"]) {
			if title := sourceText(value); title != "" {
				titles = append(titles, title)
			}
		}
		result := 0
		matched := false
		for _, key := range []string{"cn_name", "en_name"} {
			name := sourceText(meta[key])
			if name == "" {
				continue
			}
			for _, title := range titles {
				if strings.Contains(name, title) || strings.Contains(title, name) {
					matched = true
					break
				}
			}
			if matched {
				result += 2
				break
			}
		}
		year := sourceStringValue(meta["year"])
		date := sourceText(info["release_date"])
		if date == "" {
			date = sourceText(info["first_air_date"])
		}
		if year != "" && len(date) >= 4 && date[:4] == year {
			result++
		}
		return result
	}
	tvScore, movieScore := score(television), score(movie)
	if tvScore > movieScore {
		return "tv"
	}
	if movieScore > tvScore {
		return "movie"
	}
	if meta["type"] == "电视剧" {
		return "tv"
	}
	if meta["type"] == "电影" {
		return "movie"
	}
	return ""
}
