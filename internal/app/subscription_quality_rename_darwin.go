//go:build darwin

package app

import "golang.org/x/sys/unix"

func qualityRenameExchange(parent int, oldName, newName string) error {
	return unix.RenameatxNp(parent, oldName, parent, newName, unix.RENAME_SWAP)
}
