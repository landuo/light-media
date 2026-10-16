package cleanuphost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/removelink"
)

type idleWatcher struct {
	events chan Event
	errors chan error
	once   sync.Once
}

func (w *idleWatcher) Events() <-chan Event { return w.events }
func (w *idleWatcher) Errors() <-chan error { return w.errors }
func (w *idleWatcher) Close() error {
	w.once.Do(func() { close(w.events); close(w.errors) })
	return nil
}
func idleWatch(context.Context, []removelink.Watch, func(string) (*os.File, error)) (Watcher, error) {
	return &idleWatcher{events: make(chan Event, 32), errors: make(chan error, 1)}, nil
}

type fixtureEffects struct {
	mu        sync.Mutex
	receipts  map[string]Effect
	calls     []Effect
	uncertain bool
}

func (f *fixtureEffects) Commit(_ context.Context, e Effect) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.receipts[e.ID]; ok {
		return true, nil
	}
	f.calls = append(f.calls, e)
	if f.uncertain {
		return false, ErrUncertain
	}
	f.receipts[e.ID] = e
	return true, nil
}
func (f *fixtureEffects) count(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.calls {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

type fixture struct {
	root, src, media, jdir string
	opts                   Options
	j                      *FileJournal
	effects                *fixtureEffects
	clock                  atomic.Int64
	guarded                atomic.Int64
	allow                  atomic.Bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{root: root, src: filepath.Join(root, "source"), media: filepath.Join(root, "media"), jdir: filepath.Join(root, "state"), effects: &fixtureEffects{receipts: map[string]Effect{}}}
	for _, p := range []string{f.src, f.media, f.jdir} {
		if e = os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	write(t, filepath.Join(f.src, "keep.bin"), "boundary")
	write(t, filepath.Join(f.media, "keep.bin"), "boundary")
	f.j, e = NewFileJournal(f.jdir)
	if e != nil {
		t.Fatal(e)
	}
	f.clock.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	f.allow.Store(true)
	f.opts = Options{Binding: Binding{"owned-instance", 1, 1}, Roots: []Root{{"src", f.src, "source"}, {"media", f.media, "media"}}, Journal: f.j, Effects: f.effects, Watch: idleWatch, Now: func() time.Time { return time.Unix(0, f.clock.Load()).UTC() }, MediaExtensions: []string{".mkv", ".mp4"}, Guard: func(_ context.Context, _ Binding, _ string) (func(), error) {
		f.guarded.Add(1)
		if !f.allow.Load() {
			return nil, ErrUnsafe
		}
		return func() {}, nil
	}}
	return f
}
func write(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func exists(p string) bool { _, e := os.Lstat(p); return e == nil }
func (f *fixture) open(t *testing.T) *Host {
	t.Helper()
	h, e := Open(f.opts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = h.Suspend(context.Background()) })
	return h
}
func (f *fixture) config(delayed bool, extra map[string]any) json.RawMessage {
	m := map[string]any{"enabled": true, "monitor_dirs": f.src + "\n" + f.media, "delayed_deletion": delayed, "delay_seconds": 30, "delete_history": true, "delete_torrents": true, "notify": true}
	for k, v := range extra {
		m[k] = v
	}
	r, _ := json.Marshal(m)
	return r
}
func (f *fixture) links(t *testing.T) (string, string) {
	t.Helper()
	a, b := filepath.Join(f.src, "origin.mkv"), filepath.Join(f.media, "film", "linked.mkv")
	write(t, a, "fixture-media")
	if e := os.MkdirAll(filepath.Dir(b), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(a, b); e != nil {
		t.Fatal(e)
	}
	return a, b
}
func (f *fixture) remove(t *testing.T, h *Host, p, id string) Event {
	t.Helper()
	if e := os.Remove(p); e != nil {
		t.Fatal(e)
	}
	event := Event{ID: id, Kind: "deleted", Path: p, Mode: "hardlink", At: f.opts.Now()}
	if e := h.Observe(context.Background(), event); e != nil {
		t.Fatal(e)
	}
	return event
}
func snapshot(t *testing.T, h *Host) Durable {
	t.Helper()
	s, e := h.Snapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestRealLocalLinksBoundApplyAndSeparateReceipts(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
		t.Fatal(e)
	}
	if len(snapshot(t, h).Machine.State) != 4 {
		t.Fatal("baseline files missing")
	}
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	event := Event{ID: "host-bound-1", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}
	if e := h.Apply(ctx, f.opts.Binding, "unrecorded"); !errors.Is(e, ErrUnsafe) {
		t.Fatal("worker supplied an unbound event", e)
	}
	if e := h.RecordEvent(ctx, event); e != nil {
		t.Fatal(e)
	}
	if e := h.Apply(ctx, f.opts.Binding, event.ID); e != nil {
		t.Fatal(e)
	}
	if exists(b) || f.effects.count("history_src") != 2 || f.effects.count("download_file_deleted") != 2 || f.effects.count("notification") != 1 {
		t.Fatal("real cleanup or side effects missing", exists(b), f.effects.count("history_src"), f.effects.count("download_file_deleted"), f.effects.count("notification"), snapshot(t, h).Blocked)
	}
	s := snapshot(t, h)
	origin, media := 0, 0
	for _, r := range s.Effects {
		if r.Kind == "observed_delete" && r.Role == "source" {
			origin++
		}
		if r.Kind == "unlink" && r.Role == "media" && r.State == "confirmed" {
			media++
		}
	}
	if origin != 1 || media != 1 || s.Events[event.ID].State != "confirmed" {
		t.Fatal("source observation and media deletion were conflated")
	}
	write(t, b, "replacement survives receipt replay")
	if e := h.Apply(ctx, f.opts.Binding, event.ID); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(b)
	if string(raw) != "replacement survives receipt replay" || f.effects.count("history_src") != 2 {
		t.Fatal("confirmed event repeated a destructive effect")
	}
	changed := event
	changed.Path = b
	if e := h.RecordEvent(ctx, changed); !errors.Is(e, ErrUnsafe) {
		t.Fatal("same event ID accepted changed payload", e)
	}
}
func TestDelayedQueueDedupRestartAndWorkerStopDoesNotDrain(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
		t.Fatal(e)
	}
	event := f.remove(t, h, a, "delete-before-restart")
	if e := h.Observe(ctx, event); e != nil {
		t.Fatal(e)
	}
	if e := h.Observe(ctx, Event{ID: "kernel-duplicate", Kind: "deleted", Path: a, Mode: "hardlink", At: event.At}); e != nil {
		t.Fatal(e)
	}
	s := snapshot(t, h)
	if len(s.Machine.Queue) != 1 || s.Machine.TimerDue == nil {
		t.Fatal("queue did not deduplicate")
	}
	due := *s.Machine.TimerDue
	if e := h.WorkerStop(ctx); e != nil || !exists(b) {
		t.Fatal("worker stop drained queue")
	}
	if e := h.Suspend(ctx); e != nil {
		t.Fatal(e)
	}
	h = f.open(t)
	if e := h.Recover(ctx, f.opts.Binding); e != nil {
		t.Fatal(e)
	}
	if !snapshot(t, h).Machine.TimerDue.Equal(due) || !exists(b) {
		t.Fatal("restart reset deadline or drained")
	}
	f.clock.Add(31 * int64(time.Second))
	if e := h.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	if exists(b) || len(snapshot(t, h).Machine.Queue) != 0 {
		t.Fatal("persisted delayed cleanup not executed")
	}
}
func TestReloadUsesNewFlagsToDrainOldQueueAndStopIsImmediate(t *testing.T) {
	for _, reload := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "reload"}[reload], func(t *testing.T) {
			f := newFixture(t)
			a, b := f.links(t)
			h := f.open(t)
			ctx := context.Background()
			if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
				t.Fatal(e)
			}
			f.remove(t, h, a, "queued-old")
			var e error
			if reload {
				binding := f.opts.Binding
				binding.Revision++
				e = h.Configure(ctx, binding, f.config(true, map[string]any{"delete_history": false, "delete_torrents": false, "notify": false}))
			} else {
				e = h.Stop(ctx, "uninstall")
			}
			if e != nil {
				t.Fatal(e)
			}
			if exists(b) || len(snapshot(t, h).Machine.Queue) != 0 {
				t.Fatal("stop/reload waited until due")
			}
			n := 2
			if reload {
				n = 0
			}
			if f.effects.count("history_src") != n {
				t.Fatal("old flags were used during new-config drain")
			}
		})
	}
}
func TestReplacementSizeMtimeParentAndRootCAS(t *testing.T) {
	for _, change := range []string{"leaf-before-delay", "size-before-delay", "mtime-before-delay", "leaf-during-rename", "symlink-during-rename", "parent-during-rename", "root-during-rename", "parent-missing-during-rename", "root-missing-during-rename"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			a, b := f.links(t)
			f.opts.fault = func(phase, p string) error {
				if phase != "before_rename" || p != b {
					return nil
				}
				switch change {
				case "leaf-during-rename":
					if e := os.Rename(b, b+".old"); e != nil {
						return e
					}
					return os.WriteFile(b, []byte("replacement"), 0600)
				case "symlink-during-rename":
					if e := os.Rename(b, b+".old"); e != nil {
						return e
					}
					return os.Symlink(a+".outside", b)
				case "parent-during-rename", "parent-missing-during-rename":
					parent := filepath.Dir(b)
					if e := os.Rename(parent, parent+".old"); e != nil {
						return e
					}
					if change == "parent-missing-during-rename" {
						return nil
					}
					return os.Mkdir(parent, 0700)
				case "root-during-rename", "root-missing-during-rename":
					if e := os.Rename(f.media, f.media+".old"); e != nil {
						return e
					}
					if change == "root-missing-during-rename" {
						return nil
					}
					return os.Mkdir(f.media, 0700)
				}
				return nil
			}
			h := f.open(t)
			ctx := context.Background()
			if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
				t.Fatal(e)
			}
			f.remove(t, h, a, "before-cas")
			switch change {
			case "leaf-before-delay":
				if e := os.Rename(b, b+".old"); e != nil {
					t.Fatal(e)
				}
				write(t, b, "replacement")
			case "size-before-delay":
				write(t, b, "changed-size")
			case "mtime-before-delay":
				stamp := time.Now().Add(time.Hour)
				if e := os.Chtimes(b, stamp, stamp); e != nil {
					t.Fatal(e)
				}
			}
			f.clock.Add(31 * int64(time.Second))
			if e := h.Tick(ctx); e == nil {
				t.Fatal("changed entity was treated as successful cleanup")
			}
			s := snapshot(t, h)
			if s.Blocked == "" {
				t.Fatal("uncertain failure did not block automatic replays")
			}
			for _, r := range s.Effects {
				if r.Kind == "unlink" && r.State == "confirmed" {
					t.Fatal("replacement got a successful unlink receipt")
				}
			}
			if change == "leaf-before-delay" || change == "leaf-during-rename" {
				raw, _ := os.ReadFile(b)
				if string(raw) != "replacement" {
					t.Fatal("replacement deleted or not restored")
				}
			}
			if change == "symlink-during-rename" {
				info, e := os.Lstat(b)
				if e != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("symlink replacement was consumed")
				}
			}
		})
	}
}
func TestUnlinkCommitGapAndUnknownEffectsNeverReplayOnRestart(t *testing.T) {
	for _, mode := range []string{"after-stage", "after-unlink", "external-uncertain"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			a, b := f.links(t)
			injected := errors.New("synthetic crash boundary")
			f.opts.fault = func(phase, p string) error {
				if p == b && ((mode == "after-stage" && phase == "after_stage") || (mode == "after-unlink" && phase == "after_unlink")) {
					return injected
				}
				return nil
			}
			if mode == "external-uncertain" {
				f.effects.uncertain = true
			}
			h := f.open(t)
			ctx := context.Background()
			if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
				t.Fatal(e)
			}
			if e := os.Remove(a); e != nil {
				t.Fatal(e)
			}
			if e := h.Observe(ctx, Event{ID: "effect-gap", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}); e == nil {
				t.Fatal("uncertain effect falsely confirmed")
			}
			s := snapshot(t, h)
			if s.Blocked == "" || s.Events["effect-gap"].State == "confirmed" {
				t.Fatal("failure state was lost")
			}
			if e := h.Suspend(ctx); e != nil {
				t.Fatal(e)
			}
			if !exists(b) {
				write(t, b, "new path after crash")
			}
			h = f.open(t)
			if e := h.Recover(ctx, f.opts.Binding); !errors.Is(e, ErrUncertain) {
				t.Fatal("uncertain effect auto resumed", e)
			}
			if !exists(b) {
				t.Fatal("restart replay deleted new path")
			}
			if mode == "after-unlink" {
				for _, r := range s.Effects {
					if r.Path == b && r.Kind == "unlink" && r.State != "staged" {
						t.Fatal("unlink-before-receipt claimed confirmation")
					}
				}
			}
		})
	}
}
func TestLocalSTRMStorageAndUnknownReferences(t *testing.T) {
	f := newFixture(t)
	strm := filepath.Join(f.src, "clip.strm")
	video := filepath.Join(f.media, "series", "clip.2160p.mkv")
	write(t, strm, "https://never-requested.invalid/video")
	write(t, video, "local fixture video")
	write(t, filepath.Join(filepath.Dir(video), "keep.bin"), "keep")
	h := f.open(t)
	ctx := context.Background()
	cfg := f.config(false, map[string]any{"monitor_dirs": f.src, "monitor_strm_deletion": true, "strm_path_mappings": f.src + ":local:" + filepath.Dir(video)})
	if e := h.Configure(ctx, f.opts.Binding, cfg); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(strm); e != nil {
		t.Fatal(e)
	}
	if e := h.Observe(ctx, Event{ID: "strm-local", Kind: "deleted", Path: strm, Mode: "strm", At: f.opts.Now()}); e != nil {
		t.Fatal(e)
	}
	if exists(video) || f.effects.count("history_dest") != 1 || f.effects.count("download_file_deleted") != 0 {
		t.Fatal("STRM used hardlink branch or left local video")
	}
	other := f.opts.Binding
	other.Revision++
	for _, storage := range []string{"alist", "u115", "unknown"} {
		bad := f.config(false, map[string]any{"monitor_strm_deletion": true, "strm_path_mappings": f.src + ":" + storage + ":/outside"})
		if e := h.Configure(ctx, other, bad); !errors.Is(e, ErrUnsupported) {
			t.Fatal("unknown storage silently became local", e)
		}
	}
}
func TestAuthorityRejectsTraversalSymlinksMissingRootsAndRevokedGuard(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
		t.Fatal(e)
	}
	f.allow.Store(false)
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	if e := h.Observe(ctx, Event{ID: "revoked", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}); !errors.Is(e, ErrUnsafe) || !exists(b) {
		t.Fatal("revoked write lease was ignored", e)
	}
	f.allow.Store(true)
	for _, p := range []string{f.src + "/../media/film/linked.mkv", f.root + "/outside", f.src + "/./x"} {
		if e := h.Observe(ctx, Event{ID: p, Kind: "deleted", Path: p, Mode: "hardlink"}); !errors.Is(e, ErrUnsafe) {
			t.Fatal("unsafe path cleaned or accepted", p, e)
		}
	}
	link := filepath.Join(f.src, "alias")
	if e := os.Symlink(f.media, link); e != nil {
		t.Fatal(e)
	}
	if e := h.Observe(ctx, Event{ID: "alias", Kind: "created", Path: filepath.Join(link, "film/linked.mkv"), Mode: "hardlink"}); e == nil {
		t.Fatal("symlink parent was followed")
	}
	if e := h.call(ctx, func() error { h.ops.ctx = ctx; h.ops.scope = "boundary-test"; return h.ops.RemoveDir(f.src) }); !errors.Is(e, ErrUnsafe) || !exists(f.src) {
		t.Fatal("authorized root itself was deleted", e)
	}
}
func TestInterruptedJournalAndCanceledCallPreservePendingWork(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
		t.Fatal(e)
	}
	f.remove(t, h, a, "persisted-queue")
	before := snapshot(t, h)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if e := h.Tick(canceled); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if !exists(b) || len(snapshot(t, h).Machine.Queue) != 1 {
		t.Fatal("canceled command consumed queue")
	}
	f.j.beforeCommit = func() error { return errors.New("synthetic journal failure") }
	f.clock.Add(31 * int64(time.Second))
	if e := h.Tick(ctx); e == nil {
		t.Fatal("journal failure accepted")
	}
	f.j.beforeCommit = nil
	after := snapshot(t, h)
	if len(after.Machine.Queue) != len(before.Machine.Queue) || !exists(b) {
		t.Fatal("failed begin write performed unlink")
	}
	if e := h.Suspend(ctx); e != nil {
		t.Fatal(e)
	}
	// A real persisted active marker represents a process crash after begin but
	// before the machine checkpoint. Recovery must not guess an outcome.
	if e := f.j.Update(ctx, func(s *Durable) error { s.Active = &Event{ID: "crashed-active", Kind: "timer"}; return nil }); e != nil {
		t.Fatal(e)
	}
	h = f.open(t)
	if e := h.Recover(ctx, f.opts.Binding); !errors.Is(e, ErrUncertain) {
		t.Fatal("active receipt gap auto-replayed", e)
	}
}
func TestConcurrentSameEventHasSingleOutcome(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	e := Event{ID: "duplicate-32", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}
	var wg sync.WaitGroup
	fail := make(chan error, 32)
	for range 32 {
		wg.Go(func() {
			if err := h.Observe(ctx, e); err != nil {
				fail <- err
			}
		})
	}
	wg.Wait()
	close(fail)
	for err := range fail {
		t.Fatal(err)
	}
	if len(snapshot(t, h).Machine.Queue) != 1 {
		t.Fatal("duplicate queue")
	}
	f.clock.Add(31 * int64(time.Second))
	if e := h.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	if exists(b) || f.effects.count("notification") != 1 {
		t.Fatal("concurrent events repeated side effect")
	}
}

