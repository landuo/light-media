//go:build !linux

package app

// residentMemoryBytes has no portable stand-in off Linux; the reclaim log then
// reports the Go heap only.
func residentMemoryBytes() uint64 { return 0 }
