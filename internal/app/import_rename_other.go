//go:build !darwin && !linux

package app

import "errors"

func importRenameExclusive(parent int, oldName, newName string) error {
	return errors.New("exclusive rename is unavailable on this platform")
}
