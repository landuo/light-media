//go:build darwin

package app

import "golang.org/x/sys/unix"

func autoBackupRenameNoReplace(fd int, old, new string) error {
	return unix.RenameatxNp(fd, old, fd, new, unix.RENAME_EXCL)
}
