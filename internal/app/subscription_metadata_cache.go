package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/catalog"
	"time"
)

// Cache only the native provider, never plugin results. Extension discovery and
// invocation still run on every request, including edits after plugin changes.
func (a *App) cachedNativeSeasonInformation(ctx context.Context, id int64, season int, group string) (json.RawMessage, error) {
	var raw json.RawMessage
	key := fmt.Sprintf("source-season:%d:%d:%s", id, season, group)
	_, err := a.cached(ctx, key, 12*time.Hour, &raw, func() (any, error) {
		return a.catalog().SourceSeasonInformation(ctx, id, season, group)
	})
	return raw, err
}

// Opening detail and submitting a new subscription must not independently
// download the same native information. Do not trust the browser's media copy.
func (a *App) cachedSubscriptionSourceInformation(ctx context.Context, input catalog.Media) (sourceMediaInformation, error) {
	var info sourceMediaInformation
	_, err := a.cached(ctx, fmt.Sprintf("source-detail:%s:%d", input.MediaType, input.ID), 24*time.Hour, &info, func() (any, error) {
		return a.sourceRecognizeTMDBCore(ctx, input)
	})
	return info, err
}
