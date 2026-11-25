package app

import (
	"context"
	"testing"
	"time"

	"light-media/internal/sites"
)

// Keep all four readers off the organizer's writer while bounding their
// SQLite page caches individually.
func TestReadPoolKeepsFourConnectionsWithBoundedPageCache(t *testing.T) {
	a, _, _ := importTestApp(t)
	pool := a.Store.reader()
	if got := pool.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("reader pool lost concurrency: %d connections", got)
	}
	ctx := context.Background()
	for i := range 4 {
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var cacheKiB int
		if err = conn.QueryRowContext(ctx, `PRAGMA cache_size`).Scan(&cacheKiB); err != nil {
			t.Fatal(err)
		}
		if cacheKiB != -1024 {
			t.Fatalf("reader %d page cache = %d, want -1024 KiB", i, cacheKiB)
		}
		var readOnly int
		if err = conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&readOnly); err != nil {
			t.Fatal(err)
		}
		if readOnly != 1 {
			t.Fatalf("reader %d is not query_only: %d", i, readOnly)
		}
	}
}

func TestBrushPageSiteReadsContinueWhileWriterIsBusy(t *testing.T) {
	a := newTestApp(t)
	settings := a.Settings()
	settings.Sites = []SiteConfig{{Config: sites.Config{
		ID: "page-read", Name: "Page read", Kind: "nexusphp", BaseURL: "https://page-read.example.org/",
	}, Enabled: true}}
	ownedBrushInventorySave(t, a, settings)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	writer, err := a.Store.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	rows, err := a.originalSourceSiteRows(ctx)
	if err != nil || len(rows) != 1 || rows[0].site.ID != "page-read" {
		t.Fatalf("source site list under writer contention: count=%d err=%v", len(rows), err)
	}
	inventory, err := a.originalBrushInventory(ctx)
	if err != nil || len(inventory.Sites) != 1 || inventory.Sites[0].SourceID != rows[0].sourceID {
		t.Fatalf("brush inventory under writer contention: count=%d err=%v", len(inventory.Sites), err)
	}
}
