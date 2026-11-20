package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"light-media/internal/catalog"
)

// A manual check must not replay a season snapshot taken while the metadata
// service was unreachable: it drops the cache entry so the next read refetches.
func TestManualCheckDropsSeasonCache(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	key := "season:12345:1"
	if err := a.Store.Put(ctx, "cache", key, map[string]any{"at": time.Now().UTC(), "data": json.RawMessage(`{"series_id":12345,"season_number":1,"episodes":[]}`)}); err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := a.Store.Get(ctx, "cache", key, &probe); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	a.dropSubscriptionSeasonCache(ctx, Subscription{Media: catalog.Media{ID: 12345}, Season: 1})
	if err := a.Store.Get(ctx, "cache", key, &probe); err == nil {
		t.Fatal("the season snapshot must be gone after a manual check")
	}
}
