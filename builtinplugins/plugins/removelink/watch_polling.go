package removelink

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type observedPath struct {
	path     string
	dev, ino uint64
	dir      bool
}
type pollingObserver struct {
	stop, done chan struct{}
	events     chan fileEvent
	once       sync.Once
}

func (w *pollingObserver) Events() <-chan fileEvent { return w.events }
func (w *pollingObserver) Close()                   { w.once.Do(func() { close(w.stop) }); <-w.done }
func scanObserved(root string) ([]observedPath, error) {
	out := []observedPath{}
	record := func(path string) (observedPath, error) {
		info, e := os.Stat(path)
		if e != nil {
			return observedPath{}, e
		}
		stat := info.Sys().(*syscall.Stat_t)
		return observedPath{path, uint64(stat.Dev), uint64(stat.Ino), info.IsDir()}, nil
	}
	initial, e := record(root)
	if e != nil {
		return nil, e
	}
	out = append(out, initial)
	var walk func(string) error
	walk = func(path string) error {
		f, e := os.Open(path)
		if e != nil {
			if errors.Is(e, syscall.ENOENT) || errors.Is(e, syscall.ENOTDIR) || errors.Is(e, syscall.EINVAL) {
				return nil
			}
			return e
		}
		rows, e := f.Readdir(-1)
		f.Close()
		if e != nil {
			return e
		}
		entries := []observedPath{}
		for _, row := range rows {
			p := filepath.Join(path, row.Name())
			if x, e := record(p); e == nil {
				out = append(out, x)
				entries = append(entries, x)
			}
		}
		for _, x := range entries {
			if !x.dir {
				continue
			}
			if info, e := os.Lstat(x.path); e != nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if e := walk(x.path); e != nil && !errors.Is(e, os.ErrPermission) {
				return e
			}
		}
		return nil
	}
	err := walk(root)
	return out, err
}

// Source watchdog generators use os.walk: child directories, then files, then
// each child subtree. Symlink directories are not recursively followed.
func walkEventDescendants(root string, emit func(string, bool) bool) error {
	f, e := os.Open(root)
	if e != nil {
		return e
	}
	rows, e := f.Readdir(-1)
	f.Close()
	if e != nil {
		return e
	}
	for _, dir := range []bool{true, false} {
		for _, row := range rows {
			if row.IsDir() == dir {
				if !emit(filepath.Join(root, row.Name()), dir) {
					return nil
				}
			}
		}
	}
	for _, row := range rows {
		if row.IsDir() && row.Mode()&os.ModeSymlink == 0 {
			if e := walkEventDescendants(filepath.Join(root, row.Name()), emit); e != nil {
				return e
			}
		}
	}
	return nil
}

// DirectorySnapshotDiff uses each snapshot's last path for an inode, including
// paths that already existed. A deleted hardlink can therefore be a move to an
// unchanged sibling. Filtering to newly created paths would change the source.
func pollingDiff(before, after []observedPath) []fileEvent {
	type identity struct{ dev, ino uint64 }
	old, new := map[string]observedPath{}, map[string]observedPath{}
	oldInode, newInode := map[identity]string{}, map[identity]string{}
	for _, x := range before {
		old[x.path] = x
		oldInode[identity{x.dev, x.ino}] = x.path
	}
	for _, x := range after {
		new[x.path] = x
		newInode[identity{x.dev, x.ino}] = x.path
	}
	deleted, created := map[string]bool{}, map[string]bool{}
	for _, x := range before {
		y, ok := new[x.path]
		if !ok || x.dev != y.dev || x.ino != y.ino {
			deleted[x.path] = true
		}
	}
	for _, x := range after {
		y, ok := old[x.path]
		if !ok || x.dev != y.dev || x.ino != y.ino {
			created[x.path] = true
		}
	}
	moved := [][2]string{}
	seen := map[[2]string]bool{}
	addMove := func(src, dst string) {
		pair := [2]string{src, dst}
		if !seen[pair] {
			seen[pair] = true
			moved = append(moved, pair)
		}
	}
	for _, x := range before {
		if deleted[x.path] {
			if dst := newInode[identity{x.dev, x.ino}]; dst != "" {
				delete(deleted, x.path)
				addMove(x.path, dst)
			}
		}
	}
	for _, x := range after {
		if created[x.path] {
			if src := oldInode[identity{x.dev, x.ino}]; src != "" {
				delete(created, x.path)
				addMove(src, x.path)
			}
		}
	}
	out := []fileEvent{}
	// The source emitter groups files before directories. Order within each
	// Python set is unspecified; native directory order is retained here.
	for _, dir := range []bool{false, true} {
		for _, x := range before {
			if x.dir == dir && deleted[x.path] {
				out = append(out, fileEvent{Kind: "deleted", Path: x.path, Directory: dir})
			}
		}
		for _, x := range after {
			if x.dir == dir && created[x.path] {
				out = append(out, fileEvent{Kind: "created", Path: x.path, Directory: dir})
			}
		}
		for _, pair := range moved {
			if x := old[pair[0]]; x.dir == dir {
				out = append(out, fileEvent{Kind: "moved", Path: pair[0], Dest: pair[1], Directory: dir})
			}
		}
	}
	return out
}
func newPollingObserver(spec Watch) (observer, error) {
	before, e := scanObserved(spec.Path)
	if e != nil {
		return nil, e
	}
	w := &pollingObserver{stop: make(chan struct{}), done: make(chan struct{}), events: make(chan fileEvent, 128)}
	go func() {
		defer close(w.done)
		defer close(w.events)
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		send := func(e fileEvent) bool {
			select {
			case w.events <- e:
				return true
			case <-w.stop:
				return false
			}
		}
		for {
			select {
			case <-w.stop:
				return
			case <-timer.C:
			}
			after, e := scanObserved(spec.Path)
			if e != nil {
				send(fileEvent{Kind: "deleted", Path: spec.Path, Directory: true})
				return
			}
			for _, e := range pollingDiff(before, after) {
				if !send(e) {
					return
				}
			}
			before = after
		}
	}()
	return w, nil
}
