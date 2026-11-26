package app

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// A directory rename (or a hard link) may arrive while a writer still holds
// the inode. A momentary read lease distinguishes that case across containers.
// Unknown lease support is acceptable after CLOSE_WRITE or a full stability
// window. No lease is held during recognition or publication.
func directoryFileReady(path string, allowUnverified bool) bool {
	f, err := importOpen(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = unix.FcntlInt(f.Fd(), unix.F_SETLEASE, unix.F_RDLCK)
	if err == nil {
		return true
	}
	// With no lease privilege (e.g. a foreign-owned seed) or on a mount
	// without leases, a rename/create alone has no completion evidence.
	// EAGAIN means another writer is open, even if the snapshot stopped changing.
	return allowUnverified && (errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) ||
		errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS))
}

func (a *App) watchDirectoryEvents(ctx context.Context) {
	type activeWatch struct {
		fingerprint string
		cancel      context.CancelFunc
	}
	active := map[string]activeWatch{}
	var wg sync.WaitGroup
	defer wg.Wait()
	refresh := func() {
		rules := a.settingsSnapshot()
		live := map[string]bool{}
		for _, rule := range rules.Directories {
			if !rule.Enabled || rule.MonitorType != "monitor" {
				continue
			}
			root, err := directorySourceWithSettings(*rules, rule)
			if err != nil {
				continue
			}
			fingerprint := directoryFingerprint(rule) + "\x00" + root
			live[rule.ID] = true
			old, ok := active[rule.ID]
			if ok && old.fingerprint == fingerprint {
				continue
			}
			if ok {
				old.cancel()
			}
			wctx, cancel := context.WithCancel(ctx)
			active[rule.ID] = activeWatch{fingerprint, cancel}
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.watchDirectoryRule(wctx, rule, root)
			}()
		}
		for id, old := range active {
			if !live[id] {
				old.cancel()
				delete(active, id)
			}
		}
	}
	refresh()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// Keep reading kernel events while the database or downloader is busy. Both
// the userspace mailbox and the kernel queue are bounded. The existing
// periodic sweep reconciles dropped events, moved-away paths and outages.
func (a *App) watchDirectoryRule(ctx context.Context, rule DirectoryRule, root string) {
	changes := make(chan directoryWatchEvent, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		backoff := 5 * time.Second
		for ctx.Err() == nil {
			started := time.Now()
			err := watchLocalDirectory(ctx, root, func(e directoryWatchEvent) {
				select {
				case changes <- e:
				default:
				}
			})
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, errDirectoryWatchLimit) {
				backoff = 5 * time.Minute
			} else if time.Since(started) > 5*time.Minute {
				backoff = 5 * time.Second
			}
			if err != nil {
				a.log("WARN", "directories", "watch_unavailable", a.clean(err.Error()), "", "")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 5*time.Minute)
		}
	}()
	defer func() { <-done }()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-changes:
			// Collect nearby close/move events, including companions arriving
			// with a release, without delaying an isolated file for a scan tick.
			timer := time.NewTimer(100 * time.Millisecond)
			batch := []directoryWatchEvent{e}
		collect:
			for len(batch) < cap(changes) {
				select {
				case next := <-changes:
					batch = append(batch, next)
				case <-timer.C:
					break collect
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
			timer.Stop()
			if !a.paused() {
				if err := a.inspectDirectoryFiles(ctx, rule, batch); err != nil && ctx.Err() == nil {
					a.log("WARN", "directories", "watch_dispatch_failed", a.clean(err.Error()), "", "")
				}
			}
		}
	}
}

var errDirectoryWatchLimit = errors.New("目录监听达到8192个目录上限，将使用定期巡检")

