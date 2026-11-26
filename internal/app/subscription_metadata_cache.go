package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"light-media/internal/catalog"
)

// Reuse only trusted provider bytes. Category policy and image domain are live
// settings, so never reuse the derived MediaInfo or compact Media projection.
func (a *App) cachedTMDBSourceInformation(ctx context.Context, input catalog.Media) (sourceMediaInformation, error) {
	key := fmt.Sprintf("source-detail:%s:%d", input.MediaType, input.ID)
	var cached struct {
		At   time.Time                              `json:"at"`
		Data struct{ ProviderInfo json.RawMessage } `json:"data"`
	}
	fetch := func() (sourceMediaInformation, error) {
		full, err := a.sourceRecognizeTMDBCore(ctx, input)
		if err == nil {
			// Caching is optional; a validated upstream result remains usable.
			_ = a.Store.Put(ctx, "cache", key, struct {
				At   time.Time              `json:"at"`
				Data sourceMediaInformation `json:"data"`
			}{time.Now().UTC(), full})
		}
		return full, err
	}
	if a.Store.Get(ctx, "cache", key, &cached) != nil || time.Since(cached.At) >= 24*time.Hour {
		return fetch()
	}
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil {
		return sourceMediaInformation{}, err
	}
	fields := policy.extraFields(input.MediaType)
	if len(fields) != 0 {
		var provider map[string]json.RawMessage
		if json.Unmarshal(cached.Data.ProviderInfo, &provider) != nil {
			return fetch()
		}
		for _, field := range fields {
			if _, ok := provider[field]; !ok {
				return fetch()
			}
		}
	}
	media, err := a.catalog().ProjectSourceInformation(input.MediaType, input.ID, cached.Data.ProviderInfo, fields)
	if err != nil {
		return fetch()
	}
	return a.projectTMDBSourceInformation(cached.Data.ProviderInfo, media, policy)
}
