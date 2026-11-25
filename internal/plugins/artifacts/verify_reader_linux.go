//go:build linux

package artifacts

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Full verification scans can read hundreds of MiB of plugin packages. Ask
// Linux to avoid retaining pages brought in only for this read. Existing hot
// pages can still be used by a running worker or a frontend resource request.
func verificationReader(file *os.File, size int64) io.Reader {
	if size < 1<<20 {
		return file
	}
	return &verificationFileReader{file: file}
}

type verificationFileReader struct {
	file   *os.File
	offset int64
	plain  bool
}

func (r *verificationFileReader) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if !r.plain {
		for {
			n, err := unix.Preadv2(int(r.file.Fd()), [][]byte{buf}, r.offset, unix.RWF_DONTCACHE)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err == nil {
				r.offset += int64(n)
				if n == 0 {
					return 0, io.EOF
				}
				return n, nil
			}
			if err != unix.ENOSYS && err != unix.EOPNOTSUPP && err != unix.EINVAL && err != unix.EPERM {
				return 0, err
			}
			r.plain = true
			break
		}
	}
	n, err := r.file.ReadAt(buf, r.offset)
	r.offset += int64(n)
	return n, err
}
