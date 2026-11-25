package autosignin

import "context"

// The source cache's indent=4 encoder is Python code, with the fixed captured
// interpreter's recursion limit1000. Its C JSON scanner has a different bound.
// Real source entry stacks were executed separately: selected __signin helper,
// full Tjupt.signin, direct signin_site, FastAPI's sync WorkerThread, and the
// actual multiprocessing.pool.ThreadPool worker. The outer scheduler/event
// stack is absent in the latter thread. These are private logical entry labels,
// never Go stack depth, a user knob, a host grant or a general Python executor.
type sourceCacheDumpEntry uint8

const (
	sourceCacheSubmit sourceCacheDumpEntry = iota + 1
	sourceCacheAdapter
	sourceCacheVisitor
	sourceCacheAPI
	sourceCachePool
)

type sourceCacheDumpEntryKey struct{}

func cacheDumpEntry(ctx context.Context) sourceCacheDumpEntry {
	if entry, ok := ctx.Value(sourceCacheDumpEntryKey{}).(sourceCacheDumpEntry); ok && entry >= sourceCacheSubmit && entry <= sourceCachePool {
		return entry
	}
	return sourceCacheSubmit
}
func withCacheDumpEntry(ctx context.Context, entry sourceCacheDumpEntry) context.Context {
	return context.WithValue(ctx, sourceCacheDumpEntryKey{}, entry)
}
func defaultCacheDumpEntry(ctx context.Context, entry sourceCacheDumpEntry) context.Context {
	if current, ok := ctx.Value(sourceCacheDumpEntryKey{}).(sourceCacheDumpEntry); ok && current >= sourceCacheSubmit && current <= sourceCachePool {
		return ctx
	}
	return withCacheDumpEntry(ctx, entry)
}
func cacheDumpMaxDepth(ctx context.Context) int {
	// The captured encoder/iterator boundary reserves6 frames beyond the
	// observed success-log ancestors. Root cache dict indentation is level0.
	// Empty containers and scalar leaves finish without recursive descent.
	// At the last permitted level, int/float/NaN/string/null/empty leaves all
	// pass; the next nonempty list/dict level fails. Mixed trees were also
	// executed, rather than treating the standalone helper's995 as universal.
	ancestors := 2
	switch cacheDumpEntry(ctx) {
	case sourceCacheAdapter:
		ancestors = 3
	case sourceCacheVisitor:
		ancestors = 4
	case sourceCacheAPI:
		ancestors = 7
	case sourceCachePool:
		ancestors = 8
	}
	return 1000 - 6 - ancestors
}
func cacheDumpRecursionError() error {
	return &ValueError{"RecursionError", "maximum recursion depth exceeded"}
}
