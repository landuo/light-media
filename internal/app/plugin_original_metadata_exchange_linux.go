//go:build linux

package app

import "golang.org/x/sys/unix"

func originalMetadataExchange(parent int, left, right string) error {
	return unix.Renameat2(parent, left, parent, right, unix.RENAME_EXCHANGE)
}
