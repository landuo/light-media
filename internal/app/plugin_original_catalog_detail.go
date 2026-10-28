package app

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"light-media/internal/catalog"
)

// The existing native detail view is a read-only async-recognition caller.
// Plugin discovery remains outside provider caches, followed by the original
// live-input image stage. Canonical source metadata and native view additions
// remain separate projections rather than a frozen-TMDB subscription contract.
func (a *App) originalCatalogTMDBDetail(ctx context.Context, kind string, id int64) (catalog.ExploreMedia, error) {
	if !validMedia(kind, id) {
		return catalog.ExploreMedia{}, bad("INVALID_MEDIA", "请选择明确的电影或剧集身份")
	}
	input := catalog.Media{ID: id, MediaType: kind}
	var nativeView *catalog.Media
	kwargs, err := originalCatalogRecognitionKwargs("themoviedb", strconv.FormatInt(id, 10), kind)
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	recognized, err := a.originalCatalogRecognize(ctx, kwargs, func() (json.RawMessage, error) {
		var complete sourceMediaInformation
		_, e := a.cached(ctx, "source-detail:"+kind+":"+strconv.FormatInt(id, 10), 24*time.Hour, &complete, func() (any, error) { return a.sourceRecognizeTMDBCore(ctx, input) })
		if e != nil {
			return nil, e
		}
		// Retain the existing native detail view's related-title presentation. The
		// full source model above is required for ordinary plugin module arguments.
		if view, e := a.media(ctx, kind, id); e == nil {
			nativeView = &view
		} else {
			view := complete.Media
			nativeView = &view
		}
		return complete.MediaInfo, nil
	})
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	if len(recognized) == 0 {
		return catalog.ExploreMedia{}, originalRecognitionMissing()
	}
	enriched, err := a.originalObtainCatalogImages(ctx, recognized, func(raw json.RawMessage) (json.RawMessage, error) {
		return originalCatalogTMDBImages(raw, "themoviedb", func(imageKind string, tmdbID any, originalLanguage any) (json.RawMessage, error) {
			parsed, e := strconv.ParseInt(sourceStringValue(tmdbID), 10, 64)
			if e != nil {
				return nil, e
			}
			return a.catalog().SourceImages(ctx, imageKind, parsed, "zh", sourceText(originalLanguage))
		}, a.Settings().TMDBImageDomain)
	})
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	public, err := originalPublicCatalogMedia(enriched)
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	result, err := originalCatalogMediaProjection(public)
	if err != nil {
		return result, err
	}
	if nativeView != nil {
		result.Recommendations = nativeView.Recommendations
		result.Cast = nativeView.Cast
		result.Seasons = nativeView.Seasons
		result.Character = nativeView.Character
		result.Job = nativeView.Job
	}
	items := []catalog.Media{result.Media}
	if err = a.decorateMedia(ctx, items); err != nil {
		return result, err
	}
	result.Media = items[0]
	return result, nil
}
