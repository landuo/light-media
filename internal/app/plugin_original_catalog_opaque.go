package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/rsscontext"
	"light-media/internal/sourcejson"
)

func originalCatalogSource(source string) string {
	source = cases.Fold().String(legacyanime.Strip(source))
	if source == "tmdb" {
		return "themoviedb"
	}
	return source
}

// The source detail endpoint first attempts the native prefix. Conversion is
// only requested for an unknown prefix after that recognition returned falsey.
// The original full key, including its spelling, is the event's input.
func (a *App) originalCatalogDetailRecognition(ctx context.Context, fullKey, kind, title, year string, recognize func(string, string) (json.RawMessage, error), byName func(string, string) (json.RawMessage, error)) (json.RawMessage, error) {
	source, id := "", ""
	if parts := strings.SplitN(fullKey, ":", 2); len(parts) == 2 {
		source, id = originalCatalogSource(parts[0]), legacyanime.Strip(parts[1])
		if source == "" || id == "" {
			source, id = "", ""
		}
	}
	var model json.RawMessage
	var err error
	if source != "" && id != "" {
		model, err = recognize(source, id)
	}
	if err != nil || originalModuleResultTruthy(model) {
		return model, err
	}
	switch source {
	case "themoviedb", "douban", "bangumi", "anilist":
		return nil, nil
	}
	data, _ := json.Marshal(map[string]any{"mediaid": fullKey, "convert_type": "themoviedb", "media_dict": map[string]any{}})
	event := &plugins.MutableEvent{Type: "media.recognize.convert", Data: data}
	if a.sendOriginalChainEvent(ctx, event) {
		value, e := decodeSourceJSON(event.Data)
		if e != nil || sourceMap(value) == nil {
			return nil, originalCatalogMediaInvalid()
		}
		fields := sourceMap(value)
		var rawFields map[string]json.RawMessage
		_ = json.Unmarshal(event.Data, &rawFields)
		if sourceSubscriptionTruth(fields["media_dict"]) {
			converted := sourceMap(fields["media_dict"])
			if converted == nil {
				return nil, originalCatalogMediaInvalid()
			}
			if converted["id"] != nil && sourceSubscriptionTruth(fields["convert_type"]) {
				var rawConverted map[string]json.RawMessage
				_ = json.Unmarshal(rawFields["media_dict"], &rawConverted)
				convertedID, e := sourcejson.String(rawConverted["id"])
				if e != nil {
					return nil, originalCatalogMediaInvalid()
				}
				convertedType, e := sourcejson.String(rawFields["convert_type"])
				if e != nil {
					return nil, originalCatalogMediaInvalid()
				}
				model, err = recognize(convertedType, convertedID)
			}
		}
	}
	if err != nil || originalModuleResultTruthy(model) {
		return model, err
	}
	if title != "" {
		return byName(title, year)
	}
	return nil, nil
}

func (a *App) originalCatalogNameRecognition(ctx context.Context, title, year, kind string, native func(*releasemeta.MetaObject) (json.RawMessage, error)) (json.RawMessage, error) {
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return nil, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	meta, err := parser.ParseTitle(title, nil, nil)
	if err != nil {
		return nil, err
	}
	if year != "" {
		meta.Set("year", year)
	}
	if kind == "movie" {
		meta.Set("type", "电影")
	} else if kind == "tv" {
		meta.Set("type", "电视剧")
	}
	return a.originalCatalogRecognizeByMeta(ctx, meta, native)
}

// Ordinary callers which already parsed a file/title keep that same complete
// MetaBase through name-event mutation and the second recognition attempt.
func (a *App) originalCatalogRecognizeByMeta(ctx context.Context, meta *releasemeta.MetaObject, native func(*releasemeta.MetaObject) (json.RawMessage, error)) (json.RawMessage, error) {
	return a.originalCatalogRecognizeByMetaMethod(ctx, "async_recognize_media", meta, native)
}