func TestOpenDoesNotRunExpiredQueueBeforeExplicitRecover(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
		t.Fatal(e)
	}
	f.remove(t, h, a, "recover-deadline")
	if e := h.Suspend(ctx); e != nil {
		t.Fatal(e)
	}
	f.clock.Add(31 * int64(time.Second))
	h = f.open(t)
	time.Sleep(100 * time.Millisecond)
	if !exists(b) {
		t.Fatal("Open ran pending cleanup without lifecycle recovery")
	}
	if e := h.Recover(ctx, f.opts.Binding); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for exists(b) && time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
	}
	if exists(b) {
		t.Fatal("recovered continuous scheduler did not run")
	}
}
func TestMediaOriginAndSourceFollowerHaveSeparateDeletionReceipts(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
		t.Fatal(e)
	}
	f.remove(t, h, b, "media-deleted")
	if exists(a) {
		t.Fatal("source follower was not cleaned")
	}
	origin, source := 0, 0
	for _, r := range snapshot(t, h).Effects {
		if r.Kind == "observed_delete" && r.Role == "media" {
			origin++
		}
		if r.Kind == "unlink" && r.Role == "source" && r.State == "confirmed" {
			source++
		}
	}
	if origin != 1 || source != 1 {
		t.Fatal("source and media receipts conflated")
	}
}
func TestStorageAndRecursiveScrapSafetyDoNotInventSuccess(t *testing.T) {
	f := newFixture(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
		t.Fatal(e)
	}
	tree := filepath.Join(f.media, "clip.trickplay")
	file := filepath.Join(tree, "new", "image.jpg")
	write(t, file, "fixture")
	if e := h.call(ctx, func() error {
		h.ops.ctx = ctx
		h.ops.scope = "trusted-tree-test"
		if _, e := h.ops.Children(f.media); e != nil {
			return e
		}
		return h.ops.RemoveTree(tree)
	}); e != nil {
		t.Fatal(e)
	}
	if exists(tree) {
		t.Fatal("tree remained")
	}
	for _, storage := range []string{"alist", "unknown"} {
		if e := h.call(ctx, func() error { _, e := h.ops.StorageExists(removelink.Item{Storage: storage, Path: f.media}); return e }); !errors.Is(e, ErrUnsupported) {
			t.Fatal("unsupported storage returned false success", e)
		}
	}
}

