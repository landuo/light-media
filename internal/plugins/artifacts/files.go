package artifacts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func safeName(name string) bool {
	if name == "" || name == "." || strings.ContainsAny(name, "\\:\x00\r\n") || strings.HasPrefix(name, "/") {
		return false
	}
	name = strings.TrimSuffix(name, "/")
	return path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}
func ensureRoot(root string) error {
	// Reject symlink ancestors, including the root. The trusted host must own the
	// root; this package does not defend against another writer racing filesystem
	// changes beneath it. In-process acquisitions are serialized.
	cur := filepath.VolumeName(root) + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(root, cur), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			if err = os.Mkdir(cur, 0700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe root ancestor")
		}
	}
	return nil
}
func safeExistingTree(root string) error {
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe installed node %s", p)
		}
		return nil
	})
}
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return errors.New("backup contains non-regular node")
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0700)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		return errors.Join(err, out.Close())
	})
}

type extraction struct {
	root  string
	seen  map[string]bool
	count int
	total int64
}

func (x *extraction) write(name string, dir bool, size int64, r io.Reader) error {
	if !safeName(name) {
		return errors.New("unsafe archive path")
	}
	name = strings.TrimSuffix(name, "/")
	x.count++
	if x.count > MaxArchiveEntries || x.seen[name] {
		return errors.New("duplicate path or too many archive entries")
	}
	x.seen[name] = true
	if size < 0 || size > MaxExpandedBytes-x.total {
		return errors.New("expanded archive exceeds size bound")
	}
	x.total += size
	target := filepath.Join(x.root, filepath.FromSlash(name))
	if dir {
		if size != 0 {
			return errors.New("directory has content")
		}
		return os.MkdirAll(target, 0700)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, size+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if n != size {
		return errors.New("archive member size mismatch")
	}
	return nil
}
func unpack(data []byte, format, root, entry string) error {
	x := extraction{root: root, seen: map[string]bool{}}
	switch format {
	case "executable":
		return x.write(entry, false, int64(len(data)), bytes.NewReader(data))
	case "zip":
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		if len(z.File) == 0 || len(z.File) > MaxArchiveEntries {
			return errors.New("empty or oversized ZIP directory")
		}
		for _, f := range z.File {
			mode := f.Mode()
			if !mode.IsRegular() && !mode.IsDir() {
				return errors.New("ZIP link or special node rejected")
			}
			if f.UncompressedSize64 > uint64(MaxExpandedBytes) {
				return errors.New("ZIP expansion bound")
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = x.write(f.Name, mode.IsDir(), int64(f.UncompressedSize64), r)
			err = errors.Join(err, r.Close())
			if err != nil {
				return err
			}
		}
	case "tar.gz":
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
				return errors.New("TAR link or special node rejected")
			}
			if err = x.write(h.Name, h.Typeflag == tar.TypeDir, h.Size, tr); err != nil {
				return err
			}
		}
		// Consume bounded trailers to validate gzip checksum, rejecting excessive
		// padding and concatenated unindexed streams rather than silently ignoring.
		n, err := io.Copy(io.Discard, io.LimitReader(gz, 1<<20))
		if err != nil {
			return err
		}
		if n >= 1<<20 {
			return errors.New("excess TAR trailing data")
		}
		if x.count == 0 {
			return errors.New("empty TAR")
		}
	default:
		return errors.New("unsupported format")
	}
	return nil
}

// Refresh follows the original optional, post-success persistent-copy behavior.
// Its failure never rolls back an otherwise completed installation.
func refreshBackup(root, id, target string) error {
	backupRoot := filepath.Join(root, ".backups")
	if err := ensureRoot(backupRoot); err != nil {
		return err
	}
	dest := filepath.Join(backupRoot, id)
	if err := safeExistingTree(dest); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(backupRoot, ".refresh-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	prepared := filepath.Join(stage, "code")
	if err = copyTree(target, prepared); err != nil {
		return err
	}
	if err = os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(prepared, dest)
}
