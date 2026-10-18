package autobackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Filesystem struct{ Roots []string }

func (f Filesystem) ListBackups(ctx context.Context, dest string) ([]Entry, error) {
	if e := authorize(dest, f.Roots); e != nil {
		return nil, e
	}
	names, e := filepath.Glob(filepath.Join(dest, "bk**"))
	if e != nil {
		return nil, e
	}
	out := []Entry{}
	for _, p := range names {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		st, e := os.Stat(p)
		if e != nil {
			return nil, e
		}
		kind := "other"
		if st.IsDir() {
			kind = "dir"
		} else if st.Mode().IsRegular() {
			kind = "file"
		}
		created, e := creationTime(st)
		if e != nil {
			return nil, e
		}
		out = append(out, Entry{p, created, kind})
	}
	return out, nil
}
func (f Filesystem) checkDelete(p string) error {
	if !strings.HasPrefix(filepath.Base(p), "bk") {
		return errors.New("outside backup entry grant")
	}
	if e := authorize(p, f.Roots); e != nil {
		return e
	}
	for _, root := range f.Roots {
		if filepath.Clean(root) == filepath.Clean(p) {
			return errors.New("cannot delete backup grant root")
		}
	}
	return nil
}
func (f Filesystem) RemoveFile(ctx context.Context, p string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := f.checkDelete(p); e != nil {
		return e
	}
	return os.Remove(p)
}
func (f Filesystem) RemoveTree(ctx context.Context, p string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := f.checkDelete(p); e != nil {
		return e
	}
	st, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return source("OSError", "rmtree symbolic link")
	}
	return os.RemoveAll(p)
}
