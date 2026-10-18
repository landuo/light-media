//go:build darwin

package scrapehost

// kqueue observes the authorized directory descriptors. A kernel change wakes
// a descriptor-relative rescan; device/inode pairs recover moved file events.
// Linux uses inotify cookies instead. Neither backend polls to execute cleanup.
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type darwinEntry struct {
	Path  string
	Stamp Stamp
	Dir   bool
}
type darwinWatcher struct {
	fd       int
	cancel   context.CancelFunc
	done     chan struct{}
	events   chan Event
	failures chan error
	once     sync.Once
	specs    []Watch
	open     func(string) (*os.File, error)
	dirs     map[string]*os.File
	entries  []darwinEntry
	nonce    string
	sequence uint64
}

func NativeWatch(parent context.Context, specs []Watch, open func(string) (*os.File, error)) (Watcher, error) {
	fd, e := unix.Kqueue()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(parent)
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		unix.Close(fd)
		cancel()
		return nil, e
	}
	w := &darwinWatcher{fd: fd, cancel: cancel, done: make(chan struct{}), events: make(chan Event, 4096), failures: make(chan error, 1), specs: append([]Watch(nil), specs...), open: open, dirs: map[string]*os.File{}, nonce: hex.EncodeToString(nonce)}
	w.entries, e = w.scan()
	if e != nil {
		w.closeDescriptors()
		cancel()
		return nil, e
	}
	go w.loop(ctx)
	return w, nil
}
func (w *darwinWatcher) Events() <-chan Event { return w.events }
func (w *darwinWatcher) Errors() <-chan error { return w.failures }
func (w *darwinWatcher) Close() error         { w.once.Do(w.cancel); <-w.done; return nil }
func (w *darwinWatcher) closeDescriptors() {
	for _, f := range w.dirs {
		f.Close()
	}
	unix.Close(w.fd)
}
func (w *darwinWatcher) register(path string, f *os.File) error {
	if old := w.dirs[path]; old != nil {
		a, e := fileStamp(old)
		if e != nil {
			return e
		}
		b, e := fileStamp(f)
		if e != nil || !sameEntity(a, b) {
			return ErrUnsafe
		}
		return nil
	}
	duplicate, e := unix.Dup(int(f.Fd()))
	if e != nil {
		return e
	}
	unix.CloseOnExec(duplicate)
	owned := os.NewFile(uintptr(duplicate), path)
	change := unix.Kevent_t{Ident: uint64(duplicate), Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR, Fflags: unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE | unix.NOTE_LINK}
	if _, e = unix.Kevent(w.fd, []unix.Kevent_t{change}, nil, nil); e != nil {
		owned.Close()
		return e
	}
	w.dirs[path] = owned
	return nil
}
func (w *darwinWatcher) scan() ([]darwinEntry, error) {
	out := []darwinEntry{}
	seen := map[string]bool{}
	active := map[string]bool{}
	var walk func(string) error
	walk = func(path string) error {
		if active[path] {
			return nil
		}
		active[path] = true
		f, e := w.open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		if e = w.register(path, f); e != nil {
			return e
		}
		rows, e := f.Readdir(-1)
		if e != nil {
			return e
		}
		for _, row := range rows {
			if strings.HasPrefix(row.Name(), ".light-media-scrape-") || row.Mode()&os.ModeSymlink != 0 || (!row.IsDir() && !row.Mode().IsRegular()) {
				continue
			}
			p := filepath.Join(path, row.Name())
			if seen[p] {
				continue
			}
			seen[p] = true
			s, e := stamp(row)
			if e != nil {
				return e
			}
			out = append(out, darwinEntry{p, s, row.IsDir()})
			if row.IsDir() {
				if e = walk(p); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for _, spec := range w.specs {
		if e := walk(spec.Path); e != nil {
			return nil, e
		}
	}
	for path, f := range w.dirs {
		if !active[path] {
			change := unix.Kevent_t{Ident: uint64(f.Fd()), Filter: unix.EVFILT_VNODE, Flags: unix.EV_DELETE}
			_, _ = unix.Kevent(w.fd, []unix.Kevent_t{change}, nil, nil)
			f.Close()
			delete(w.dirs, path)
		}
	}
	return out, nil
}
func (w *darwinWatcher) send(event Event) error {
	selected := false
	for _, spec := range w.specs {
		if inside(spec.Path, event.Path) || (event.Kind == "moved" && inside(spec.Path, event.Dest)) {
			selected = true
			break
		}
	}
	if !selected {
		return nil
	}
	w.sequence++
	event.ID = fmt.Sprintf("kqueue:%s:%d", w.nonce, w.sequence)
	event.Mode = "hardlink"
	event.At = time.Now()
	select {
	case w.events <- event:
		return nil
	default:
		return errors.New("kqueue adapter event queue overflow; reconcile captured history")
	}
}
func (w *darwinWatcher) changed(next []darwinEntry) error {
	old := map[string]darwinEntry{}
	current := map[string]darwinEntry{}
	for _, r := range w.entries {
		old[r.Path] = r
	}
	for _, r := range next {
		current[r.Path] = r
	}
	removed := []darwinEntry{}
	added := []darwinEntry{}
	for _, r := range w.entries {
		n, ok := current[r.Path]
		if !ok || !sameEntity(n.Stamp, r.Stamp) {
			removed = append(removed, r)
		}
	}
	for _, r := range next {
		o, ok := old[r.Path]
		if !ok || !sameEntity(o.Stamp, r.Stamp) {
			added = append(added, r)
		}
	}
	used := map[string]bool{}
	moved := map[string]bool{}
	// Emit an observed moved pair before a separate create so the source handler
	// can inspect a destination's old entity when a rename overwrote it.
	for _, r := range removed {
		for _, a := range added {
			if used[a.Path] || r.Path == a.Path || !sameEntity(r.Stamp, a.Stamp) {
				continue
			}
			if e := w.send(Event{Kind: "moved", Path: r.Path, Dest: a.Path, Directory: r.Dir}); e != nil {
				return e
			}
			used[a.Path] = true
			moved[r.Path] = true
			break
		}
	}
	for _, a := range added {
		if !used[a.Path] {
			if e := w.send(Event{Kind: "created", Path: a.Path, Directory: a.Dir}); e != nil {
				return e
			}
		}
	}
	for _, r := range removed {
		if !moved[r.Path] {
			if e := w.send(Event{Kind: "deleted", Path: r.Path, Directory: r.Dir}); e != nil {
				return e
			}
		}
	}
	w.entries = next
	return nil
}
func (w *darwinWatcher) loop(ctx context.Context) {
	defer close(w.done)
	defer close(w.events)
	defer close(w.failures)
	defer w.closeDescriptors()
	events := make([]unix.Kevent_t, 256)
	for {
		if ctx.Err() != nil {
			return
		}
		timeout := unix.NsecToTimespec(int64(100 * time.Millisecond))
		n, e := unix.Kevent(w.fd, nil, events, &timeout)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			w.failures <- e
			return
		}
		if n == 0 {
			continue
		}
		next, e := w.scan()
		if e == nil {
			e = w.changed(next)
		}
		if e != nil {
			w.failures <- e
			return
		}
	}
}
