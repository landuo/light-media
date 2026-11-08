//go:build linux

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Path.unlink needs parent write/search access, not permission to read either
// the file or the directory. O_PATH preserves that Linux source behavior while
// binding every component with O_NOFOLLOW. No worker payload selects flags.
func pluginStorageDirectory(path string) (*os.File, error) {
	if !importPath(path) {
		return nil, bad("INVALID_PATH", "目录路径无效")
	}
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if component == "" {
			continue
		}
		next, openErr := unix.Openat(fd, component, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			if errors.Is(openErr, unix.ENOENT) {
				return nil, os.ErrNotExist
			}
			return nil, fmt.Errorf("%w: %w", bad("INVALID_PATH", "目录不可访问或包含符号链接"), openErr)
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}

func pluginStorageEntryAt(parent *os.File, name string) (os.FileInfo, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	return file.Stat()
}

func pluginStorageEntryInfo(path string) (os.FileInfo, error) {
	parent, err := pluginStorageDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return pluginStorageEntryAt(parent, filepath.Base(path))
}

func (a *App) originalHostLocalRemove(ctx context.Context, scope originalBaseCallScope, path string, expected []browserStamp) (bool, error) {
	parent, err := pluginStorageDirectory(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	defer parent.Close()
	name := filepath.Base(path)
	info, err := pluginStorageEntryAt(parent, name)
	if err != nil {
		return false, err
	}
	stamp, err := browserInfo(path, info)
	if err != nil {
		return false, err
	}
	matched := false
	for _, old := range expected {
		if old.Path == path {
			matched = old == stamp
			break
		}
	}
	if !matched {
		return false, bad("FILE_SNAPSHOT_CHANGED", "删除前文件发生变化")
	}
	var directory *os.Root
	if info.IsDir() {
		directory, err = os.OpenRoot(path)
		if err != nil {
			return false, err
		}
		defer directory.Close()
		bound, statErr := directory.Stat(".")
		if statErr != nil || !os.SameFile(info, bound) {
			return false, bad("FILE_PARENT_CHANGED", "删除前目录发生变化")
		}
	}
	if err = scope.validate(ctx); err != nil {
		return false, err
	}
	release, err := a.beginWrite()
	if err != nil {
		return false, err
	}
	defer release()
	flags := 0
	if directory != nil {
		file, openErr := directory.Open(".")
		if openErr != nil {
			return false, nil
		}
		entries, readErr := file.ReadDir(-1)
		file.Close()
		if readErr != nil {
			return false, nil
		}
		for _, entry := range entries {
			if err = directory.RemoveAll(entry.Name()); err != nil {
				return false, nil
			}
		}
		flags = unix.AT_REMOVEDIR
	}
	if err = unix.Unlinkat(int(parent.Fd()), name, flags); err != nil {
		return false, nil
	}
	var after unix.Stat_t
	err = unix.Fstatat(int(parent.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW)
	return errors.Is(err, unix.ENOENT), nil
}
