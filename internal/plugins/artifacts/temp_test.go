package artifacts

import (
	"fmt"
	"os"
	"testing"
)

// The acquisition preflight rejects trusted roots whose ancestors are symlinks.
// macOS puts the default TMPDIR under /var, itself a symlink, so the package
// allocates its test temp directory under the checkout instead.
func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dir, err := os.MkdirTemp(wd, ".artifact-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = os.Setenv("TMPDIR", dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
