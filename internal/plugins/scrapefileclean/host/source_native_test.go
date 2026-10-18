package scrapehost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type sourceEffects struct {
	mu     sync.Mutex
	events []Effect
}

func (e *sourceEffects) Commit(ctx context.Context, x Effect) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, x)
	return x.Kind != "history_dest", nil
}

type sourceWatch struct {
	events chan Event
	errors chan error
	once   sync.Once
}

func (w *sourceWatch) Events() <-chan Event { return w.events }
func (w *sourceWatch) Errors() <-chan error { return w.errors }
func (w *sourceWatch) Close() error {
	w.once.Do(func() { close(w.events); close(w.errors) })
	return nil
}

type sourceFixture struct {
	h                *Host
	j                *FileJournal
	effects          *sourceEffects
	binding          Binding
	root, src, alias string
	now              time.Time
	clockMu          sync.Mutex
	options          Options
}

func sourceNativeFixture(t *testing.T, delayed bool) *sourceFixture {
	t.Helper()
	dir, canonicalErr := filepath.EvalSymlinks(t.TempDir())
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	f := &sourceFixture{root: filepath.Join(dir, "monitor"), binding: Binding{"owned-scrape", 1, 1}, now: time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)}
	if e := os.Mkdir(f.root, 0700); e != nil {
		t.Fatal(e)
	}
	f.src = filepath.Join(f.root, "source.mkv")
	f.alias = filepath.Join(f.root, "movie.mkv")
	if e := os.WriteFile(f.src, []byte("owned-hardlink"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(f.src, f.alias); e != nil {
		t.Fatal(e)
	}
	j, e := NewFileJournal(dir)
	if e != nil {
		t.Fatal(e)
	}
	f.j = j
	f.effects = &sourceEffects{}
	f.options = Options{Binding: f.binding, Roots: []Root{{"owned-monitor", f.root, "source"}}, Journal: j, Effects: f.effects, Guard: func(context.Context, Binding, string) (func(), error) { return func() {}, nil }, Now: func() time.Time { f.clockMu.Lock(); defer f.clockMu.Unlock(); return f.now }, Watch: func(context.Context, []Watch, func(string) (*os.File, error)) (Watcher, error) {
		return &sourceWatch{events: make(chan Event), errors: make(chan error)}, nil
	}}
	f.h, e = Open(f.options)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if f.h != nil {
			_ = f.h.Suspend(context.Background())
		}
	})
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": f.root, "delayed_deletion": delayed, "delay_seconds": 10, "delete_history": true, "delete_torrents": true, "notify": true})
	if e = f.h.Configure(context.Background(), f.binding, cfg); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *sourceFixture) deleted(t *testing.T) {
	t.Helper()
	if e := os.Remove(f.src); e != nil {
		t.Fatal(e)
	}
	if e := f.h.Observe(context.Background(), Event{ID: "owned-delete", Kind: "deleted", Path: f.src, Mode: "hardlink", At: f.now}); e != nil {
		t.Fatal(e)
	}
}
func nativeExists(p string) bool { _, e := os.Lstat(p); return e == nil }
func TestSourceNativeImmediateHardlinkHistoryNotice(t *testing.T) {
	f := sourceNativeFixture(t, false)
	f.deleted(t)
	if nativeExists(f.alias) {
		t.Fatal("actual alias not removed")
	}
	s, e := f.h.Snapshot(context.Background())
	if e != nil || s.Blocked != "" {
		t.Fatal(s, e)
	}
	kinds := []string{}
	for _, x := range f.effects.events {
		kinds = append(kinds, x.Kind)
	}
	joined := strings.Join(kinds, ",")
	for _, k := range []string{"history_dest,history_src", "download_file_deleted", "notification"} {
		if !strings.Contains(joined, k) {
			t.Fatal(k, kinds)
		}
	}
	for _, x := range s.Effects {
		if x.Kind == "unlink" && x.State != "confirmed" {
			t.Fatal("filesystem receipt not confirmed", x)
		}
	}
}
func TestSourceNativeStopDiscardsInsteadOfDraining(t *testing.T) {
	for _, op := range []string{"stop", "reload", "cold"} {
		t.Run(op, func(t *testing.T) {
			f := sourceNativeFixture(t, true)
			f.deleted(t)
			s, e := f.h.Snapshot(context.Background())
			if e != nil || len(s.Machine.Queue) != 1 {
				t.Fatal(s, e)
			}
			if e = f.h.WorkerStop(context.Background()); e != nil {
				t.Fatal(e)
			}
			s, _ = f.h.Snapshot(context.Background())
			if len(s.Machine.Queue) != 1 {
				t.Fatal("short SDK stop closed native owner")
			}
			switch op {
			case "stop":
				e = f.h.Stop(context.Background(), "owned-stop")
			case "reload":
				cfg := append([]byte(nil), s.Config...)
				f.binding.Revision++
				e = f.h.Configure(context.Background(), f.binding, cfg)
			case "cold":
				if e = f.h.Suspend(context.Background()); e != nil {
					t.Fatal(e)
				}
				f.h, e = Open(f.options)
				if e == nil {
					e = f.h.Recover(context.Background(), f.binding)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			s, e = f.h.Snapshot(context.Background())
			if e != nil || len(s.Machine.Queue) != 0 || s.Machine.TimerDue != nil {
				t.Fatal(s, e)
			}
			if !nativeExists(f.alias) || len(f.effects.events) != 0 {
				t.Fatal("stop/reload/cold performed premature cleanup", f.effects.events)
			}
		})
	}
}
func TestSourceNativeKnownPathsProtectReorganizedHardlinks(t *testing.T) {
	f := sourceNativeFixture(t, true)
	f.deleted(t)
	late := filepath.Join(f.root, "reorganized.mkv")
	if e := os.Link(f.alias, late); e != nil {
		t.Fatal(e)
	}
	if e := f.h.Observe(context.Background(), Event{ID: "owned-late-created", Kind: "created", Path: late, Mode: "hardlink", At: f.now}); e != nil {
		t.Fatal(e)
	}
	f.clockMu.Lock()
	f.now = f.now.Add(11 * time.Second)
	f.clockMu.Unlock()
	if e := f.h.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !nativeExists(f.alias) || !nativeExists(late) {
		t.Fatal("new same-inode path not protected")
	}
	s, e := f.h.Snapshot(context.Background())
	if e != nil || len(s.Machine.Queue) != 0 {
		t.Fatal(s, e)
	}
}
