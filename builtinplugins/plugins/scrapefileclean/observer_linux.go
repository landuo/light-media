//go:build linux

package scrapefileclean

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"syscall"
)

type renamePending struct {
	path      string
	directory bool
	at        time.Time
	order     uint64
}

func openNativeDirectory(path string, pin *authorizedRoot) (*os.File, error) {
	if !validPath(path) || path == "/" {
		return nil, fs.ErrPermission
	}
	const flags = syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fd, err := syscall.Open("/", flags, 0)
	if err != nil {
		return nil, err
	}
	var walked string
	matched := pin == nil
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := syscall.Openat(fd, component, flags, 0)
		_ = syscall.Close(fd)
		if err != nil {
			if !matched {
				return nil, fs.ErrPermission // Losing the root is not evidence of an individual unlink.
			}
			return nil, err
		}
		fd = next
		walked += "/" + component
		if pin != nil && walked == pin.path {
			var stat syscall.Stat_t
			if err := syscall.Fstat(fd, &stat); err != nil || uint64(stat.Dev) != pin.id.Dev || uint64(stat.Ino) != pin.id.Inode {
				_ = syscall.Close(fd)
				return nil, fs.ErrPermission
			}
			matched = true
		}
	}
	return os.NewFile(uintptr(fd), "/proc/self/fd/"+strconv.Itoa(fd)), nil
}

type nativeWatcher struct {
	fd         int
	cancel     context.CancelFunc
	done       chan struct{}
	events     chan Event
	report     func(error)
	once       sync.Once
	root       string
	dirs       map[int]string
	known      map[string]bool
	pending    map[uint32]renamePending
	open       func(string) (*os.File, error)
	pendingSeq uint64
}

