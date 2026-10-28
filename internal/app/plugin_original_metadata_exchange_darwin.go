//go:build darwin

package app

import "golang.org/x/sys/unix"

func originalMetadataExchange(parent int, left, right string) error {
	return unix.RenameatxNp(parent, left, parent, right, unix.RENAME_SWAP)
}
