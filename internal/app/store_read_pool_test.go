package app

import (
	"context"
	"testing"
)

// The reader pool exists to keep UI reads off the organizer's writer. Its page
// cache is Go heap that GC never reclaims, so the budget is asserted here: four
// connections must survive, but each must not reserve the old 8 MiB cache.
func TestReadPoolKeepsFourConnectionsWithBoundedPageCache(t *testing.T) {
	a, _, _ := importTestApp(t)
	pool := a.Store.reader()
	if got := pool.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("reader pool lost concurrency: %d connections", got)
	}
	ctx := context.Background()
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var cacheKiB int
	if err = conn.QueryRowContext(ctx, `PRAGMA cache_size`).Scan(&cacheKiB); err != nil {
		t.Fatal(err)
	}
	// A positive value is a page count (and would be a hard-coded page size);
	// negative means KiB. The bound must stay below the previous 8192.
	if cacheKiB != -2048 {
		t.Fatalf("reader page cache = %d, want -2048 KiB", cacheKiB)
	}
	var readOnly int
	if err = conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&readOnly); err != nil {
		t.Fatal(err)
	}
	if readOnly != 1 {
		t.Fatalf("reader connection is not query_only: %d", readOnly)
	}
}
