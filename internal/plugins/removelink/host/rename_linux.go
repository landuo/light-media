//go:build linux

package cleanuphost

import "golang.org/x/sys/unix"

func renameExclusive(parent int, oldName, newName string) error {
	return unix.Renameat2(parent, oldName, parent, newName, unix.RENAME_NOREPLACE)
}