func (a *App) originalCatalogRecognizeByMetaMethod(ctx context.Context, method string, meta *releasemeta.MetaObject, native func(*releasemeta.MetaObject) (json.RawMessage, error)) (json.RawMessage, error) {
	if meta == nil {
		return nil, nil
	}
	call := func() (json.RawMessage, error) {
		fields, e := meta.ToDict()
		if e != nil {
			return nil, e
		}
		kwargs, e := originalCatalogRecognitionMetaKwargs(fields)
		if e != nil {
			return nil, e
		}
		return a.originalCatalogRecognizeMethod(ctx, method, kwargs, func() (json.RawMessage, error) { return native(meta) })
	}
	pluginCall := func() (json.RawMessage, error) {
		data, _ := json.Marshal(map[string]any{"title": meta.Get("title")})
		event := &plugins.MutableEvent{Type: "name.recognize", Data: data}
		if !a.sendOriginalChainEvent(ctx, event) {
			return nil, nil
		}
		value, e := decodeSourceJSON(event.Data)
		if e != nil || sourceMap(value) == nil {
			return nil, originalCatalogMediaInvalid()
		}
		fields := sourceMap(value)
		var rawFields map[string]json.RawMessage
		_ = json.Unmarshal(event.Data, &rawFields)
		name, eventYear := "", ""
		if sourceSubscriptionTruth(fields["name"]) {
			text, e := sourcejson.String(rawFields["name"])
			if e != nil {
				return nil, originalCatalogMediaInvalid()
			}
			name = strings.ReplaceAll(legacyanime.Strip(strings.SplitN(text, "/", 2)[0]), ".", " ")
		}
		if sourceSubscriptionTruth(fields["year"]) {
			text, e := sourcejson.String(rawFields["year"])
			if e != nil {
				return nil, originalCatalogMediaInvalid()
			}
			eventYear = legacyanime.Strip(strings.SplitN(text, "/", 2)[0])
		}
		if name == "" || name == "Unknown" {
			return nil, nil
		}
		var parsedYear any
		if legacyanime.IsDigit(eventYear) {
			parsedYear = eventYear
		}
		if name == meta.Name() && parsedYear == meta.Get("year") {
			return nil, nil
		}
		if err := meta.SetName(name); err != nil {
			return nil, err
		}
		meta.Set("year", parsedYear)
		for _, key := range []string{"season", "episode"} {
			var number any
			text := legacyanime.Strip(sourceStringValue(fields[key]))
			if legacyanime.IsDigit(text) {
				n, err := legacyanime.Integer(text)
				if err != nil {
					return nil, err
				}
				number = n
			}
			meta.Set("begin_"+key, number)
			if number != nil {
				meta.Set("type", "电视剧")
			}
		}
		return call()
	}
	available := a.originalEventHasHandler(plugins.EventQuery{Kind: plugins.ChainEventKind, Type: "name.recognize"})
	if a.Settings().RecognizePluginFirst && available {
		model, err := pluginCall()
		if err != nil || originalModuleResultTruthy(model) {
			return model, err
		}
		return call()
	}
	model, err := call()
	if err != nil || originalModuleResultTruthy(model) || !available {
		return model, err
	}
	return pluginCall()
}