func TestCapturedEventsRequireOrderAndReloadDrainsPendingUnderNewFlags(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	first := Event{ID: "captured-first", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}
	later := Event{ID: "captured-later", Kind: "created", Path: filepath.Join(f.src, "keep.bin"), Mode: "hardlink", At: f.opts.Now()}
	if e := h.RecordEvent(ctx, first); e != nil {
		t.Fatal(e)
	}
	if e := h.RecordEvent(ctx, later); e != nil {
		t.Fatal(e)
	}
	if e := h.Apply(ctx, f.opts.Binding, later.ID); !errors.Is(e, ErrOrder) {
		t.Fatal("later captured event overtook source event", e)
	}
	binding := f.opts.Binding
	binding.Revision++
	if e := h.Configure(ctx, binding, f.config(true, map[string]any{"delete_history": false, "delete_torrents": false, "notify": false})); e != nil {
		t.Fatal(e)
	}
	if exists(b) || f.effects.count("history_src") != 0 {
		t.Fatal("reload did not drain pending events under new flags")
	}
	s := snapshot(t, h)
	if s.Events[first.ID].State != "confirmed" || s.Events[later.ID].State != "confirmed" {
		t.Fatal("pending captures were dropped")
	}
	if e := h.Apply(ctx, binding, first.ID); !errors.Is(e, ErrUnsafe) {
		t.Fatal("old revision event accepted as a new worker invocation", e)
	}
}

