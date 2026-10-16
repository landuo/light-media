//go:build linux

package cleanuphost

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitState(t *testing.T, h *Host, test func(Durable) bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		s := snapshot(t, h)
		if s.Blocked != "" {
			t.Fatal("native watcher blocked", s.Blocked)
		}
		if test(s) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("native filesystem event was not committed before deadline")
}
func tracked(s Durable, p string) bool {
	for _, r := range s.Machine.State {
		if r.Path == p {
			return true
		}
	}
	return false
}
func TestNativeInotifyRenameDeleteAndDurableBoundEffects(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	f.opts.Watch = nil
	f.opts.Now = time.Now
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
		t.Fatal(e)
	}
	moved := filepath.Join(f.src, "renamed.mkv")
	if e := os.Rename(a, moved); e != nil {
		t.Fatal(e)
	}
	waitState(t, h, func(s Durable) bool { return tracked(s, moved) && !tracked(s, a) })
	if !exists(b) || f.effects.count("history_src") != 0 {
		t.Fatal("rename was treated as deletion")
	}
	if e := os.Remove(moved); e != nil {
		t.Fatal(e)
	}
	waitState(t, h, func(s Durable) bool { return !exists(b) && f.effects.count("notification") == 1 })
	if f.effects.count("history_src") != 2 || f.effects.count("download_file_deleted") != 2 {
		t.Fatal("real inotify failed to drive source and follower effects")
	}
	if e := h.Stop(ctx, "fixture-end"); e != nil {
		t.Fatal(e)
	}
}

