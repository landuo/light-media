//go:build linux

package app

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux futimens is utimensat(fd, NULL, times, 0). Using the held descriptor
// preserves nanoseconds without following a replaced stage path or depending
// on /proc, and works on kernels predating AT_EMPTY_PATH for utimensat.
func importCopyTimes(output *os.File, source os.FileInfo) error {
	stat, ok := source.Sys().(*syscall.Stat_t)
	if !ok {
		return bad("INVALID_FILE", "复制源时间戳不可读取")
	}
	times := [2]unix.Timespec{unix.NsecToTimespec(stat.Atim.Nano()), unix.NsecToTimespec(stat.Mtim.Nano())}
	_, _, err := unix.Syscall6(unix.SYS_UTIMENSAT, output.Fd(), 0, uintptr(unsafe.Pointer(&times[0])), 0, 0, 0)
	if err != 0 {
		return err
	}
	return nil
}
