// Measure per-connection SQLite page-cache bytes, not process RSS.
// Uses a fresh, synthetic database and never connects to the service.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
)

func run(kib, idle int) (map[string]any, error) {
	if kib < 1 || kib > 8192 || idle < 0 || idle > 4 {
		return nil, fmt.Errorf("invalid experiment budget")
	}
	dir, e := os.MkdirTemp("", "lm-sqlitebudget-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, "fixture.db")
	db, e := sql.Open("sqlite", p)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	_, e = db.Exec(`CREATE TABLE sample(id INTEGER PRIMARY KEY,b BLOB NOT NULL); WITH RECURSIVE s(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM s WHERE n<4096) INSERT INTO sample SELECT n,zeroblob(3000) FROM s`)
	if e != nil {
		return nil, e
	}
	if e = db.Close(); e != nil {
		return nil, e
	}
	rd, e := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(ON)&_pragma=mmap_size(0)&_pragma=cache_size(-%d)", p, kib))
	if e != nil {
		return nil, e
	}
	defer rd.Close()
	rd.SetMaxOpenConns(4)
	rd.SetMaxIdleConns(idle)
	ctx := context.Background()
	cs := make([]*sql.Conn, 4)
	for i := range cs {
		cs[i], e = rd.Conn(ctx)
		if e != nil {
			return nil, e
		}
		defer cs[i].Close()
	}
	var total int64
	start := time.Now()
	for pass := 0; pass < 4; pass++ {
		for _, c := range cs {
			var n int64
			if e = c.QueryRowContext(ctx, `SELECT sum(length(substr(b,1,16))) FROM sample`).Scan(&n); e != nil {
				return nil, e
			}
			if n != 4096*16 {
				return nil, fmt.Errorf("incorrect scan result")
			}
			total += n
		}
	}
	ms := time.Since(start).Milliseconds()
	sumCounter := func(conns []*sql.Conn, op sqlite.DBStatusOp) (int, error) {
		var total int
		for _, c := range conns {
			if err := c.Raw(func(dc any) error {
				status, ok := dc.(sqlite.DBStatus)
				if !ok {
					return fmt.Errorf("driver DBStatus missing")
				}
				v, _, e := status.Status(op, false)
				total += v
				return e
			}); err != nil {
				return 0, err
			}
		}
		return total, nil
	}
	warm, e := sumCounter(cs, sqlite.DBStatusCacheUsed)
	if e != nil {
		return nil, e
	}
	hits, e := sumCounter(cs, sqlite.DBStatusCacheHit)
	if e != nil {
		return nil, e
	}
	misses, e := sumCounter(cs, sqlite.DBStatusCacheMiss)
	if e != nil {
		return nil, e
	}
	if warm <= 0 || misses <= 0 {
		return nil, fmt.Errorf("cache counters unavailable")
	}
	for _, c := range cs {
		if e = c.Close(); e != nil {
			return nil, e
		}
	}

	idleCount := rd.Stats().Idle
	if idleCount != idle {
		return nil, fmt.Errorf("unexpected idle pool size: %d", idleCount)
	}
	// Borrow only the known idle connections, keeping them all held so none is counted twice.
	held := make([]*sql.Conn, 0, idleCount)
	for i := 0; i < idleCount; i++ {
		c, err := rd.Conn(ctx)
		if err != nil {
			return nil, err
		}
		held = append(held, c)
		defer c.Close()
	}
	retained, e := sumCounter(held, sqlite.DBStatusCacheUsed)
	if e != nil {
		return nil, e
	}
	return map[string]any{"cache_kib": kib, "idle_limit": idle, "idle_connections": idleCount, "validated_scan_sum": total, "scan_ms": ms, "held_cache_bytes": warm, "idle_cache_bytes": retained, "hits": hits, "misses": misses}, nil
}
func main() {
	k := flag.Int("cache-kib", 2048, "per reader page-cache budget")
	i := flag.Int("idle", 4, "max idle readers")
	flag.Parse()
	v, e := run(*k, *i)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	if e = json.NewEncoder(os.Stdout).Encode(v); e != nil {
		os.Exit(1)
	}
}
