//go:build !linux && !darwin

package app

import "errors"

func autoBackupRenameNoReplace(int, string, string) error {
	return errors.New("atomic backup retention unsupported on this platform")
}
