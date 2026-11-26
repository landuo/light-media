//go:build !linux

package app

import "os"

func openGuardedDirectory(_ string) (*os.File, bool, error) { return nil, false, nil }