func (a *App) originalCatalogExploreDetail(ctx context.Context, source, kind, id, title, year string, explorer *catalog.Explorer, fullKeys ...string) (catalog.ExploreMedia, error) {
	var nativeView *catalog.ExploreMedia
	recognize := func(source, id string) (json.RawMessage, error) {
		kwargs, e := originalCatalogRecognitionKwargs(source, id, kind)
		if e != nil {
			return nil, e
		}
		return a.originalCatalogRecognize(ctx, kwargs, func() (json.RawMessage, error) {
			if !catalog.ValidExploreIdentity(source, kind, id) {
				return nil, nil
			}
			if source == "themoviedb" {
				n, _ := strconv.ParseInt(id, 10, 64)
				var complete sourceMediaInformation
				_, e := a.cached(ctx, "source-detail:"+kind+":"+id, 24*time.Hour, &complete, func() (any, error) { return a.sourceRecognizeTMDBCore(ctx, catalog.Media{ID: n, MediaType: kind}) })
				return complete.MediaInfo, e
			}
			var view catalog.ExploreMedia
			_, e := a.cached(ctx, "explore-detail:v1:"+source+":"+kind+":"+id, 12*time.Hour, &view, func() (any, error) { return explorer.Detail(catalog.OriginalPluginContext(ctx), source, kind, id) })
			if e != nil {
				return nil, e
			}
			nativeView = &view
			return originalCatalogNativeSourceModel(view)
		})
	}
	key := source + ":" + id
	if len(fullKeys) > 0 {
		key = fullKeys[0]
	}
	model, err := a.originalCatalogDetailRecognition(ctx, key, kind, title, year, recognize, func(title, year string) (json.RawMessage, error) {
		return a.originalCatalogNameRecognition(ctx, title, year, kind, func(meta *releasemeta.MetaObject) (json.RawMessage, error) {
			query := catalog.ExploreMedia{Media: catalog.Media{Title: meta.Name(), MediaType: kind}, Source: source, SourceID: id}
			query.Year, _ = strconv.Atoi(sourceStringValue(meta.Get("year")))
			if meta.Get("type") == "电视剧" {
				query.MediaType = "tv"
			}
			resolved, e := a.catalog().ResolveExploreMedia(catalog.OriginalPluginContext(ctx), query)
			if e != nil || resolved.Media == nil {
				return nil, e
			}
			complete, e := a.sourceRecognizeTMDBCore(ctx, *resolved.Media)
			return complete.MediaInfo, e
		})
	})
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	if !originalModuleResultTruthy(model) {
		return catalog.ExploreMedia{}, originalRecognitionMissing()
	}
	model, err = a.originalObtainCatalogImages(ctx, model, func(raw json.RawMessage) (json.RawMessage, error) {
		return originalCatalogTMDBImages(raw, "themoviedb", func(imageKind string, tmdbID any, language any) (json.RawMessage, error) {
			n, e := strconv.ParseInt(sourceStringValue(tmdbID), 10, 64)
			if e != nil {
				return nil, e
			}
			return a.catalog().SourceImages(catalog.OriginalPluginContext(ctx), imageKind, n, "zh", sourceText(language))
		}, a.Settings().TMDBImageDomain)
	})
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	public, err := originalPublicCatalogMedia(model)
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	result, err := originalCatalogMediaProjection(public)
	if err != nil {
		return result, err
	}
	if nativeView != nil {
		result.Recommendations, result.Cast, result.Seasons = nativeView.Recommendations, nativeView.Cast, nativeView.Seasons
	}
	if result.Source == "themoviedb" {
		items := []catalog.Media{result.Media}
		if err = a.decorateMedia(ctx, items); err != nil {
			return result, err
		}
		result.Media = items[0]
	}
	return result, nil
}

// Existing non-TMDB clients expose a native view rather than a source provider
// dataclass. Retain that view while passing its explicit fields to the module
// boundary; this does not invent an unqueried private provider payload.
func originalCatalogNativeSourceModel(view catalog.ExploreMedia) (json.RawMessage, error) {
	if len(view.OriginalMedia) > 0 {
		return originalLiveMediaImagesJSON(view.OriginalMedia)
	}
	model, err := rsscontext.NewMediaInfo(nil)
	if err != nil {
		return nil, err
	}
	kind := "电影"
	if view.MediaType == "tv" {
		kind = "电视剧"
	}
	fields := map[string]any{"source": view.Source, "media_id": view.SourceID, "type": kind, "title": view.Title, "original_title": view.OriginalTitle, "year": strconv.Itoa(view.Year), "overview": view.Overview, "poster_path": view.PosterPath, "backdrop_path": view.BackdropPath, "imdb_id": view.IMDbID, "vote_average": view.VoteAverage, "vote_count": view.VoteCount, "names": view.Aliases}
	if key := map[string]string{"douban": "douban_id", "bangumi": "bangumi_id", "anilist": "anilist_id"}[view.Source]; key != "" {
		fields[key] = view.SourceID
	}
	if err = model.FromDict(fields); err != nil {
		return nil, err
	}
	return json.Marshal(model.Fields)
}
