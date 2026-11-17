//go:build unix

package selfupdate

import "syscall"

// Exec replaces the process image with the installed artifact, keeping the
// original argv and environment plus the loop marker.
func Exec(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}