func TestCanceledAtSelectionCannotMoveOrUnlink(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.opts.fault = func(phase, p string) error {
		if phase == "before_rename" && p == b {
			cancel()
		}
		return nil
	}
	h := f.open(t)
	if e := h.Configure(context.Background(), f.opts.Binding, f.config(false, nil)); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	if e := h.Observe(ctx, Event{ID: "cancel-before-rename", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}); e == nil {
		t.Fatal("canceled selection confirmed")
	}
	// Wait behind the owner to observe all cleanup work after cancellation.
	if e := h.call(context.Background(), func() error { return nil }); e != nil {
		t.Fatal(e)
	}
	if !exists(b) {
		t.Fatal("cancellation moved/unlinked the selected file")
	}
}

func TestMovedDestinationSymlinkCannotAuthorizeFallbackCleanup(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(f.root, "outside")
	if e := os.Mkdir(outside, 0700); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(f.src, "alias")
	if e := os.Symlink(outside, alias); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(a, filepath.Join(outside, "preserved.mkv")); e != nil {
		t.Fatal(e)
	}
	if e := h.Observe(ctx, Event{ID: "unverifiable-destination", Kind: "moved", Path: a, Dest: filepath.Join(alias, "gone.mkv"), Mode: "hardlink", At: f.opts.Now()}); e == nil {
		t.Fatal("unsafe destination looked like a missing fallback")
	}
	if !exists(b) || f.effects.count("download_file_deleted") != 0 {
		t.Fatal("unverifiable move destination triggered follower cleanup")
	}
}

