package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// An O_PATH descriptor preserves ordinary stat permission semantics while
// every ancestor and the selected entry are opened without following links.
func sharedLocalStorageStat(path string) (*os.File, os.FileInfo, error) {
	// Native directory names are raw POSIX bytes. Reject an unrepresentable
	// path before JSON encoding could replace it with a different U+FFFD name.
	if !utf8.ValidString(path) {
		return nil, nil, bad("INVALID_PATH", "本地文件路径编码无效")
	}
	parent, e := importDir(filepath.Dir(path), false)
	if e != nil {
		// An overlong ordinary component is a native query failure, not a
		// no-follow authorization guard. Preserve dispatch and system-error
		// reporting even when importDir wrapped this errno in its public guard.
		if errors.Is(e, unix.ENAMETOOLONG) {
			return nil, nil, unix.ENAMETOOLONG
		}
		// Path.exists() treats a descendant of a regular file as missing. A
		// no-follow directory open also returns ENOTDIR for symlinks, so never
		// normalize that errno without inspecting the refused entry itself.
		if errors.Is(e, unix.ENOTDIR) && sharedLocalStorageRegularAncestor(filepath.Dir(path)) {
			return nil, nil, os.ErrNotExist
		}
		return nil, nil, e
	}
	fd, e := unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return parent, nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return parent, nil, e
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return parent, nil, bad("INVALID_FILE", "只允许不含符号链接的普通文件或目录")
	}
	return parent, info, nil
}

func sharedLocalStorageRegularAncestor(path string) bool {
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return false
	}
	defer func() { _ = unix.Close(fd) }()
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			if !errors.Is(e, unix.ENOTDIR) {
				return false
			}
			var st unix.Stat_t
			return unix.Fstatat(fd, part, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFREG
		}
		_ = unix.Close(fd)
		fd = next
	}
	return false
}

func sharedLocalStorageItem(path string) (any, error) {
	parent, info, e := sharedLocalStorageStat(path)
	if parent != nil {
		defer parent.Close()
	}
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return originalLocalFileItemFromStat(path, info), nil
}
func (a *App) sharedLocalStorageList(ctx context.Context, path string, recursive bool, check func() error) ([]any, error) {
	out := []any{}
	var walk func(string) error
	walk = func(path string) error {
		if e := check(); e != nil {
			return e
		}
		if e := a.sharedLocalStorageAllowed(path); e != nil {
			return e
		}
		parent, info, e := sharedLocalStorageStat(path)
		if parent != nil {
			parent.Close()
		}
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if info.Mode().IsRegular() {
			out = append(out, originalLocalFileItemFromStat(path, info))
			return nil
		}
		dir, e := importDir(path, false)
		if e != nil {
			return e
		}
		entries, e := dir.ReadDir(-1)
		dir.Close()
		if e != nil {
			return e
		}
		// Source directories first, no added lexical sort, hidden files included.
		for _, directory := range []bool{true, false} {
			for _, entry := range entries {
				child := filepath.Join(path, entry.Name())
				if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() != directory || !directory && !entry.Type().IsRegular() || browserInternal(child) || within(a.Dir, child) || directory && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "@eaDir") {
					continue
				}
				if recursive && directory {
					if e := walk(child); e != nil {
						return e
					}
					continue
				}
				if e := check(); e != nil {
					return e
				}
				v, e := sharedLocalStorageItem(child)
				if e != nil {
					return e
				}
				if v != nil {
					out = append(out, v)
				}
			}
		}
		return ctx.Err()
	}
	e := walk(path)
	return out, e
}
func (a *App) sharedLocalStorageDelete(ctx context.Context, path string, check func() error) (bool, error) {
	if e := check(); e != nil {
		return false, e
	}
	parent, info, e := sharedLocalStorageStat(path)
	if parent != nil {
		defer parent.Close()
	}
	if errors.Is(e, os.ErrNotExist) {
		return true, nil
	}
	if e != nil {
		// Source Path.exists precedes LocalStorage.delete's try/except. Query
		// errors therefore reach the module error reporter; only unlink errors
		// below are represented as a normal false result.
		return false, e
	}
	if info.Mode().IsRegular() {
		if e := check(); e != nil {
			return false, e
		}
		if e := sharedLocalStorageSameDirectory(filepath.Dir(path), parent); e != nil {
			return false, e
		}
		return unix.Unlinkat(int(parent.Fd()), filepath.Base(path), 0) == nil, nil
	}
	// shutil.rmtree(ignore_errors=True) continues across ordinary OS errors and
	// returns True even with leftovers. Authorization/cancellation is NOT an OS
	// error to suppress. Hidden children are removed too (unlike list filtering).
	var removeTree func(string) error
	removeTree = func(path string) error {
		if e := check(); e != nil {
			return e
		}
		if e := a.sharedLocalStorageAllowed(path); e != nil {
			return e
		}
		dir, e := importDir(path, false)
		if e != nil {
			if sharedLocalStorageGuard(e) {
				return e
			}
			return nil
		}
		entries, e := dir.ReadDir(-1)
		if e != nil {
			dir.Close()
			return nil
		}
		for _, entry := range entries {
			child := filepath.Join(path, entry.Name())
			if browserInternal(child) || within(a.Dir, child) || entry.Type()&os.ModeSymlink != 0 {
				dir.Close()
				return bad("INVALID_FILE", "目录包含受保护项或符号链接")
			}
			if e := check(); e != nil {
				dir.Close()
				return e
			}
			if entry.IsDir() {
				if e := removeTree(child); e != nil {
					dir.Close()
					return e
				}
			} else {
				p, st, e := sharedLocalStorageStat(child)
				if p != nil {
					p.Close()
				}
				if e != nil {
					if sharedLocalStorageGuard(e) {
						dir.Close()
						return e
					}
					continue
				}
				if !st.Mode().IsRegular() {
					dir.Close()
					return bad("INVALID_FILE", "目录包含特殊文件")
				}
				if e := check(); e != nil {
					dir.Close()
					return e
				}
				if e := sharedLocalStorageSameDirectory(path, dir); e != nil {
					dir.Close()
					return e
				}
				_ = unix.Unlinkat(int(dir.Fd()), entry.Name(), 0)
			}
		}
		dir.Close()
		p, _, e := sharedLocalStorageStat(path)
		if p != nil {
			defer p.Close()
		}
		if e != nil {
			if sharedLocalStorageGuard(e) {
				return e
			}
			return nil
		}
		if e := check(); e != nil {
			return e
		}
		if e := sharedLocalStorageSameDirectory(filepath.Dir(path), p); e != nil {
			return e
		}
		_ = unix.Unlinkat(int(p.Fd()), filepath.Base(path), unix.AT_REMOVEDIR)
		return nil
	}
	if e := removeTree(path); e != nil {
		return false, e
	}
	return true, ctx.Err()
}
func sharedLocalStorageGuard(e error) bool {
	var guard *APIError
	return errors.As(e, &guard) && !errors.Is(e, os.ErrPermission)
}

// Same physical parent check complements the no-follow walk. A renamed/replaced
// directory must not turn a previously opened descriptor into new authority.
func sharedLocalStorageSameDirectory(path string, held *os.File) error {
	current, e := importDir(path, false)
	if e != nil {
		return bad("FILE_PARENT_CHANGED", "文件父目录已变化")
	}
	defer current.Close()
	before, e := held.Stat()
	if e != nil {
		return e
	}
	after, e := current.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(before, after) {
		return bad("FILE_PARENT_CHANGED", "文件父目录已变化")
	}
	return nil
}
