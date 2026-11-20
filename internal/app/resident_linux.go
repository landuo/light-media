//go:build linux

package app

import (
	"os"
	"strconv"
	"strings"
)

// residentMemoryBytes reports the kernel's resident set for this process. The
// reclaim log needs the number the container bills, not only the Go heap.
func residentMemoryBytes() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}