func TestStopDrainsCapturedEventsAndRevokesFurtherDelivery(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	e := Event{ID: "pending-at-stop", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}
	if err := h.RecordEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := h.Stop(ctx, "disable"); err != nil {
		t.Fatal(err)
	}
	if exists(b) || snapshot(t, h).Events[e.ID].State != "confirmed" {
		t.Fatal("stop dropped captured event before queue drain")
	}
	if err := h.Observe(ctx, Event{ID: "after-stop", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}); !errors.Is(err, ErrUnsafe) {
		t.Fatal("stopped service accepted new destructive work", err)
	}
}

func TestCleanCloseRecoverRestoresMonitoring(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	ctx := context.Background()
	h := f.open(t)
	if err := h.Configure(ctx, f.opts.Binding, f.config(false, nil)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, h)
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	closed, err := f.j.Read(ctx)
	if err != nil || len(closed.Machine.Watches) != 0 {
		t.Fatal("clean stop did not persist its stopped watch state", err)
	}
	reopened := f.open(t)
	if err = reopened.Recover(ctx, f.opts.Binding); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err = reopened.Observe(ctx, Event{ID: "after-clean-close", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}); err != nil {
		t.Fatal("recovered monitor rejected its authorized event", err)
	}
	after := snapshot(t, reopened)
	if exists(b) || len(after.Machine.Watches) != len(before.Machine.Watches) || after.Events["after-clean-close"].State != "confirmed" {
		t.Fatal("clean restart lost monitoring or failed to remove the selected hardlink")
	}
	if f.effects.count("history_src") != 2 || f.effects.count("download_file_deleted") != 2 || f.effects.count("notification") != 1 {
		t.Fatal("clean restart did not commit the source and follower effects exactly once")
	}
}

