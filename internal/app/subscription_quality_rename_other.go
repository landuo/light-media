//go:build !darwin && !linux

package app

import "errors"

func qualityRenameExchange(parent int, oldName, newName string) error {
	return errors.New("atomic quality replacement unavailable on this platform")
}
