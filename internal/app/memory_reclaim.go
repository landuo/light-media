package app

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// The Go runtime keeps a freed heap mapped after a burst. GOMEMLIMIT only
// lowers the GC target; it does not hand pages back while the process is idle,
// so a container that has run a scrape or a batch keeps the high-water mark for
// its whole lifetime. This reclaim loop returns those pages when nothing is
// allocating, without adding a second GC policy: it reuses the runtime's own
// scavenger and stays out of the way while work is in flight.
const defaultMemoryReclaimInterval = 5 * time.Minute

// A process that just finished a batch still allocates a little while settling.
// Reclaiming only below this window keeps a busy host untouched.
const memoryReclaimIdleAlloc = 48 << 20

// memoryReclaimInterval reads LIGHT_MEDIA_MEMORY_RECLAIM_INTERVAL. An empty
// value keeps the default; "off" and "0" disable the loop so an operator can
// take the whole mechanism out without rebuilding.
func memoryReclaimInterval(lookup func(string) (string, bool)) (time.Duration, error) {
	raw, ok := lookup("LIGHT_MEDIA_MEMORY_RECLAIM_INTERVAL")
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultMemoryReclaimInterval, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "0", "false", "disabled":
		return 0, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid LIGHT_MEDIA_MEMORY_RECLAIM_INTERVAL: %w", err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("invalid LIGHT_MEDIA_MEMORY_RECLAIM_INTERVAL: must be positive")
	}
	return value, nil
}

// shouldReclaim reports whether the sampled allocations are quiet enough to
// justify a scavenge. TotalAlloc is monotonic, so a small delta over one
// interval means no batch, scrape or plugin call has been allocating.
func shouldReclaim(previous, current runtime.MemStats, idleAlloc uint64) bool {
	if current.TotalAlloc < previous.TotalAlloc {
		return false
	}
	return current.TotalAlloc-previous.TotalAlloc < idleAlloc
}

func memorySnapshot() string {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return fmt.Sprintf("heap_inuse=%dMiB heap_idle=%dMiB sys=%dMiB rss=%dMiB goroutine=%d",
		stats.HeapInuse>>20, stats.HeapIdle>>20, stats.Sys>>20, residentMemoryBytes()>>20, runtime.NumGoroutine())
}

func (a *App) runMemoryReclaim(ctx context.Context) {
	interval, err := memoryReclaimInterval(os.LookupEnv)
	if err != nil {
		a.log("ERROR", "runtime", "memory_reclaim_disabled", err.Error(), "", "")
		return
	}
	if interval == 0 {
		a.log("INFO", "runtime", "memory_reclaim_disabled", "内存归还已关闭（LIGHT_MEDIA_MEMORY_RECLAIM_INTERVAL=off）", "", "")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var previous runtime.MemStats
	runtime.ReadMemStats(&previous)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var current runtime.MemStats
		runtime.ReadMemStats(&current)
		quiet := shouldReclaim(previous, current, memoryReclaimIdleAlloc)
		previous = current
		if !quiet {
			continue
		}
		before := memorySnapshot()
		debug.FreeOSMemory()
		a.log("INFO", "runtime", "memory_reclaimed", "空闲期归还内存："+before+" -> "+memorySnapshot(), "", "")
	}
}
