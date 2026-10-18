//go:build linux

package autobackup

import (
	"os"
	"syscall"
)

func creationTime(info os.FileInfo) (float64, error) {
	v := info.Sys().(*syscall.Stat_t)
	return float64(v.Ctim.Sec) + float64(v.Ctim.Nsec)/1e9, nil
}