func TestRecoverScansOfflineFilesWithoutResettingQueueOrFrozenTargets(t *testing.T) {
	for _, mode := range []string{"new-independent-entity", "new-same-entity-protects-queue", "changed-existing-target"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			a, b := f.links(t)
			h := f.open(t)
			ctx := context.Background()
			if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
				t.Fatal(e)
			}
			f.remove(t, h, a, "offline-queue")
			before := snapshot(t, h)
			if e := h.Suspend(ctx); e != nil {
				t.Fatal(e)
			}
			fresh := filepath.Join(f.src, "offline-new.mkv")
			switch mode {
			case "new-independent-entity":
				write(t, fresh, "created offline")
			case "new-same-entity-protects-queue":
				if e := os.Link(b, fresh); e != nil {
					t.Fatal(e)
				}
			case "changed-existing-target":
				write(t, b, "changed while service stopped")
			}
			f.clock.Add(5 * int64(time.Second))
			h = f.open(t)
			if e := h.Recover(ctx, f.opts.Binding); e != nil {
				t.Fatal(e)
			}
			after := snapshot(t, h)
			if len(after.Machine.Queue) != 1 || !after.Machine.TimerDue.Equal(*before.Machine.TimerDue) || after.Frozen[b] != before.Frozen[b] {
				t.Fatal("recovery reset deadline or frozen deletion snapshot")
			}
			if mode != "changed-existing-target" {
				seen := false
				for _, r := range after.Machine.State {
					if r.Path == fresh && r.Identity.Added.Equal(f.opts.Now()) {
						seen = true
					}
				}
				if !seen {
					t.Fatal("offline new file was not baselined")
				}
			}
			f.clock.Add(26 * int64(time.Second))
			err := h.Tick(ctx)
			switch mode {
			case "new-independent-entity":
				if err != nil || exists(b) || !exists(fresh) {
					t.Fatal("new unrelated file blocked pending cleanup or was consumed", err)
				}
			case "new-same-entity-protects-queue":
				if err != nil || !exists(b) || !exists(fresh) {
					t.Fatal("recent offline hardlink was not protected", err)
				}
			case "changed-existing-target":
				if err == nil || !exists(b) {
					t.Fatal("recovery overwrote the frozen CAS and consumed changed data")
				}
			}
		})
	}
}
func TestCapturedEventCannotBorrowGlobalAuthorityOutsideMonitorScope(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(false, map[string]any{"monitor_dirs": f.src})); e != nil {
		t.Fatal(e)
	}
	// The media root is authorized for local storage, but it is not a watched
	// hardlink source for this configuration and cannot supply a forged callback.
	e := Event{ID: "wrong-monitor", Kind: "deleted", Path: b, Mode: "hardlink", At: f.opts.Now()}
	if err := h.RecordEvent(ctx, e); !errors.Is(err, ErrUnsafe) {
		t.Fatal("global root authorization replaced plugin watch scope", err)
	}
	if !exists(a) || !exists(b) {
		t.Fatal("out-of-scope event mutated files")
	}
}

func TestCapturedButUnappliedEventAfterCrashRequiresReview(t *testing.T) {
	f := newFixture(t)
	a, b := f.links(t)
	h := f.open(t)
	ctx := context.Background()
	if e := h.Configure(ctx, f.opts.Binding, f.config(true, nil)); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	event := Event{ID: "captured-before-crash", Kind: "deleted", Path: a, Mode: "hardlink", At: f.opts.Now()}
	if e := h.RecordEvent(ctx, event); e != nil {
		t.Fatal(e)
	}
	if e := h.Suspend(ctx); e != nil {
		t.Fatal(e)
	}
	// A new file now owns the old source path. Its current identity must not be
	// substituted into a previously captured deletion during startup rescan.
	write(t, a, "new owner after crash")
	h = f.open(t)
	if snapshot(t, h).Blocked == "" {
		t.Fatal("startup hid the unresolved captured event")
	}
	if e := h.Recover(ctx, f.opts.Binding); !errors.Is(e, ErrUncertain) {
		t.Fatal("unapplied captured event was guessed/replayed", e)
	}
	if !exists(a) || !exists(b) || f.effects.count("history_src") != 0 {
		t.Fatal("uncertain captured event mutated current files/history")
	}
}
