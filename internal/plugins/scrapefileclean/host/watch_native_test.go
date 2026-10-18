//go:build linux || darwin

package scrapehost

import (
	"context"
	"os"
	"testing"
	"time"
)

func sourceWait(t *testing.T, d time.Duration, fn func() bool) {
	t.Helper()
	until := time.Now().Add(d)
	for time.Now().Before(until) {
		if fn() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("owned native observer condition not reached")
}
func sourceUseActualWatcher(t *testing.T, f *sourceFixture) {
	t.Helper()
	if e := f.h.Suspend(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.options.Watch = nil
	f.options.Now = time.Now
	var e error
	f.h, e = Open(f.options)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.h.Recover(context.Background(), f.binding); e != nil {
		t.Fatal(e)
	}
}
func TestSourceNativeActualOSWatcherImmediate(t *testing.T) {
	f := sourceNativeFixture(t, false)
	sourceUseActualWatcher(t, f)
	if e := os.Remove(f.src); e != nil {
		t.Fatal(e)
	}
	sourceWait(t, 4*time.Second, func() bool { return !nativeExists(f.alias) })
	s, e := f.h.Snapshot(context.Background())
	if e != nil || s.Blocked != "" {
		t.Fatal(s, e)
	}
}
func TestSourceNativeActualOSWatcherAndRealDelayTimer(t *testing.T) {
	f := sourceNativeFixture(t, true)
	sourceUseActualWatcher(t, f)
	at := time.Now()
	if e := os.Remove(f.src); e != nil {
		t.Fatal(e)
	}
	sourceWait(t, 4*time.Second, func() bool { s, e := f.h.Snapshot(context.Background()); return e == nil && len(s.Machine.Queue) == 1 })
	if !nativeExists(f.alias) {
		t.Fatal("native watcher deleted before source delay")
	}
	sourceWait(t, 13*time.Second, func() bool { return !nativeExists(f.alias) })
	if time.Since(at) < 10*time.Second {
		t.Fatal("source actual minimum delay shortened")
	}
	s, e := f.h.Snapshot(context.Background())
	if e != nil || s.Blocked != "" || len(s.Machine.Queue) != 0 {
		t.Fatal(s, e)
	}
}
