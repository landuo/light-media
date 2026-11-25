//go:build linux

package removelink

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type pendingMove struct {
	path      string
	directory bool
	at        time.Time
	order     uint64
}
type inotifyObserver struct {
	fd         int
	root       string
	dirs       map[int]string
	pending    map[uint32]pendingMove
	seq        uint64
	events     chan fileEvent
	stop, done chan struct{}
	once       sync.Once
}

var errObserverEnded = errors.New("source observer ended")

func (w *inotifyObserver) Events() <-chan fileEvent { return w.events }
func (w *inotifyObserver) Close()                   { w.once.Do(func() { close(w.stop) }); <-w.done }
func sourceObserver(spec Watch) (observer, error) {
	return sourceObserverWithInit(spec, syscall.InotifyInit1)
}

// watchdog selects its Linux observer before starting its native descriptor.
// A start failure belongs to init_plugin's per-directory catch, not fallback.
func sourceObserverWithInit(spec Watch, init func(int) (int, error)) (observer, error) {
	fd, e := init(syscall.IN_NONBLOCK | syscall.IN_CLOEXEC)
	if e != nil {
		return nil, sourceInotifyError(e)
	}
	w := &inotifyObserver{fd: fd, root: spec.Path, dirs: map[int]string{}, pending: map[uint32]pendingMove{}, events: make(chan fileEvent, 128), stop: make(chan struct{}), done: make(chan struct{})}
	if e = w.addTree(spec.Path, false); e != nil {
		syscall.Close(fd)
		return nil, e
	}
	go w.run()
	return w, nil
}

type inotifyLimitError struct {
	errno syscall.Errno
	text  string
}

func (e inotifyLimitError) Error() string { return fmt.Sprintf("[Errno %d] %s", e.errno, e.text) }
func (e inotifyLimitError) Unwrap() error { return e.errno }
func sourceInotifyError(err error) error {
	switch {
	case errors.Is(err, syscall.ENOSPC):
		return inotifyLimitError{syscall.ENOSPC, "inotify watch limit reached"}
	case errors.Is(err, syscall.EMFILE):
		return inotifyLimitError{syscall.EMFILE, "inotify instance limit reached"}
	default:
		return err
	}
}
func (w *inotifyObserver) send(e fileEvent) bool {
	select {
	case w.events <- e:
		return true
	case <-w.stop:
		return false
	}
}
func (w *inotifyObserver) addTree(path string, emit bool) error {
	mask := uint32(syscall.IN_CREATE | syscall.IN_DELETE | syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF | syscall.IN_ONLYDIR)
	wd, e := syscall.InotifyAddWatch(w.fd, path, mask)
	if e != nil {
		return sourceInotifyError(e)
	}
	w.dirs[wd] = path
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	rows, e := f.Readdir(-1)
	f.Close()
	if e != nil {
		return e
	}
	if emit {
		for _, dir := range []bool{true, false} {
			for _, row := range rows {
				if row.IsDir() == dir {
					if !w.send(fileEvent{Kind: "created", Path: filepath.Join(path, row.Name()), Directory: dir}) {
						return nil
					}
				}
			}
		}
	}
	for _, row := range rows {
		if row.IsDir() && row.Mode()&os.ModeSymlink == 0 {
			if e := w.addTree(filepath.Join(path, row.Name()), emit); e != nil {
				return e
			}
		}
	}

	return nil
}
func (w *inotifyObserver) expire() {
	cookies := []uint32{}
	for cookie, p := range w.pending {
		if time.Since(p.at) >= 500*time.Millisecond {
			cookies = append(cookies, cookie)
		}
	}
	sort.Slice(cookies, func(i, j int) bool { return w.pending[cookies[i]].order < w.pending[cookies[j]].order })
	for _, cookie := range cookies {
		p := w.pending[cookie]
		delete(w.pending, cookie)
		w.send(fileEvent{Kind: "deleted", Path: p.path, Directory: p.directory})
		if p.directory {
			for wd, dir := range w.dirs {
				if dir == p.path || strings.HasPrefix(dir, p.path+"/") {
					syscall.InotifyRmWatch(w.fd, uint32(wd))
					delete(w.dirs, wd)
				}
			}
		}
	}
}
func (w *inotifyObserver) movedDirectory(src, dest string) {
	for wd, p := range w.dirs {
		if p == src || strings.HasPrefix(p, src+"/") {
			w.dirs[wd] = dest + strings.TrimPrefix(p, src)
		}
	}
	e := walkEventDescendants(dest, func(path string, dir bool) bool {
		return w.send(fileEvent{Kind: "moved", Path: strings.ReplaceAll(path, dest, src), Dest: path, Directory: dir})
	})
	if e != nil {
		w.send(fileEvent{Err: e})
	}

}
func (w *inotifyObserver) event(wd int, mask, cookie uint32, name string) error {
	if mask&syscall.IN_Q_OVERFLOW != 0 {
		return errors.New("inotify event queue overflow")
	}
	root, ok := w.dirs[wd]
	if !ok {
		return nil
	}
	if mask&syscall.IN_IGNORED != 0 {
		delete(w.dirs, wd)
		return nil
	}
	if name == "" {
		if mask&syscall.IN_DELETE_SELF != 0 && root == w.root {
			w.send(fileEvent{Kind: "deleted", Path: root, Directory: true})
			return errObserverEnded
		}
		return nil
	}
	path := filepath.Join(root, name)
	dir := mask&syscall.IN_ISDIR != 0
	if mask&syscall.IN_MOVED_FROM != 0 {
		w.seq++
		w.pending[cookie] = pendingMove{path, dir, time.Now(), w.seq}
		return nil
	}
	if mask&syscall.IN_MOVED_TO != 0 {
		if from, ok := w.pending[cookie]; ok {
			delete(w.pending, cookie)
			w.send(fileEvent{Kind: "moved", Path: from.path, Dest: path, Directory: dir})
			if dir {
				w.movedDirectory(from.path, path)
			}
			return nil
		}
	}
	if mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0 {
		w.send(fileEvent{Kind: "created", Path: path, Directory: dir})
		if dir {
			return w.addTree(path, true)
		}
		return nil
	}
	if mask&syscall.IN_DELETE != 0 {
		w.send(fileEvent{Kind: "deleted", Path: path, Directory: dir})
	}
	return nil
}
func (w *inotifyObserver) run() {
	defer close(w.done)
	defer close(w.events)
	defer syscall.Close(w.fd)
	buf := make([]byte, 64<<10)
	timer := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-timer.C:
		}
		for {
			select {
			case <-w.stop:
				return
			default:
			}
			n, e := syscall.Read(w.fd, buf)
			if errors.Is(e, syscall.EAGAIN) || errors.Is(e, syscall.EINTR) {
				break
			}
			if e != nil {
				w.send(fileEvent{Err: e})
				return
			}
			if n == 0 {
				break
			}
			for at := 0; at+16 <= n; {
				wd := int(int32(binary.NativeEndian.Uint32(buf[at:])))
				mask := binary.NativeEndian.Uint32(buf[at+4:])
				cookie := binary.NativeEndian.Uint32(buf[at+8:])
				length := int(binary.NativeEndian.Uint32(buf[at+12:]))
				end := at + 16 + length
				if end > n {
					w.send(fileEvent{Err: errors.New("invalid inotify event")})
					return
				}
				name := strings.TrimRight(string(buf[at+16:end]), "\x00")
				if e := w.event(wd, mask, cookie, name); e != nil {
					if !errors.Is(e, errObserverEnded) {
						w.send(fileEvent{Err: e})
					}
					return
				}
				at = end
			}
		}
		w.expire()
	}
}
