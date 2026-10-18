//go:build linux

package app

import "golang.org/x/sys/unix"

func autoBackupRenameNoReplace(fd int, old, new string) error {
	return unix.Renameat2(fd, old, fd, new, unix.RENAME_NOREPLACE)
}
