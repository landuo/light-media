package app

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Have Linux validate every component in the same lookup that opens the
// directory. Older kernels and restrictive seccomp policies use openat walking.
func openGuardedDirectory(name string) (*os.File, bool, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
	if err == nil {
		return os.NewFile(uintptr(fd), name), true, nil
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EINVAL) {
		return nil, false, nil
	}
	if errors.Is(err, unix.ENOENT) {
		return nil, true, os.ErrNotExist
	}
	return nil, true, fmt.Errorf("%w: %w", bad("INVALID_PATH", "目录不可访问或包含符号链接"), err)
}
