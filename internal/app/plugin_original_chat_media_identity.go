package app

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"light-media/internal/catalog"
)

// Keep the selected provider identity alongside any verified TMDB mapping.
// A source-only selection can be recognized again for subscription creation.
type originalChatMediaProvider struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	TMDBID int64  `json:"tmdb_id"`
}

type originalChatMediaProviderKey struct{}

func originalChatWithMediaProvider(ctx context.Context, provider *originalChatMediaProvider) context.Context {
	if provider == nil {
		return ctx
	}
	return context.WithValue(ctx, originalChatMediaProviderKey{}, *provider)
}

func originalChatMediaProviderFrom(ctx context.Context) *originalChatMediaProvider {
	provider, ok := ctx.Value(originalChatMediaProviderKey{}).(originalChatMediaProvider)
	if !ok {
		return nil
	}
	return &provider
}

func (p originalChatMediaProvider) field() string {
	switch p.Source {
	case "douban":
		return "douban_id"
	case "bangumi":
		return "bangumi_id"
	case "anilist":
		return "anilist_id"
	default:
		return ""
	}
}

func (p originalChatMediaProvider) valid(id int64) bool {
	return p.sourceValid() && id > 0 && p.TMDBID == id
}

func (p originalChatMediaProvider) sourceValid() bool {
	number, err := strconv.ParseInt(p.ID, 10, 64)
	return p.field() != "" && err == nil && number > 0 && p.ID == strconv.FormatInt(number, 10)
}

func (p *originalChatMediaProvider) sourceOnly() bool {
	return p != nil && p.TMDBID == 0 && p.sourceValid()
}

func (a *App) originalChatResolveMediaSelection(ctx context.Context, selection catalog.ExploreMedia) (catalog.Media, *originalChatMediaProvider, catalog.ExploreMedia, error) {
	if selection.Source == "themoviedb" && validMedia(selection.MediaType, selection.ID) {
		return selection.Media, nil, selection, nil
	}
	provider := &originalChatMediaProvider{Source: selection.Source, ID: selection.SourceID}
	if !provider.sourceValid() || (selection.MediaType != "movie" && selection.MediaType != "tv") {
		return catalog.Media{}, nil, catalog.ExploreMedia{}, bad("INVALID_MEDIA", "无法识别所选媒体！")
	}
	sourceType := "电影"
	if selection.MediaType == "tv" {
		sourceType = "电视剧"
	}
	kwargs, err := originalCatalogRecognitionCallKwargs(nil, map[string]any{
		"mtype": sourceType, "source": provider.Source, "mediaid": provider.ID,
	})
	if err != nil {
		return catalog.Media{}, nil, catalog.ExploreMedia{}, err
	}
	ctx = context.WithValue(ctx, originalHostMediaNativeSourceKey{}, provider.Source)
	model, err := a.originalCatalogRecognizeMethod(ctx, "recognize_media", kwargs, func() (json.RawMessage, error) {
		return a.originalHostMediaRecognizeSource(ctx, provider.Source, kwargs)
	})
	if err != nil {
		return catalog.Media{}, nil, catalog.ExploreMedia{}, err
	}
	if !originalModuleResultTruthy(model) {
		return catalog.Media{}, nil, catalog.ExploreMedia{}, bad("MEDIA_NOT_FOUND", "无法重新识别所选来源的媒体")
	}
	view, err := originalCatalogMediaProjection(model)
	if err != nil {
		return catalog.Media{}, nil, catalog.ExploreMedia{}, err
	}
	if view.Source != provider.Source || view.SourceID != provider.ID || view.MediaType != selection.MediaType {
		return catalog.Media{}, nil, catalog.ExploreMedia{}, bad("MEDIA_SOURCE_CHANGED", "所选媒体的来源身份或类型发生变化")
	}
	view.PosterPath = catalog.ExploreImageURL(provider.Source, view.PosterPath)
	view.BackdropPath = catalog.ExploreImageURL(provider.Source, view.BackdropPath)
	resolved, err := a.catalog().ResolveExploreMedia(ctx, view)
	if ctx.Err() != nil {
		return catalog.Media{}, nil, catalog.ExploreMedia{}, ctx.Err()
	}
	if err != nil || resolved.Media == nil || !validMedia(selection.MediaType, resolved.Media.ID) || resolved.Media.MediaType != selection.MediaType {
		return view.Media, provider, view, nil
	}
	provider.TMDBID = resolved.Media.ID
	if !provider.valid(resolved.Media.ID) {
		return catalog.Media{}, nil, catalog.ExploreMedia{}, bad("MEDIA_SOURCE_CHANGED", "所选媒体的来源编号无法核对")
	}
	return *resolved.Media, provider, view, nil
}

