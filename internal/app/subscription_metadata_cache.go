package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/catalog"
	"time"
)

// Reuse only trusted provider bytes. Category policy and image domain are live
// settings, so never reuse the derived MediaInfo or compact Media projection.
func (a *App) cachedSubscriptionSourceInformation(ctx context.Context, input catalog.Media) (sourceMediaInformation, error) {
	var cached struct {
		At   time.Time              `json:"at"`
		Data sourceMediaInformation `json:"data"`
	}
	key := fmt.Sprintf("source-detail:%s:%d", input.MediaType, input.ID)
	if a.Store.Get(ctx, "cache", key, &cached) != nil || time.Since(cached.At) >= 24*time.Hour {
		return a.sourceRecognizeTMDBCore(ctx, input)
	}
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil {
		return sourceMediaInformation{}, err
	}
	fields := policy.extraFields(input.MediaType)
	var provider map[string]json.RawMessage
	if json.Unmarshal(cached.Data.ProviderInfo, &provider) != nil {
		return a.sourceRecognizeTMDBCore(ctx, input)
	}
	for _, field := range fields {
		if _, ok := provider[field]; !ok {
			return a.sourceRecognizeTMDBCore(ctx, input)
		}
	}
	media, err := a.catalog().ProjectSourceInformation(input.MediaType, input.ID, cached.Data.ProviderInfo, fields)
	if err != nil {
		return a.sourceRecognizeTMDBCore(ctx, input)
	}
	category, err := policy.categoryWithError(media)
	if err != nil {
		return sourceMediaInformation{}, err
	}
	projection, err := sourceTMDBMediaInfo(cached.Data.ProviderInfo, input.MediaType, a.Settings().TMDBImageDomain, category)
	if err != nil {
		return sourceMediaInformation{}, err
	}
	projected, err := decodeSourceJSON(projection)
	if err != nil {
		return sourceMediaInformation{}, err
	}
	media.Title, media.OriginalTitle = sourceText(sourceMap(projected)["title"]), sourceText(sourceMap(projected)["original_title"])
	return sourceMediaInformation{ProviderInfo: cached.Data.ProviderInfo, MediaInfo: projection, Media: media}, nil
}