func watchLocalDirectory(ctx context.Context, root string, emit func(directoryWatchEvent)) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	const mask = unix.IN_CLOSE_WRITE | unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR | unix.IN_DONT_FOLLOW
	watched := map[int]string{}
	var addTree func(string) error
	addTree = func(path string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The reconciliation scanner descends at most 16 directories.
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel != "." && strings.Count(filepath.ToSlash(rel), "/") >= 16 {
			return nil
		}
		if len(watched) >= 8192 {
			return errDirectoryWatchLimit
		}
		wd, err := unix.InotifyAddWatch(fd, path, mask)
		if errors.Is(err, fs.ErrNotExist) && path != root {
			return nil
		}
		if err != nil {
			return err
		}
		watched[wd] = path
		dir, err := importDir(path, false)
		if errors.Is(err, fs.ErrNotExist) && path != root {
			return nil
		}
		if err != nil {
			return err
		}
		defer dir.Close()
		for ctx.Err() == nil {
			children, readErr := dir.ReadDir(128)
			for _, child := range children {
				if child.IsDir() && !strings.HasPrefix(child.Name(), ".") {
					if err := addTree(filepath.Join(path, child.Name())); err != nil {
						return err
					}
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
		return ctx.Err()
	}
	if err := addTree(root); err != nil {
		return err
	}
	buf := make([]byte, 16*1024)
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for ctx.Err() == nil {
		n, err := unix.Poll(poll, 500)
		if err == unix.EINTR || n == 0 && err == nil {
			continue
		}
		if err != nil {
			return err
		}
		n, err = unix.Read(fd, buf)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			header := buf[off:]
			wd := int(int32(binary.NativeEndian.Uint32(header)))
			flags := binary.NativeEndian.Uint32(header[4:])
			size := int(binary.NativeEndian.Uint32(header[12:]))
			off += unix.SizeofInotifyEvent
			if size > n-off {
				return errors.New("目录监听事件不完整，将重新监听")
			}
			name := strings.TrimRight(string(buf[off:off+size]), "\x00")
			off += size
			if flags&unix.IN_Q_OVERFLOW != 0 {
				return errors.New("目录监听事件溢出，将重新监听")
			}
			dir, ok := watched[wd]
			if !ok {
				continue
			}
			if flags&unix.IN_IGNORED != 0 {
				delete(watched, wd)
				if dir == root {
					return errors.New("监听根目录已移除，将重新监听")
				}
				continue
			}
			if flags&(unix.IN_MOVE_SELF|unix.IN_DELETE_SELF) != 0 {
				if dir == root {
					return errors.New("监听根目录已变更，将重新监听")
				}
				// An already remapped descriptor can receive MOVE_SELF after
				// its parent's MOVED_TO. Keep the valid new pathname.
				if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
					delete(watched, wd)
					_, _ = unix.InotifyRmWatch(fd, uint32(wd))
				}
				continue
			}
			if name == "" || strings.HasPrefix(name, ".") {
				continue
			}
			path := filepath.Join(dir, name)
			isDir := flags&unix.IN_ISDIR != 0
			if isDir && flags&unix.IN_MOVED_FROM != 0 {
				for child, watchedPath := range watched {
					if within(path, watchedPath) {
						delete(watched, child)
						_, _ = unix.InotifyRmWatch(fd, uint32(child))
					}
				}
				continue
			}
			if isDir {
				if flags&(unix.IN_MOVED_TO|unix.IN_CREATE) == 0 {
					continue
				}
				if err := addTree(path); err != nil {
					return err
				}
			} else if flags&(unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE) == 0 {
				// The link(2) operation publishes an existing complete file
				// without opening or closing it in the watched directory.
				var st unix.Stat_t
				if flags&unix.IN_CREATE == 0 || unix.Lstat(path, &st) != nil || st.Nlink < 2 || st.Mode&unix.S_IFMT != unix.S_IFREG {
					continue
				}
			}
			emit(directoryWatchEvent{path: path, dir: isDir, closed: flags&unix.IN_CLOSE_WRITE != 0})
		}
	}
	return ctx.Err()
}
