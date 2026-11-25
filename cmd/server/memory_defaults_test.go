package main

import (
	"runtime/debug"
	"testing"
)

func TestConfigureMemoryDefaults(t *testing.T) {
	// The runtime parses environment values before main. Install the values it
	// would have parsed, then check that main does not replace them.
	oldGC := debug.SetGCPercent(177)
	oldLimit := debug.SetMemoryLimit(72 << 20)
	defer debug.SetGCPercent(oldGC)
	defer debug.SetMemoryLimit(oldLimit)
	for _, tc := range []struct {
		gc, limit string
		wantGC    int
		wantLimit int64
	}{
		{"", "", 200, 56 << 20}, {"177", "", 177, 56 << 20},
		{"", "72MiB", 200, 72 << 20}, {"177", "72MiB", 177, 72 << 20},
	} {
		t.Setenv("GOGC", tc.gc)
		t.Setenv("GOMEMLIMIT", tc.limit)
		debug.SetGCPercent(177)
		debug.SetMemoryLimit(72 << 20)
		configureMemoryDefaults()
		if gc, limit := debug.SetGCPercent(177), debug.SetMemoryLimit(-1); gc != tc.wantGC || limit != tc.wantLimit {
			t.Fatalf("env %q/%q: got %d/%d want %d/%d", tc.gc, tc.limit, gc, limit, tc.wantGC, tc.wantLimit)
		}
	}
}
