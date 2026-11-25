package app

import (
	"runtime"
	"testing"
	"time"
)

func TestMemoryReclaimInterval(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		have    bool
		want    time.Duration
		wantErr bool
	}{
		{name: "unset keeps default", have: false, want: defaultMemoryReclaimInterval},
		{name: "blank keeps default", value: "   ", have: true, want: defaultMemoryReclaimInterval},
		{name: "duration", value: "2m", have: true, want: 2 * time.Minute},
		{name: "off disables", value: "off", have: true, want: 0},
		{name: "zero disables", value: "0", have: true, want: 0},
		{name: "disabled disables", value: "Disabled", have: true, want: 0},
		{name: "garbage fails", value: "soon", have: true, wantErr: true},
		{name: "negative fails", value: "-5m", have: true, wantErr: true},
		{name: "zero duration fails", value: "0s", have: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(key string) (string, bool) {
				if key != "LIGHT_MEDIA_MEMORY_RECLAIM_INTERVAL" {
					t.Fatalf("unexpected key %q", key)
				}
				return tc.value, tc.have
			}
			got, err := memoryReclaimInterval(lookup)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestShouldReclaimOnlyWhenQuiet(t *testing.T) {
	previous := runtime.MemStats{TotalAlloc: 100 << 20}
	current := runtime.MemStats{TotalAlloc: previous.TotalAlloc + 1<<20, HeapIdle: 80 << 20, HeapReleased: 64 << 20}
	if !shouldReclaim(previous, current, memoryReclaimIdleAlloc) {
		t.Fatal("quiet interval with 16 MiB still retained should reclaim")
	}
	current.HeapReleased++
	if shouldReclaim(previous, current, memoryReclaimIdleAlloc) {
		t.Fatal("already scavenged pages must not trigger a GC")
	}
	current.HeapReleased = 0
	current.TotalAlloc = previous.TotalAlloc + memoryReclaimIdleAlloc
	if shouldReclaim(previous, current, memoryReclaimIdleAlloc) {
		t.Fatal("a busy interval must not trigger a scavenge")
	}
	current.TotalAlloc = previous.TotalAlloc - 1
	if shouldReclaim(previous, current, memoryReclaimIdleAlloc) {
		t.Fatal("a counter reset must not trigger a scavenge")
	}
}
