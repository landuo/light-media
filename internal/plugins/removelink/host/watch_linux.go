//go:build linux

package cleanuphost

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins/removelink"
)

type renamePending struct {
	path      string
	directory bool
	at        time.Time
	order     uint64
}
type nativeWatcher struct {
	fd         int
	cancel     context.CancelFunc
	done       chan struct{}
	events     chan Event
	errors     chan error
	once       sync.Once
	specs      []removelink.Watch
	dirs       map[int]string
	known      map[string]bool
	pending    map[uint32]renamePending
	open       func(string) (*os.File, error)
	nonce      string
	seq        uint64
	pendingSeq uint64
}

// observerInitError marks only a native observer syscall, never authorization,
// directory validation, randomness, or a later running observer failure.
type observerInitError struct{ cause error }

func (e *observerInitError) Error() string { return e.cause.Error() }
func (e *observerInitError) Unwrap() error { return e.cause }

func NativeWatch(parent context.Context, specs []removelink.Watch, open func(string) (*os.File, error)) (Watcher, error) {
	fd, e := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if e != nil {
		// Shared-fd allocation precedes any directory attempt. It cannot
		// honestly identify a source per-directory observer failure.
		return nil, e
	}
	ctx, cancel := context.WithCancel(parent)
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		unix.Close(fd)
		cancel()
		return nil, e
	}
	w := &nativeWatcher{fd: fd, cancel: cancel, done: make(chan struct{}), events: make(chan Event, 4096), errors: make(chan error, 1), specs: append([]removelink.Watch(nil), specs...), dirs: map[int]string{}, known: map[string]bool{}, pending: map[uint32]renamePending{}, open: open, nonce: hex.EncodeToString(nonce[:])}
	for ordinal, s := range specs {
		if e = w.addTree(s.Path, false); e != nil {
			unix.Close(fd)
			cancel()
			var native *observerInitError
			if errors.As(e, &native) {
				return nil, &WatchInitFailure{s, ordinal, "inotify_add_watch", native.cause}
			}
			return nil, e
		}
	}
	go w.run(ctx)
	return w, nil
}
func (w *nativeWatcher) Events() <-chan Event { return w.events }
func (w *nativeWatcher) Errors() <-chan error { return w.errors }
func (w *nativeWatcher) Close() error         { w.once.Do(w.cancel); <-w.done; return nil }
func (w *nativeWatcher) fail(e error) {
	select {
	case w.errors <- e:
	default:
	}
	w.cancel()
}
func (w *nativeWatcher) send(e Event) {
	for i, s := range w.specs {
		src, dst := inside(s.Path, e.Path), e.Dest != "" && inside(s.Path, e.Dest)
		next := e
		if e.Kind == "moved" {
			switch {
			case src && dst:
			case src:
				next.Kind = "deleted"
				next.Dest = ""
			case dst:
				next.Kind = "created"
				next.Path = e.Dest
				next.Dest = ""
			default:
				continue
			}
		} else if !src {
			continue
		}
		w.seq++
		next.ID = fmt.Sprintf("inotify:%s:%d:%d", w.nonce, w.seq, i)
		next.Mode = s.Mode
		next.At = time.Now()
		select {
		case w.events <- next:
		default:
			w.fail(errors.New("inotify adapter event queue overflow; history must be reconciled"))
			return
		}
	}
}
func (w *nativeWatcher) addTree(p string, emit bool) error {
	f, e := w.open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	// The watched directory is the already-authorized O_NOFOLLOW descriptor,
	// not a path that a concurrent symlink could redirect during AddWatch.
	mask := uint32(unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR)
	wd, e := unix.InotifyAddWatch(w.fd, fmt.Sprintf("/proc/self/fd/%d", f.Fd()), mask)
	if e != nil {
		return &observerInitError{e}
	}
	if old, ok := w.dirs[wd]; ok && old != p {
		return errors.New("physical watch alias is ambiguous")
	}
	w.dirs[wd] = p
	rows, e := f.Readdir(-1)
	if e != nil {
		return e
	}
	// Watch ignored subtrees too: their rename cookies are required to let the
	// original handler distinguish moving into/out of trickplay from deletion.
	for _, r := range rows {
		path := filepath.Join(p, r.Name())
		if strings.HasPrefix(r.Name(), ".light-media-rmlink-") {
			continue
		}
		if r.Mode()&os.ModeSymlink != 0 {
			if removelink.Trickplay(path) {
				continue
			}
			return ErrUnsafe
		}
		if !r.IsDir() {
			if !r.Mode().IsRegular() {
				if removelink.Trickplay(path) {
					continue
				}
				return ErrUnsafe
			}
			if emit && !w.known[path] {
				w.send(Event{Kind: "created", Path: path})
			}
			w.known[path] = true
		}
	}
	for _, r := range rows {
		if r.IsDir() && !strings.HasPrefix(r.Name(), ".light-media-rmlink-") {
			if e = w.addTree(filepath.Join(p, r.Name()), emit); e != nil {
				return e
			}
		}
	}

	return nil
}
func (w *nativeWatcher) watched(p string) bool {
	for _, s := range w.specs {
		if inside(s.Path, p) {
			return true
		}
	}
	return false
}
func (w *nativeWatcher) directoryMove(src, dst string) {
	for wd, p := range w.dirs {
		if inside(src, p) {
			if w.watched(dst) {
				w.dirs[wd] = dst + strings.TrimPrefix(p, src)
			} else {
				_, _ = unix.InotifyRmWatch(w.fd, uint32(wd))
				delete(w.dirs, wd)
			}
		}
	}
	for p := range w.known {
		if inside(src, p) {
			delete(w.known, p)
		}
	}
	if !w.watched(dst) {
		return
	}
	// watchdog synthesizes descendant moves from the destination os.walk order.
	var walk func(string) error
	walk = func(path string) error {
		f, e := w.open(path)
		if e != nil {
			return e
		}
		rows, e := f.Readdir(-1)
		f.Close()
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.Mode().IsRegular() {
				next := filepath.Join(path, r.Name())
				old := src + strings.TrimPrefix(next, dst)
				w.known[next] = true
				w.send(Event{Kind: "moved", Path: old, Dest: next})
			} else if !r.IsDir() && !removelink.Trickplay(filepath.Join(path, r.Name())) {
				return ErrUnsafe
			}
		}
		for _, r := range rows {
			if r.IsDir() {
				if e = walk(filepath.Join(path, r.Name())); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e := walk(dst); e != nil {
		w.fail(e)
	}
}

func (w *nativeWatcher) expire(all bool) {
	var cookies []uint32
	for cookie, p := range w.pending {
		if all || time.Since(p.at) >= 500*time.Millisecond {
			cookies = append(cookies, cookie)
		}
	}
	sort.Slice(cookies, func(i, j int) bool { return w.pending[cookies[i]].order < w.pending[cookies[j]].order })
	for _, cookie := range cookies {
		p := w.pending[cookie]
		w.send(Event{Kind: "deleted", Path: p.path, Directory: p.directory})
		delete(w.pending, cookie)
		if !p.directory {
			delete(w.known, p.path)
		} else {
			for wd, path := range w.dirs {
				if inside(p.path, path) {
					_, _ = unix.InotifyRmWatch(w.fd, uint32(wd))
					delete(w.dirs, wd)
				}
			}
			for path := range w.known {
				if inside(p.path, path) {
					delete(w.known, path)
				}
			}
		}
	}
}

func (w *nativeWatcher) event(wd int, mask, cookie uint32, name string) {
	if mask&unix.IN_Q_OVERFLOW != 0 {
		w.fail(errors.New("inotify kernel queue overflow; no deletion replay"))
		return
	}
	p, ok := w.dirs[wd]
	if !ok {
		return
	}
	if mask&unix.IN_IGNORED != 0 {
		delete(w.dirs, wd)
		return
	}
	if name == "" {
		if mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF) != 0 {
			for _, spec := range w.specs {
				if spec.Path == p {
					w.fail(errors.New("authorized watch root moved or disappeared"))
					return
				}
			}
		}
		if mask&unix.IN_DELETE_SELF != 0 {
			delete(w.dirs, wd)
		}
		return
	}
	p = filepath.Join(p, name)
	dir := mask&unix.IN_ISDIR != 0
	if strings.HasPrefix(name, ".light-media-rmlink-") {
		return
	}
	if mask&unix.IN_MOVED_FROM != 0 {
		if len(w.pending) >= 4096 {
			w.fail(errors.New("inotify rename budget exceeded"))
			return
		}
		w.pendingSeq++
		w.pending[cookie] = renamePending{p, dir, time.Now(), w.pendingSeq}
		return
	}
	if mask&unix.IN_MOVED_TO != 0 {
		if old, ok := w.pending[cookie]; ok {
			delete(w.pending, cookie)
			w.send(Event{Kind: "moved", Path: old.path, Dest: p, Directory: dir})
			if dir {
				w.directoryMove(old.path, p)
				if e := w.addTree(p, true); e != nil {
					w.fail(e)
				}
			} else {
				delete(w.known, old.path)
				w.known[p] = false
			}
			return
		}
	}
	if mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
		w.send(Event{Kind: "created", Path: p, Directory: dir})
		if dir {
			if e := w.addTree(p, true); e != nil {
				w.fail(e)
			}
		} else {
			w.known[p] = false
		}
		return
	}
	if mask&unix.IN_DELETE != 0 {
		w.send(Event{Kind: "deleted", Path: p, Directory: dir})
		delete(w.known, p)
	}
}
func (w *nativeWatcher) run(ctx context.Context) {
	defer close(w.done)
	defer close(w.events)
	defer close(w.errors)
	defer unix.Close(w.fd)
	data := make([]byte, 64<<10)
	for {
		n, e := unix.Read(w.fd, data)
		if e == nil {
			for pos := 0; pos+unix.SizeofInotifyEvent <= n; {
				wd := int(int32(binary.NativeEndian.Uint32(data[pos:])))
				mask := binary.NativeEndian.Uint32(data[pos+4:])
				cookie := binary.NativeEndian.Uint32(data[pos+8:])
				size := int(binary.NativeEndian.Uint32(data[pos+12:]))
				end := pos + unix.SizeofInotifyEvent + size
				if end > n {
					w.fail(errors.New("incomplete inotify event"))
					return
				}
				name := strings.TrimRight(string(data[pos+unix.SizeofInotifyEvent:end]), "\x00")
				w.event(wd, mask, cookie, name)
				pos = end
			}
			if n > 0 {
				w.expire(false)
				continue
			}
		} else if !errors.Is(e, unix.EAGAIN) && !errors.Is(e, unix.EINTR) {
			w.fail(e)
			return
		}
		w.expire(false)
		select {
		case <-ctx.Done():
			w.expire(true)
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}