func TestNativeInotifyCleanCloseRecoverStillDeletes(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	f.opts.Watch = nil
	f.opts.Now = time.Now
	ctx := context.Background()
	h := f.open(t)
	if err := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened := f.open(t)
	if err := reopened.Recover(ctx, f.opts.Binding); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	// No Observe/RecordEvent call: only the newly recovered native inotify
	// observer can turn this real unlink into a confirmed follower deletion.
	waitState(t, reopened, func(s Durable) bool {
		return !exists(b) && f.effects.count("notification") == 1
	})
	state := snapshot(t, reopened)
	found := false
	for _, receipt := range state.Events {
		if receipt.Event.Path == a && receipt.Event.Kind == "deleted" && receipt.Event.Mode == "hardlink" && receipt.State == "confirmed" {
			found = true
		}
	}
	if !found || f.effects.count("history_src") != 2 || f.effects.count("download_file_deleted") != 2 {
		t.Fatal("recovered native watcher did not commit actual source/follower deletion")
	}
	if err := reopened.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestNativeInotifyRecursiveMovePreservesEntityAndStopsOutsideScope(t *testing.T) {
	f := newFixture(t)
	f.opts.Watch = nil
	f.opts.Now = time.Now
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
		t.Fatal(e)
	}
	a := filepath.Join(f.src, "new", "sub", "episode.mkv")
	write(t, a, "nested real video")
	b := filepath.Join(f.media, "linked.mkv")
	if e := os.Link(a, b); e != nil {
		t.Fatal(e)
	}
	waitState(t, h, func(s Durable) bool { return tracked(s, a) && tracked(s, b) })
	if e := os.Rename(filepath.Join(f.src, "new"), filepath.Join(f.src, "moved")); e != nil {
		t.Fatal(e)
	}
	moved := filepath.Join(f.src, "moved", "sub", "episode.mkv")
	waitState(t, h, func(s Durable) bool { return tracked(s, moved) && !tracked(s, a) })
	if !exists(b) || f.effects.count("history_src") != 0 {
		t.Fatal("recursive rename triggered cleanup")
	}
	outside := filepath.Join(f.root, "outside.mkv")
	if e := os.Rename(moved, outside); e != nil {
		t.Fatal(e)
	}
	waitState(t, h, func(s Durable) bool { return !exists(b) })
	if !exists(outside) {
		t.Fatal("unwatched destination was deleted")
	}
	if e := h.Stop(ctx, "fixture-end"); e != nil {
		t.Fatal(e)
	}
}
func TestNativeInotifyContinuousDeadlineActuallyFires(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	f.opts.Watch = nil
	f.opts.Now = time.Now
	h := f.open(t)
	ctx := context.Background()
	cfg := f.config(true, map[string]any{"delay_seconds": 10})
	if e := h.Configure(ctx, f.opts.Binding, cfg); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	waitState(t, h, func(s Durable) bool { return len(s.Machine.Queue) == 1 })
	if !exists(b) {
		t.Fatal("delayed task ran immediately")
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !exists(b) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if exists(b) || time.Since(start) < 9*time.Second {
		t.Fatal("continuous timer did not respect the configured period")
	}
	if e := h.Stop(ctx, "fixture-end"); e != nil {
		t.Fatal(e)
	}
}

func TestNativeTrickplayMovesArePairedBeforeSourceFilter(t *testing.T) {
	for _, direction := range []string{"into", "out-of"} {
		t.Run(direction, func(t *testing.T) {
			f := newFixture(t)
			regular := filepath.Join(f.src, "ordinary.mkv")
			ignored := filepath.Join(f.src, "preview.trickplay", "video.mkv")
			source, dest := regular, ignored
			if direction == "out-of" {
				source, dest = ignored, regular
			}
			write(t, source, "same entity")
			if e := os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(f.media, "follower.mkv")
			if e := os.Link(source, target); e != nil {
				t.Fatal(e)
			}
			f.opts.Watch = nil
			f.opts.Now = time.Now
			h := f.open(t)
			ctx := context.Background()
			if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
				t.Fatal(e)
			}
			if e := os.Rename(source, dest); e != nil {
				t.Fatal(e)
			}
			waitState(t, h, func(s Durable) bool {
				for _, r := range s.Events {
					if r.Event.Kind == "moved" && r.Event.Path == source && r.Event.Dest == dest && r.State == "confirmed" {
						return true
					}
				}
				return false
			})
			if !exists(target) || f.effects.count("download_file_deleted") != 0 || tracked(snapshot(t, h), regular) {
				t.Fatal("trickplay rename became a destructive delete or added tracking")
			}
			if e := h.Stop(ctx, "fixture-end"); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestNativeUnpairedMoveOrderAndInstalledWatchdogGrace(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	other := filepath.Join(f.src, "second.mkv")
	write(t, other, "other entity")
	otherLink := filepath.Join(f.media, "second.mkv")
	if e := os.Link(other, otherLink); e != nil {
		t.Fatal(e)
	}
	f.opts.Watch = nil
	f.opts.Now = time.Now
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e := os.Rename(a, filepath.Join(f.root, "first-outside.mkv")); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(other, filepath.Join(f.root, "second-outside.mkv")); e != nil {
		t.Fatal(e)
	}
	waitState(t, h, func(s Durable) bool { return len(s.Machine.Queue) == 2 })
	s := snapshot(t, h)
	if time.Since(start) < 450*time.Millisecond || s.Machine.Queue[0].Path != a || s.Machine.Queue[1].Path != other {
		t.Fatal("unpaired move order/grace differs from installed watchdog 6.0.0")
	}
	if !exists(b) || !exists(otherLink) {
		t.Fatal("delayed unpaired moves executed early")
	}
	if e := h.Stop(ctx, "fixture-end"); e != nil {
		t.Fatal(e)
	}
}

func TestNativeOverlappingHardlinkAndSTRMWatchesBothExecute(t *testing.T) {
	f := newFixture(t)
	strm := filepath.Join(f.src, "clip.strm")
	video := filepath.Join(f.media, "series", "clip.2160p.mkv")
	write(t, strm, "https://never-requested.invalid/video")
	write(t, video, "local video")
	write(t, filepath.Join(filepath.Dir(video), "keep.bin"), "keep")
	f.opts.Watch = nil
	f.opts.Now = time.Now
	h := f.open(t)
	ctx := context.Background()
	cfg := f.config(false, map[string]any{"monitor_dirs": f.src, "monitor_strm_deletion": true, "strm_path_mappings": f.src + ":local:" + filepath.Dir(video), "delete_torrents": false, "notify": false})
	if e := h.Configure(ctx, f.opts.Binding, cfg); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(strm); e != nil {
		t.Fatal(e)
	}
	waitState(t, h, func(s Durable) bool { return !exists(video) })
	seen := map[string]bool{}
	for _, r := range snapshot(t, h).Events {
		if r.Event.Path == strm && r.State == "confirmed" {
			seen[r.Event.Mode] = true
		}
	}
	if !seen["hardlink"] || !seen["strm"] || f.effects.count("history_src") != 1 || f.effects.count("history_dest") != 1 {
		t.Fatal("path dedup suppressed one of the original overlapping watch handlers", seen)
	}
	if e := h.Stop(ctx, "fixture-end"); e != nil {
		t.Fatal(e)
	}
}
