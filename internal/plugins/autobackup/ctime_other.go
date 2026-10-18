//go:build !darwin && !linux

package autobackup

import "os"

// Other OS backends must supply an authoritative creation/change-time adapter.
func creationTime(info os.FileInfo) (float64, error) {
	return 0, source("NotImplementedError", "OS change-time backend")
}
