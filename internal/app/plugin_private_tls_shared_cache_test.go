package app

import (
	"context"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func TestOriginalPluginCatalogCacheRemainsSharedAndClearable(t *testing.T) {
	a := newTestApp(t)
	config := a.Settings()
	config.TMDB = TMDBConfig{APIKey: "owned-policy"}
	a.cfgMu.Lock()
	a.config = config
	a.cfgMu.Unlock()
	ctx := context.Background()
	client := a.catalog()
	key := "source-search:movie:owned-tls-policy:"
	calls := 0
	fetch := func() (any, error) { calls++; return map[string]int{"value": calls}, nil }
	var first, second, third map[string]int
	if _, err := a.cached(catalog.OriginalPluginContext(ctx), key, time.Hour, &first, fetch); err != nil {
		t.Fatal(err)
	}
	if _, err := a.cached(ctx, key, time.Hour, &second, fetch); err != nil {
		t.Fatal(err)
	}
	if first["value"] != 1 || second["value"] != 1 || calls != 1 || a.catalog() != client {
		t.Fatal("plugin context forked client/cache")
	}
	// Existing real module clear path, same owned Store; no worker or HTTP.
	if err := a.clearOriginalModuleCaches(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.cached(catalog.OriginalPluginContext(ctx), key, time.Hour, &third, fetch); err != nil {
		t.Fatal(err)
	}
	if third["value"] != 2 || calls != 2 || a.catalog() != client {
		t.Fatal("clear_cache missed plugin cache or replaced client")
	}
	if originalPluginSiteRequest(ctx) || originalPluginSiteRequest(context.WithValue(ctx, "originalPluginSiteHTTPS", true)) || !originalPluginSiteRequest(originalPluginSiteContext(ctx)) {
		t.Fatal("site TLS selection accepted a non-host marker")
	}
	canceled, cancel := context.WithCancel(ctx)
	marked := originalPluginSiteContext(canceled)
	cancel()
	if marked.Err() != context.Canceled {
		t.Fatal("site marker dropped cancellation")
	}
}
