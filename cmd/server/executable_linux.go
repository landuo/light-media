package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Large device read-ahead, including Btrfs's independent backing-device
// window, can evict hot code on every fault when the service is under memory.high.
func configureExecutableReadAhead() {
	var exe unix.Stat_t
	if unix.Stat("/proc/self/exe", &exe) != nil {
		return
	}
	major, minor := unix.Major(exe.Dev), unix.Minor(exe.Dev)
	var fs unix.Statfs_t
	var btrfsPath string
	if unix.Statfs("/proc/self/exe", &fs) == nil && fs.Type == unix.BTRFS_SUPER_MAGIC {
		// Btrfs stat reports the subvolume device, while maps uses the superblock.
		// Match the proc-reported executable pathname and inode on this filesystem.
		btrfsPath, _ = os.Readlink("/proc/self/exe")
	} else {
		b, err := os.ReadFile(fmt.Sprintf("/sys/dev/block/%d:%d/queue/read_ahead_kb", major, minor))
		if err != nil {
			return
		}
		kiB, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || kiB < 8192 {
			return
		}
	}
	maps, err := os.Open("/proc/self/maps")
	if err != nil {
		return
	}
	defer maps.Close()
	visitExecutableMappings(maps, fmt.Sprintf("%02x:%02x", major, minor), strconv.FormatUint(exe.Ino, 10), btrfsPath, func(base, length uintptr) {
		unix.Syscall(unix.SYS_MADVISE, base, length, unix.MADV_RANDOM)
	})
}

func visitExecutableMappings(maps io.Reader, device, inode, btrfsPath string, visit func(uintptr, uintptr)) {
	lines := bufio.NewScanner(maps)
	for lines.Scan() {
		line := lines.Text()
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[4] != inode || (fields[1] != "r-xp" && fields[1] != "r--p") {
			continue
		}
		if fields[3] != device {
			if btrfsPath == "" {
				continue
			}
			path := line
			for range 5 {
				_, rest, _ := strings.Cut(path, " ")
				path = strings.TrimLeft(rest, " ")
			}
			if path != btrfsPath {
				continue
			}
		}
		start, end, ok := strings.Cut(fields[0], "-")
		base, startErr := strconv.ParseUint(start, 16, strconv.IntSize)
		limit, endErr := strconv.ParseUint(end, 16, strconv.IntSize)
		if ok && startErr == nil && endErr == nil && base < limit {
			visit(uintptr(base), uintptr(limit-base))
		}
	}
}
