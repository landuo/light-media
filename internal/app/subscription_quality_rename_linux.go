//go:build linux

package app

import "golang.org/x/sys/unix"

func qualityRenameExchange(parent int, oldName, newName string) error {
	return unix.Renameat2(parent, oldName, parent, newName, unix.RENAME_EXCHANGE)
}
