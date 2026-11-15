//go:build darwin

package app

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Set only timestamps on the open stage; chmod/copystat would replace the
// destination directory's inherited access policy. Attribute order is MODTIME
// then ACCTIME, matching the ascending Darwin common-attribute bitmap.
func importCopyTimes(output *os.File, source os.FileInfo) error {
	stat, ok := source.Sys().(*syscall.Stat_t)
	if !ok {
		return bad("INVALID_FILE", "复制源时间戳不可读取")
	}
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_MODTIME | unix.ATTR_CMN_ACCTIME}
	times := [2]unix.Timespec{unix.NsecToTimespec(stat.Mtimespec.Nano()), unix.NsecToTimespec(stat.Atimespec.Nano())}
	_, _, err := unix.Syscall6(unix.SYS_FSETATTRLIST, output.Fd(), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&times[0])), unsafe.Sizeof(times), 0, 0)
	if err != 0 {
		return err
	}
	return nil
}
