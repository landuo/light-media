//go:build darwin

package scrapehost

import "golang.org/x/sys/unix"

func renameExclusive(parent int, oldName, newName string) error {
	return unix.RenameatxNp(parent, oldName, parent, newName, unix.RENAME_EXCL)
}
