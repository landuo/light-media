package durable

import (
	"os"
	"path/filepath"
)

func readAutoSignInFixture(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "plugins", "auto-signin-2.9.14", "durable", name))
}
