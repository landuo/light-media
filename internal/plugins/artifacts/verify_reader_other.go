//go:build !linux

package artifacts

import (
	"io"
	"os"
)

func verificationReader(file *os.File, size int64) io.Reader { return file }