// MoviePilot supplements only TMDB compatibility fields, preserving the
// selected provider's primary identity and its display/recognition fields.
func originalChatSupplementMedia(primary, native json.RawMessage, provider *originalChatMediaProvider, media catalog.Media) (json.RawMessage, catalog.Media, error) {
	if provider == nil {
		return native, media, nil
	}
	primaryValue, err := decodeSourceJSON(primary)
	if err != nil || sourceMap(primaryValue) == nil || !provider.valid(media.ID) {
		return nil, media, originalCatalogMediaInvalid()
	}
	fields := sourceMap(primaryValue)
	if len(native) > 0 {
		nativeValue, err := decodeSourceJSON(native)
		tmdb := sourceMap(nativeValue)
		if err != nil || sourceText(tmdb["source"]) != "themoviedb" || sourceStringValue(tmdb["tmdb_id"]) != strconv.FormatInt(media.ID, 10) {
			return nil, media, originalCatalogMediaInvalid()
		}
		if sourceSubscriptionTruth(tmdb["tmdb_info"]) {
			fields["tmdb_info"] = tmdb["tmdb_info"]
		}
		for _, key := range []string{"category", "genre_ids", "imdb_id", "tvdb_id", "collection_id"} {
			if !sourceSubscriptionTruth(fields[key]) {
				fields[key] = tmdb[key]
			}
		}
	}
	fields["tmdb_id"] = media.ID
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, media, err
	}
	media, err = originalChatProviderTargetMedia(encoded, provider, media)
	return encoded, media, err
}

func originalChatProviderTargetMedia(raw json.RawMessage, provider *originalChatMediaProvider, native catalog.Media) (catalog.Media, error) {
	if provider.sourceOnly() {
		info, err := originalChatProviderSearchInformation(raw, provider, native.MediaType)
		if err != nil {
			return native, err
		}
		if !providerNativeMedia(native) || mediaKey(native) != mediaKey(info.Media) {
			return native, bad("MEDIA_SOURCE_CHANGED", "所选媒体与已核对的来源身份不一致")
		}
		return info.Media, nil
	}
	value, err := decodeSourceJSON(raw)
	fields := sourceMap(value)
	if err != nil || fields == nil || !provider.valid(native.ID) || sourceText(fields["source"]) != provider.Source ||
		sourceStringValue(fields["media_id"]) != provider.ID || sourceStringValue(fields[provider.field()]) != provider.ID ||
		sourceStringValue(fields["tmdb_id"]) != strconv.FormatInt(native.ID, 10) ||
		sourceText(fields["type"]) != map[string]string{"movie": "电影", "tv": "电视剧"}[native.MediaType] {
		return native, bad("MEDIA_SOURCE_CHANGED", "所选媒体与已核对的来源身份不一致")
	}
	aliases := slices.Clone(native.Aliases)
	addAlias := func(title string) {
		if title != "" && title != native.Title && title != native.OriginalTitle && !slices.Contains(aliases, title) {
			aliases = append(aliases, title)
		}
	}
	for _, key := range []string{"title", "original_title", "en_title", "hk_title", "tw_title", "sg_title"} {
		addAlias(sourceText(fields[key]))
	}
	for _, name := range sourceRows(fields["names"]) {
		addAlias(sourceText(name))
	}
	native.Aliases = aliases
	return native, nil
}

func (a *App) originalChatFrozenDownloadTarget(native catalog.Media, raw json.RawMessage, provider *originalChatMediaProvider) (catalog.Media, error) {
	if provider.sourceOnly() {
		return originalChatProviderTargetMedia(raw, provider, native)
	}
	if provider != nil {
		value, err := decodeSourceJSON(raw)
		fields := sourceMap(value)
		if err != nil || fields == nil {
			return native, originalCatalogMediaInvalid()
		}
		// Earlier saved downloads used a native primary with an alternate ID
		// sidecar. Allow those sealed records to finish with their old context.
		if source := sourceText(fields["source"]); (source != "" && source != "themoviedb") || sourceStringValue(fields[provider.field()]) != provider.ID {
			return originalChatProviderTargetMedia(raw, provider, native)
		}
	}
	info, err := a.originalRecognitionProjection(native, raw)
	return info.Media, err
}

func originalChatAttachProvider(raw json.RawMessage, provider *originalChatMediaProvider, tmdbID int64) (json.RawMessage, error) {
	if provider == nil {
		return raw, nil
	}
	if !provider.valid(tmdbID) {
		return nil, bad("MEDIA_SOURCE_CHANGED", "所选媒体与已核对的来源身份不一致")
	}
	value, err := decodeSourceJSON(raw)
	fields := sourceMap(value)
	if err != nil || fields == nil || sourceStringValue(fields["tmdb_id"]) != strconv.FormatInt(tmdbID, 10) {
		return nil, originalCatalogMediaInvalid()
	}
	if provider.Source == "douban" {
		fields[provider.field()] = provider.ID
	} else {
		fields[provider.field()] = json.Number(provider.ID)
	}
	return json.Marshal(fields)
}
