//go:build !linux

package main

import "os"

func processUsage(_ *os.ProcessState) childUsage {
	return childUsage{Scope: "unavailable_outside_linux;_wrapper_memory_is_not_child_memory"}
}
