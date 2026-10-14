//go:build linux

package main

import (
	"os"
	"syscall"
)

func processUsage(state *os.ProcessState) childUsage {
	u := childUsage{Scope: "sqlitebench_wait4_maxrss_includes_waited_descendants_max_not_sum"}
	if state == nil {
		return u
	}
	if ru, ok := state.SysUsage().(*syscall.Rusage); ok {
		u.Available = true
		u.MaximumRSSBytes = ru.Maxrss * 1024
	}
	return u
}
