//go:build darwin

package autobackup

import (
	"os"
	"syscall"
)

func creationTime(info os.FileInfo) (float64, error) {
	v := info.Sys().(*syscall.Stat_t)
	return float64(v.Ctimespec.Sec) + float64(v.Ctimespec.Nsec)/1e9, nil
}