func NativeWatch(parent context.Context, root string, open func(string) (*os.File, error), report func(error)) (Watcher, error) {
	fd, e := syscall.InotifyInit1(syscall.IN_NONBLOCK | syscall.IN_CLOEXEC)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(parent)
	w := &nativeWatcher{fd: fd, cancel: cancel, done: make(chan struct{}), events: make(chan Event, 4096), report: report, root: root, dirs: map[int]string{}, known: map[string]bool{}, pending: map[uint32]renamePending{}, open: open}
	if e = w.addTree(root, false); e != nil {
		syscall.Close(fd)
		cancel()
		return nil, e
	}
	go w.run(ctx)
	return w, nil
}
func (w *nativeWatcher) Events() <-chan Event { return w.events }
func (w *nativeWatcher) Close() error         { w.once.Do(w.cancel); <-w.done; return nil }
func (w *nativeWatcher) fail(e error) {
	w.report(e)
	w.cancel()
}
func (w *nativeWatcher) send(e Event) {
	if internalPath(e.Path) || internalPath(e.Dest) {
		return
	}
	src, dst := inside(w.root, e.Path), e.Dest != "" && inside(w.root, e.Dest)
	if e.Kind == "moved" {
		switch {
		case src && dst:
		case src:
			e.Kind, e.Dest = "deleted", ""
		case dst:
			e.Kind, e.Path, e.Dest = "created", e.Dest, ""
		default:
			return
		}
	} else if !src {
		return
	}
	select {
	case w.events <- e:
	default:
		w.fail(errors.New("inotify adapter event queue overflow; history must be reconciled"))
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
	mask := uint32(syscall.IN_CREATE | syscall.IN_DELETE | syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF | syscall.IN_ONLYDIR)
	wd, e := syscall.InotifyAddWatch(w.fd, fmt.Sprintf("/proc/self/fd/%d", f.Fd()), mask)
	if e != nil {
		return e
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
		if internalPath(path) {
			continue
		}
		if r.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !r.IsDir() {
			if !r.Mode().IsRegular() {
				continue
			}
			if emit && !w.known[path] {
				w.send(Event{Kind: "created", Path: path})
			}
			w.known[path] = true
		}
	}
	for _, r := range rows {
		if r.IsDir() && !internalPath(filepath.Join(p, r.Name())) {
			if e = w.addTree(filepath.Join(p, r.Name()), emit); e != nil {
				return e
			}
		}
	}

	return nil
}
func (w *nativeWatcher) watched(p string) bool {
	return !internalPath(p) && inside(w.root, p)
}
func (w *nativeWatcher) directoryMove(src, dst string) {
	for wd, p := range w.dirs {
		if inside(src, p) {
			if w.watched(dst) {
				w.dirs[wd] = dst + strings.TrimPrefix(p, src)
			} else {
				_, _ = syscall.InotifyRmWatch(w.fd, uint32(wd))
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

func (w *nativeWatcher) expire() {
	var cookies []uint32
	for cookie, p := range w.pending {
		if time.Since(p.at) >= 500*time.Millisecond {
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
					_, _ = syscall.InotifyRmWatch(w.fd, uint32(wd))
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
	if mask&syscall.IN_Q_OVERFLOW != 0 {
		w.fail(errors.New("inotify kernel queue overflow; no deletion replay"))
		return
	}
	p, ok := w.dirs[wd]
	if !ok {
		return
	}
	if mask&syscall.IN_IGNORED != 0 {
		delete(w.dirs, wd)
		return
	}
	if name == "" {
		if mask&(syscall.IN_DELETE_SELF|syscall.IN_MOVE_SELF) != 0 && w.root == p {
			w.fail(errors.New("authorized watch root moved or disappeared"))
			return
		}
		if mask&syscall.IN_DELETE_SELF != 0 {
			delete(w.dirs, wd)
		}
		return
	}
	p = filepath.Join(p, name)
	dir := mask&syscall.IN_ISDIR != 0
	if mask&syscall.IN_MOVED_FROM != 0 {
		if len(w.pending) >= 4096 {
			w.fail(errors.New("inotify rename budget exceeded"))
			return
		}
		w.pendingSeq++
		w.pending[cookie] = renamePending{p, dir, time.Now(), w.pendingSeq}
		return
	}
	if mask&syscall.IN_MOVED_TO != 0 {
		if old, ok := w.pending[cookie]; ok {
			delete(w.pending, cookie)
			// The host's quarantine and possible rollback are part of one
			// guarded effect, not new source filesystem deletions.
			if internalPath(old.path) || internalPath(p) {
				if dir {
					if internalPath(p) {
						w.directoryMove(old.path, p)
					} else if e := w.addTree(p, false); e != nil {
						w.fail(e)
					}
				} else {
					delete(w.known, old.path)
					if !internalPath(p) {
						w.known[p] = true
					}
				}
				return
			}
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
	if internalPath(p) {
		return
	}
	if mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0 {
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
	if mask&syscall.IN_DELETE != 0 {
		w.send(Event{Kind: "deleted", Path: p, Directory: dir})
		delete(w.known, p)
	}
}
func (w *nativeWatcher) run(ctx context.Context) {
	defer close(w.done)
	defer close(w.events)
	defer syscall.Close(w.fd)
	data := make([]byte, 64<<10)
	for ctx.Err() == nil {
		n, e := syscall.Read(w.fd, data)
		if e == nil {
			for pos := 0; pos+syscall.SizeofInotifyEvent <= n; {
				if ctx.Err() != nil {
					return
				}
				wd := int(int32(binary.NativeEndian.Uint32(data[pos:])))
				mask := binary.NativeEndian.Uint32(data[pos+4:])
				cookie := binary.NativeEndian.Uint32(data[pos+8:])
				size := int(binary.NativeEndian.Uint32(data[pos+12:]))
				end := pos + syscall.SizeofInotifyEvent + size
				if end > n {
					w.fail(errors.New("incomplete inotify event"))
					return
				}
				name := strings.TrimRight(string(data[pos+syscall.SizeofInotifyEvent:end]), "\x00")
				w.event(wd, mask, cookie, name)
				pos = end
			}
			if n > 0 {
				w.expire()
				continue
			}
		} else if !errors.Is(e, syscall.EAGAIN) && !errors.Is(e, syscall.EINTR) {
			w.fail(e)
			return
		}
		w.expire()
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}
